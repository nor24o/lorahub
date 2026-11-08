package api

import (
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"github.com/nor24o/lorahub/internal/formatter"
	"github.com/nor24o/lorahub/internal/model"
	"github.com/nor24o/lorahub/internal/processor"
)

func (s *Server) getSchemes(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.Proc.Schemes())
}

func (s *Server) putSchemes(w http.ResponseWriter, r *http.Request) {
	var doc formatter.SchemeDoc
	if err := readJSON(r, &doc); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if doc.Schemes == nil {
		doc.Schemes = []formatter.Scheme{}
	}
	if err := s.Proc.SetSchemes(&doc); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, doc)
}

// testSchemes decodes a sample. With "scheme" it runs that one scheme as
// edited; otherwise it shows which saved scheme a packet would hit.
func (s *Server) testSchemes(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PayloadHex string            `json:"payload_hex"`
		Scheme     *formatter.Scheme `json:"scheme"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	b, err := hex.DecodeString(strings.NewReplacer(" ", "", ":", "").Replace(req.PayloadHex))
	if err != nil || len(b) == 0 {
		writeErr(w, 400, "payload must be hex")
		return
	}
	now := time.Now().UTC()
	if req.Scheme == nil {
		writeJSON(w, 200, s.Proc.Schemes().Decode(b, now, nil, true))
		return
	}
	check := formatter.SchemeDoc{Schemes: []formatter.Scheme{*req.Scheme}}
	if err := check.Validate(); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	data, warns, errs := req.Scheme.Run(b, now, nil)
	writeJSON(w, 200, map[string]any{"matched": req.Scheme.Matches(b), "data": data, "warnings": warns, "errors": errs})
}

func (s *Server) listRules(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.Proc.Rules())
}

func (s *Server) saveRule(w http.ResponseWriter, r *http.Request) {
	var rule model.Rule
	if err := readJSON(r, &rule); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	rule.ID = r.PathValue("id")
	saved, err := s.Proc.SaveRule(rule)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, saved)
}

func (s *Server) deleteRule(w http.ResponseWriter, r *http.Request) {
	if err := s.Proc.DeleteRule(r.PathValue("id")); err != nil {
		writeErr(w, 404, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) fireRule(w http.ResponseWriter, r *http.Request) {
	if err := s.Proc.FireRule(r.PathValue("id")); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) simulateRule(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Operator  string  `json:"operator"`
		Threshold float64 `json:"threshold"`
		Value     float64 `json:"value"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	matched, ok := processor.CompareRule(req.Operator, req.Value, req.Threshold)
	if !ok {
		writeErr(w, 400, "unknown operator")
		return
	}
	writeJSON(w, 200, map[string]bool{"matched": matched})
}
