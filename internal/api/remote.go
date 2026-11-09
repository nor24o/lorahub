package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/nor24o/lorahub/internal/ingest"
	"github.com/nor24o/lorahub/internal/model"
)

// Remote gateways are controlled through raw_lora_app's own commands: JSON
// get_config / set_config on its UDP downlink port, and JSON status on TCP.
const (
	controlTimeout    = 2 * time.Second
	applyTimeout      = 25 * time.Second // set_config restarts the SX1302
	defaultStatusPort = 5557
)

// Downlink ports raw_lora_app is usually configured with, tried when a
// gateway's own port is not known yet.
var commonControlPorts = []int{5556, 1700}

// readStatus fetches the JSON status raw_lora_app serves on its TCP port.
func readStatus(addr string) (json.RawMessage, error) {
	c, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(2 * time.Second))
	b, _ := io.ReadAll(io.LimitReader(c, 8192))
	if !json.Valid(b) || len(b) == 0 {
		return nil, errors.New("no valid status received")
	}
	return b, nil
}

// linkInfo is what a gateway last reported about its link.
type linkInfo struct {
	Link int  `json:"link"`
	Auth bool `json:"auth"`
}

func linkOf(g *model.Gateway) linkInfo {
	var l linkInfo
	if len(g.LastStatus) > 0 {
		json.Unmarshal(g.LastStatus, &l)
	}
	return l
}

// linkWire builds a command for a gateway, signing it when the gateway is
// paired. The result is called again if the gateway asks for a fresh nonce.
func (s *Server) linkWire(g *model.Gateway, req map[string]any) func(nonce string) []byte {
	l := linkOf(g)
	if l.Link >= ingest.LinkVersion {
		req["request_id"] = ingest.NewRequestID()
	}
	payload, _ := json.Marshal(req)
	key := s.Proc.Settings().LinkKey
	return func(nonce string) []byte {
		if l.Link >= ingest.LinkVersion && l.Auth && key != "" {
			return ingest.SignLink(key, nonce, payload)
		}
		return payload
	}
}

// controlExchange sends one command and waits for the reply of type want.
func controlExchange(host string, port int, wire func(nonce string) []byte, want string, timeout time.Duration) (map[string]any, error) {
	c, err := net.Dial("udp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return nil, err
	}
	defer c.Close()
	if _, err := c.Write(wire(ingest.StubNonce)); err != nil {
		return nil, err
	}
	c.SetReadDeadline(time.Now().Add(timeout))
	retried := false
	buf := make([]byte, 16384)
	for {
		n, err := c.Read(buf)
		if err != nil {
			return nil, fmt.Errorf("no answer from raw_lora_app on UDP port %d", port)
		}
		var m map[string]any
		if json.Unmarshal(buf[:n], &m) != nil {
			continue
		}
		switch m["msg_type"] {
		case want:
			return m, nil
		case "auth_required":
			nonce, _ := m["nonce"].(string)
			if retried || nonce == "" {
				return nil, errors.New("the gateway rejected LoRaHub's link key")
			}
			retried = true
			c.Write(wire(nonce))
		}
	}
}

func gatewayHost(g *model.Gateway) string {
	if h, _, err := net.SplitHostPort(g.LastAddr); err == nil {
		return h
	}
	return g.LastAddr
}

func (s *Server) controlPortOf(g *model.Gateway) int {
	if g.ControlPort > 0 {
		return g.ControlPort
	}
	return s.Cfg.Bobcat.DownlinkPort
}

// isLocalGateway reports whether g is the concentrator on this machine.
func (s *Server) isLocalGateway(g *model.Gateway) bool {
	b, err := readStatus(s.Cfg.Concentrator.StatusAddr)
	if err != nil {
		return false
	}
	var st struct {
		EUI string `json:"eui"`
	}
	return json.Unmarshal(b, &st) == nil && st.EUI != "" && strings.EqualFold(st.EUI, g.EUI)
}

type remoteTarget struct {
	host                    string
	controlPort, statusPort int
	local                   bool
}

func (s *Server) remoteTargetOf(g *model.Gateway) (remoteTarget, error) {
	if g.Kind != "bobcat" {
		return remoteTarget{}, errors.New("this gateway does not run raw_lora_app, so LoRaHub cannot change its radio settings")
	}
	t := remoteTarget{host: gatewayHost(g), controlPort: s.controlPortOf(g), statusPort: g.StatusPort, local: s.isLocalGateway(g)}
	if t.statusPort == 0 {
		t.statusPort = defaultStatusPort
	}
	if t.local {
		t.host, t.controlPort = "127.0.0.1", s.Cfg.Bobcat.DownlinkPort
	}
	if t.host == "" {
		return t, errors.New("the gateway's address is not known yet; wait for its next heartbeat")
	}
	return t, nil
}

