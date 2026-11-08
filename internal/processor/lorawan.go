package processor

import (
	"encoding/hex"
	"fmt"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/nor24o/lorahub/internal/band"
	"github.com/nor24o/lorahub/internal/lorawan"
	"github.com/nor24o/lorahub/internal/model"
)

var requiredSNR = map[int]float64{7: -7.5, 8: -10, 9: -12.5, 10: -15, 11: -17.5, 12: -20}

func (p *Processor) handleJoin(fr *Frame, f *lorawan.Frame) bool {
	devEUI := f.DevEUI.String()
	cands := p.store.FindDevices(func(d *model.Device) bool {
		return d.Mode == "otaa" && strings.EqualFold(d.DevEUI, devEUI)
	})
	if len(cands) == 0 {
		return false
	}
	d := cands[0]
	unlock := p.lockDevice(d.AppID, d.ID)
	defer unlock()

	appKey, err := lorawan.ParseKey(d.AppKey)
	if err != nil || !f.VerifyJoinMIC(appKey) {
		p.emit(&model.Event{Name: "ns.join.mic_failed", AppID: d.AppID, DevID: d.ID, Gateways: gatewayIDs(fr.Meta),
			Summary: "Join request MIC check failed (wrong AppKey?)", Data: map[string]any{"dev_eui": devEUI, "join_eui": f.JoinEUI.String()}}, true)
		return true
	}
	for _, n := range d.UsedNonces {
		if n == f.DevNonce {
			p.emit(&model.Event{Name: "ns.join.nonce_reused", AppID: d.AppID, DevID: d.ID, Gateways: gatewayIDs(fr.Meta),
				Summary: fmt.Sprintf("DevNonce %d already used, join ignored", f.DevNonce), Data: map[string]any{"dev_nonce": f.DevNonce}}, true)
			return true
		}
	}

	s := p.Settings()
	b := band.Get(d.Band)
	nb, _ := hex.DecodeString(s.NetID)
	var netID [3]byte
	netID[0], netID[1], netID[2] = nb[2], nb[1], nb[0]
	var appNonce [3]byte
	copy(appNonce[:], lorawan.RandomBytes(3))
	prefix, _ := lorawan.ParseDevAddr(s.DevAddrPrefix)
	bits := s.DevAddrPrefixBits
	if bits < 0 || bits > 31 {
		bits = 7
	}
	mask := uint32(0xFFFFFFFF) << (32 - uint(bits))
	if bits == 0 {
		mask = 0
	}
	devAddr := (prefix & mask) | (rand.Uint32() &^ mask)

	nwk, app := lorawan.DeriveSessionKeys(appKey, appNonce, netID, f.DevNonce)
	var cflist []byte
	if b.Name == "EU868" {
		for _, fq := range []uint32{8671000, 8673000, 8675000, 8677000, 8679000} {
			cflist = append(cflist, byte(fq), byte(fq>>8), byte(fq>>16))
		}
		cflist = append(cflist, 0)
	}
	ja := lorawan.BuildJoinAccept(lorawan.JoinAcceptParams{AppNonce: appNonce, NetID: netID, DevAddr: devAddr,
		RX2DR: uint8(b.RX2DR), RxDelay: uint8(s.RX1Delay), CFList: cflist}, appKey)

	// Persist session before transmitting.
	updated, err := p.store.UpdateDevice(d.AppID, d.ID, func(dv *model.Device) error {
		dv.Session = &model.Session{DevAddr: lorawan.DevAddrString(devAddr), NwkSKey: strings.ToUpper(hex.EncodeToString(nwk[:])),
			AppSKey: strings.ToUpper(hex.EncodeToString(app[:])), StartedAt: time.Now().UTC()}
		dv.UsedNonces = append(dv.UsedNonces, f.DevNonce)
		if len(dv.UsedNonces) > 64 {
			dv.UsedNonces = dv.UsedNonces[len(dv.UsedNonces)-64:]
		}
		p.touchDevice(dv, fr, nil)(dv)
		return nil
	})
	if err != nil {
		return true
	}

	gw, txErr := p.scheduleClassA(fr, b, ja, time.Duration(b.JoinDelay1)*time.Second, true)
	data := map[string]any{
		"end_device_ids": map[string]any{"device_id": d.ID, "application_ids": map[string]string{"application_id": d.AppID},
			"dev_eui": devEUI, "join_eui": f.JoinEUI.String(), "dev_addr": updated.Session.DevAddr},
		"join_accept": map[string]any{"session_key_id": appNonce, "received_at": fr.At, "dev_nonce": f.DevNonce},
		"rx_metadata": fr.Meta,
		"settings":    fr.Settings,
	}
	summary := "Join accepted · DevAddr " + updated.Session.DevAddr
	if txErr != nil {
		summary += " · downlink failed: " + txErr.Error()
		data["downlink_error"] = txErr.Error()
	} else {
		summary += " · via " + gw
		data["downlink_gateway"] = gw
	}
	p.emit(&model.Event{Name: "ns.join.accept", AppID: d.AppID, DevID: d.ID, Gateways: gatewayIDs(fr.Meta), Summary: summary, Data: data}, true)
	return true
}

