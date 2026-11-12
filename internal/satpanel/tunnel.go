package satpanel

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/nor24o/lorahub/internal/wgstat"
)

// Tunnel is the WireGuard link to the hub.
type Tunnel struct {
	wgstat.Status
	HubIP        string  `json:"hub_ip,omitempty"`
	CheckLabel   string  `json:"check_label"` // "the hub" on a satellite, "the router" on the hub
	CheckIP      string  `json:"check_ip"`
	HubReachable bool    `json:"hub_reachable"` // the check target answers
	HubPingMS    float64 `json:"hub_ping_ms,omitempty"`
	LAN          string  `json:"lan"` // the hub on the home network, host:port
	WAN          string  `json:"wan"` // the hub's internet address, host:port, optional
	Roaming      bool    `json:"roaming"`
}

func (s *Server) tunnel(ctx context.Context) Tunnel {
	t := Tunnel{Status: wgstat.Status{Interface: s.opt.WGInterface, Peers: []wgstat.Peer{}}}
	if !s.hub() {
		t.HubIP = s.hubIP()
	}
	if svc, err := s.run.Run(ctx, "", "systemctl", "is-active", "wg-quick@"+s.opt.WGInterface); err == nil || svc != "" {
		t.Service = strings.TrimSpace(svc)
	}
	if dump, err := s.run.Run(ctx, "", "wg", "show", s.opt.WGInterface, "dump"); err == nil && strings.TrimSpace(dump) != "" {
		t.Installed, t.Up = true, true
		wgstat.ParseDump(dump, s.now(), &t.Status)
	}
	t.Address = s.wgAddress()
	if conf, err := os.ReadFile(s.path(s.opt.WGConf)); err == nil {
		names := wgstat.PeerNames(string(conf))
		for i := range t.Peers {
			t.Peers[i].Name = names[t.Peers[i].PublicKey]
		}
	}
	env := s.roamEnv()
	t.LAN, t.WAN, t.Roaming = env["LAN"], env["WAN"], env["WAN"] != ""
	t.HubReachable, t.HubPingMS = s.hubPing(ctx, false)
	t.CheckLabel, t.CheckIP = s.checkTarget(ctx)
	return t
}

