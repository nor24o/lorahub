package api

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nor24o/lorahub/internal/model"
	"github.com/nor24o/lorahub/internal/processor"
)

func runCmd(timeout time.Duration, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	return string(out), err
}

func (s *Server) serviceState() string {
	out, _ := runCmd(3*time.Second, "systemctl", "is-active", s.Cfg.Concentrator.Service)
	if st := strings.TrimSpace(out); st != "" && !strings.Contains(st, "\n") {
		return st
	}
	return "unknown"
}

var serviceCmds = map[string]bool{"start": true, "stop": true, "restart": true}

func (s *Server) concentratorService(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Cmd string `json:"cmd"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if !serviceCmds[req.Cmd] {
		writeErr(w, 400, "cmd must be start, stop or restart")
		return
	}
	if out, err := runCmd(30*time.Second, "systemctl", req.Cmd, s.Cfg.Concentrator.Service); err != nil {
		writeErr(w, 500, fmt.Sprintf("%v: %s", err, out))
		return
	}
	writeJSON(w, 200, map[string]string{"service": s.serviceState()})
}

func (s *Server) getLogs(w http.ResponseWriter, r *http.Request) {
	unit := map[string]string{"concentrator": s.Cfg.Concentrator.Service, "lorahub": "lorahub"}[r.URL.Query().Get("source")]
	if unit == "" {
		writeErr(w, 400, "source must be concentrator or lorahub")
		return
	}
	lines, _ := strconv.Atoi(r.URL.Query().Get("lines"))
	if lines <= 0 || lines > 2000 {
		lines = 300
	}
	out, err := runCmd(8*time.Second, "journalctl", "-u", unit, "-n", strconv.Itoa(lines), "--no-pager", "-o", "short-iso")
	if err != nil {
		writeErr(w, 500, fmt.Sprintf("cannot read the system log: %v", err))
		return
	}
	writeJSON(w, 200, map[string]string{"log": out})
}

type txRequest struct {
	Gateway       string  `json:"gateway"`
	Abort         bool    `json:"abort"`
	Modu          string  `json:"modu"`
	Freq          float64 `json:"freq"`
	Datr          string  `json:"datr"`
	Codr          string  `json:"codr"`
	Power         int     `json:"powe"`
	RFChain       int     `json:"rfch"`
	InvertPol     bool    `json:"ipol"`
	Preamble      int     `json:"prea"`
	NoCRC         bool    `json:"ncrc"`
	NoHeader      bool    `json:"nhdr"`
	FDev          int     `json:"f_dev"`
	FOff          int     `json:"f_off"`
	PayloadHex    string  `json:"payload_hex"`
	ASCII         string  `json:"ascii"`
	RandomLen     int     `json:"random_len"`
	AppendCounter bool    `json:"append_counter"`
	Count         int     `json:"count"`
	IntervalMs    int     `json:"interval_ms"`
	DelayMs       int     `json:"delay_ms"`
}

// txState tracks the test sequence in progress.
type txState struct {
	sync.Mutex
	running     bool
	sent, total int
	lastErr     string
	stop        chan struct{}
}

// txAddress returns where raw_lora_app listens for the chosen gateway.
func (s *Server) txAddress(id string) (string, *model.Gateway, error) {
	if id == "" {
		return fmt.Sprintf("127.0.0.1:%d", s.Cfg.Bobcat.DownlinkPort), nil, nil
	}
	g, err := s.Store.Gateway(id)
	if err != nil {
		return "", nil, errors.New("unknown gateway")
	}
	t, err := s.remoteTargetOf(g)
	if err != nil {
		return "", nil, err
	}
	return net.JoinHostPort(t.host, strconv.Itoa(t.controlPort)), g, nil
}

func validateTX(req *txRequest) error {
	if req.RFChain != 0 && req.RFChain != 1 {
		return errors.New("RF chain must be 0 or 1")
	}
	if req.Modu != "LORA" && req.Modu != "FSK" && req.Modu != "CW" {
		return errors.New("mode must be LORA, FSK or CW")
	}
	if req.Freq < 100 || req.Freq > 1100 {
		return errors.New("frequency must be given in MHz")
	}
	if req.Power < 0 || req.Power > 27 {
		return errors.New("power must be 0 to 27 dBm")
	}
	if req.Count == 0 {
		req.Count = 1
	}
	if req.Count < 1 || req.Count > 1000 {
		return errors.New("repeat count must be 1 to 1000")
	}
	if req.Count > 1 && req.IntervalMs < 100 {
		return errors.New("the interval between packets must be at least 100 ms")
	}
	if req.DelayMs < 0 || req.DelayMs > 60000 {
		return errors.New("start delay must be 0 to 60000 ms")
	}
	if req.RandomLen < 0 || req.RandomLen > 255 {
		return errors.New("random payload must be 0 to 255 bytes")
	}
	return nil
}

// txPayload returns the payload generator of a request.
func txPayload(req *txRequest) (func(i int) []byte, error) {
	base := []byte(req.ASCII)
	if req.PayloadHex != "" {
		var err error
		if base, err = hex.DecodeString(strings.ReplaceAll(req.PayloadHex, " ", "")); err != nil {
			return nil, errors.New("payload must be hex")
		}
	}
	size := len(base)
	if req.RandomLen > 0 {
		size = req.RandomLen
	}
	if req.AppendCounter {
		size += 2
	}
	if size > 255 {
		return nil, errors.New("payload is longer than 255 bytes")
	}
	return func(i int) []byte {
		b := append([]byte{}, base...)
		if req.RandomLen > 0 {
			b = make([]byte, req.RandomLen)
			rand.Read(b)
		}
		if req.AppendCounter {
			b = append(b, byte(i>>8), byte(i))
		}
		return b
	}, nil
}

func txMessage(req *txRequest, data []byte) map[string]any {
	msg := map[string]any{"imme": true, "freq": req.Freq, "rfch": req.RFChain, "powe": req.Power, "modu": req.Modu, "codr": req.Codr,
		"ipol": req.InvertPol, "ncrc": req.NoCRC, "nhdr": req.NoHeader, "size": len(data), "data": base64.StdEncoding.EncodeToString(data)}
	if req.Preamble > 0 {
		msg["prea"] = req.Preamble
	}
	if req.Modu == "FSK" {
		rate, _ := strconv.Atoi(req.Datr)
		msg["datr"] = rate
		if req.FDev > 0 {
			msg["f_dev"] = req.FDev
		}
	} else {
		msg["datr"] = req.Datr
	}
	if req.Modu == "CW" && req.FOff != 0 {
		msg["f_off"] = req.FOff
	}
	return msg
}

func udpSend(addr string, msg map[string]any) error {
	c, err := net.Dial("udp", addr)
	if err != nil {
		return err
	}
	defer c.Close()
	b, _ := json.Marshal(msg)
	_, err = c.Write(b)
	return err
}

// concentratorTX hands a test transmission straight to raw_lora_app, on this
// machine or on a satellite, once or as a timed sequence.
func (s *Server) concentratorTX(w http.ResponseWriter, r *http.Request) {
	var req txRequest
	if err := readJSON(r, &req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	addr, g, err := s.txAddress(req.Gateway)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if req.Abort {
		s.tx.Lock()
		if s.tx.running {
			close(s.tx.stop)
			s.tx.running = false
		}
		s.tx.Unlock()
		if err := udpSend(addr, map[string]any{"abort": true, "rfch": req.RFChain}); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		writeJSON(w, 200, map[string]bool{"aborted": true})
		return
	}
	if err := validateTX(&req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if req.Modu == "FSK" {
		if _, err := strconv.Atoi(req.Datr); err != nil {
			writeErr(w, 400, "FSK data rate must be a number in bit/s")
			return
		}
	}
	payload, err := txPayload(&req)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if g != nil {
		req.Power = processor.TxPowerFor(g, req.Power)
	}
	if req.Count == 1 && req.DelayMs == 0 {
		msg := txMessage(&req, payload(0))
		if err := udpSend(addr, msg); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		writeJSON(w, 200, map[string]any{"sent": msg})
		return
	}
	s.tx.Lock()
	if s.tx.running {
		s.tx.Unlock()
		writeErr(w, 409, "a transmit sequence is already running; stop it first")
		return
	}
	stop := make(chan struct{})
	s.tx.running, s.tx.sent, s.tx.total, s.tx.lastErr, s.tx.stop = true, 0, req.Count, "", stop
	s.tx.Unlock()
	go s.runTXJob(addr, req, payload, stop)
	writeJSON(w, 200, map[string]any{"job": true, "total": req.Count, "power": req.Power})
}

func (s *Server) runTXJob(addr string, req txRequest, payload func(int) []byte, stop <-chan struct{}) {
	wait := func(ms int) bool {
		select {
		case <-stop:
			return false
		case <-time.After(time.Duration(ms) * time.Millisecond):
			return true
		}
	}
	if !wait(req.DelayMs) {
		return
	}
	for i := 0; i < req.Count; i++ {
		err := udpSend(addr, txMessage(&req, payload(i)))
		s.tx.Lock()
		if err != nil {
			s.tx.lastErr = err.Error()
		} else {
			s.tx.sent++
		}
		s.tx.Unlock()
		if err != nil || (i+1 < req.Count && !wait(req.IntervalMs)) {
			break
		}
	}
	s.tx.Lock()
	select {
	case <-stop:
	default:
		s.tx.running = false
	}
	s.tx.Unlock()
}

func (s *Server) txJobStatus(w http.ResponseWriter, r *http.Request) {
	s.tx.Lock()
	defer s.tx.Unlock()
	writeJSON(w, 200, map[string]any{"running": s.tx.running, "sent": s.tx.sent, "total": s.tx.total, "error": s.tx.lastErr})
}

func (s *Server) haEntities(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.MQTT.EntityReport())
}

func (s *Server) haRepublish(w http.ResponseWriter, r *http.Request) {
	s.MQTT.RepublishHA()
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) haRemove(w http.ResponseWriter, r *http.Request) {
	s.MQTT.RemoveHA()
	writeJSON(w, 200, map[string]bool{"ok": true})
}