// discoverControl asks the gateway for its configuration on the known port,
// then on the common ones, and remembers a port that answers.
func (s *Server) discoverControl(g *model.Gateway, t remoteTarget) (map[string]any, int, error) {
	ports := []int{t.controlPort}
	if !t.local && g.ControlPort == 0 {
		for _, p := range commonControlPorts {
			if p != t.controlPort {
				ports = append(ports, p)
			}
		}
	}
	var last error
	for _, p := range ports {
		m, err := controlExchange(t.host, p, s.linkWire(g, map[string]any{"cmd": "get_config", "full": true}), "config_resp", controlTimeout)
		if err != nil {
			last = err
			continue
		}
		if !t.local && g.ControlPort != p {
			s.Store.UpdateGateway(g.ID, true, func(x *model.Gateway) { x.ControlPort = p })
		}
		return m, p, nil
	}
	return nil, t.controlPort, last
}

func (s *Server) getRemote(w http.ResponseWriter, r *http.Request) {
	g, err := s.Store.Gateway(r.PathValue("id"))
	if err != nil {
		storeErr(w, err)
		return
	}
	out := map[string]any{"online": s.gatewayOnline(g), "supported": false}
	t, err := s.remoteTargetOf(g)
	if err != nil {
		out["error"] = err.Error()
		writeJSON(w, 200, out)
		return
	}
	out["supported"], out["local"], out["host"], out["status_port"] = true, t.local, t.host, t.statusPort
	out["control_port"] = t.controlPort
	if st, err := readStatus(net.JoinHostPort(t.host, strconv.Itoa(t.statusPort))); err == nil {
		out["status"] = st
	} else {
		out["status_error"] = err.Error()
	}
	if r.URL.Query().Get("status") == "1" {
		writeJSON(w, 200, out)
		return
	}
	cfg, port, err := s.discoverControl(g, t)
	out["control_port"] = port
	switch {
	case err == nil:
		out["config"] = cfg
		out["link"] = linkOf(g)
	case out["status"] != nil:
		out["error"] = "This gateway reports its status but does not answer remote settings commands. Its raw_lora_app build is too old for this, or its control port is wrong (see the gateway's Settings tab). Live status and TX test still work if the port is right."
	default:
		out["error"] = err.Error()
	}
	writeJSON(w, 200, out)
}

// validateRemote checks settings for a gateway. Current engines take any
// setting of their config file; older ones only the original few.
func validateRemote(cfg map[string]any, full bool) error {
	allowed := map[string]bool{"rf0_freq": true, "rf1_freq": true, "tx_default_freq": true, "tx_default_power": true, "sync_word": true, "target_ip": true, "port_up": true}
	if len(cfg) == 0 {
		return errors.New("nothing to change")
	}
	num := func(k string, lo, hi float64) error {
		v, ok := cfg[k].(float64)
		if !ok || v < lo || v > hi {
			return fmt.Errorf("%s must be between %.0f and %.0f", k, lo, hi)
		}
		return nil
	}
	for k := range cfg {
		var err error
		switch {
		case k == "link_key", !allowed[k] && !full:
			err = fmt.Errorf("%s cannot be changed remotely", k)
		case k == "rf0_freq", k == "rf1_freq", k == "tx_default_freq":
			err = num(k, 100e6, 1100e6)
		case k == "tx_default_power":
			err = num(k, 0, 27)
		case k == "port_up":
			err = num(k, 1, 65535)
		case k == "sync_word":
			if v, _ := cfg[k].(string); v != "public" && v != "private" {
				err = errors.New("sync_word must be public or private")
			}
		case k == "target_ip":
			if v, _ := cfg[k].(string); net.ParseIP(v) == nil || !strings.Contains(v, ".") {
				err = errors.New("target_ip must be an IPv4 address")
			}
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) putRemoteConfig(w http.ResponseWriter, r *http.Request) {
	g, err := s.Store.Gateway(r.PathValue("id"))
	if err != nil {
		storeErr(w, err)
		return
	}
	t, err := s.remoteTargetOf(g)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	var cfg map[string]any
	if err := readJSON(r, &cfg); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if err := validateRemote(cfg, linkOf(g).Link >= ingest.LinkVersion); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	ack, err := controlExchange(t.host, t.controlPort, s.linkWire(g, map[string]any{"cmd": "set_config", "config": cfg}), "config_ack", applyTimeout)
	if err != nil {
		writeErr(w, 502, err.Error())
		return
	}
	if ack["status"] != "ok" {
		writeErr(w, 502, fmt.Sprint(ack["message"]))
		return
	}
	writeJSON(w, 200, ack)
}

// ProbeControlPorts finds the UDP control port of raw_lora_app gateways, so
// downlinks reach a satellite whose port differs from the hub's default.
func (s *Server) ProbeControlPorts(stop <-chan struct{}) {
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	for {
		for _, g := range s.Store.Gateways() {
			if g.Kind != "bobcat" || g.ControlPort != 0 || !s.gatewayOnline(g) {
				continue
			}
			if t, err := s.remoteTargetOf(g); err == nil && !t.local {
				s.discoverControl(g, t)
			}
		}
		select {
		case <-stop:
			return
		case <-tick.C:
		}
	}
}
