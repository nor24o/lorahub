package satpanel

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Ethernet is the wired connection as NetworkManager has it.
type Ethernet struct {
	Device    string   `json:"device"`
	Present   bool     `json:"present"`
	State     string   `json:"state,omitempty"`
	UUID      string   `json:"uuid,omitempty"`
	Name      string   `json:"name,omitempty"`
	Method    string   `json:"method,omitempty"` // auto (DHCP) or manual
	Addresses []string `json:"addresses"`
	Gateway   string   `json:"gateway,omitempty"`
	DNS       []string `json:"dns"`
	Current   []string `json:"current"` // addresses on the device now
	Bridged   bool     `json:"bridged"` // the settings belong to the access point's bridge
}

// SavedWiFi is a stored Wi-Fi profile.
type SavedWiFi struct {
	UUID        string `json:"uuid"`
	Name        string `json:"name"`
	Autoconnect bool   `json:"autoconnect"`
	Active      bool   `json:"active"`
}

// WiFi is the wireless device, its connection and saved profiles.
type WiFi struct {
	Device  string      `json:"device"`
	Present bool        `json:"present"`
	Radio   bool        `json:"radio"`
	State   string      `json:"state,omitempty"`
	UUID    string      `json:"uuid,omitempty"`
	Name    string      `json:"name,omitempty"`
	SSID    string      `json:"ssid,omitempty"`
	Signal  int         `json:"signal,omitempty"`
	Current []string    `json:"current"`
	Saved   []SavedWiFi `json:"saved"`
	// IP settings of the connected network's profile.
	Method    string   `json:"method,omitempty"`
	Addresses []string `json:"addresses"`
	Gateway   string   `json:"gateway,omitempty"`
	DNS       []string `json:"dns"`
}

// Network is what the Network tab shows.
type Network struct {
	Ethernet Ethernet    `json:"ethernet"`
	WiFi     WiFi        `json:"wifi"`
	AP       AccessPoint `json:"ap"`
	Change   *Change     `json:"change,omitempty"`
	Hub      bool        `json:"hub_reachable"`
}

// AP is one Wi-Fi network found by a scan.
type AP struct {
	SSID     string `json:"ssid"`
	Signal   int    `json:"signal"`
	Security string `json:"security,omitempty"`
	Channel  string `json:"channel,omitempty"`
	InUse    bool   `json:"in_use"`
}

type nmDevice struct{ typ, state, conn string }

type nmConn struct {
	uuid, typ, device, name string
	autoconnect             bool
}

// nmVerbs change NetworkManager's state; after one, cached answers are dropped.
var nmVerbs = map[string]bool{"add": true, "modify": true, "up": true, "down": true, "delete": true,
	"connect": true, "disconnect": true, "on": true, "off": true, "reapply": true}

func (s *Server) nmcli(ctx context.Context, args ...string) (string, error) {
	for _, a := range args {
		if nmVerbs[a] {
			s.nmMu.Lock()
			s.connsAt = time.Time{}
			s.nmMu.Unlock()
			break
		}
	}
	return s.run.Run(ctx, "", "nmcli", args...)
}

func (s *Server) nmDevices(ctx context.Context) map[string]nmDevice {
	out, _ := s.nmcli(ctx, "-t", "-f", "DEVICE,TYPE,STATE,CONNECTION", "device")
	devs := map[string]nmDevice{}
	for _, l := range lines(out) {
		if f := splitTerse(l); len(f) >= 4 {
			devs[f[0]] = nmDevice{typ: f[1], state: f[2], conn: f[3]}
		}
	}
	return devs
}

// nmConns lists the profiles. One page refresh asks several times, so the
// answer is reused for a moment.
func (s *Server) nmConns(ctx context.Context) []nmConn {
	s.nmMu.Lock()
	if s.now().Sub(s.connsAt) < connsFor {
		list := s.conns
		s.nmMu.Unlock()
		return list
	}
	s.nmMu.Unlock()
	list := s.readConns(ctx)
	s.nmMu.Lock()
	s.conns, s.connsAt = list, s.now()
	s.nmMu.Unlock()
	return list
}

const connsFor = 1500 * time.Millisecond

