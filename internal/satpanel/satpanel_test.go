package satpanel

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeRun answers commands from a table keyed by the full command line and
// records every call.
type fakeRun struct {
	mu    sync.Mutex
	calls []string
	resp  func(cmd string) (string, error)
}

func (f *fakeRun) Run(_ context.Context, _ string, name string, args ...string) (string, error) {
	cmd := strings.TrimSpace(name + " " + strings.Join(args, " "))
	f.mu.Lock()
	f.calls = append(f.calls, cmd)
	f.mu.Unlock()
	if f.resp == nil {
		return "", nil
	}
	return f.resp(cmd)
}

func (f *fakeRun) called(prefix string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			out = append(out, c)
		}
	}
	return out
}

func testServer(t *testing.T, run *fakeRun) *Server {
	t.Helper()
	root := t.TempDir()
	write := func(p, s string) {
		full := filepath.Join(root, filepath.FromSlash(p))
		os.MkdirAll(filepath.Dir(full), 0o755)
		if err := os.WriteFile(full, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("/etc/lora/config.json", `{"target_ip":"10.77.0.1","status_port":1,"rf0_freq":867500000,"link_key":"secret"}`)
	write("/etc/wireguard/wg0.conf", "[Interface]\nAddress = 10.77.0.3/24\n\n[Peer]\n# LoRaHub Bobcat\nPublicKey = HUBKEY\nEndpoint = 192.168.1.15:51820\nAllowedIPs = 10.77.0.0/24\n")
	write("/etc/wireguard/roam.env", "PEER='HUBKEY'\nLAN='192.168.1.15:51820'\nWAN=''\n")
	write("/sys/class/net/end0/carrier", "1\n")
	write("/proc/meminfo", "MemTotal:        2026668 kB\nMemFree:  100 kB\nMemAvailable:    1795000 kB\n")
	write("/proc/loadavg", "0.26 0.27 0.14 1/200 1234\n")
	write("/proc/uptime", "321.5 1000.0\n")
	write("/etc/hostname", "bobcat-sat3\n")
	write("/sys/class/thermal/thermal_zone0/type", "cpu-thermal\n")
	write("/sys/class/thermal/thermal_zone0/temp", "51250\n")
	return New(Options{Root: root, Run: run, CheckFor: 150 * time.Millisecond, CheckEvery: 20 * time.Millisecond,
		ApplyDelay: time.Millisecond, LoginDelay: time.Millisecond, CheckPass: func(p string) bool { return p == "right" },
		Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
}

func waitDone(t *testing.T, s *Server) Change {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if c := s.currentChange(); c != nil && c.done() {
			return *c
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("change did not finish")
	return Change{}
}

func TestSplitTerse(t *testing.T) {
	got := splitTerse(`*:72:WPA2:6:My\:Net\\x`)
	want := []string{"*", "72", "WPA2", "6", `My:Net\x`}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %q", got)
	}
}

func TestSystemInfo(t *testing.T) {
	s := testServer(t, &fakeRun{})
	si := s.system()
	if si.Hostname != "bobcat-sat3" || si.MemTotal != 2026668*1024 || si.MemAvail != 1795000*1024 {
		t.Fatalf("system %+v", si)
	}
	if si.Load[0] != 0.26 || si.UptimeS != 321.5 || si.Temps["cpu-thermal"] != 51.2 {
		t.Fatalf("system %+v", si)
	}
	r := radioSummary(s.loraConfig())
	if _, leaked := r["link_key"]; leaked || r["paired"] != true || r["rf0_freq"].(float64) != 867500000 {
		t.Fatalf("radio summary %v", r)
	}
}

func TestEthernetArgs(t *testing.T) {
	ok := []EthernetRequest{
		{Method: "auto"},
		{Method: "manual", Address: "192.168.1.16/24", Gateway: "192.168.1.1", DNS: "1.1.1.1, 8.8.8.8"},
	}
	for _, r := range ok {
		if _, err := r.args(); err != nil {
			t.Errorf("%+v: %v", r, err)
		}
	}
	bad := []EthernetRequest{
		{Method: "x"},
		{Method: "manual", Address: "192.168.1.16"},
		{Method: "manual", Address: "192.168.1.0/24"},
		{Method: "manual", Address: "192.168.1.16/24", Gateway: "10.0.0.1"},
		{Method: "auto", DNS: "dns.example"},
	}
	for _, r := range bad {
		if _, err := r.args(); err == nil {
			t.Errorf("%+v accepted", r)
		}
	}
	a, _ := EthernetRequest{Method: "manual", Address: "192.168.1.16/24", Gateway: "192.168.1.1"}.args()
	if strings.Join(a, " ") != "ipv4.method manual ipv4.addresses 192.168.1.16/24 ipv4.gateway 192.168.1.1 ipv4.dns  ipv4.ignore-auto-dns no" {
		t.Fatalf("args %q", a)
	}
}

func TestCheckEndpoint(t *testing.T) {
	for _, ep := range []string{"192.168.1.15:51820", "home.example.net:51820"} {
		if _, err := checkEndpoint(ep, true); err != nil {
			t.Errorf("%s: %v", ep, err)
		}
	}
	for _, ep := range []string{"", "192.168.1.15", "x:0", "a b:1", "[::1]:51820", "'; rm -rf /:1"} {
		if _, err := checkEndpoint(ep, true); err == nil {
			t.Errorf("%q accepted", ep)
		}
	}
}

// nmState answers the nmcli queries for a board with one wired profile.
func nmState(hub *bool) func(string) (string, error) {
	return func(cmd string) (string, error) {
		switch {
		case cmd == "ping -c 1 -W 2 10.77.0.1":
			if *hub {
				return "64 bytes from 10.77.0.1: icmp_seq=1 ttl=64 time=1.42 ms\n", nil
			}
			return "", errors.New("no answer")
		case strings.HasPrefix(cmd, "nmcli -t -f UUID,TYPE,DEVICE,AUTOCONNECT,NAME connection show"):
			return "u-wired:802-3-ethernet:end0:yes:Conexiune prin fir 1\nu-link2:802-11-wireless::no:Link2\n", nil
		case strings.HasPrefix(cmd, "nmcli -g ipv4.method,ipv4.addresses,ipv4.gateway,ipv4.dns") && strings.HasSuffix(cmd, "connection show uuid u-wired"):
			return "auto\n\n\n\nno\n", nil
		case strings.HasPrefix(cmd, "nmcli -t -f DEVICE,TYPE,STATE,CONNECTION device"):
			return "end0:ethernet:connected:Conexiune prin fir 1\nwlan0:wifi:disconnected:\n", nil
		case cmd == "iw list":
			return "\tSupported interface modes:\n\t\t * managed\n\t\t * AP\n", nil
		}
		return "", nil
	}
}

func TestChangeRevertedWhenHubSilent(t *testing.T) {
	hub := false
	run := &fakeRun{}
	run.resp = nmState(&hub)
	s := testServer(t, run)
	if _, err := s.setEthernet(context.Background(), EthernetRequest{Method: "manual", Address: "192.168.1.50/24", Gateway: "192.168.1.1"}); err != nil {
		t.Fatal(err)
	}
	c := waitDone(t, s)
	if c.State != "reverted" {
		t.Fatalf("state %s: %s", c.State, c.Message)
	}
	mods := run.called("nmcli connection modify uuid u-wired")
	if len(mods) != 2 || !strings.Contains(mods[0], "ipv4.addresses 192.168.1.50/24") || !strings.Contains(mods[1], "ipv4.method auto ipv4.addresses  ipv4.gateway ") {
		t.Fatalf("modify calls %q", mods)
	}
}

func TestChangeKeptWhenHubAnswers(t *testing.T) {
	hub := true
	run := &fakeRun{}
	run.resp = nmState(&hub)
	s := testServer(t, run)
	if _, err := s.setEthernet(context.Background(), EthernetRequest{Method: "auto", DNS: "1.1.1.1"}); err != nil {
		t.Fatal(err)
	}
	if c := waitDone(t, s); c.State != "kept" {
		t.Fatalf("state %s: %s", c.State, c.Message)
	}
	if m := run.called("nmcli connection modify uuid u-wired"); len(m) != 1 || !strings.HasSuffix(m[0], "ipv4.dns 1.1.1.1 ipv4.ignore-auto-dns yes") {
		t.Fatalf("modify calls %q, want one that replaces the router's DNS", m)
	}
	if len(run.called("nmcli device reapply end0")) != 1 {
		t.Fatal("the change was not put into effect in place")
	}
	// A second change waits for the first to finish; this one is finished.
	if _, err := s.disconnectWiFi(context.Background(), false); err == nil {
		t.Fatal("disconnect accepted with no Wi-Fi connected")
	}
}

func TestChangeFailedApplyIsReverted(t *testing.T) {
	hub := true
	run := &fakeRun{}
	base := nmState(&hub)
	run.resp = func(cmd string) (string, error) {
		if cmd == "nmcli device reapply end0" && len(run.called("nmcli device reapply")) == 1 {
			return "", errors.New("Connection activation failed")
		}
		if strings.HasPrefix(cmd, "nmcli --wait 45 connection up uuid u-wired") && len(run.called("nmcli --wait 45 connection up")) == 1 {
			return "", errors.New("Connection activation failed")
		}
		return base(cmd)
	}
	s := testServer(t, run)
	s.setEthernet(context.Background(), EthernetRequest{Method: "manual", Address: "192.168.1.50/24"})
	c := waitDone(t, s)
	if c.State != "failed" || !strings.Contains(c.Message, "Connection activation failed") || !strings.Contains(c.Message, "previous setting is back") {
		t.Fatalf("state %s: %s", c.State, c.Message)
	}
}

func TestAccessPointBridge(t *testing.T) {
	hub := true
	run := &fakeRun{}
	run.resp = nmState(&hub)
	s := testServer(t, run)
	if _, err := s.setAP(context.Background(), APRequest{Enabled: true, SSID: "sat-wifi", Channel: 6, OnlyWired: true}); err == nil {
		t.Fatal("new access point without a password accepted")
	}
	if _, err := s.setAP(context.Background(), APRequest{Enabled: true, SSID: "sat-wifi", Password: "longenough", Channel: 6, OnlyWired: true}); err != nil {
		t.Fatal(err)
	}
	if c := waitDone(t, s); c.State != "kept" {
		t.Fatalf("state %s: %s", c.State, c.Message)
	}
	want := []string{
		"nmcli connection add type bridge ifname br0 con-name lorahub-br0 bridge.stp no connection.autoconnect yes ipv4.method auto",
		"nmcli connection add type ethernet ifname end0 con-name lorahub-br0-port master br0 slave-type bridge connection.autoconnect yes",
		"nmcli connection add type wifi ifname wlan0 con-name lorahub-ap master br0 slave-type bridge 802-11-wireless.ssid sat-wifi 802-11-wireless.mode ap 802-11-wireless.band bg 802-11-wireless.channel 6 connection.autoconnect no wifi-sec.key-mgmt wpa-psk",
		"nmcli connection modify uuid u-wired connection.autoconnect no",
		"nmcli --wait 45 connection up lorahub-br0-port",
		"nmcli --wait 30 connection up lorahub-ap",
	}
	for _, w := range want {
		if len(run.called(w)) == 0 {
			t.Errorf("missing %q\ncalls:\n%s", w, strings.Join(run.calls, "\n"))
		}
	}
	// The Wi-Fi client is refused while the access point holds the Wi-Fi.
	run.resp = func(cmd string) (string, error) {
		if strings.HasPrefix(cmd, "nmcli -t -f UUID,TYPE,DEVICE,AUTOCONNECT,NAME connection show") {
			return "u-br:bridge:br0:yes:lorahub-br0\nu-port:802-3-ethernet:end0:yes:lorahub-br0-port\nu-ap:802-11-wireless:wlan0:no:lorahub-ap\nu-wired:802-3-ethernet::no:Conexiune prin fir 1\n", nil
		}
		return nmState(&hub)(cmd)
	}
	if _, err := s.connectWiFi(context.Background(), WiFiRequest{SSID: "Link2", Password: "whatever1"}); err == nil {
		t.Fatal("Wi-Fi client accepted while the access point is on and the cable is in")
	}
	if c, ok := s.ipConn(context.Background()); !ok || c.name != bridgeConn {
		t.Fatalf("IP settings should belong to the bridge, got %+v", c)
	}
}

func TestTunnelReverted(t *testing.T) {
	hub := false
	run := &fakeRun{}
	run.resp = func(cmd string) (string, error) {
		if strings.HasPrefix(cmd, "wg show wg0 dump") {
			return "PRIVATE\tSATKEY\t40000\toff\nHUBKEY\t(none)\t192.168.1.15:51820\t10.77.0.0/24\t0\t10\t20\t25\n", nil
		}
		return nmState(&hub)(cmd)
	}
	s := testServer(t, run)
	if _, err := s.setTunnel(context.Background(), TunnelRequest{LAN: "192.168.1.99:51820", WAN: "home.example.net:51820"}); err != nil {
		t.Fatal(err)
	}
	if c := waitDone(t, s); c.State != "reverted" {
		t.Fatalf("state %s: %s", c.State, c.Message)
	}
	conf, _ := os.ReadFile(s.path(s.opt.WGConf))
	if !strings.Contains(string(conf), "Endpoint = 192.168.1.15:51820") {
		t.Fatalf("wg0.conf not restored:\n%s", conf)
	}
	sets := run.called("wg set wg0 peer HUBKEY endpoint")
	if len(sets) != 2 || !strings.HasSuffix(sets[0], "192.168.1.99:51820") || !strings.HasSuffix(sets[1], "192.168.1.15:51820") {
		t.Fatalf("wg set calls %q", sets)
	}
	if st := s.tunnel(context.Background()); st.PublicKey != "SATKEY" || len(st.Peers) != 1 || st.Peers[0].Name != "LoRaHub Bobcat" {
		t.Fatalf("tunnel %+v", st)
	}
}

func TestLoginAndGuards(t *testing.T) {
	hub := true
	run := &fakeRun{}
	run.resp = nmState(&hub)
	s := testServer(t, run)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	post := func(path, body, cookie string, csrf bool) *http.Response {
		req, _ := http.NewRequest("POST", srv.URL+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if csrf {
			req.Header.Set(csrfHeader, csrfValue)
		}
		if cookie != "" {
			req.AddCookie(&http.Cookie{Name: cookieName, Value: cookie})
		}
		r, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		return r
	}
	if r, _ := http.Get(srv.URL + "/api/status"); r.StatusCode != 401 {
		t.Fatalf("status without a session: %d", r.StatusCode)
	}
	if r := post("/api/login", `{"password":"right"}`, "", false); r.StatusCode != 403 {
		t.Fatalf("login without the page header: %d", r.StatusCode)
	}
	for i := 0; i < maxFails; i++ {
		if r := post("/api/login", `{"password":"wrong"}`, "", true); r.StatusCode != 401 {
			t.Fatalf("wrong password %d: %d", i, r.StatusCode)
		}
	}
	if r := post("/api/login", `{"password":"right"}`, "", true); r.StatusCode != 429 {
		t.Fatalf("after %d wrong passwords: %d, want 429", maxFails, r.StatusCode)
	}
	s.auth.mu.Lock()
	s.auth.fails = map[string]*failRec{}
	s.auth.mu.Unlock()
	r := post("/api/login", `{"password":"right"}`, "", true)
	if r.StatusCode != 200 {
		t.Fatalf("login: %d", r.StatusCode)
	}
	var tok string
	for _, c := range r.Cookies() {
		if c.Name == cookieName {
			tok = c.Value
			if !c.HttpOnly || c.SameSite != http.SameSiteStrictMode {
				t.Fatalf("cookie %+v", c)
			}
		}
	}
	req, _ := http.NewRequest("GET", srv.URL+"/api/status", nil)
	req.AddCookie(&http.Cookie{Name: cookieName, Value: tok})
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("status with a session: %v %v", err, resp.StatusCode)
	}
	var st map[string]any
	json.NewDecoder(resp.Body).Decode(&st)
	resp.Body.Close()
	if st["system"] == nil || st["tunnel"] == nil || st["engine_error"] == nil {
		t.Fatalf("status %v", st)
	}
	if b, _ := json.Marshal(st); strings.Contains(string(b), "secret") || strings.Contains(string(b), "PRIVATE") {
		t.Fatalf("status leaks a key: %s", b)
	}
	if r := post("/api/radio/restart", `{}`, tok, false); r.StatusCode != 403 {
		t.Fatalf("change without the page header: %d", r.StatusCode)
	}
	if r := post("/api/radio/restart", `{}`, tok, true); r.StatusCode != 200 || len(run.called("systemctl restart raw-lora")) != 1 {
		t.Fatalf("radio restart: %d", r.StatusCode)
	}
	// A restart of the page keeps the session; the file holds only its hash.
	again := New(Options{Root: s.root, Run: run, CheckPass: func(string) bool { return false }, Log: s.log})
	if !again.auth.valid(tok) {
		t.Fatal("session lost after a restart")
	}
	if b, _ := os.ReadFile(filepath.Join(s.root, "var", "lib", "lorahub-sat", "sessions.json")); len(b) == 0 || strings.Contains(string(b), tok) {
		t.Fatalf("session file: %q", b)
	}
	if r := post("/api/logout", `{}`, tok, true); r.StatusCode != 200 || s.auth.valid(tok) {
		t.Fatal("logout did not end the session")
	}
	if r, _ := http.Get(srv.URL + "/style.css"); r.StatusCode != 200 {
		t.Fatalf("style.css: %d", r.StatusCode)
	}
}
