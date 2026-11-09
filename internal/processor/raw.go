package processor

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/nor24o/lorahub/internal/band"
	"github.com/nor24o/lorahub/internal/formatter"
	"github.com/nor24o/lorahub/internal/lorawan"
	"github.com/nor24o/lorahub/internal/model"
	"github.com/nor24o/lorahub/internal/store"
)

var reCache sync.Map // string -> *regexp.Regexp

// MatchRaw reports whether payload matches a raw device rule and returns
// the number of matched prefix bytes.
func MatchRaw(m *model.RawMatch, payload []byte) (bool, int) {
	if m == nil || m.Value == "" {
		return false, 0
	}
	if m.MinLen > 0 && len(payload) < m.MinLen {
		return false, 0
	}
	if m.Length > 0 && len(payload) != m.Length {
		return false, 0
	}
	switch m.Type {
	case "prefix", "":
		v, err := hex.DecodeString(cleanHex(m.Value))
		if err != nil || len(v) == 0 {
			return false, 0
		}
		return bytes.HasPrefix(payload, v), len(v)
	case "bytes":
		v, err := hex.DecodeString(cleanHex(m.Value))
		if err != nil || len(v) == 0 || m.Offset < 0 || m.Offset+len(v) > len(payload) {
			return false, 0
		}
		return bytes.Equal(payload[m.Offset:m.Offset+len(v)], v), 0
	case "regex_hex":
		var re *regexp.Regexp
		if v, ok := reCache.Load(m.Value); ok {
			re = v.(*regexp.Regexp)
		} else {
			var err error
			re, err = regexp.Compile("(?i)" + m.Value)
			if err != nil {
				return false, 0
			}
			reCache.Store(m.Value, re)
		}
		return re.MatchString(strings.ToUpper(hex.EncodeToString(payload))), 0
	}
	return false, 0
}

func cleanHex(s string) string {
	return strings.NewReplacer(" ", "", ":", "", "0x", "", "-", "").Replace(strings.TrimSpace(s))
}

func (p *Processor) handleRaw(fr *Frame) bool {
	type cand struct {
		d     *model.Device
		score int
		strip int
	}
	var best *cand
	for _, d := range p.store.FindDevices(func(d *model.Device) bool { return d.Mode == "raw" && d.RawMatch != nil }) {
		ok, n := MatchRaw(d.RawMatch, fr.Payload)
		if !ok {
			continue
		}
		score := len(cleanHex(d.RawMatch.Value))
		if best == nil || score > best.score {
			strip := 0
			if d.RawMatch.StripPrefix {
				strip = n
			}
			best = &cand{d, score, strip}
		}
	}
	if best == nil {
		return false
	}
	d := best.d
	unlock := p.lockDevice(d.AppID, d.ID)
	defer unlock()
	payload := fr.Payload[best.strip:]
	fport := d.RawFPort
	if fport <= 0 {
		fport = 1
	}
	msg := p.buildUplink(d, fr, fport, nil, payload, false, "")
	decoded := msg["uplink_message"].(map[string]any)["decoded_payload"]
	updated, err := p.store.UpdateDevice(d.AppID, d.ID, func(dv *model.Device) error {
		p.touchDevice(dv, fr, decoded)(dv)
		return nil
	})
	if err != nil {
		return true
	}
	p.publishUplink(updated, msg, fr)
	if len(updated.Queue) > 0 && updated.RawDownlink != nil && updated.RawDownlink.Mode == "after_uplink" {
		p.sendRawDown(updated, fr)
	}
	return true
}