func (s *Server) readConns(ctx context.Context) []nmConn {
	out, _ := s.nmcli(ctx, "-t", "-f", "UUID,TYPE,DEVICE,AUTOCONNECT,NAME", "connection", "show")
	var list []nmConn
	for _, l := range lines(out) {
		f := splitTerse(l)
		if len(f) < 5 {
			continue
		}
		list = append(list, nmConn{uuid: f[0], typ: f[1], device: f[2], autoconnect: f[3] == "yes", name: strings.Join(f[4:], ":")})
	}
	return list
}

// connValues reads properties of one profile with `nmcli -g`, one per line.
func (s *Server) connValues(ctx context.Context, uuid string, fields ...string) []string {
	out, _ := s.nmcli(ctx, "-g", strings.Join(fields, ","), "connection", "show", "uuid", uuid)
	vals := strings.Split(strings.TrimRight(strings.ReplaceAll(out, "\r\n", "\n"), "\n"), "\n")
	for len(vals) < len(fields) {
		vals = append(vals, "")
	}
	for i := range vals {
		vals[i] = strings.ReplaceAll(strings.TrimSpace(vals[i]), `\:`, ":")
	}
	return vals
}

func deviceAddrs(dev string) []string {
	out := []string{}
	in, err := net.InterfaceByName(dev)
	if err != nil {
		return out
	}
	addrs, _ := in.Addrs()
	for _, a := range addrs {
		if ipn, ok := a.(*net.IPNet); ok && ipn.IP.To4() != nil {
			out = append(out, ipn.String())
		}
	}
	return out
}

// ipConn is the profile holding the wired IPv4 settings: the bridge while
// the access point is set up, otherwise the board's own wired profile.
func (s *Server) ipConn(ctx context.Context) (nmConn, bool) {
	if c, ok := s.findConn(ctx, bridgeConn); ok {
		return c, true
	}
	return s.wiredConn(ctx)
}

func (s *Server) network(ctx context.Context) Network {
	devs := s.nmDevices(ctx)
	n := Network{
		Ethernet: Ethernet{Device: s.opt.Ethernet, Addresses: []string{}, DNS: []string{}, Current: deviceAddrs(s.opt.Ethernet)},
		WiFi:     WiFi{Device: s.opt.WiFi, Current: deviceAddrs(s.opt.WiFi), Saved: []SavedWiFi{}, Addresses: []string{}, DNS: []string{}},
	}
	if d, ok := devs[s.opt.Ethernet]; ok {
		n.Ethernet.Present, n.Ethernet.State = true, d.state
	}
	if c, ok := s.ipConn(ctx); ok {
		if c.name == bridgeConn {
			n.Ethernet.Bridged, n.Ethernet.Current = true, deviceAddrs(bridgeIf)
		}
		v := s.connValues(ctx, c.uuid, "ipv4.method", "ipv4.addresses", "ipv4.gateway", "ipv4.dns")
		n.Ethernet.UUID, n.Ethernet.Name, n.Ethernet.Method = c.uuid, c.name, v[0]
		n.Ethernet.Addresses, n.Ethernet.DNS = nonNil(splitList(v[1])), nonNil(splitList(v[3]))
		if v[2] != "--" {
			n.Ethernet.Gateway = v[2]
		}
	}
	if d, ok := devs[s.opt.WiFi]; ok {
		n.WiFi.Present, n.WiFi.State = true, d.state
		radio, _ := s.nmcli(ctx, "radio", "wifi")
		n.WiFi.Radio = strings.TrimSpace(radio) == "enabled"
	}
	for _, c := range s.nmConns(ctx) {
		if c.typ != "802-11-wireless" || c.name == apConn {
			continue
		}
		active := c.device == s.opt.WiFi
		n.WiFi.Saved = append(n.WiFi.Saved, SavedWiFi{UUID: c.uuid, Name: c.name, Autoconnect: c.autoconnect, Active: active})
		if active {
			n.WiFi.UUID, n.WiFi.Name = c.uuid, c.name
		}
	}
	if n.WiFi.UUID != "" {
		v := s.connValues(ctx, n.WiFi.UUID, "ipv4.method", "ipv4.addresses", "ipv4.gateway", "ipv4.dns")
		n.WiFi.Method, n.WiFi.Addresses, n.WiFi.DNS = v[0], nonNil(splitList(v[1])), nonNil(splitList(v[3]))
		if v[2] != "--" {
			n.WiFi.Gateway = v[2]
		}
		for _, ap := range s.scan(ctx, false) {
			if ap.InUse {
				n.WiFi.SSID, n.WiFi.Signal = ap.SSID, ap.Signal
			}
		}
	}
	n.AP = s.accessPoint(ctx)
	n.Hub, _ = s.hubPing(ctx, false)
	n.Change = s.currentChange()
	return n
}