func (p *Processor) handleDataUp(fr *Frame, f *lorawan.Frame) bool {
	addr := lorawan.DevAddrString(f.DevAddr)
	cands := p.store.FindDevices(func(d *model.Device) bool {
		return (d.Mode == "otaa" || d.Mode == "abp") && d.Session != nil && strings.EqualFold(d.Session.DevAddr, addr)
	})
	for _, c := range cands {
		if p.tryDataUp(fr, f, c) {
			return true
		}
	}
	if len(cands) > 0 {
		p.emit(&model.Event{Name: "ns.up.mic_failed", Gateways: gatewayIDs(fr.Meta), AppID: cands[0].AppID, DevID: cands[0].ID,
			Summary: "Uplink MIC check failed for DevAddr " + addr + " (wrong session keys or counter)",
			Data:    map[string]any{"dev_addr": addr, "f_cnt": f.FCnt16, "raw_payload_hex": strings.ToUpper(hex.EncodeToString(fr.Payload))}}, true)
		return true
	}
	return false
}

func (p *Processor) tryDataUp(fr *Frame, f *lorawan.Frame, c *model.Device) bool {
	unlock := p.lockDevice(c.AppID, c.ID)
	defer unlock()
	d, err := p.store.Device(c.AppID, c.ID)
	if err != nil || d.Session == nil {
		return false
	}
	nwk, err1 := lorawan.ParseKey(d.Session.NwkSKey)
	appS, err2 := lorawan.ParseKey(d.Session.AppSKey)
	if err1 != nil || err2 != nil {
		return false
	}
	fcnt := uint32(f.FCnt16)
	if d.Session.HasUplink {
		fcnt = lorawan.FullFCnt(d.Session.FCntUp, f.FCnt16)
	}
	ok := f.VerifyDataMIC(nwk, fcnt)
	if !ok && fcnt != uint32(f.FCnt16) {
		// Device may have been reset (counter restarted)
		if f.VerifyDataMIC(nwk, uint32(f.FCnt16)) {
			fcnt, ok = uint32(f.FCnt16), true
		}
	}
	if !ok {
		return false
	}
	confirmed := f.MType == lorawan.ConfirmedDataUp
	if d.Session.HasUplink && fcnt <= d.Session.FCntUp && !d.SkipFCntCheck {
		if fcnt == d.Session.FCntUp {
			p.emit(&model.Event{Name: "ns.up.retransmission", AppID: d.AppID, DevID: d.ID, Gateways: gatewayIDs(fr.Meta),
				Summary: fmt.Sprintf("Retransmission of FCnt %d", fcnt), Data: map[string]any{"f_cnt": fcnt, "rx_metadata": fr.Meta}}, true)
			if confirmed {
				p.sendLoRaWANDown(fr, d, true, nil, nil)
			}
			return true
		}
		p.emit(&model.Event{Name: "ns.up.fcnt_rejected", AppID: d.AppID, DevID: d.ID, Gateways: gatewayIDs(fr.Meta),
			Summary: fmt.Sprintf("FCnt %d <= last %d — possible replay; enable 'skip frame counter check' or reset session", fcnt, d.Session.FCntUp),
			Data:    map[string]any{"f_cnt": fcnt, "last_f_cnt": d.Session.FCntUp}}, true)
		return true
	}

	plain := f.Decrypt(nwk, appS, fcnt)
	fport := -1
	if f.FPort != nil {
		fport = *f.FPort
	}

	// MAC commands (FOpts or FPort 0)
	var macCmds []byte
	if fport == 0 {
		macCmds = plain
	} else {
		macCmds = f.FOpts
	}
	var macAns []byte
	for i := 0; i < len(macCmds); i++ {
		switch macCmds[i] {
		case 0x02: // LinkCheckReq
			margin := fr.Meta[0].SNR - requiredSNR[fr.Settings.SF]
			if margin < 0 {
				margin = 0
			}
			macAns = append(macAns, 0x02, byte(margin), byte(len(fr.Meta)))
		case 0x0D: // DeviceTimeReq
			gps := time.Now().UTC().Sub(time.Date(1980, 1, 6, 0, 0, 0, 0, time.UTC)) + 18*time.Second
			secs := uint32(gps / time.Second)
			frac := byte((gps % time.Second) * 256 / time.Second)
			macAns = append(macAns, 0x0D, byte(secs), byte(secs>>8), byte(secs>>16), byte(secs>>24), frac)
		default:
			i = len(macCmds) // unknown command: stop parsing
		}
	}

	var msg map[string]any
	if fport > 0 {
		msg = p.buildUplink(d, fr, fport, &fcnt, plain, confirmed, d.Session.DevAddr)
	}
	var decoded any
	if msg != nil {
		decoded = msg["uplink_message"].(map[string]any)["decoded_payload"]
	}
	updated, err := p.store.UpdateDevice(d.AppID, d.ID, func(dv *model.Device) error {
		dv.Session.FCntUp = fcnt
		dv.Session.HasUplink = true
		p.touchDevice(dv, fr, decoded)(dv)
		return nil
	})
	if err != nil {
		return true
	}
	if msg != nil {
		p.publishUplink(updated, msg, fr)
	} else {
		p.emit(&model.Event{Name: "ns.up.mac", AppID: d.AppID, DevID: d.ID, Gateways: gatewayIDs(fr.Meta),
			Summary: fmt.Sprintf("MAC-only uplink FCnt %d", fcnt), Data: map[string]any{"f_cnt": fcnt, "mac_commands": strings.ToUpper(hex.EncodeToString(macCmds))}}, true)
	}

	if confirmed || len(updated.Queue) > 0 || len(macAns) > 0 {
		p.sendLoRaWANDown(fr, updated, confirmed, macAns, nil)
	}
	return true
}