// sendRawDown transmits the first queued item of a raw device. If fr is
// nil it is sent immediately via the last gateway that heard the device.
func (p *Processor) sendRawDown(d *model.Device, fr *Frame) error {
	if len(d.Queue) == 0 {
		return nil
	}
	item := d.Queue[0]
	app, _ := p.store.Application(d.AppID)
	cfg := downlinkCfg(app, d)
	var g *model.Gateway
	var tmst uint32
	freq := uint64(cfg.Frequency * 1e6)
	datr := cfg.DataRate
	if fr != nil {
		gws := p.pickGateways(fr.Meta)
		if len(gws) == 0 {
			return p.downFailed(d, "no gateway with downlink capability heard the device")
		}
		g, tmst = gws[0].gw, gws[0].meta.Timestamp
		if freq == 0 {
			freq = fr.Settings.Frequency
		}
		if datr == "" {
			datr = fr.Settings.DataRate
		}
	} else {
		var err error
		g, err = p.store.Gateway(d.LastGateway)
		if err != nil {
			return p.downFailed(d, "device has not been heard by any gateway yet")
		}
	}
	if freq == 0 {
		freq = d.LastFreq
	}
	if datr == "" {
		datr = d.LastDataRate
	}
	if freq == 0 || datr == "" {
		return p.downFailed(d, "no frequency / data rate known yet: wait for a packet from the node or set them in the group settings")
	}
	if !g.Downlink || g.Disabled {
		return p.downFailed(d, "downlinks are turned off for gateway "+g.ID)
	}
	dl := p.downlinker(g.Kind)
	if dl == nil || !dl.CanSend(g.EUI) {
		return p.downFailed(d, "gateway "+g.ID+" cannot send downlinks right now")
	}
	cr := cfg.CodingRate
	if cr == "" {
		cr = "4/5"
	}
	power := cfg.Power
	if power == 0 {
		power = 14
	}
	tx := model.TxPacket{Payload: item.Payload, Frequency: freq, DataRate: datr, CodingRate: cr, Power: TxPowerFor(g, power),
		InvertPol: cfg.InvertPol, Immediate: fr == nil}
	if fr != nil {
		delay := cfg.DelayMs
		if delay <= 0 {
			delay = 1000
		}
		tx.Timestamp = tmst + uint32(delay*1000)
	}
	if err := p.reserveAirtime(g, tx); err != nil {
		return p.downFailed(d, err.Error())
	}
	if err := dl.Send(g.EUI, tx); err != nil {
		return p.downFailed(d, err.Error())
	}
	p.countTx(g.ID)
	p.store.UpdateDevice(d.AppID, d.ID, func(dv *model.Device) error {
		if len(dv.Queue) > 0 && dv.Queue[0].ID == item.ID {
			dv.Queue = dv.Queue[1:]
		}
		return nil
	})
	p.emit(&model.Event{Name: "ns.down.sent", AppID: d.AppID, DevID: d.ID, Gateways: []string{g.ID},
		Summary: fmt.Sprintf("Raw downlink via %s · %.3f MHz %s · %s", g.ID, float64(freq)/1e6, datr, truncHex(item.Payload, 16)),
		Data:    map[string]any{"gateway_id": g.ID, "frm_payload_hex": strings.ToUpper(hex.EncodeToString(item.Payload)), "tx": tx}}, true)
	return nil
}

func (p *Processor) downFailed(d *model.Device, msg string) error {
	p.emit(&model.Event{Name: "ns.down.failed", AppID: d.AppID, DevID: d.ID, Summary: "Downlink failed: " + msg, Data: map[string]any{"error": msg}}, true)
	return fmt.Errorf("%s", msg)
}

// EnqueueDownlink queues (and for raw immediate / class C devices sends) a
// downlink. If decoded is non-nil the downlink formatter encodes it.
func (p *Processor) EnqueueDownlink(appID, devID string, fport int, payload []byte, decoded any, confirmed, replace bool) (*model.QueuedDownlink, error) {
	d, err := p.store.Device(appID, devID)
	if err != nil {
		return nil, err
	}
	if decoded != nil && payload == nil {
		f := d.Downlink
		if f.Type == "" || f.Type == "inherit" {
			if app, err := p.store.Application(appID); err == nil {
				f = app.Downlink
			}
		}
		if f.Type != "javascript" {
			return nil, fmt.Errorf("decoded_payload given but no JavaScript downlink formatter (encodeDownlink) configured")
		}
		payload, fport, err = formatter.EncodeJS(f.Code, decoded, fport)
		if err != nil {
			return nil, fmt.Errorf("encodeDownlink: %w", err)
		}
	}
	if d.Mode == "node" {
		if app, err := p.store.Application(appID); err == nil {
			payload = p.NodeDownlinkPayload(app, d, payload)
		}
	}
	if (d.Mode == "otaa" || d.Mode == "abp") && (fport < 1 || fport > 223) {
		return nil, fmt.Errorf("f_port must be 1..223")
	}
	if len(payload) > 242 {
		return nil, fmt.Errorf("payload too long (%d bytes)", len(payload))
	}
	item := model.QueuedDownlink{ID: store.NewToken(6), FPort: fport, Payload: payload, Confirmed: confirmed, CreatedAt: time.Now().UTC(), Decoded: decoded}
	updated, err := p.store.UpdateDevice(appID, devID, func(dv *model.Device) error {
		if replace {
			dv.Queue = nil
		}
		if len(dv.Queue) >= 16 {
			return fmt.Errorf("downlink queue full")
		}
		dv.Queue = append(dv.Queue, item)
		return nil
	})
	if err != nil {
		return nil, err
	}
	p.emit(&model.Event{Name: "as.down.queued", AppID: appID, DevID: devID,
		Summary: fmt.Sprintf("Downlink queued · FPort %d · %s", fport, truncHex(payload, 16)),
		Data:    map[string]any{"id": item.ID, "f_port": fport, "frm_payload_hex": strings.ToUpper(hex.EncodeToString(payload)), "confirmed": confirmed, "decoded_payload": decoded}}, true)

	isRaw := updated.Mode == "raw" || updated.Mode == "node"
	app, _ := p.store.Application(appID)
	if isRaw && downlinkCfg(app, updated).Mode != "after_uplink" {
		go p.sendRawDown(updated, nil)
	} else if !isRaw && updated.Class == "C" && updated.Session != nil {
		go p.sendClassC(updated)
	}
	return &item, nil
}

