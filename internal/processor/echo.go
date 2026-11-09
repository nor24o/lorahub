package processor

import (
	"encoding/hex"
	"encoding/json"
	"sync"
	"time"

	"github.com/nor24o/lorahub/internal/model"
)

// echoWindow is how long a transmission can come back through another
// gateway: the class A delay, the longest airtime and the link's retries.
const echoWindow = 20 * time.Second

// echoGuard remembers what the gateways just transmitted. A downlink sent
// without inverted IQ is a normal LoRa packet, so the other gateways receive
// it too; without this it would be read as an uplink from the node.
type echoGuard struct {
	mu   sync.Mutex
	sent map[string]time.Time
}

func (g *echoGuard) note(payload []byte) {
	now := time.Now()
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.sent == nil {
		g.sent = map[string]time.Time{}
	}
	for k, at := range g.sent {
		if now.Sub(at) > echoWindow {
			delete(g.sent, k)
		}
	}
	g.sent[hex.EncodeToString(payload)] = now
}

func (g *echoGuard) heard(payload []byte) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	at, ok := g.sent[hex.EncodeToString(payload)]
	return ok && time.Since(at) <= echoWindow
}

// gatewayAlive reports whether a gateway has been heard from lately. An
// engine on link protocol 2 reports every 10 s; when it stops, its radio is
// down even though the hub still knows its address.
func (p *Processor) gatewayAlive(g *model.Gateway) bool {
	if g.LastSeen == nil {
		return true // never heard: nothing to judge by
	}
	limit := time.Duration(p.Settings().GatewayOfflineSec) * time.Second
	if g.Kind == "bobcat" && linkVersion(g) >= 2 {
		limit = 45 * time.Second
	}
	return limit <= 0 || time.Since(*g.LastSeen) < limit
}

func linkVersion(g *model.Gateway) int {
	var st struct {
		Link int `json:"link"`
	}
	json.Unmarshal(g.LastStatus, &st)
	return st.Link
}

// aliveGateway picks another gateway that can transmit now, the most
// recently heard first.
func (p *Processor) aliveGateway(except string) *model.Gateway {
	var best *model.Gateway
	for _, g := range p.store.Gateways() {
		if g.ID == except || !g.Downlink || g.Disabled || g.LastSeen == nil || !p.gatewayAlive(g) {
			continue
		}
		if dl := p.downlinker(g.Kind); dl == nil || !dl.CanSend(g.EUI) {
			continue
		}
		if best == nil || g.LastSeen.After(*best.LastSeen) {
			best = g
		}
	}
	return best
}

// send hands a packet to a gateway, remembering it so its echo is ignored.
func (p *Processor) send(dl Downlinker, eui string, tx model.TxPacket) error {
	p.echo.note(tx.Payload)
	return dl.Send(eui, tx)
}
