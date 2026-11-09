package processor

import (
	"fmt"
	"math"
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
