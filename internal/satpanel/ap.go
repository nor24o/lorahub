package satpanel

import (
	"context"
	"errors"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

// The access point has two modes.
//
// Bridge: a Wi-Fi port of a bridge that also holds the wired port, so phones on
// it join the home network and get addresses from the home router. Three
// NetworkManager profiles make it up; the board's own wired profile is kept
// (autoconnect off) and comes back when the access point is turned off.
//
// Hotspot: a network of its own, 10.42.0.0/24, with this board handing out
// addresses (NetworkManager's shared mode, which needs dnsmasq) and sharing its
// other connection. It also works with no cable at all, to reach the board in
// the field.
const (
	apConn      = "lorahub-ap"
	bridgeConn  = "lorahub-br0"
	portConn    = "lorahub-br0-port"
	bridgeIf    = "br0"
	modeBridge  = "bridge"
	modeHotspot = "hotspot"
	hotspotAddr = "10.42.0.1/24"
)

// AccessPoint is the state of the bridged access point.
type AccessPoint struct {
	Supported bool   `json:"supported"`
	HotspotOK bool   `json:"hotspot_ok"` // dnsmasq is installed
	Enabled   bool   `json:"enabled"`
	Mode      string `json:"mode,omitempty"` // bridge or hotspot
	Address   string `json:"address,omitempty"`
	Active    bool   `json:"active"`
	SSID      string `json:"ssid,omitempty"`
	Channel   int    `json:"channel,omitempty"`
	OnlyWired bool   `json:"only_wired"` // on only while the cable is connected
	Clients   int    `json:"clients"`
	Cable     bool   `json:"cable"`
	Bridged   bool   `json:"bridged"`
}

func (s *Server) findConn(ctx context.Context, name string) (nmConn, bool) {
	for _, c := range s.nmConns(ctx) {
		if c.name == name {
			return c, true
		}
	}
	return nmConn{}, false
}

func (s *Server) cable() bool {
	return s.readText("/sys/class/net/"+s.opt.Ethernet+"/carrier") == "1"
}

func (s *Server) apSupported(ctx context.Context) bool {
	s.apOnce.Do(func() {
		out, _ := s.run.Run(ctx, "", "iw", "list")
		s.apOK = strings.Contains(out, "* AP\n") || strings.Contains(out, "* AP\r\n")
	})
	return s.apOK
}

func (s *Server) hotspotOK() bool {
	for _, p := range []string{"/usr/sbin/dnsmasq", "/usr/bin/dnsmasq"} {
		if _, err := os.Stat(s.path(p)); err == nil {
			return true
		}
	}
	return false
}

// apMode tells the two kinds of access point profile apart.
func (s *Server) apMode(ctx context.Context, uuid string) string {
	if v := s.connValues(ctx, uuid, "connection.master"); v[0] != "" && v[0] != "--" {
		return modeBridge
	}
	return modeHotspot
}

func (s *Server) accessPoint(ctx context.Context) AccessPoint {
	ap := AccessPoint{Supported: s.apSupported(ctx), HotspotOK: s.hotspotOK(), Cable: s.cable()}
	_, ap.Bridged = s.findConn(ctx, bridgeConn)
	c, ok := s.findConn(ctx, apConn)
	if !ok {
		return ap
	}
	ap.Enabled, ap.Active, ap.OnlyWired = true, c.device == s.opt.WiFi, !c.autoconnect
	v := s.connValues(ctx, c.uuid, "802-11-wireless.ssid", "802-11-wireless.channel", "connection.master")
	ap.SSID = v[0]
	ap.Channel, _ = strconv.Atoi(v[1])
	ap.Mode = modeHotspot
	if v[2] != "" && v[2] != "--" {
		ap.Mode = modeBridge
	} else {
		ap.Address = hotspotAddr
	}
	if ap.Active {
		out, _ := s.run.Run(ctx, "", "iw", "dev", s.opt.WiFi, "station", "dump")
		ap.Clients = strings.Count(out, "Station ")
	}
	return ap
}

// APRequest turns the access point on, changes it or turns it off.
type APRequest struct {
	Enabled   bool   `json:"enabled"`
	Mode      string `json:"mode"` // bridge (default) or hotspot
	SSID      string `json:"ssid"`
	Password  string `json:"password"` // empty keeps the current one
	Channel   int    `json:"channel"`
	OnlyWired bool   `json:"only_wired"`
	Keep      bool   `json:"keep"`
}

func (r *APRequest) check(exists bool) error {
	if r.Mode == "" {
		r.Mode = modeBridge
	}
	if r.Mode != modeBridge && r.Mode != modeHotspot {
		return errors.New("mode must be bridge or hotspot")
	}
	if n := len(r.SSID); n == 0 || n > 32 {
		return errors.New("the access point name must be 1 to 32 bytes")
	}
	if r.Password == "" && !exists {
		return errors.New("choose a password of 8 to 63 characters")
	}
	if p := r.Password; p != "" && (len(p) < 8 || len(p) > 63) {
		return errors.New("the password must have 8 to 63 characters")
	}
	if r.Channel == 0 {
		r.Channel = 6
	}
	if r.Channel < 1 || r.Channel > 13 {
		return errors.New("the channel must be 1 to 13")
	}
	return nil
}

// wiredConn is the board's own wired profile (not the bridge port).
func (s *Server) wiredConn(ctx context.Context) (nmConn, bool) {
	var first *nmConn
	for _, c := range s.nmConns(ctx) {
		if c.typ != "802-3-ethernet" || c.name == portConn {
			continue
		}
		if c.device == s.opt.Ethernet {
			return c, true
		}
		if first == nil {
			c := c
			first = &c
		}
	}
	if first != nil {
		return *first, true
	}
	return nmConn{}, false
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func (s *Server) setAP(ctx context.Context, req APRequest) (Change, error) {
	if !s.apSupported(ctx) {
		return Change{}, badRequest(errors.New("this Wi-Fi chip cannot be an access point"))
	}
	cur, exists := s.findConn(ctx, apConn)
	_, bridged := s.findConn(ctx, bridgeConn)
	if !req.Enabled {
		if !exists && !bridged {
			return Change{}, badRequest(errors.New("the access point is already off"))
		}
		return s.startChange("Access point off", true, s.teardownAP, func(context.Context) error { return nil })
	}
	samePassword := exists && s.apMode(ctx, cur.uuid) == req.Mode
	if err := req.check(samePassword); err != nil {
		return Change{}, badRequest(err)
	}
	if req.Mode == modeHotspot && !s.hotspotOK() {
		return Change{}, badRequest(errors.New("an access point with its own network needs dnsmasq; install it with: apt install dnsmasq-base"))
	}
	if exists && s.apMode(ctx, cur.uuid) == req.Mode && (req.Mode == modeHotspot || bridged) {
		return s.startChange("Access point "+req.SSID, true, func(ctx context.Context) error { return s.updateAP(ctx, req) },
			func(context.Context) error { return nil })
	}
	if req.Mode == modeHotspot {
		return s.startChange("Access point "+req.SSID+", own network "+hotspotAddr, req.Keep,
			func(ctx context.Context) error { s.teardownAP(ctx); return s.setupHotspot(ctx, req) }, s.teardownAP)
	}
	w, ok := s.wiredConn(ctx)
	if !ok {
		return Change{}, badRequest(errors.New("there is no wired connection profile to bridge"))
	}
	return s.startChange("Access point "+req.SSID+", bridged to the wired network", req.Keep,
		func(ctx context.Context) error {
			if exists {
				s.teardownAP(ctx)
			}
			return s.setupAP(ctx, w, req)
		}, s.teardownAP)
}

// setupHotspot makes the access point a network of its own.
func (s *Server) setupHotspot(ctx context.Context, req APRequest) error {
	s.deleteAPConns(ctx)
	args := append([]string{"connection", "add", "type", "wifi", "ifname", s.opt.WiFi, "con-name", apConn,
		"ipv4.method", "shared", "ipv4.addresses", hotspotAddr, "ipv6.method", "disabled"}, s.apProps(req)...)
	if _, err := s.nmcli(ctx, args...); err != nil {
		return err
	}
	if s.cable() || !req.OnlyWired {
		s.nmcli(ctx, "radio", "wifi", "on")
		if _, err := s.nmcli(ctx, "--wait", "30", "connection", "up", apConn); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) apProps(req APRequest) []string {
	p := []string{"802-11-wireless.ssid", req.SSID, "802-11-wireless.mode", "ap", "802-11-wireless.band", "bg",
		"802-11-wireless.channel", strconv.Itoa(req.Channel), "connection.autoconnect", yesNo(!req.OnlyWired)}
	if req.Password != "" {
		p = append(p, "wifi-sec.key-mgmt", "wpa-psk", "wifi-sec.proto", "rsn", "wifi-sec.pairwise", "ccmp",
			"wifi-sec.group", "ccmp", "wifi-sec.psk", req.Password)
	}
	return p
}

func (s *Server) setupAP(ctx context.Context, w nmConn, req APRequest) error {
	v := s.connValues(ctx, w.uuid, "ipv4.method", "ipv4.addresses", "ipv4.gateway", "ipv4.dns")
	s.deleteAPConns(ctx)
	// The bridge takes over the wired IPv4 settings and the wired MAC address,
	// so the home router hands out the same address as before.
	br := []string{"connection", "add", "type", "bridge", "ifname", bridgeIf, "con-name", bridgeConn,
		"bridge.stp", "no", "connection.autoconnect", "yes", "ipv4.method", v[0]}
	if in, err := net.InterfaceByName(s.opt.Ethernet); err == nil && len(in.HardwareAddr) == 6 {
		br = append(br, "bridge.mac-address", in.HardwareAddr.String())
	}
	if v[0] == "manual" {
		br = append(br, "ipv4.addresses", strings.Join(splitList(v[1]), ","))
		if v[2] != "" && v[2] != "--" {
			br = append(br, "ipv4.gateway", v[2])
		}
	}
	if dns := splitList(v[3]); len(dns) > 0 {
		br = append(br, "ipv4.dns", strings.Join(dns, ","))
	}
	if _, err := s.nmcli(ctx, br...); err != nil {
		return err
	}
	if _, err := s.nmcli(ctx, "connection", "add", "type", "ethernet", "ifname", s.opt.Ethernet, "con-name", portConn,
		"master", bridgeIf, "slave-type", "bridge", "connection.autoconnect", "yes"); err != nil {
		return err
	}
	ap := append([]string{"connection", "add", "type", "wifi", "ifname", s.opt.WiFi, "con-name", apConn,
		"master", bridgeIf, "slave-type", "bridge"}, s.apProps(req)...)
	if _, err := s.nmcli(ctx, ap...); err != nil {
		return err
	}
	if _, err := s.nmcli(ctx, "connection", "modify", "uuid", w.uuid, "connection.autoconnect", "no"); err != nil {
		return err
	}
	if _, err := s.nmcli(ctx, "--wait", "45", "connection", "up", portConn); err != nil {
		return err
	}
	if s.cable() || !req.OnlyWired {
		s.nmcli(ctx, "radio", "wifi", "on")
		if _, err := s.nmcli(ctx, "--wait", "30", "connection", "up", apConn); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) updateAP(ctx context.Context, req APRequest) error {
	c, ok := s.findConn(ctx, apConn)
	if !ok {
		return errors.New("the access point profile is gone")
	}
	if _, err := s.nmcli(ctx, append([]string{"connection", "modify", "uuid", c.uuid}, s.apProps(req)...)...); err != nil {
		return err
	}
	if s.cable() || !req.OnlyWired {
		_, err := s.nmcli(ctx, "--wait", "30", "connection", "up", "uuid", c.uuid)
		return err
	}
	if c.device != "" {
		s.nmcli(ctx, "connection", "down", "uuid", c.uuid)
	}
	return nil
}

// teardownAP gives the wired port back to the board's own profile and removes
// the bridge and access point profiles.
func (s *Server) teardownAP(ctx context.Context) error {
	var upErr error
	_, bridged := s.findConn(ctx, bridgeConn)
	if w, ok := s.wiredConn(ctx); ok && bridged {
		s.nmcli(ctx, "connection", "modify", "uuid", w.uuid, "connection.autoconnect", "yes")
		if _, err := s.nmcli(ctx, "--wait", "45", "connection", "up", "uuid", w.uuid); err != nil && s.cable() {
			upErr = err
		}
	}
	s.deleteAPConns(ctx)
	return upErr
}

func (s *Server) deleteAPConns(ctx context.Context) {
	for _, name := range []string{apConn, portConn, bridgeConn} {
		for {
			c, ok := s.findConn(ctx, name)
			if !ok {
				break
			}
			if _, err := s.nmcli(ctx, "connection", "delete", "uuid", c.uuid); err != nil {
				break
			}
		}
	}
}

// apLoop keeps an "only while the cable is connected" access point in step
// with the cable: on when it is plugged in, off when it is not, so the Wi-Fi
// can then reach the hub as a normal client.
func (s *Server) apLoop(ctx context.Context) {
	t := time.NewTicker(s.opt.APEvery)
	defer t.Stop()
	last, same := false, 0
	var checkedAt time.Time
	checkedCable := false
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if c := s.currentChange(); c != nil && !c.done() {
			continue
		}
		cable := s.cable()
		if cable == last {
			same++
		} else {
			last, same = cable, 1
		}
		if same < 2 { // the same reading twice in a row
			continue
		}
		// NetworkManager is asked only when the cable changed, or once a minute.
		if !checkedAt.IsZero() && cable == checkedCable && s.now().Sub(checkedAt) < time.Minute {
			continue
		}
		checkedAt, checkedCable = s.now(), cable
		cctx, cancel := context.WithTimeout(ctx, 45*time.Second)
		c, ok := s.findConn(cctx, apConn)
		if ok && !c.autoconnect {
			active := c.device == s.opt.WiFi
			switch {
			case cable && !active:
				s.log.Info("cable connected, starting the access point")
				if _, err := s.nmcli(cctx, "--wait", "30", "connection", "up", "uuid", c.uuid); err != nil {
					s.log.Warn("access point did not start", "err", err)
				}
			case !cable && active:
				s.log.Info("cable disconnected, stopping the access point")
				s.nmcli(cctx, "connection", "down", "uuid", c.uuid)
			}
		}
		cancel()
	}
}
