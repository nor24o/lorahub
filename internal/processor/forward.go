package processor

import (
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/nor24o/lorahub/internal/band"
	"github.com/nor24o/lorahub/internal/ingest"
	"github.com/nor24o/lorahub/internal/lorawan"
	"github.com/nor24o/lorahub/internal/model"
)

// ReloadForwarders (re)creates upstream forwarders for all gateways.
func (p *Processor) ReloadForwarders() {
	p.fmu.Lock()
	for _, fs := range p.forwarders {
		for _, f := range fs {
			f.Close()
		}
	}
	p.forwarders = map[string][]*ingest.Forwarder{}
	p.fmu.Unlock()
	for _, g := range p.store.Gateways() {
		for _, fc := range g.Forwarders {
			if !fc.Enabled || fc.Address == "" {
				continue
			}
			gw := g
			var onDown func(model.TxPacket)
			if fc.Downlink {
				onDown = func(tx model.TxPacket) {
					dl := p.downlinker(gw.Kind)
					if dl == nil {
						return
					}
					if err := dl.Send(gw.EUI, tx); err == nil {
						p.countTx(gw.ID)
						p.emit(&model.Event{Name: "gs.down.forwarded", Gateways: []string{gw.ID},
							Summary: fmt.Sprintf("Downlink from upstream %s · %.3f MHz %s", fc.Name, float64(tx.Frequency)/1e6, tx.DataRate),
							Data:    map[string]any{"upstream": fc.Address, "tx": tx, "payload_hex": strings.ToUpper(hex.EncodeToString(tx.Payload))}}, true)
					}
				}
			}
			f, err := ingest.NewForwarder(g.EUI, fc.Address, onDown, p.log)
			if err != nil {
				p.log.Warn("forwarder", "gateway", g.ID, "addr", fc.Address, "err", err)
				continue
			}
			p.fmu.Lock()
			p.forwarders[g.ID] = append(p.forwarders[g.ID], f)
			p.fmu.Unlock()
		}
	}
}

// ForwarderStatus reports upstream connection state per gateway.
func (p *Processor) ForwarderStatus(gwID string) map[string]bool {
	p.fmu.Lock()
	defer p.fmu.Unlock()
	out := map[string]bool{}
	for _, f := range p.forwarders[gwID] {
		out[f.Addr] = f.Connected()
	}
	return out
}

func (p *Processor) forwardUplink(g *model.Gateway, pkt model.RxPacket) {
	p.fmu.Lock()
	fs := p.forwarders[g.ID]
	p.fmu.Unlock()
	if len(fs) == 0 {
		return
	}
	r := ingest.PacketToRxpk(pkt)
	for _, f := range fs {
		f.Push([]map[string]any{r}, nil)
	}
}

func (p *Processor) forwardStatus(g *model.Gateway, status map[string]any) {
	p.fmu.Lock()
	fs := p.forwarders[g.ID]
	p.fmu.Unlock()
	if len(fs) == 0 {
		return
	}
	st := map[string]any{"time": time.Now().UTC().Format("2006-01-02 15:04:05 MST"), "rxnb": g.RxCount, "rxok": g.RxCount,
		"rxfw": g.RxCount, "ackr": 100.0, "dwnb": g.TxCount, "txnb": g.TxCount}
	if g.Location != nil {
		st["lati"], st["long"], st["alti"] = g.Location.Latitude, g.Location.Longitude, int(g.Location.Altitude)
	}
	for _, f := range fs {
		f.Push(nil, st)
	}
}

// SimulateRequest injects a synthetic uplink.
type SimulateRequest struct {
	GatewayID  string  `json:"gateway_id"`
	PayloadHex string  `json:"payload_hex"`
	Frequency  float64 `json:"frequency"` // MHz
	DataRate   string  `json:"data_rate"`
	RSSI       float64 `json:"rssi"`
	SNR        float64 `json:"snr"`
	// If AppID/DevID are set for a LoRaWAN device, PayloadHex is the
	// application payload which is encrypted with the device session.
	AppID     string `json:"application_id"`
	DevID     string `json:"device_id"`
	FPort     int    `json:"f_port"`
	Confirmed bool   `json:"confirmed"`
}

