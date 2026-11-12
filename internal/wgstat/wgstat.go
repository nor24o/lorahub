// Package wgstat reads the state of a WireGuard interface from `wg show <if> dump`
// and the peer names written as comments in a wg-quick file. It is shared by the
// hub's VPN page and the satellite page.
package wgstat

import (
	"strconv"
	"strings"
	"time"
)

// ConnectedFor is how recent a handshake must be for a peer to count as
// connected; peers shake hands at least every two minutes while traffic flows.
const ConnectedFor = 180 * time.Second

// Peer is one [Peer] of the interface.
type Peer struct {
	Name          string     `json:"name,omitempty"`
	PublicKey     string     `json:"public_key"`
	Endpoint      string     `json:"endpoint,omitempty"`
	AllowedIPs    []string   `json:"allowed_ips"`
	LastHandshake *time.Time `json:"last_handshake,omitempty"`
	Connected     bool       `json:"connected"`
	RX            uint64     `json:"rx_bytes"`
	TX            uint64     `json:"tx_bytes"`
}

// Status is the interface and its peers. It never holds the private key.
type Status struct {
	Installed  bool   `json:"installed"`
	Service    string `json:"service,omitempty"`
	Up         bool   `json:"up"`
	Interface  string `json:"interface"`
	Address    string `json:"address,omitempty"`
	PublicKey  string `json:"public_key,omitempty"`
	ListenPort int    `json:"listen_port,omitempty"`
	Peers      []Peer `json:"peers"`
}

// ParseDump reads `wg show <if> dump`. The first line holds the interface's
// private key, which is deliberately not kept.
func ParseDump(out string, now time.Time, st *Status) {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if f := strings.Split(lines[0], "\t"); len(f) >= 3 {
		st.PublicKey = f[1]
		st.ListenPort, _ = strconv.Atoi(f[2])
	}
	for _, line := range lines[1:] {
		f := strings.Split(line, "\t")
		if len(f) < 7 {
			continue
		}
		p := Peer{PublicKey: f[0], AllowedIPs: strings.Split(f[3], ",")}
		if f[2] != "(none)" {
			p.Endpoint = f[2]
		}
		if at, _ := strconv.ParseInt(f[4], 10, 64); at > 0 {
			t := time.Unix(at, 0).UTC()
			p.LastHandshake, p.Connected = &t, now.Sub(t) < ConnectedFor
		}
		p.RX, _ = strconv.ParseUint(f[5], 10, 64)
		p.TX, _ = strconv.ParseUint(f[6], 10, 64)
		st.Peers = append(st.Peers, p)
	}
}

// PeerNames finds the comment naming each [Peer] of a wg-quick file.
func PeerNames(conf string) map[string]string {
	names := map[string]string{}
	comment, inPeer := "", false
	for _, line := range strings.Split(conf, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "["):
			inPeer, comment = strings.EqualFold(line, "[Peer]"), ""
		case inPeer && strings.HasPrefix(line, "#"):
			if comment == "" {
				comment = strings.TrimSpace(strings.TrimLeft(line, "#"))
			}
		case inPeer:
			if k, v, ok := strings.Cut(line, "="); ok && strings.EqualFold(strings.TrimSpace(k), "PublicKey") {
				names[strings.TrimSpace(v)] = comment
			}
		}
	}
	return names
}
