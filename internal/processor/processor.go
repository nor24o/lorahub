// Package processor is the heart of LoRaHub: it registers gateways,
// de-duplicates receptions from multiple gateways, authenticates and
// decrypts LoRaWAN frames, matches raw (non-LoRaWAN) devices, runs payload
// formatters, schedules downlinks and publishes events.
package processor

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/nor24o/lorahub/internal/band"
	"github.com/nor24o/lorahub/internal/config"
	"github.com/nor24o/lorahub/internal/events"
	"github.com/nor24o/lorahub/internal/formatter"
	"github.com/nor24o/lorahub/internal/ingest"
	"github.com/nor24o/lorahub/internal/lorawan"
	"github.com/nor24o/lorahub/internal/model"
	"github.com/nor24o/lorahub/internal/store"
)

// Downlinker sends downlinks to a gateway.
type Downlinker interface {
	CanSend(eui string) bool
	Send(eui string, tx model.TxPacket) error
}

// Processor implements ingest.Sink.
type Processor struct {
	store *store.Store
	bus   *events.Bus
	log   *slog.Logger

	smu      sync.RWMutex
	settings config.Settings

	dmu         sync.Mutex
	downlinkers map[string]Downlinker // by gateway kind

	qmu   sync.Mutex
	dedup map[string]*dedupEntry

	fmu        sync.Mutex
	forwarders map[string][]*ingest.Forwarder // gateway ID -> forwarders

	devLocks sync.Map // app/dev -> *sync.Mutex

	rmu     sync.RWMutex
	schemes *formatter.SchemeDoc
	rules   []model.Rule

	dutyMu sync.Mutex
	duty   map[string][]dutyEntry

	Started time.Time
}

type dedupEntry struct {
	pkts []model.RxPacket
}

// New creates a processor.
func New(st *store.Store, bus *events.Bus, log *slog.Logger) *Processor {
	p := &Processor{store: st, bus: bus, log: log, downlinkers: map[string]Downlinker{},
		dedup: map[string]*dedupEntry{}, forwarders: map[string][]*ingest.Forwarder{}, Started: time.Now()}
	s := config.DefaultSettings()
	st.GetSetting("settings", &s)
	if s.LinkKey == "" {
		s.LinkKey = store.NewToken(24)
		if err := st.SetSetting("settings", s); err != nil {
			log.Warn("save link key", "err", err)
		}
	}
	p.settings = s
	p.loadPipeline()
	return p
}

// Settings returns the current runtime settings.
func (p *Processor) Settings() config.Settings {
	p.smu.RLock()
	defer p.smu.RUnlock()
	return p.settings
}

// SetSettings replaces and persists the runtime settings.
func (p *Processor) SetSettings(s config.Settings) error {
	if s.DedupWindowMs < 0 || s.DedupWindowMs > 2000 {
		return fmt.Errorf("dedup window must be between 0 and 2000 ms")
	}
	if _, err := hex.DecodeString(s.NetID); err != nil || len(s.NetID) != 6 {
		return fmt.Errorf("net_id must be 6 hex characters")
	}
	if _, err := lorawan.ParseDevAddr(s.DevAddrPrefix); err != nil {
		return fmt.Errorf("dev_addr_prefix must be 8 hex characters")
	}
	if s.RX1Delay < 1 || s.RX1Delay > 15 {
		s.RX1Delay = 1
	}
	if err := p.store.SetSetting("settings", s); err != nil {
		return err
	}
	p.smu.Lock()
	p.settings = s
	p.smu.Unlock()
	return nil
}

// RegisterDownlinker registers a downlink path for a gateway kind.
func (p *Processor) RegisterDownlinker(kind string, d Downlinker) {
	p.dmu.Lock()
	p.downlinkers[kind] = d
	p.dmu.Unlock()
}

func (p *Processor) downlinker(kind string) Downlinker {
	p.dmu.Lock()
	defer p.dmu.Unlock()
	return p.downlinkers[kind]
}