func nonNil(l []string) []string {
	if l == nil {
		return []string{}
	}
	return l
}

// scan lists Wi-Fi networks, strongest first, one entry per name.
func (s *Server) scan(ctx context.Context, rescan bool) []AP {
	re := "no"
	if rescan {
		re = "yes"
	}
	out, _ := s.nmcli(ctx, "-t", "-f", "IN-USE,SIGNAL,SECURITY,CHAN,SSID", "device", "wifi", "list", "ifname", s.opt.WiFi, "--rescan", re)
	best := map[string]AP{}
	for _, l := range lines(out) {
		f := splitTerse(l)
		if len(f) < 5 {
			continue
		}
		ssid := strings.Join(f[4:], ":")
		if ssid == "" || ssid == "--" {
			continue
		}
		sig, _ := strconv.Atoi(f[1])
		ap := AP{SSID: ssid, Signal: sig, Security: strings.TrimSpace(f[2]), Channel: f[3], InUse: strings.TrimSpace(f[0]) == "*"}
		if old, ok := best[ssid]; ok {
			ap.InUse = ap.InUse || old.InUse
			if old.Signal >= sig {
				ap.Signal, ap.Security, ap.Channel = old.Signal, old.Security, old.Channel
			}
		}
		best[ssid] = ap
	}
	list := make([]AP, 0, len(best))
	for _, ap := range best {
		list = append(list, ap)
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].Signal != list[j].Signal {
			return list[i].Signal > list[j].Signal
		}
		return list[i].SSID < list[j].SSID
	})
	return list
}

// EthernetRequest is a new wired address setting.
type EthernetRequest struct {
	Iface   string `json:"iface"`   // ethernet (default) or wifi: the connected Wi-Fi network
	Method  string `json:"method"`  // auto (DHCP) or manual
	Address string `json:"address"` // 192.168.1.16/24 when manual
	Gateway string `json:"gateway"`
	DNS     string `json:"dns"` // space or comma separated
	Keep    bool   `json:"keep"`
}

// args checks the request and returns the nmcli property list for it.
func (r EthernetRequest) args() ([]string, error) {
	var dns []string
	for _, d := range splitList(r.DNS) {
		a, err := netip.ParseAddr(d)
		if err != nil || !a.Is4() {
			return nil, fmt.Errorf("DNS server %q is not an IPv4 address", d)
		}
		dns = append(dns, a.String())
	}
	dnsArg := strings.Join(dns, ",")
	ownDNS := []string{"ipv4.ignore-auto-dns", yesNo(len(dns) > 0)} // typed-in servers replace the router's
	switch r.Method {
	case "auto":
		return append([]string{"ipv4.method", "auto", "ipv4.addresses", "", "ipv4.gateway", "", "ipv4.dns", dnsArg}, ownDNS...), nil
	case "manual":
		p, err := netip.ParsePrefix(strings.TrimSpace(r.Address))
		if err != nil || !p.Addr().Is4() || p.Bits() < 8 {
			return nil, errors.New("the address must look like 192.168.1.16/24")
		}
		if p.Addr() == p.Masked().Addr() && p.Bits() < 31 {
			return nil, errors.New("that is the network's own address, not one for this Bobcat")
		}
		gw := ""
		if g := strings.TrimSpace(r.Gateway); g != "" {
			a, err := netip.ParseAddr(g)
			if err != nil || !a.Is4() {
				return nil, errors.New("the gateway must be an IPv4 address such as 192.168.1.1")
			}
			if !p.Masked().Contains(a) {
				return nil, fmt.Errorf("the gateway %s is outside %s", a, p.Masked())
			}
			gw = a.String()
		}
		return append([]string{"ipv4.method", "manual", "ipv4.addresses", p.String(), "ipv4.gateway", gw, "ipv4.dns", dnsArg}, ownDNS...), nil
	}
	return nil, errors.New("method must be auto (DHCP) or manual")
}

