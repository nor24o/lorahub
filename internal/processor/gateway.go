package processor

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/nor24o/lorahub/internal/band"
	"github.com/nor24o/lorahub/internal/model"
)

const dutyWindow = time.Hour

type dutyEntry struct {
	at time.Time
	ms float64
}

// TxPowerFor applies a gateway's power limit and antenna gain.
func TxPowerFor(g *model.Gateway, power int) int {
	if g.TxPower > 0 && power > g.TxPower {
		power = g.TxPower
	}
	if g.AntennaGain > 0 {
		power -= int(math.Ceil(g.AntennaGain))
	}
	if power < 0 {
		return 0
	}
	return power
}

// rejectedByFilter applies the gateway's calibration and reception limits to
// a received packet. It reports whether the packet must be dropped.
func rejectedByFilter(g *model.Gateway, m *model.RxMetadata) bool {
	if g.RSSIOffset != 0 {
		m.RSSI += g.RSSIOffset
		m.ChannelRSSI += g.RSSIOffset
		if m.SignalRSSI != nil {
			v := *m.SignalRSSI + g.RSSIOffset
			m.SignalRSSI = &v
		}
	}
	return (g.MinRSSI != 0 && m.RSSI < g.MinRSSI) || (g.MinSNR != nil && m.SNR < *g.MinSNR)
}

// reserveAirtime books a transmission against the gateway's duty-cycle limit,
// over a sliding one hour window.
func (p *Processor) reserveAirtime(g *model.Gateway, tx model.TxPacket) error {
	if g.DutyCyclePct <= 0 {
		return nil
	}
	dr, err := band.ParseDatr(tx.DataRate)
	if err != nil {
		return nil // FSK or unknown rate: cannot be estimated
	}
	preamble := tx.Preamble
	if preamble == 0 {
		preamble = 8
	}
	ms := band.AirtimeMs(len(tx.Payload), dr, tx.CodingRate, preamble, true, !tx.NoCRC)
	limit := float64(dutyWindow.Milliseconds()) * g.DutyCyclePct / 100
	now := time.Now()

	p.dutyMu.Lock()
	defer p.dutyMu.Unlock()
	if p.duty == nil {
		p.duty = map[string][]dutyEntry{}
	}
	used, kept := 0.0, p.duty[g.ID][:0]
	for _, e := range p.duty[g.ID] {
		if now.Sub(e.at) < dutyWindow {
			kept = append(kept, e)
			used += e.ms
		}
	}
	if used+ms > limit {
		p.duty[g.ID] = kept
		return fmt.Errorf("gateway %s would exceed its %.2f%% duty-cycle limit (%.0f of %.0f ms used in the last hour)", g.ID, g.DutyCyclePct, used, limit)
	}
	p.duty[g.ID] = append(kept, dutyEntry{now, ms})
	return nil
}

func reverseEUI(eui string) string {
	if len(eui) != 16 {
		return ""
	}
	var b strings.Builder
	for i := 14; i >= 0; i -= 2 {
		b.WriteString(eui[i : i+2])
	}
	return b.String()
}

// adoptReversedEUI corrects a gateway registered from a big-endian binary
// heartbeat, which older engines sent, so it keeps its identity and history.
func (p *Processor) adoptReversedEUI(eui, kind string) *model.Gateway {
	if kind != "bobcat" {
		return nil
	}
	eui = strings.ToUpper(eui)
	g, err := p.store.GatewayByEUI(reverseEUI(eui))
	if err != nil {
		return nil
	}
	if err := p.store.UpdateGateway(g.ID, true, func(x *model.Gateway) { x.EUI = eui }); err != nil {
		p.log.Warn("correct gateway EUI", "gateway", g.ID, "err", err)
		return nil
	}
	p.log.Info("corrected gateway EUI", "gateway", g.ID, "eui", eui)
	g.EUI = eui
	p.renameToEUI(g, reverseEUI(eui))
	if fixed, err := p.store.GatewayByEUI(eui); err == nil {
		return fixed
	}
	return g
}

// renameToEUI gives a gateway the ID that belongs to its correct EUI.
func (p *Processor) renameToEUI(g *model.Gateway, oldEUI string) {
	newID := gatewayIDFromEUI(g.EUI)
	if err := p.store.RenameGateway(g.ID, newID, oldEUI); err != nil {
		p.log.Warn("correct gateway ID", "gateway", g.ID, "err", err)
		return
	}
	p.log.Info("corrected gateway ID", "from", g.ID, "to", newID)
	p.renamed = append(p.renamed, g.ID)
}

// fixReversedIDs corrects gateways whose ID was made from a reversed EUI.
func (p *Processor) fixReversedIDs() {
	for _, g := range p.store.Gateways() {
		old := reverseEUI(g.EUI)
		if g.Kind == "bobcat" && old != "" && old != g.EUI && g.ID == gatewayIDFromEUI(old) {
			p.renameToEUI(g, old)
		}
	}
}

// RenamedGateways lists former IDs corrected at startup, so what was published
// under them can be withdrawn.
func (p *Processor) RenamedGateways() []string { return append([]string{}, p.renamed...) }

var modelNames = map[string]string{"dragino-lps8": "Dragino LPS8", "bobcat-300": "Bobcat 300"}

// applyLinkStatus takes the ports and model a gateway reports about itself.
func (p *Processor) applyLinkStatus(g *model.Gateway, status map[string]any) {
	if status == nil {
		return
	}
	down, _ := num(status["port_down"])
	stat, _ := num(status["status_port"])
	name := g.Name
	if label := modelNames[fmt.Sprint(status["model"])]; label != "" && len(g.EUI) == 16 {
		// Only a name LoRaHub chose itself when it first saw the gateway.
		if i := strings.LastIndexByte(g.Name, ' '); i > 0 && len(g.Name)-i == 7 && g.Name[:i] == kindLabel("bobcat") {
			name = label + " " + g.EUI[10:]
		}
	}
	wantDown, wantStat := int(down), int(stat)
	if name == g.Name && (wantDown == 0 || wantDown == g.ControlPort) && (wantStat == 0 || wantStat == g.StatusPort) {
		return
	}
	p.store.UpdateGateway(g.ID, true, func(x *model.Gateway) {
		x.Name = name
		if wantDown > 0 {
			x.ControlPort = wantDown
		}
		if wantStat > 0 {
			x.StatusPort = wantStat
		}
	})
}
