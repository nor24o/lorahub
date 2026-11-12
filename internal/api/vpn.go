package api

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/nor24o/lorahub/internal/wgstat"
)

const (
	wgInterface     = "wg0"
	wgService       = "wg-quick@" + wgInterface
	wgConfigPath    = "/etc/wireguard/" + wgInterface + ".conf"
	tailscaleRoutes = "10.77.0.0/24"
)

// The parser lives in internal/wgstat, shared with the satellite page.
type (
	wgPeer   = wgstat.Peer
	wgStatus = wgstat.Status
)

func parseWGDump(out string, now time.Time, st *wgStatus) { wgstat.ParseDump(out, now, st) }

func parseWGPeerNames(conf string) map[string]string { return wgstat.PeerNames(conf) }

func (s *Server) getWireGuard(w http.ResponseWriter, r *http.Request) {
	st := wgStatus{Interface: wgInterface, Peers: []wgPeer{}}
	if _, err := exec.LookPath("wg"); err != nil {
		writeJSON(w, 200, st)
		return
	}
	st.Installed = true
	out, _ := runCmd(3*time.Second, "systemctl", "is-active", wgService)
	st.Service = strings.TrimSpace(out)
	if dump, err := runCmd(3*time.Second, "wg", "show", wgInterface, "dump"); err == nil {
		st.Up = true
		parseWGDump(dump, time.Now(), &st)
	}
	if out, err := runCmd(3*time.Second, "ip", "-4", "-o", "addr", "show", wgInterface); err == nil {
		if f := strings.Fields(out); len(f) > 3 {
			st.Address = f[3]
		}
	}
	if conf, err := os.ReadFile(wgConfigPath); err == nil {
		names := parseWGPeerNames(string(conf))
		for i := range st.Peers {
			st.Peers[i].Name = names[st.Peers[i].PublicKey]
		}
	}
	writeJSON(w, 200, st)
}

func (s *Server) wireGuardService(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Cmd string `json:"cmd"`
	}
	if err := readJSON(r, &req); err != nil || !serviceCmds[req.Cmd] {
		writeErr(w, 400, "cmd must be start, stop or restart")
		return
	}
	if out, err := runCmd(15*time.Second, "systemctl", req.Cmd, wgService); err != nil {
		writeErr(w, 500, strings.TrimSpace(out+" "+err.Error()))
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

type tsPeer struct {
	Name     string     `json:"name"`
	IP       string     `json:"ip,omitempty"`
	OS       string     `json:"os,omitempty"`
	Online   bool       `json:"online"`
	LastSeen *time.Time `json:"last_seen,omitempty"`
}

type tsStatus struct {
	Installed  bool     `json:"installed"`
	State      string   `json:"state,omitempty"`
	AuthURL    string   `json:"auth_url,omitempty"`
	Version    string   `json:"version,omitempty"`
	Hostname   string   `json:"hostname,omitempty"`
	DNSName    string   `json:"dns_name,omitempty"`
	IPs        []string `json:"ips"`
	Advertised []string `json:"advertised_routes"`
	Approved   []string `json:"approved_routes"`
	Peers      []tsPeer `json:"peers"`
	Health     []string `json:"health"`
}

// parseTailscaleStatus reads `tailscale status --json`.
func parseTailscaleStatus(out []byte, st *tsStatus) error {
	type node struct {
		HostName      string
		DNSName       string
		OS            string
		Online        bool
		TailscaleIPs  []string
		PrimaryRoutes []string
		LastSeen      time.Time
	}
	var raw struct {
		Version      string
		BackendState string
		AuthURL      string
		Health       []string
		TailscaleIPs []string
		Self         *node
		Peer         map[string]node
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return err
	}
	st.State, st.AuthURL, st.Version = raw.BackendState, raw.AuthURL, raw.Version
	if raw.TailscaleIPs != nil {
		st.IPs = raw.TailscaleIPs
	}
	if raw.Health != nil {
		st.Health = raw.Health
	}
	if raw.Self != nil {
		st.Hostname, st.DNSName = raw.Self.HostName, strings.TrimSuffix(raw.Self.DNSName, ".")
		if raw.Self.PrimaryRoutes != nil {
			st.Approved = raw.Self.PrimaryRoutes
		}
	}
	for _, p := range raw.Peer {
		v := tsPeer{Name: p.HostName, OS: p.OS, Online: p.Online}
		if len(p.TailscaleIPs) > 0 {
			v.IP = p.TailscaleIPs[0]
		}
		if !p.LastSeen.IsZero() {
			t := p.LastSeen
			v.LastSeen = &t
		}
		st.Peers = append(st.Peers, v)
	}
	return nil
}

func readTailscale() tsStatus {
	st := tsStatus{IPs: []string{}, Advertised: []string{}, Approved: []string{}, Peers: []tsPeer{}, Health: []string{}}
	if _, err := exec.LookPath("tailscale"); err != nil {
		return st
	}
	st.Installed = true
	if out, err := exec.Command("tailscale", "status", "--json").Output(); err == nil {
		parseTailscaleStatus(out, &st)
	} else {
		st.State = "NotRunning"
	}
	if out, err := exec.Command("tailscale", "debug", "prefs").Output(); err == nil {
		var prefs struct{ AdvertiseRoutes []string }
		if json.Unmarshal(out, &prefs) == nil && prefs.AdvertiseRoutes != nil {
			st.Advertised = prefs.AdvertiseRoutes
		}
	}
	return st
}

func (s *Server) getTailscale(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, readTailscale())
}

// tailscaleAction changes the Tailscale connection. `tailscale up` returning
// on its timeout is normal while it waits for a sign-in: the status sent back
// then carries the sign-in link.
func (s *Server) tailscaleAction(w http.ResponseWriter, r *http.Request) {
	up := []string{"up", "--accept-dns=false", "--advertise-routes=" + tailscaleRoutes, "--timeout=8s"}
	var failed string
	switch r.PathValue("action") {
	case "connect":
		runCmd(12*time.Second, "tailscale", up...)
	case "new-link":
		runCmd(10*time.Second, "tailscale", "logout")
		runCmd(12*time.Second, "tailscale", up...)
	case "disconnect":
		if out, err := runCmd(10*time.Second, "tailscale", "down"); err != nil {
			failed = strings.TrimSpace(out + " " + err.Error())
		}
	case "signout":
		if out, err := runCmd(10*time.Second, "tailscale", "logout"); err != nil {
			failed = strings.TrimSpace(out + " " + err.Error())
		}
	default:
		writeErr(w, 404, "unknown action")
		return
	}
	if failed != "" {
		writeErr(w, 500, failed)
		return
	}
	writeJSON(w, 200, readTailscale())
}