func (s *Server) wgAddress() string {
	conf, err := os.ReadFile(s.path(s.opt.WGConf))
	if err != nil {
		return ""
	}
	for _, l := range strings.Split(string(conf), "\n") {
		if k, v, ok := strings.Cut(l, "="); ok && strings.EqualFold(strings.TrimSpace(k), "Address") {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

var pingTime = regexp.MustCompile(`time[=<]([0-9.]+) ?ms`)

// checkTarget is what the board must still reach after a network change: the
// hub through the tunnel on a satellite, the home router on the hub.
func (s *Server) checkTarget(ctx context.Context) (label, ip string) {
	if !s.hub() {
		return "the hub", s.hubIP()
	}
	out, _ := s.run.Run(ctx, "", "ip", "-4", "route", "show", "default")
	f := strings.Fields(out)
	for i := 0; i+1 < len(f); i++ {
		if f[i] == "via" {
			return "the router", f[i+1]
		}
	}
	return "the router", ""
}

// hubPing pings the check target; results are reused for a few seconds
// unless fresh is set.
func (s *Server) hubPing(ctx context.Context, fresh bool) (bool, float64) {
	_, ip := s.checkTarget(ctx)
	s.pingMu.Lock()
	defer s.pingMu.Unlock()
	if !fresh && ip == s.pingTo && s.now().Sub(s.pingAt) < 4*time.Second {
		return s.pingOK, s.pingMS
	}
	if ip == "" {
		s.pingAt, s.pingOK, s.pingMS, s.pingTo = s.now(), false, 0, ""
		return false, 0
	}
	out, err := s.run.Run(ctx, "", "ping", "-c", "1", "-W", "2", ip)
	s.pingAt, s.pingOK, s.pingMS, s.pingTo = s.now(), err == nil, 0, ip
	if m := pingTime.FindStringSubmatch(out); m != nil && err == nil {
		s.pingMS, _ = strconv.ParseFloat(m[1], 64)
	}
	return s.pingOK, s.pingMS
}

// roamEnv reads /etc/wireguard/roam.env (PEER, LAN and WAN in shell syntax).
func (s *Server) roamEnv() map[string]string {
	env := map[string]string{}
	b, err := os.ReadFile(s.path(s.opt.RoamEnv))
	if err != nil {
		return env
	}
	for _, l := range strings.Split(string(b), "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(l), "="); ok {
			env[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `'"`)
		}
	}
	return env
}

var hostName = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`)

// checkEndpoint accepts host:port with an IPv4 address or a host name.
func checkEndpoint(ep string, required bool) (string, error) {
	ep = strings.TrimSpace(ep)
	if ep == "" {
		if required {
			return "", errors.New("the hub's home address is needed, for example 192.168.1.15:51820")
		}
		return "", nil
	}
	host, port, err := net.SplitHostPort(ep)
	if err != nil {
		return "", fmt.Errorf("%q must be address:port, for example 192.168.1.15:51820", ep)
	}
	if p, err := strconv.Atoi(port); err != nil || p < 1 || p > 65535 {
		return "", fmt.Errorf("the port in %q must be 1 to 65535", ep)
	}
	if ip := net.ParseIP(host); ip != nil {
		if ip.To4() == nil {
			return "", fmt.Errorf("use an IPv4 address in %q", ep)
		}
	} else if !hostName.MatchString(host) {
		return "", fmt.Errorf("%q is not an address or host name", host)
	}
	return net.JoinHostPort(host, port), nil
}

// TunnelRequest sets where the satellite finds the hub.
type TunnelRequest struct {
	LAN  string `json:"lan"`
	WAN  string `json:"wan"`
	Keep bool   `json:"keep"`
}

var endpointLine = regexp.MustCompile(`(?m)^([ \t]*Endpoint[ \t]*=[ \t]*).*$`)

func (s *Server) setTunnel(ctx context.Context, req TunnelRequest) (Change, error) {
	lan, err := checkEndpoint(req.LAN, true)
	if err != nil {
		return Change{}, badRequest(err)
	}
	wan, err := checkEndpoint(req.WAN, false)
	if err != nil {
		return Change{}, badRequest(err)
	}
	envPath, confPath := s.path(s.opt.RoamEnv), s.path(s.opt.WGConf)
	oldEnv, _ := os.ReadFile(envPath)
	oldConf, err := os.ReadFile(confPath)
	if err != nil {
		return Change{}, badRequest(errors.New("this Bobcat has no tunnel configuration yet; run add-satellite.sh first"))
	}
	t := s.tunnel(ctx)
	peer := s.roamEnv()["PEER"]
	oldEndpoint := ""
	for _, p := range t.Peers {
		if peer == "" || p.PublicKey == peer {
			peer, oldEndpoint = p.PublicKey, p.Endpoint
			break
		}
	}
	if peer == "" {
		return Change{}, badRequest(errors.New("the tunnel has no peer for the hub"))
	}
	newEnv := fmt.Sprintf("PEER='%s'\nLAN='%s'\nWAN='%s'\n", peer, lan, wan)
	newConf := string(oldConf)
	if endpointLine.MatchString(newConf) {
		newConf = endpointLine.ReplaceAllString(newConf, "${1}"+lan)
	} else {
		newConf = strings.Replace(newConf, "[Peer]", "[Peer]\nEndpoint = "+lan, 1)
	}
	apply := func(ctx context.Context) error {
		if err := writeAtomic(envPath, []byte(newEnv), 0o644); err != nil {
			return err
		}
		if err := writeAtomic(confPath, []byte(newConf), 0o600); err != nil {
			return err
		}
		_, err := s.run.Run(ctx, "", "wg", "set", s.opt.WGInterface, "peer", peer, "endpoint", lan)
		return err
	}
	revert := func(ctx context.Context) error {
		if len(oldEnv) > 0 {
			writeAtomic(envPath, oldEnv, 0o644)
		}
		if err := writeAtomic(confPath, oldConf, 0o600); err != nil {
			return err
		}
		if oldEndpoint == "" {
			return nil
		}
		_, err := s.run.Run(ctx, "", "wg", "set", s.opt.WGInterface, "peer", peer, "endpoint", oldEndpoint)
		return err
	}
	what := "Hub address: " + lan
	if wan != "" {
		what += ", away from home " + wan
	}
	return s.startChange(what, req.Keep, apply, revert)
}

func writeAtomic(path string, data []byte, mode os.FileMode) error {
	tmp := path + ".new"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