// setEthernet applies a wired setting under the hub check, restoring the
// profile's previous values if the hub stops answering.
func (s *Server) setEthernet(ctx context.Context, req EthernetRequest) (Change, error) {
	props, err := req.args()
	if err != nil {
		return Change{}, badRequest(err)
	}
	c, ok := s.ipConn(ctx)
	label := "Wired address"
	if req.Iface == "wifi" {
		if err := s.wifiBusyWithAP(ctx); err != nil {
			return Change{}, err
		}
		uuid := s.activeWiFi(ctx)
		if uuid == "" {
			return Change{}, badRequest(errors.New("Wi-Fi is not connected to a network"))
		}
		c, ok, label = nmConn{uuid: uuid, device: s.opt.WiFi}, true, "Wi-Fi address"
	} else if req.Iface != "" && req.Iface != "ethernet" {
		return Change{}, badRequest(errors.New("iface must be ethernet or wifi"))
	}
	if !ok {
		return Change{}, badRequest(errors.New("there is no wired connection profile"))
	}
	old := s.connValues(ctx, c.uuid, "ipv4.method", "ipv4.addresses", "ipv4.gateway", "ipv4.dns", "ipv4.ignore-auto-dns")
	gw := old[2]
	if gw == "--" {
		gw = ""
	}
	prev := []string{"ipv4.method", old[0], "ipv4.addresses", strings.Join(splitList(old[1]), ","), "ipv4.gateway", gw, "ipv4.dns", strings.Join(splitList(old[3]), ",")}
	if old[4] == "yes" || old[4] == "no" {
		prev = append(prev, "ipv4.ignore-auto-dns", old[4])
	}
	set := func(ctx context.Context, props []string) error {
		if _, err := s.nmcli(ctx, append([]string{"connection", "modify", "uuid", c.uuid}, props...)...); err != nil {
			return err
		}
		return s.reapply(ctx, c)
	}
	apply := func(ctx context.Context) error { return set(ctx, props) }
	revert := func(ctx context.Context) error { return set(ctx, prev) }
	what := label + ": DHCP"
	if req.Method == "manual" {
		what = label + ": " + props[3]
	}
	if d := props[7]; d != "" {
		what += ", DNS " + strings.ReplaceAll(d, ",", " ")
	}
	return s.startChange(what, req.Keep, apply, revert)
}

// reapply puts a changed profile into effect. On an active connection
// `nmcli device reapply` does it in place, so a Wi-Fi link is not dropped;
// otherwise, or if that fails, the profile is brought up again.
func (s *Server) reapply(ctx context.Context, c nmConn) error {
	if c.device != "" {
		if _, err := s.nmcli(ctx, "device", "reapply", c.device); err == nil {
			return nil
		}
	}
	_, err := s.nmcli(ctx, "--wait", "45", "connection", "up", "uuid", c.uuid)
	return err
}

// WiFiRequest connects to a Wi-Fi network.
type WiFiRequest struct {
	SSID     string `json:"ssid"`
	Password string `json:"password"`
	Keep     bool   `json:"keep"`
}

func (r WiFiRequest) check() error {
	if n := len(r.SSID); n == 0 || n > 32 {
		return errors.New("the network name must be 1 to 32 bytes")
	}
	if p := r.Password; p != "" && (len(p) < 8 || len(p) > 64) {
		return errors.New("a Wi-Fi password has 8 to 63 characters")
	}
	return nil
}

// activeWiFi is the profile now active on the wireless device.
func (s *Server) activeWiFi(ctx context.Context) string {
	for _, c := range s.nmConns(ctx) {
		if c.typ == "802-11-wireless" && c.device == s.opt.WiFi && c.name != apConn {
			return c.uuid
		}
	}
	return ""
}

