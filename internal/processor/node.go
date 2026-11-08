package processor

import (
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/nor24o/lorahub/internal/band"
	"github.com/nor24o/lorahub/internal/config"
	"github.com/nor24o/lorahub/internal/formatter"
	"github.com/nor24o/lorahub/internal/model"
)

// NodeHeader is the parsed NODE_ID | SERIAL | data header of a packet.
type NodeHeader struct {
	NodeID string // upper-case hex
	Serial string // upper-case hex
	Data   []byte
}

// ParseNodeHeader splits a payload according to the packet format.
func ParseNodeHeader(pf config.PacketFormat, b []byte) (NodeHeader, bool) {
	if pf.NodeIDLength <= 0 || pf.SerialLength <= 0 {
		return NodeHeader{}, false
	}
	end := max(pf.NodeIDOffset+pf.NodeIDLength, pf.SerialOffset+pf.SerialLength, pf.DataOffset)
	if pf.NodeIDOffset < 0 || pf.SerialOffset < 0 || len(b) < end {
		return NodeHeader{}, false
	}
	return NodeHeader{
		NodeID: strings.ToUpper(hex.EncodeToString(b[pf.NodeIDOffset : pf.NodeIDOffset+pf.NodeIDLength])),
		Serial: strings.ToUpper(hex.EncodeToString(b[pf.SerialOffset : pf.SerialOffset+pf.SerialLength])),
		Data:   b[pf.DataOffset:],
	}, true
}

func (p *Processor) groupByNodeID(nodeID string) *model.Application {
	for _, a := range p.store.Applications() {
		if strings.EqualFold(a.NodeID, nodeID) {
			return a
		}
	}
	return nil
}

// handleNode processes NODE_ID | SERIAL | data packets.
func (p *Processor) handleNode(fr *Frame) bool {
	s := p.Settings()
	hdr, ok := ParseNodeHeader(s.PacketFormat, fr.Payload)
	if !ok {
		return false
	}
	app := p.groupByNodeID(hdr.NodeID)
	if app == nil {
		if !s.AutoCreateGroups {
			return false
		}
		now := time.Now().UTC()
		app = &model.Application{ID: "node-" + strings.ToLower(hdr.NodeID), Name: "Node " + hdr.NodeID, NodeID: hdr.NodeID,
			AutoRegister: true, Uplink: model.Formatter{Type: "none"}, Downlink: model.Formatter{Type: "none"}, CreatedAt: now, UpdatedAt: now}
		if err := p.store.SaveApplication(app); err != nil {
			return false
		}
		p.emit(&model.Event{Name: "as.group.created", AppID: app.ID, Summary: "Node group " + hdr.NodeID + " created automatically",
			Data: map[string]any{"node_id": hdr.NodeID}}, true)
	}
	devs := p.store.FindDevices(func(d *model.Device) bool {
		return d.AppID == app.ID && d.Mode == "node" && strings.EqualFold(d.Serial, hdr.Serial)
	})
	var d *model.Device
	if len(devs) > 0 {
		d = devs[0]
	} else {
		if !app.AutoRegister {
			p.emit(&model.Event{Name: "ns.up.unmatched", AppID: app.ID, Gateways: gatewayIDs(fr.Meta),
				Summary: fmt.Sprintf("Unknown serial %s in group %s (auto-register is off)", hdr.Serial, hdr.NodeID),
				Data:    p.unmatchedData(fr, &hdr)}, true)
			return true
		}
		now := time.Now().UTC()
		d = &model.Device{ID: strings.ToLower(hdr.Serial), AppID: app.ID, Mode: "node", Serial: hdr.Serial, Name: hdr.NodeID + "-" + hdr.Serial,
			Uplink: model.Formatter{Type: "inherit"}, Downlink: model.Formatter{Type: "inherit"}, CreatedAt: now, UpdatedAt: now}
		if err := p.store.SaveDevice(d); err != nil {
			p.log.Warn("auto-register node", "err", err)
			return true
		}
		p.emit(&model.Event{Name: "as.node.registered", AppID: app.ID, DevID: d.ID, Gateways: gatewayIDs(fr.Meta),
			Summary: fmt.Sprintf("New node %s / %s registered automatically", hdr.NodeID, hdr.Serial), Data: map[string]any{"node_id": hdr.NodeID, "serial": hdr.Serial}}, true)
	}

	unlock := p.lockDevice(d.AppID, d.ID)
	defer unlock()
	msg := p.buildNodeMessage(app, d, &hdr, fr)
	updated, err := p.store.UpdateDevice(d.AppID, d.ID, func(dv *model.Device) error {
		p.touchDevice(dv, fr, msg["decoded"])(dv)
		return nil
	})
	if err != nil {
		return true
	}
	summary := fmt.Sprintf("%s / %s · %s", hdr.NodeID, hdr.Serial, truncHex(hdr.Data, 16))
	if dec, ok := msg["decoded"]; ok {
		summary += " · " + compactJSON(dec, 140)
	}
	p.emit(&model.Event{Name: "as.up", AppID: d.AppID, DevID: d.ID, Gateways: gatewayIDs(fr.Meta), Summary: summary, Data: msg}, true)
	if len(updated.Queue) > 0 {
		if cfg := downlinkCfg(app, updated); cfg.Mode == "after_uplink" {
			p.sendRawDown(updated, fr)
		}
	}
	return true
}

