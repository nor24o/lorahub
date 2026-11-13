package processor

import (
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/nor24o/lorahub/internal/config"
	"github.com/nor24o/lorahub/internal/model"
)

// FSK bridge: FSK packets are not node packets. With the bridge on, an FSK
// packet heard by any gateway skips node processing, Home Assistant and rules,
// and goes to FSKOut as it is, once, with the signal of every gateway that
// heard it. SendFSK puts bytes on the air as FSK. The UDP side is in
// internal/integration/fskbridge.go.

// FSKSend says how one packet goes on the air; zero values take the bridge
// settings.
type FSKSend struct {
	Gateway   string  `json:"gateway,omitempty"`
	Frequency float64 `json:"frequency,omitempty"` // MHz
	BitRate   int     `json:"bit_rate,omitempty"`
	Deviation int     `json:"deviation,omitempty"` // kHz
	Power     int     `json:"power,omitempty"`
	Preamble  int     `json:"preamble,omitempty"` // bytes
}

// handleFSK takes an FSK packet when the bridge is on.
func (p *Processor) handleFSK(fr *Frame) bool {
	if !strings.EqualFold(fr.Settings.Modulation, "FSK") || !p.Settings().FSKBridge.Enabled {
		return false
	}
	gws := make([]map[string]any, 0, len(fr.Meta))
	for _, m := range fr.Meta {
		gws = append(gws, map[string]any{"gateway_id": m.GatewayIDs.GatewayID, "eui": m.GatewayIDs.EUI, "rssi": m.RSSI, "timestamp": m.Timestamp})
	}
	if len(fr.Meta) > 0 {
		p.fskMu.Lock()
		p.fskBest = fr.Meta[0].GatewayIDs.GatewayID
		p.fskMu.Unlock()
	}
	freq := float64(fr.Settings.Frequency) / 1e6
	msg := map[string]any{
		"data_hex":    strings.ToUpper(hex.EncodeToString(fr.Payload)),
		"size":        len(fr.Payload),
		"received_at": fr.At,
		"frequency":   freq,
		"bit_rate":    fr.Settings.BitRate,
		"gateways":    gws,
	}
	rssi := 0.0
	if len(fr.Meta) > 0 {
		rssi = fr.Meta[0].RSSI
	}
	p.emit(&model.Event{Name: "fsk.up", Gateways: gatewayIDs(fr.Meta),
		Summary: fmt.Sprintf("FSK %d bytes · %.3f MHz · %d bit/s · RSSI %.0f · %d gateway(s) · %s", len(fr.Payload), freq,
			fr.Settings.BitRate, rssi, len(fr.Meta), truncHex(fr.Payload, 16)),
		Data: msg}, true)
	if out := p.FSKOut; out != nil {
		out(fr.Payload, msg)
	}
	return true
}

// SendFSK transmits data as FSK through one gateway and returns its ID.
func (p *Processor) SendFSK(data []byte, o FSKSend) (string, error) {
	s := p.Settings().FSKBridge
	if len(data) == 0 || len(data) > 255 {
		return "", errors.New("an FSK packet has 1 to 255 bytes")
	}
	if o.Gateway == "" {
		o.Gateway = s.Gateway
	}
	if o.Frequency == 0 {
		o.Frequency = s.Frequency
	}
	if o.BitRate == 0 {
		o.BitRate = s.BitRate
	}
	if o.Deviation == 0 {
		o.Deviation = s.Deviation
	}
	if o.Power == 0 {
		o.Power = s.Power
	}
	if o.Preamble == 0 {
		o.Preamble = s.Preamble
	}
	if o.Frequency < 100 || o.BitRate < 500 || o.BitRate > 250000 || o.Deviation < 1 || o.Deviation > 200 {
		return "", fmt.Errorf("FSK settings out of range: %.3f MHz, %d bit/s, %d kHz", o.Frequency, o.BitRate, o.Deviation)
	}
	g, err := p.fskGateway(o.Gateway)
	if err != nil {
		return "", err
	}
	dl := p.downlinker(g.Kind)
	tx := model.TxPacket{Payload: data, Frequency: uint64(o.Frequency*1e6 + 0.5), DataRate: strconv.Itoa(o.BitRate), Modulation: "FSK",
		FDev: o.Deviation, Power: TxPowerFor(g, o.Power), Preamble: o.Preamble, Immediate: true}
	if err := p.send(dl, g.EUI, tx); err != nil {
		return "", err
	}
	p.countTx(g.ID)
	p.emit(&model.Event{Name: "fsk.down", Gateways: []string{g.ID},
		Summary: fmt.Sprintf("FSK sent %d bytes · %.3f MHz · %d bit/s · ±%d kHz · %d dBm · %s", len(data), o.Frequency, o.BitRate,
			o.Deviation, tx.Power, truncHex(data, 16)),
		Data: map[string]any{"data_hex": strings.ToUpper(hex.EncodeToString(data)), "gateway_id": g.ID, "frequency": o.Frequency,
			"bit_rate": o.BitRate, "deviation": o.Deviation, "power": tx.Power}}, true)
	return g.ID, nil
}