func (s *Server) connectWiFi(ctx context.Context, req WiFiRequest) (Change, error) {
	if err := req.check(); err != nil {
		return Change{}, badRequest(err)
	}
	if err := s.wifiBusyWithAP(ctx); err != nil {
		return Change{}, err
	}
	prev := s.activeWiFi(ctx)
	apply := func(ctx context.Context) error {
		s.nmcli(ctx, "radio", "wifi", "on")
		args := []string{"--wait", "45", "device", "wifi", "connect", req.SSID}
		if req.Password != "" {
			args = append(args, "password", req.Password)
		}
		if _, err := s.nmcli(ctx, append(args, "ifname", s.opt.WiFi)...); err != nil {
			return err
		}
		if now := s.activeWiFi(ctx); now != "" { // reconnect after a reboot too
			_, err := s.nmcli(ctx, "connection", "modify", "uuid", now, "connection.autoconnect", "yes")
			return err
		}
		return nil
	}
	revert := func(ctx context.Context) error {
		if prev != "" {
			_, err := s.nmcli(ctx, "--wait", "45", "connection", "up", "uuid", prev)
			return err
		}
		if now := s.activeWiFi(ctx); now != "" {
			s.nmcli(ctx, "connection", "modify", "uuid", now, "connection.autoconnect", "no")
		}
		_, err := s.nmcli(ctx, "device", "disconnect", s.opt.WiFi)
		return err
	}
	return s.startChange("Wi-Fi: connect to "+req.SSID, req.Keep, apply, revert)
}

// setWiFiRadio switches the Wi-Fi radio; on the hub it may be the only way to
// the router, so it is undone when the router stops answering.
func (s *Server) setWiFiRadio(ctx context.Context, req RadioRequest) (Change, error) {
	to, back := "on", "off"
	if !req.On {
		to, back = "off", "on"
	}
	apply := func(ctx context.Context) error {
		_, err := s.nmcli(ctx, "radio", "wifi", to)
		return err
	}
	revert := func(ctx context.Context) error {
		_, err := s.nmcli(ctx, "radio", "wifi", back)
		return err
	}
	return s.startChange("Wi-Fi radio "+to, req.Keep, apply, revert)
}

// wifiBusyWithAP refuses client changes while the Wi-Fi is the access point.
func (s *Server) wifiBusyWithAP(ctx context.Context) error {
	if c, ok := s.findConn(ctx, apConn); ok && (c.autoconnect || s.cable()) {
		return badRequest(errors.New("the Wi-Fi is the access point now; turn the access point off first, or unplug the cable if it runs only while the cable is connected"))
	}
	return nil
}

func (s *Server) disconnectWiFi(ctx context.Context, keep bool) (Change, error) {
	prev := s.activeWiFi(ctx)
	if prev == "" {
		return Change{}, badRequest(errors.New("Wi-Fi is not connected"))
	}
	apply := func(ctx context.Context) error {
		_, err := s.nmcli(ctx, "device", "disconnect", s.opt.WiFi)
		return err
	}
	revert := func(ctx context.Context) error {
		_, err := s.nmcli(ctx, "--wait", "45", "connection", "up", "uuid", prev)
		return err
	}
	return s.startChange("Wi-Fi: disconnect", keep, apply, revert)
}

func (s *Server) savedWiFi(ctx context.Context, uuid string) (nmConn, bool) {
	for _, c := range s.nmConns(ctx) {
		if c.typ == "802-11-wireless" && c.uuid == uuid && c.name != apConn {
			return c, true
		}
	}
	return nmConn{}, false
}

func (s *Server) setAutoconnect(ctx context.Context, uuid string, on bool) error {
	if _, ok := s.savedWiFi(ctx, uuid); !ok {
		return badRequest(errors.New("no such Wi-Fi profile"))
	}
	v := "no"
	if on {
		v = "yes"
	}
	_, err := s.nmcli(ctx, "connection", "modify", "uuid", uuid, "connection.autoconnect", v)
	return err
}

func (s *Server) forgetWiFi(ctx context.Context, uuid string) error {
	c, ok := s.savedWiFi(ctx, uuid)
	if !ok {
		return badRequest(errors.New("no such Wi-Fi profile"))
	}
	if c.device != "" {
		return badRequest(errors.New("disconnect this network before forgetting it"))
	}
	_, err := s.nmcli(ctx, "connection", "delete", "uuid", uuid)
	return err
}

// waitCtx gives slow nmcli calls room.
func waitCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, 60*time.Second)
}
