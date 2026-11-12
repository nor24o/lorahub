package api

import (
	"encoding/hex"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/nor24o/lorahub/internal/model"
	"github.com/nor24o/lorahub/internal/processor"
)

// QueuedDownlink is one downlink still waiting in a node's queue, as listed on
// the Overview page.
type QueuedDownlink struct {
	AppID      string     `json:"application_id"`
	DevID      string     `json:"device_id"`
	Name       string     `json:"name,omitempty"`
	NodeID     string     `json:"node_id,omitempty"`
	Serial     string     `json:"serial,omitempty"`
	Class      string     `json:"class"`
	WaitsFor   string     `json:"waits_for"` // "packet": the node's next packet; "gateway": sent as soon as a gateway can
	LastSeen   *time.Time `json:"last_seen,omitempty"`
	ID         string     `json:"id"`
	Position   int        `json:"position"` // 1 = sent next
	PayloadHex string     `json:"payload_hex"`
	Confirmed  bool       `json:"confirmed,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"` // broadcast copy for a class A node
}

// queuedDownlinks lists every downlink waiting in any node's queue, oldest
// first. Broadcast copies whose time has run out are left out: they are
// removed at the node's next packet and will never be sent.
func (s *Server) queuedDownlinks(w http.ResponseWriter, r *http.Request) {
	apps := map[string]*model.Application{}
	for _, a := range s.Store.Applications() {
		apps[a.ID] = a
	}
	now := time.Now()
	out := []QueuedDownlink{}
	for _, d := range s.Store.Devices("") {
		if len(d.Queue) == 0 {
			continue
		}
		app := apps[d.AppID]
		nodeID, waits := "", "gateway"
		if app != nil {
			nodeID = app.NodeID
		}
		if processor.WaitsForUplink(app, d) {
			waits = "packet"
		}
		pos := 0
		for _, q := range d.Queue {
			if q.ExpiresAt != nil && now.After(*q.ExpiresAt) {
				continue
			}
			pos++
			out = append(out, QueuedDownlink{
				AppID: d.AppID, DevID: d.ID, Name: d.Name, NodeID: nodeID, Serial: d.Serial, Class: d.Class, WaitsFor: waits, LastSeen: d.LastSeen,
				ID: q.ID, Position: pos, PayloadHex: strings.ToUpper(hex.EncodeToString(q.Payload)), Confirmed: q.Confirmed,
				CreatedAt: q.CreatedAt, ExpiresAt: q.ExpiresAt,
			})
		}
	}
	slices.SortStableFunc(out, func(a, b QueuedDownlink) int { return a.CreatedAt.Compare(b.CreatedAt) })
	writeJSON(w, 200, out)
}