// fskGateway is the named gateway, else the one that heard the last FSK
// packet best, else the first gateway that can send.
func (p *Processor) fskGateway(id string) (*model.Gateway, error) {
	usable := func(g *model.Gateway) bool {
		dl := p.downlinker(g.Kind)
		return dl != nil && dl.CanSend(g.EUI)
	}
	if id != "" {
		g, err := p.store.Gateway(id)
		if err != nil {
			return nil, fmt.Errorf("gateway %s: %w", id, err)
		}
		if !usable(g) {
			return nil, fmt.Errorf("gateway %s has no active downlink path", id)
		}
		return g, nil
	}
	p.fskMu.Lock()
	best := p.fskBest
	p.fskMu.Unlock()
	if best != "" {
		if g, err := p.store.Gateway(best); err == nil && usable(g) {
			return g, nil
		}
	}
	offline := time.Duration(p.Settings().GatewayOfflineSec) * time.Second
	for _, g := range p.store.Gateways() {
		if g.LastSeen != nil && time.Since(*g.LastSeen) < offline && usable(g) {
			return g, nil
		}
	}
	return nil, errors.New("no gateway can send at the moment")
}

// checkFSKBridge checks the bridge settings and fills in what is missing.
func checkFSKBridge(b *config.FSKBridge) error {
	d := config.DefaultSettings().FSKBridge
	b.Format = strings.ToLower(strings.TrimSpace(b.Format))
	if b.Format == "" {
		b.Format = d.Format
	}
	if b.Format != "json" && b.Format != "raw" {
		return errors.New("FSK bridge format must be json or raw")
	}
	b.SendTo, b.Listen = strings.TrimSpace(b.SendTo), strings.TrimSpace(b.Listen)
	if b.SendTo != "" {
		if _, port, err := net.SplitHostPort(b.SendTo); err != nil || port == "" || port == "0" {
			return fmt.Errorf("FSK bridge: send to %q is not host:port", b.SendTo)
		}
	}
	if b.Listen != "" {
		if _, err := net.ResolveUDPAddr("udp", b.Listen); err != nil {
			return fmt.Errorf("FSK bridge: listen address %q: %v", b.Listen, err)
		}
	}
	if b.Frequency == 0 {
		b.Frequency = d.Frequency
	}
	if b.BitRate == 0 {
		b.BitRate = d.BitRate
	}
	if b.Deviation == 0 {
		b.Deviation = d.Deviation
	}
	if b.Power == 0 {
		b.Power = d.Power
	}
	if b.Preamble == 0 {
		b.Preamble = d.Preamble
	}
	switch {
	case b.Frequency < 150 || b.Frequency > 1000:
		return fmt.Errorf("FSK bridge: frequency %.3f MHz is out of range", b.Frequency)
	case b.BitRate < 500 || b.BitRate > 250000:
		return fmt.Errorf("FSK bridge: bit rate %d is out of range (500 to 250000)", b.BitRate)
	case b.Deviation < 1 || b.Deviation > 200:
		return fmt.Errorf("FSK bridge: deviation %d kHz is out of range (1 to 200)", b.Deviation)
	case b.Power < -10 || b.Power > 30:
		return fmt.Errorf("FSK bridge: power %d dBm is out of range", b.Power)
	case b.Preamble < 3 || b.Preamble > 64:
		return fmt.Errorf("FSK bridge: preamble %d bytes is out of range (3 to 64)", b.Preamble)
	}
	return nil
}
