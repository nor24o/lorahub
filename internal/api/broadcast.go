package api

import (
	"encoding/hex"
	"net/http"
	"time"

	"github.com/nor24o/lorahub/internal/model"
	"github.com/nor24o/lorahub/internal/processor"
)

func (s *Server) broadcast(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Scope         string            `json:"scope"` // group | all
		DataHex       string            `json:"data_hex"`
		Radio         model.RawDownlink `json:"radio"`
		Gateways      []string          `json:"gateways"`
		ClassAMinutes int               `json:"class_a_minutes"` // 0 leaves class A nodes out
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if req.Scope != "group" && req.Scope != "all" {
		writeErr(w, 400, "scope must be group or all")
		return
	}
	if req.ClassAMinutes < 0 || req.ClassAMinutes > 7*24*60 {
		writeErr(w, 400, "class A nodes can wait between 0 minutes and 7 days")
		return
	}
	data, err := hex.DecodeString(normHex(req.DataHex))
	if err != nil {
		writeErr(w, 400, "data must be hex")
		return
	}
	res, err := s.Proc.SendBroadcast(processor.Broadcast{AppID: r.PathValue("app"), Everyone: req.Scope == "all", Data: data, Radio: req.Radio,
		Gateways: req.Gateways, ClassAFor: time.Duration(req.ClassAMinutes) * time.Minute})
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, res)
}