func (p *Processor) lockDevice(app, dev string) func() {
	m, _ := p.devLocks.LoadOrStore(app+"/"+dev, &sync.Mutex{})
	mu := m.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// emit stores (optionally) and publishes an event.
func (p *Processor) emit(e *model.Event, persist bool) {
	if e.Time.IsZero() {
		e.Time = time.Now().UTC()
	}
	if persist {
		if err := p.store.AddEvent(e); err != nil {
			p.log.Warn("store event", "err", err)
		}
	}
	p.bus.Publish(e)
}

// ---------------- Gateways ----------------

func gatewayIDFromEUI(eui string) string { return "eui-" + strings.ToLower(eui) }

// resolveGateway returns the gateway for eui, auto-registering if allowed.
func (p *Processor) resolveGateway(eui, kind, addr string) (*model.Gateway, bool) {
	g, err := p.store.GatewayByEUI(eui)
	if err == nil {
		return g, !g.Disabled
	}
	if g := p.adoptReversedEUI(eui, kind); g != nil {
		return g, !g.Disabled
	}
	if !p.Settings().AutoRegisterGateways {
		return nil, false
	}
	now := time.Now().UTC()
	g = &model.Gateway{ID: gatewayIDFromEUI(eui), EUI: strings.ToUpper(eui), Name: kindLabel(kind) + " " + eui[len(eui)-6:],
		Kind: kind, Band: p.Settings().DefaultBand, Downlink: true, CreatedAt: now, UpdatedAt: now}
	if err := p.store.SaveGateway(g); err != nil {
		p.log.Warn("auto-register gateway", "eui", eui, "err", err)
		return nil, false
	}
	p.log.Info("gateway auto-registered", "id", g.ID, "kind", kind, "addr", addr)
	p.emit(&model.Event{Name: "gs.gateway.registered", Gateways: []string{g.ID},
		Summary: fmt.Sprintf("Gateway %s auto-registered (%s from %s)", g.ID, kind, addr),
		Data:    map[string]any{"gateway_id": g.ID, "eui": g.EUI, "kind": kind, "addr": addr}}, true)
	return g, true
}

func kindLabel(k string) string {
	switch k {
	case "bobcat":
		return "Bobcat"
	case "semtech_udp":
		return "UDP gateway"
	case "http":
		return "HTTP gateway"
	}
	return "Gateway"
}

// HandleStatus implements ingest.Sink.
func (p *Processor) HandleStatus(eui, kind, addr string, status map[string]any) bool {
	g, ok := p.resolveGateway(eui, kind, addr)
	if !ok {
		return false
	}
	now := time.Now().UTC()
	wasOffline := g.LastSeen == nil || now.Sub(*g.LastSeen) > time.Duration(p.Settings().GatewayOfflineSec)*time.Second
	p.store.UpdateGateway(g.ID, wasOffline, func(gw *model.Gateway) {
		gw.LastSeen = &now
		gw.LastAddr = addr
		if gw.Kind == "" {
			gw.Kind = kind
		}
		if status != nil {
			merged := map[string]any{}
			if len(gw.LastStatus) > 0 {
				jsonUnmarshal(gw.LastStatus, &merged)
			}
			for k, v := range status {
				merged[k] = v
			}
			merged["updated_at"] = now
			gw.LastStatus = jsonMarshal(merged)
			if gw.Location == nil {
				if lat, ok := num(status["lati"]); ok && lat != 0 {
					lon, _ := num(status["long"])
					alt, _ := num(status["alti"])
					gw.Location = &model.Location{Latitude: lat, Longitude: lon, Altitude: alt, Source: "gps"}
				}
			}
		}
	})
	if wasOffline {
		p.emit(&model.Event{Name: "gs.gateway.connect", Gateways: []string{g.ID},
			Summary: fmt.Sprintf("Gateway connected via %s from %s", kindLabel(kind), addr),
			Data:    map[string]any{"gateway_id": g.ID, "eui": g.EUI, "addr": addr, "kind": kind}}, true)
	}
	p.applyLinkStatus(g, status)
	if status != nil {
		p.emit(&model.Event{Name: "gs.status", Gateways: []string{g.ID}, Summary: "Gateway status", Data: status}, false)
		p.forwardStatus(g, status)
	}
	return true
}

// HandleTxAck implements ingest.Sink.
func (p *Processor) HandleTxAck(eui string, ack map[string]any) {
	g, err := p.store.GatewayByEUI(eui)
	if err != nil {
		return
	}
	errStr, _ := ack["error"].(string)
	if errStr == "" || errStr == "NONE" {
		return
	}
	p.emit(&model.Event{Name: "gs.down.tx_ack", Gateways: []string{g.ID},
		Summary: "Downlink rejected by gateway: " + errStr, Data: ack}, true)
}

// ---------------- Uplink path ----------------

// HandleUplink implements ingest.Sink.
func (p *Processor) HandleUplink(pkt model.RxPacket) {
	g, ok := p.resolveGateway(pkt.GatewayEUI, pkt.Meta.Source, "")
	if !ok {
		return
	}
	now := time.Now().UTC()
	p.store.UpdateGateway(g.ID, false, func(gw *model.Gateway) {
		gw.RxCount++
		gw.LastSeen = &now
	})
	if rejectedByFilter(g, &pkt.Meta) {
		return
	}
	pkt.Meta.GatewayIDs = model.GatewayIDs{GatewayID: g.ID, EUI: g.EUI}
	if g.Location != nil {
		loc := *g.Location
		pkt.Meta.Location = &loc
	}

	// Gateway-level raw event (like the TTN gateway live data).
	ev := map[string]any{
		"gateway_ids":     pkt.Meta.GatewayIDs,
		"raw_payload":     base64.StdEncoding.EncodeToString(pkt.Payload),
		"raw_payload_hex": strings.ToUpper(hex.EncodeToString(pkt.Payload)),
		"settings":        pkt.Settings,
		"rx_metadata":     pkt.Meta,
		"size":            len(pkt.Payload),
	}
	summary := fmt.Sprintf("%d bytes · %s · %.3f MHz · RSSI %.0f · SNR %.1f", len(pkt.Payload), pkt.Settings.DataRate,
		float64(pkt.Settings.Frequency)/1e6, pkt.Meta.RSSI, pkt.Meta.SNR)
	if f, err := lorawan.Parse(pkt.Payload); err == nil {
		info := map[string]any{"m_type": lorawan.MTypeName(f.MType)}
		switch f.MType {
		case lorawan.JoinRequest:
			info["dev_eui"], info["join_eui"], info["dev_nonce"] = f.DevEUI.String(), f.JoinEUI.String(), f.DevNonce
			summary = "Join request " + f.DevEUI.String() + " · " + summary
		case lorawan.UnconfirmedDataUp, lorawan.ConfirmedDataUp:
			info["dev_addr"], info["f_cnt"] = lorawan.DevAddrString(f.DevAddr), f.FCnt16
			if f.FPort != nil {
				info["f_port"] = *f.FPort
			}
			summary = fmt.Sprintf("DevAddr %s FCnt %d · %s", lorawan.DevAddrString(f.DevAddr), f.FCnt16, summary)
		}
		ev["lorawan"] = info
	}
	if pkt.Meta.CRCStatus == "bad" {
		summary = "CRC error · " + summary
	}
	p.emit(&model.Event{Name: "gs.up", Gateways: []string{g.ID}, Summary: summary, Data: ev}, p.Settings().StoreGatewayRaw)
	p.forwardUplink(g, pkt)

	if pkt.Meta.CRCStatus == "bad" && p.Settings().DropCRCErrors {
		return
	}

	key := hex.EncodeToString(pkt.Payload)
	window := time.Duration(p.Settings().DedupWindowMs) * time.Millisecond
	p.qmu.Lock()
	if e, ok := p.dedup[key]; ok {
		e.pkts = append(e.pkts, pkt)
		p.qmu.Unlock()
		return
	}
	p.dedup[key] = &dedupEntry{pkts: []model.RxPacket{pkt}}
	p.qmu.Unlock()
	flush := func() {
		p.qmu.Lock()
		e := p.dedup[key]
		delete(p.dedup, key)
		p.qmu.Unlock()
		if e != nil {
			p.process(e.pkts)
		}
	}
	if window <= 0 {
		go flush()
	} else {
		time.AfterFunc(window, flush)
	}
}

// Frame is a de-duplicated uplink with metadata from all gateways.
type Frame struct {
	Payload  []byte
	Settings model.LoRaSettings
	Meta     []model.RxMetadata
	At       time.Time
}

func (p *Processor) process(pkts []model.RxPacket) {
	var good []model.RxPacket
	for _, k := range pkts {
		if k.Meta.CRCStatus != "bad" {
			good = append(good, k)
		}
	}
	if len(good) == 0 {
		good = pkts
	}
	sort.SliceStable(good, func(i, j int) bool {
		if good[i].Meta.SNR != good[j].Meta.SNR {
			return good[i].Meta.SNR > good[j].Meta.SNR
		}
		return good[i].Meta.RSSI > good[j].Meta.RSSI
	})
	fr := &Frame{Payload: good[0].Payload, Settings: good[0].Settings, At: time.Now().UTC()}
	seen := map[string]bool{}
	for _, k := range good {
		if seen[k.Meta.GatewayIDs.GatewayID] {
			continue
		}
		seen[k.Meta.GatewayIDs.GatewayID] = true
		fr.Meta = append(fr.Meta, k.Meta)
	}
	defer func() {
		if r := recover(); r != nil {
			p.log.Error("panic while processing uplink", "err", r)
		}
	}()

	if p.handleNode(fr) {
		return
	}
	if f, err := lorawan.Parse(fr.Payload); err == nil && p.Settings().LoRaWANEnabled {
		switch f.MType {
		case lorawan.JoinRequest:
			if p.handleJoin(fr, f) {
				return
			}
		case lorawan.UnconfirmedDataUp, lorawan.ConfirmedDataUp:
			if p.handleDataUp(fr, f) {
				return
			}
		}
	}
	if p.handleRaw(fr) {
		return
	}
	p.emitUnmatched(fr)
}

func gatewayIDs(meta []model.RxMetadata) []string {
	out := make([]string, 0, len(meta))
	for _, m := range meta {
		out = append(out, m.GatewayIDs.GatewayID)
	}
	return out
}

func (p *Processor) emitUnmatched(fr *Frame) {
	summary := fmt.Sprintf("Unmatched %d byte packet %s", len(fr.Payload), truncHex(fr.Payload, 12))
	var hdr *NodeHeader
	if h, ok := ParseNodeHeader(p.Settings().PacketFormat, fr.Payload); ok {
		hdr = &h
		summary = fmt.Sprintf("Unknown node group %s (serial %s) — create a group for it", h.NodeID, h.Serial)
	}
	data := p.unmatchedData(fr, hdr)
	data["raw_payload_hex"] = data["payload_hex"]
	if f, err := lorawan.Parse(fr.Payload); err == nil && p.Settings().LoRaWANEnabled {
		info := map[string]any{"m_type": lorawan.MTypeName(f.MType)}
		if f.MType == lorawan.JoinRequest {
			info["dev_eui"], info["join_eui"] = f.DevEUI.String(), f.JoinEUI.String()
			summary = "Join request from unknown device " + f.DevEUI.String()
		} else {
			info["dev_addr"], info["f_cnt"] = lorawan.DevAddrString(f.DevAddr), f.FCnt16
			summary = "Uplink from unknown DevAddr " + lorawan.DevAddrString(f.DevAddr)
		}
		data["lorawan"] = info
	}
	p.emit(&model.Event{Name: "ns.up.unmatched", Gateways: gatewayIDs(fr.Meta), Summary: summary, Data: data}, true)
}

func truncHex(b []byte, n int) string {
	h := strings.ToUpper(hex.EncodeToString(b))
	if len(h) > n*2 {
		return h[:n*2] + "…"
	}
	return h
}

// ---------------- Uplink message construction ----------------

func (p *Processor) buildUplink(d *model.Device, fr *Frame, fport int, fcnt *uint32, payload []byte, confirmed bool, devAddr string) map[string]any {
	ids := map[string]any{
		"device_id":       d.ID,
		"application_ids": map[string]string{"application_id": d.AppID},
	}
	if d.DevEUI != "" {
		ids["dev_eui"] = strings.ToUpper(d.DevEUI)
	}
	if d.JoinEUI != "" {
		ids["join_eui"] = strings.ToUpper(d.JoinEUI)
	}
	if devAddr != "" {
		ids["dev_addr"] = devAddr
	}
	dr := band.DataRate{SF: fr.Settings.SF, BW: fr.Settings.BW}
	airtime := band.AirtimeMs(len(fr.Payload), dr, fr.Settings.CodingRate, 8, true, true)
	settings := map[string]any{
		"data_rate": map[string]any{"lora": map[string]any{"bandwidth": fr.Settings.BW * 1000, "spreading_factor": fr.Settings.SF,
			"coding_rate": fr.Settings.CodingRate}},
		"frequency": fmt.Sprintf("%d", fr.Settings.Frequency),
		"timestamp": fr.Meta[0].Timestamp,
	}
	if fr.Meta[0].Time != nil {
		settings["time"] = fr.Meta[0].Time
	}
	up := map[string]any{
		"f_port":           fport,
		"frm_payload":      base64.StdEncoding.EncodeToString(payload),
		"frm_payload_hex":  strings.ToUpper(hex.EncodeToString(payload)),
		"rx_metadata":      fr.Meta,
		"settings":         settings,
		"received_at":      fr.At,
		"consumed_airtime": fmt.Sprintf("%.6fs", airtime/1000),
		"confirmed":        confirmed,
		"gateway_count":    len(fr.Meta),
		"lorawan":          d.Mode != "raw",
	}
	if fcnt != nil {
		up["f_cnt"] = *fcnt
	}
	if d.Location != nil {
		up["locations"] = map[string]any{"user": d.Location}
	}
	// Payload formatter: device override, else application formatter.
	fmtCfg := d.Uplink
	if fmtCfg.Type == "" || fmtCfg.Type == "inherit" {
		if app, err := p.store.Application(d.AppID); err == nil {
			fmtCfg = app.Uplink
		}
	}
	res := decode(fmtCfg, payload, fport, fr.At, d.Attributes)
	if res != nil {
		if res.Data != nil {
			up["decoded_payload"] = res.Data
		}
		if len(res.Warnings) > 0 {
			up["decoded_payload_warnings"] = res.Warnings
		}
		if len(res.Errors) > 0 {
			up["decoded_payload_errors"] = res.Errors
		}
	}
	return map[string]any{
		"end_device_ids":  ids,
		"correlation_ids": []string{"lorahub:uplink:" + store.NewToken(8)},
		"received_at":     fr.At,
		"uplink_message":  up,
	}
}

func (p *Processor) publishUplink(d *model.Device, msg map[string]any, fr *Frame) {
	up := msg["uplink_message"].(map[string]any)
	summary := fmt.Sprintf("FPort %v · %s", up["f_port"], truncHex(mustHex(up["frm_payload_hex"]), 16))
	if fc, ok := up["f_cnt"]; ok {
		summary = fmt.Sprintf("FCnt %v · ", fc) + summary
	}
	if dp, ok := up["decoded_payload"]; ok {
		summary += " · " + compactJSON(dp, 140)
	}
	p.emit(&model.Event{Name: "as.up", AppID: d.AppID, DevID: d.ID, Gateways: gatewayIDs(fr.Meta), Summary: summary, Data: msg}, true)
}

func mustHex(v any) []byte {
	s, _ := v.(string)
	b, _ := hex.DecodeString(s)
	return b
}

func (p *Processor) touchDevice(d *model.Device, fr *Frame, decoded any) func(*model.Device) {
	return func(dv *model.Device) {
		dv.LastSeen = &fr.At
		dv.UplinkCount++
		dv.LastRSSI = fr.Meta[0].RSSI
		dv.LastSNR = fr.Meta[0].SNR
		dv.LastGateway = fr.Meta[0].GatewayIDs.GatewayID
		dv.LastFreq = fr.Settings.Frequency
		dv.LastDataRate = fr.Settings.DataRate
		if decoded != nil {
			dv.LastDecoded = decoded
		}
	}
}