// sendLoRaWANDown sends the next queued downlink (or an empty ACK / MAC
// answer) in RX1 (falling back to RX2) of the given uplink.
func (p *Processor) sendLoRaWANDown(fr *Frame, d *model.Device, ack bool, macAns []byte, _ any) {
	nwk, _ := lorawan.ParseKey(d.Session.NwkSKey)
	appS, _ := lorawan.ParseKey(d.Session.AppSKey)
	dn := lorawan.DataDown{DevAddr: mustAddr(d.Session.DevAddr), ACK: ack, FCnt: d.Session.FCntDown}
	var item *model.QueuedDownlink
	if len(d.Queue) > 0 {
		q := d.Queue[0]
		item = &q
		fp := q.FPort
		dn.FPort = &fp
		dn.Payload = q.Payload
		dn.Confirmed = q.Confirmed
		dn.FPending = len(d.Queue) > 1
		if len(macAns) <= 15 {
			dn.FOpts = macAns
		}
	} else if len(macAns) > 0 {
		if len(macAns) <= 15 {
			dn.FOpts = macAns
		} else {
			z := 0
			dn.FPort, dn.Payload = &z, macAns
		}
	}
	phy := lorawan.BuildDataDown(dn, nwk, appS)
	b := band.Get(d.Band)
	gw, err := p.scheduleClassA(fr, b, phy, time.Duration(p.Settings().RX1Delay)*time.Second, false)
	if err != nil {
		p.emit(&model.Event{Name: "ns.down.failed", AppID: d.AppID, DevID: d.ID, Summary: "Downlink failed: " + err.Error(),
			Data: map[string]any{"error": err.Error()}}, true)
		return
	}
	p.store.UpdateDevice(d.AppID, d.ID, func(dv *model.Device) error {
		if dv.Session != nil {
			dv.Session.FCntDown = dn.FCnt + 1
		}
		if item != nil && len(dv.Queue) > 0 && dv.Queue[0].ID == item.ID {
			dv.Queue = dv.Queue[1:]
		}
		return nil
	})
	data := map[string]any{"f_cnt": dn.FCnt, "ack": ack, "gateway_id": gw, "phy_payload_hex": strings.ToUpper(hex.EncodeToString(phy))}
	summary := fmt.Sprintf("Downlink FCnt %d via %s", dn.FCnt, gw)
	if item != nil {
		data["f_port"], data["frm_payload_hex"], data["confirmed"] = item.FPort, strings.ToUpper(hex.EncodeToString(item.Payload)), item.Confirmed
		summary += fmt.Sprintf(" · FPort %d · %s", item.FPort, truncHex(item.Payload, 16))
	} else if ack {
		summary += " · ACK"
	}
	if len(macAns) > 0 {
		data["mac_answers"] = strings.ToUpper(hex.EncodeToString(macAns))
	}
	p.emit(&model.Event{Name: "ns.down.sent", AppID: d.AppID, DevID: d.ID, Gateways: []string{gw}, Summary: summary, Data: data}, true)
}