// Simulate injects an uplink as if received by a gateway.
func (p *Processor) Simulate(r SimulateRequest) error {
	payload, err := hex.DecodeString(cleanHex(r.PayloadHex))
	if err != nil {
		return fmt.Errorf("payload_hex: %w", err)
	}
	if r.DevID != "" {
		d, err := p.store.Device(r.AppID, r.DevID)
		if err != nil {
			return err
		}
		if d.Mode == "node" {
			pf := p.Settings().PacketFormat
			buf := make([]byte, max(pf.DataOffset, pf.NodeIDOffset+pf.NodeIDLength, pf.SerialOffset+pf.SerialLength))
			app, err := p.store.Application(r.AppID)
			if err != nil {
				return err
			}
			nid, _ := hex.DecodeString(app.NodeID)
			ser, _ := hex.DecodeString(d.Serial)
			copy(buf[pf.NodeIDOffset:pf.NodeIDOffset+pf.NodeIDLength], nid)
			copy(buf[pf.SerialOffset:pf.SerialOffset+pf.SerialLength], ser)
			payload = append(buf, payload...)
		} else if d.Mode != "raw" {
			if d.Session == nil {
				return fmt.Errorf("device has no session (OTAA device not joined yet)")
			}
			nwk, _ := lorawan.ParseKey(d.Session.NwkSKey)
			app, _ := lorawan.ParseKey(d.Session.AppSKey)
			fc := d.Session.FCntUp + 1
			if !d.Session.HasUplink {
				fc = 0
			}
			fp := r.FPort
			if fp == 0 {
				fp = 1
			}
			payload = lorawan.BuildDataUp(r.Confirmed, mustAddr(d.Session.DevAddr), fc, fp, payload, nwk, app)
		}
	}
	gw, err := p.store.Gateway(r.GatewayID)
	if err != nil {
		gs := p.store.Gateways()
		if len(gs) == 0 {
			return fmt.Errorf("no gateway registered")
		}
		gw = gs[0]
	}
	if r.Frequency == 0 {
		r.Frequency = 868.1
	}
	if r.DataRate == "" {
		r.DataRate = "SF7BW125"
	}
	dr, err := band.ParseDatr(r.DataRate)
	if err != nil {
		return fmt.Errorf("data_rate: %w", err)
	}
	if r.RSSI == 0 {
		r.RSSI = -60
	}
	now := time.Now().UTC()
	pkt := model.RxPacket{Payload: payload, GatewayEUI: gw.EUI,
		Settings: model.LoRaSettings{Modulation: "LORA", SF: dr.SF, BW: dr.BW, CodingRate: "4/5", Frequency: uint64(r.Frequency*1e6 + 0.5), DataRate: dr.String()},
		Meta: model.RxMetadata{GatewayIDs: model.GatewayIDs{EUI: gw.EUI}, Source: "simulated", Timestamp: uint32(now.UnixMicro()),
			RSSI: r.RSSI, ChannelRSSI: r.RSSI, SNR: r.SNR, CRCStatus: "ok", ReceivedAt: now, Time: &now}}
	p.HandleUplink(pkt)
	return nil
}

// Housekeeping prunes old events and flushes counters periodically.
func (p *Processor) Housekeeping(stop <-chan struct{}) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-stop:
			p.store.FlushGateways()
			return
		case <-t.C:
			s := p.Settings()
			p.store.PruneEvents(time.Duration(s.EventRetentionHours)*time.Hour, s.EventMaxRows)
			p.store.FlushGateways()
		}
	}
}

// SendRaw transmits an arbitrary packet through a gateway (test tool).
func (p *Processor) SendRaw(gwID string, tx model.TxPacket) error {
	g, err := p.store.Gateway(gwID)
	if err != nil {
		return err
	}
	dl := p.downlinker(g.Kind)
	if dl == nil || !dl.CanSend(g.EUI) {
		return fmt.Errorf("gateway %s has no active downlink path", gwID)
	}
	if tx.CodingRate == "" {
		tx.CodingRate = "4/5"
	}
	if tx.Power == 0 {
		tx.Power = 14
	}
	if err := dl.Send(g.EUI, tx); err != nil {
		return err
	}
	p.countTx(g.ID)
	p.emit(&model.Event{Name: "gs.down.manual", Gateways: []string{g.ID},
		Summary: fmt.Sprintf("Manual TX %.3f MHz %s %d dBm · %s", float64(tx.Frequency)/1e6, tx.DataRate, tx.Power, truncHex(tx.Payload, 16)),
		Data:    map[string]any{"tx": tx, "payload_hex": strings.ToUpper(hex.EncodeToString(tx.Payload))}}, true)
	return nil
}
