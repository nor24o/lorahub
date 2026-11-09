package processor

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/nor24o/lorahub/internal/band"
	"github.com/nor24o/lorahub/internal/model"
	"github.com/nor24o/lorahub/internal/store"
)

// seqCounter numbers broadcasts 0..15 for the control byte.
type seqCounter struct{ n atomic.Uint32 }

func (c *seqCounter) next() byte { return byte((c.n.Add(1) - 1) & 0x0F) }

// Broadcast is one message for a whole node group or for every group.
type Broadcast struct {
	AppID     string
	Everyone  bool
	Data      []byte
	Radio     model.RawDownlink // frequency and data rate are required
	Gateways  []string          // gateway IDs, empty = every gateway that can transmit
	ClassAFor time.Duration     // how long it waits for class A receive windows, 0 = not at all
}

// BroadcastResult says where a broadcast went.
type BroadcastResult struct {
	Gateways     []string `json:"gateways"`
	ClassAQueued int      `json:"class_a_queued"`
	PayloadHex   string   `json:"payload_hex"`
}

// SendBroadcast transmits a message for several nodes. Nodes that are always
// listening get it now; nodes that only listen after transmitting get a copy
// in their queue for their next receive window, until ClassAFor runs out.
func (p *Processor) SendBroadcast(b Broadcast) (*BroadcastResult, error) {
	app, err := p.store.Application(b.AppID)
	if err != nil {
		return nil, err
	}
	if !b.Everyone && !app.DownlinkControl {
		return nil, fmt.Errorf("turn on the control byte in this group's settings first")
	}
	if b.Radio.Frequency == 0 || b.Radio.DataRate == "" {
		return nil, fmt.Errorf("a broadcast needs a frequency and a data rate")
	}
	if err := b.Radio.Validate(); err != nil {
		return nil, err
	}
	if len(b.Data) == 0 {
		return nil, fmt.Errorf("a broadcast needs data to send")
	}
	pf := p.Settings().PacketFormat
	audience, nodeID := model.ControlGroup, []byte(nil)
	if b.Everyone {
		audience, nodeID = model.ControlAll, bytes.Repeat([]byte{0xFF}, pf.NodeIDLength)
	} else if nodeID, err = hex.DecodeString(app.NodeID); err != nil {
		return nil, fmt.Errorf("group has no valid NODE_ID")
	}
	packet := append(append([]byte{}, nodeID...), bytes.Repeat([]byte{0xFF}, pf.SerialLength)...)
	packet = append(append(packet, model.ControlByte(audience, false, p.broadcastSeq.next())), b.Data...)
	if len(packet) > 242 {
		return nil, fmt.Errorf("broadcast too long (%d bytes)", len(packet))
	}

	var gateways []*model.Gateway
	res := &BroadcastResult{Gateways: []string{}, PayloadHex: strings.ToUpper(hex.EncodeToString(packet))}
	for _, g := range p.store.Gateways() {
		if len(b.Gateways) > 0 && !slices.Contains(b.Gateways, g.ID) {
			continue
		}
		if dl := p.downlinker(g.Kind); g.Downlink && !g.Disabled && dl != nil && dl.CanSend(g.EUI) && p.gatewayAlive(g) {
			gateways = append(gateways, g)
			res.Gateways = append(res.Gateways, g.ID)
		}
	}
	if b.ClassAFor > 0 {
		res.ClassAQueued = p.queueForClassA(app, b, packet)
	}
	if len(gateways) == 0 && res.ClassAQueued == 0 {
		return nil, fmt.Errorf("no gateway can transmit right now")
	}
	go p.transmitBroadcast(b, gateways, packet)
	return res, nil
}

func (p *Processor) queueForClassA(app *model.Application, b Broadcast, packet []byte) int {
	groups := []*model.Application{app}
	if b.Everyone {
		groups = p.store.Applications()
	}
	expires := time.Now().UTC().Add(b.ClassAFor)
	queued := 0
	for _, g := range groups {
		for _, d := range p.store.Devices(g.ID) {
			if d.Mode != "node" || !g.NodeControlOn(d) || downlinkCfg(g, d).Mode != "after_uplink" {
				continue
			}
			item := model.QueuedDownlink{ID: store.NewToken(6), FPort: 1, Payload: packet, CreatedAt: time.Now().UTC(), ExpiresAt: &expires}
			_, err := p.store.UpdateDevice(d.AppID, d.ID, func(dv *model.Device) error {
				if len(dv.Queue) >= 16 {
					return fmt.Errorf("downlink queue full")
				}
				dv.Queue = append(dv.Queue, item)
				return nil
			})
			if err == nil {
				queued++
			}
		}
	}
	return queued
}

// transmitBroadcast sends through one gateway at a time, so gateways do not
// talk over each other. A node hearing more than one sees the same sequence number.
func (p *Processor) transmitBroadcast(b Broadcast, gateways []*model.Gateway, packet []byte) {
	cr := b.Radio.CodingRate
	if cr == "" {
		cr = "4/5"
	}
	power := b.Radio.Power
	if power == 0 {
		power = 14
	}
	gap := 100 * time.Millisecond
	if dr, err := band.ParseDatr(b.Radio.DataRate); err == nil {
		preamble := b.Radio.Preamble
		if preamble == 0 {
			preamble = 8
		}
		gap += time.Duration(band.AirtimeMs(len(packet), dr, cr, preamble, !b.Radio.NoHeader, !b.Radio.NoCRC) * float64(time.Millisecond))
	}
	scope := "group " + b.AppID
	if b.Everyone {
		scope = "all groups"
	}
	for i, g := range gateways {
		if i > 0 {
			time.Sleep(gap)
		}
		tx := model.TxPacket{Payload: packet, Frequency: uint64(b.Radio.Frequency*1e6 + 0.5), DataRate: b.Radio.DataRate, CodingRate: cr, Power: TxPowerFor(g, power),
			InvertPol: b.Radio.InvertPol, Immediate: true, Preamble: b.Radio.Preamble, NoCRC: b.Radio.NoCRC, NoHeader: b.Radio.NoHeader}
		err := p.reserveAirtime(g, tx)
		if err == nil {
			err = p.send(p.downlinker(g.Kind), g.EUI, tx)
		}
		if err != nil {
			p.emit(&model.Event{Name: "as.down.broadcast.failed", AppID: b.AppID, Gateways: []string{g.ID}, Summary: "Broadcast to " + scope + " failed via " + g.ID + ": " + err.Error(),
				Data: map[string]any{"gateway_id": g.ID, "error": err.Error()}}, true)
			continue
		}
		p.countTx(g.ID)
		p.emit(&model.Event{Name: "as.down.broadcast", AppID: b.AppID, Gateways: []string{g.ID},
			Summary: fmt.Sprintf("Broadcast to %s via %s · %.3f MHz %s · %s", scope, g.ID, float64(tx.Frequency)/1e6, tx.DataRate, truncHex(packet, 16)),
			Data:    map[string]any{"gateway_id": g.ID, "frm_payload_hex": strings.ToUpper(hex.EncodeToString(packet)), "tx": tx}}, true)
	}
}