func mustAddr(s string) uint32 {
	a, _ := lorawan.ParseDevAddr(s)
	return a
}

// pickGateways returns gateways of the frame able to send downlinks.
func (p *Processor) pickGateways(meta []model.RxMetadata) []struct {
	gw   *model.Gateway
	meta model.RxMetadata
	dl   Downlinker
} {
	var out []struct {
		gw   *model.Gateway
		meta model.RxMetadata
		dl   Downlinker
	}
	for _, m := range meta {
		g, err := p.store.Gateway(m.GatewayIDs.GatewayID)
		if err != nil || !g.Downlink || g.Disabled {
			continue
		}
		dl := p.downlinker(g.Kind)
		if dl == nil || !dl.CanSend(g.EUI) {
			continue
		}
		out = append(out, struct {
			gw   *model.Gateway
			meta model.RxMetadata
			dl   Downlinker
		}{g, m, dl})
	}
	return out
}

// scheduleClassA transmits phy in RX1, or RX2 if RX1 fails.
func (p *Processor) scheduleClassA(fr *Frame, b *band.Band, phy []byte, delay time.Duration, join bool) (string, error) {
	gws := p.pickGateways(fr.Meta)
	if len(gws) == 0 {
		return "", fmt.Errorf("no gateway with downlink capability received this uplink")
	}
	g := gws[0]
	up := band.DataRate{SF: fr.Settings.SF, BW: fr.Settings.BW}
	f1, dr1 := b.RX1(fr.Settings.Frequency, up)
	power := b.RX1Power
	if g.gw.TxPower > 0 {
		power = g.gw.TxPower
	}
	tx := model.TxPacket{Payload: phy, Frequency: f1, DataRate: dr1.String(), CodingRate: "4/5", Power: power, InvertPol: true,
		Timestamp: g.meta.Timestamp + uint32(delay/time.Microsecond), RFChain: 0, NoCRC: true}
	if err := g.dl.Send(g.gw.EUI, tx); err == nil {
		p.countTx(g.gw.ID)
		return g.gw.ID, nil
	}
	tx.Frequency, tx.DataRate = b.RX2Freq, b.RX2DataRate.String()
	tx.Power = b.RX2Power
	if g.gw.TxPower > 0 && g.gw.TxPower < tx.Power {
		tx.Power = g.gw.TxPower
	}
	tx.Timestamp = g.meta.Timestamp + uint32((delay+time.Second)/time.Microsecond)
	if err := g.dl.Send(g.gw.EUI, tx); err != nil {
		return g.gw.ID, err
	}
	p.countTx(g.gw.ID)
	return g.gw.ID, nil
}

func (p *Processor) countTx(gwID string) {
	p.store.UpdateGateway(gwID, false, func(g *model.Gateway) { g.TxCount++ })
}