// sendClassC sends the next downlink immediately on RX2 parameters.
func (p *Processor) sendClassC(d *model.Device) {
	unlock := p.lockDevice(d.AppID, d.ID)
	defer unlock()
	d, err := p.store.Device(d.AppID, d.ID)
	if err != nil || len(d.Queue) == 0 || d.Session == nil {
		return
	}
	g, err := p.store.Gateway(d.LastGateway)
	if err != nil {
		p.downFailed(d, "class C device has not been heard by any gateway")
		return
	}
	dl := p.downlinker(g.Kind)
	if dl == nil || !dl.CanSend(g.EUI) {
		p.downFailed(d, "gateway "+g.ID+" cannot send downlinks right now")
		return
	}
	nwk, _ := lorawan.ParseKey(d.Session.NwkSKey)
	appS, _ := lorawan.ParseKey(d.Session.AppSKey)
	q := d.Queue[0]
	fp := q.FPort
	phy := lorawan.BuildDataDown(lorawan.DataDown{DevAddr: mustAddr(d.Session.DevAddr), FCnt: d.Session.FCntDown, FPort: &fp,
		Payload: q.Payload, Confirmed: q.Confirmed, FPending: len(d.Queue) > 1}, nwk, appS)
	b := band.Get(d.Band)
	power := b.RX2Power
	if g.TxPower > 0 && g.TxPower < power {
		power = g.TxPower
	}
	tx := model.TxPacket{Payload: phy, Frequency: b.RX2Freq, DataRate: b.RX2DataRate.String(), CodingRate: "4/5", Power: power,
		InvertPol: true, Immediate: true, NoCRC: true}
	if err := dl.Send(g.EUI, tx); err != nil {
		p.downFailed(d, err.Error())
		return
	}
	p.countTx(g.ID)
	p.store.UpdateDevice(d.AppID, d.ID, func(dv *model.Device) error {
		dv.Session.FCntDown++
		if len(dv.Queue) > 0 && dv.Queue[0].ID == q.ID {
			dv.Queue = dv.Queue[1:]
		}
		return nil
	})
	p.emit(&model.Event{Name: "ns.down.sent", AppID: d.AppID, DevID: d.ID, Gateways: []string{g.ID},
		Summary: fmt.Sprintf("Class C downlink FCnt %d via %s · FPort %d", d.Session.FCntDown, g.ID, q.FPort),
		Data:    map[string]any{"gateway_id": g.ID, "f_port": q.FPort, "frm_payload_hex": strings.ToUpper(hex.EncodeToString(q.Payload))}}, true)
}

// ---------------- helpers ----------------

func decode(f model.Formatter, payload []byte, fport int, t time.Time, attrs map[string]string) *formatter.Result {
	var meta map[string]any
	if len(attrs) > 0 {
		meta = map[string]any{}
		for k, v := range attrs {
			meta[k] = v
		}
	}
	return formatter.Decode(f, formatter.Input{Bytes: payload, FPort: fport, RecvTime: t, Meta: meta})
}

func jsonMarshal(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

func jsonUnmarshal(b []byte, v any) { json.Unmarshal(b, v) }

func num(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case int:
		return float64(x), true
	case uint32:
		return float64(x), true
	}
	return 0, false
}

func compactJSON(v any, max int) string {
	b, _ := json.Marshal(v)
	s := string(b)
	if len(s) > max {
		s = s[:max] + "…"
	}
	return s
}
