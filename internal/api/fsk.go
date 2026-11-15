package api

import (
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/nor24o/lorahub/internal/processor"
)

// fskStatus is the FSK bridge page: its counters and each gateway's FSK
// channel, as the engine (2.3 and later) reports it.
func (s *Server) fskStatus(w http.ResponseWriter, r *http.Request) {
	type gw struct {
		ID     string          `json:"gateway_id"`
		Name   string          `json:"name,omitempty"`
		Online bool            `json:"online"`
		Engine string          `json:"engine,omitempty"`
		FSK    json.RawMessage `json:"fsk"` // null when the channel is off or the engine does not report it
	}
	gws := []gw{}
	for _, g := range s.Store.Gateways() {
		var st struct {
			Engine string          `json:"engine"`
			FSK    json.RawMessage `json:"fsk"`
		}
		json.Unmarshal(g.LastStatus, &st)
		if len(st.FSK) == 0 {
			st.FSK = json.RawMessage("null")
		}
		gws = append(gws, gw{ID: g.ID, Name: g.Name, Online: s.gatewayOnline(g), Engine: st.Engine, FSK: st.FSK})
	}
	out := map[string]any{"settings": s.Proc.Settings().FSKBridge, "gateways": gws}
	if s.FSK != nil {
		out["stats"] = s.FSK.Stats()
	}
	writeJSON(w, 200, out)
}

// fskSend puts one packet on the air as FSK (the page's test button).
func (s *Server) fskSend(w http.ResponseWriter, r *http.Request) {
	var req struct {
		processor.FSKSend
		DataHex string `json:"data_hex"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	data, err := hex.DecodeString(strings.NewReplacer(" ", "", ":", "").Replace(req.DataHex))
	if err != nil || len(data) == 0 {
		writeErr(w, 400, "data_hex must be hex bytes")
		return
	}
	id, err := s.Proc.SendFSK(data, req.FSKSend)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "gateway_id": id})
}