func downlinkCfg(app *model.Application, d *model.Device) model.RawDownlink {
	if d != nil && d.RawDownlink != nil {
		return *d.RawDownlink
	}
	if app != nil && app.RawDownlink != nil {
		return *app.RawDownlink
	}
	return model.RawDownlink{Mode: "immediate", Power: 14}
}

// gatewayInfo is the per-gateway metadata in node messages.
func (p *Processor) gatewayInfo(meta []model.RxMetadata) []map[string]any {
	out := make([]map[string]any, 0, len(meta))
	for _, m := range meta {
		g := map[string]any{
			"gateway_id": m.GatewayIDs.GatewayID, "eui": m.GatewayIDs.EUI, "source": m.Source,
			"rssi": m.RSSI, "snr": m.SNR, "channel": m.Channel, "rf_chain": m.RFChain,
			"frequency_offset": m.FreqOffset, "timestamp": m.Timestamp, "received_at": m.ReceivedAt,
		}
		if gw, err := p.store.Gateway(m.GatewayIDs.GatewayID); err == nil && gw.Name != "" {
			g["name"] = gw.Name
		}
		if m.SignalRSSI != nil {
			g["signal_rssi"] = *m.SignalRSSI
		}
		if m.Time != nil {
			g["time"] = m.Time
		}
		if m.Location != nil {
			g["location"] = m.Location
		}
		out = append(out, g)
	}
	return out
}

func (p *Processor) buildNodeMessage(app *model.Application, d *model.Device, hdr *NodeHeader, fr *Frame) map[string]any {
	dr := band.DataRate{SF: fr.Settings.SF, BW: fr.Settings.BW}
	msg := map[string]any{
		"node_id":       hdr.NodeID,
		"serial":        hdr.Serial,
		"group":         app.ID,
		"device":        d.ID,
		"name":          d.Name,
		"received_at":   fr.At,
		"payload_hex":   strings.ToUpper(hex.EncodeToString(fr.Payload)),
		"data_hex":      strings.ToUpper(hex.EncodeToString(hdr.Data)),
		"size":          len(fr.Payload),
		"gateway_count": len(fr.Meta),
		"gateways":      p.gatewayInfo(fr.Meta),
		"radio": map[string]any{
			"frequency": fr.Settings.Frequency, "data_rate": fr.Settings.DataRate, "spreading_factor": fr.Settings.SF,
			"bandwidth_khz": fr.Settings.BW, "coding_rate": fr.Settings.CodingRate,
			"airtime_ms": float64(int(band.AirtimeMs(len(fr.Payload), dr, fr.Settings.CodingRate, 8, true, true)*100)) / 100,
		},
	}
	if d.Location != nil {
		msg["location"] = d.Location
	}
	f := d.Uplink
	if f.Type == "" || f.Type == "inherit" {
		f = app.Uplink
	}
	var attrs map[string]any
	if len(d.Attributes) > 0 {
		attrs = map[string]any{}
		for k, v := range d.Attributes {
			attrs[k] = v
		}
	}
	res := formatter.Decode(f, formatter.Input{Bytes: hdr.Data, FPort: 1, RecvTime: fr.At, Meta: attrs,
		Vars: map[string]any{"node_id": hdr.NodeID, "serial": hdr.Serial, "payload": fr.Payload}})
	if res != nil {
		if res.Data != nil {
			msg["decoded"] = res.Data
		}
		if len(res.Warnings) > 0 {
			msg["decoder_warnings"] = res.Warnings
		}
		if len(res.Errors) > 0 {
			msg["decoder_errors"] = res.Errors
		}
	}
	return msg
}

func (p *Processor) unmatchedData(fr *Frame, hdr *NodeHeader) map[string]any {
	data := map[string]any{
		"received_at": fr.At,
		"payload_hex": strings.ToUpper(hex.EncodeToString(fr.Payload)),
		"gateways":    p.gatewayInfo(fr.Meta),
		"radio":       fr.Settings,
	}
	if hdr != nil {
		data["node_id"], data["serial"], data["data_hex"] = hdr.NodeID, hdr.Serial, strings.ToUpper(hex.EncodeToString(hdr.Data))
	}
	return data
}

// NodeDownlinkPayload prepends NODE_ID+SERIAL when the group requests it.
func (p *Processor) NodeDownlinkPayload(app *model.Application, d *model.Device, data []byte) []byte {
	if d.Mode != "node" || !app.DownlinkHeader {
		return data
	}
	nid, _ := hex.DecodeString(app.NodeID)
	ser, _ := hex.DecodeString(d.Serial)
	out := append(append(append([]byte{}, nid...), ser...), data...)
	return out
}
