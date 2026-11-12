package satpanel

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func hubServer(t *testing.T, run *fakeRun) *Server {
	t.Helper()
	s := testServer(t, run)
	o := s.opt
	o.Role, o.SessionFile = RoleHub, ""
	o.Backup = func(_ context.Context, dst string) error { return os.WriteFile(dst, []byte("SQLite format 3"), 0o600) }
	return New(o)
}

func writeFile(t *testing.T, s *Server, p, content string) {
	t.Helper()
	full := s.path(p)
	os.MkdirAll(filepath.Dir(full), 0o755)
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// routerState answers like the hub: on Wi-Fi, router at 192.168.1.1.
func routerState(router *bool) func(string) (string, error) {
	hub := true
	base := nmState(&hub)
	return func(cmd string) (string, error) {
		switch cmd {
		case "ip -4 route show default":
			return "default via 192.168.1.1 dev wlan0 proto dhcp src 192.168.1.15 metric 600\n", nil
		case "ping -c 1 -W 2 192.168.1.1":
			if *router {
				return "64 bytes from 192.168.1.1: icmp_seq=1 ttl=64 time=2.10 ms\n", nil
			}
			return "", errors.New("no answer")
		case "ping -c 1 -W 2 10.77.0.1":
			return "", errors.New("the hub does not ping itself")
		}
		return base(cmd)
	}
}

func TestHubChecksTheRouter(t *testing.T) {
	router := false
	run := &fakeRun{}
	run.resp = routerState(&router)
	s := hubServer(t, run)
	if _, err := s.setWiFiRadio(context.Background(), RadioRequest{On: false}); err != nil {
		t.Fatal(err)
	}
	c := waitDone(t, s)
	if c.State != "reverted" || !strings.Contains(c.Message, "The router at 192.168.1.1") {
		t.Fatalf("state %s: %s", c.State, c.Message)
	}
	if got := run.called("nmcli radio wifi"); len(got) != 2 || got[0] != "nmcli radio wifi off" || got[1] != "nmcli radio wifi on" {
		t.Fatalf("radio calls %q", got)
	}
	router = true
	s.setWiFiRadio(context.Background(), RadioRequest{On: true})
	if c := waitDone(t, s); c.State != "kept" || c.Message != "Applied. The router answers." {
		t.Fatalf("state %s: %s", c.State, c.Message)
	}
	if _, err := s.setEthernet(context.Background(), EthernetRequest{Iface: "wifi", Method: "auto"}); err == nil {
		t.Fatal("Wi-Fi address change accepted with no Wi-Fi network connected")
	}
	// The hub has no "where the hub is" setting.
	srv := httptest.NewServer(s.API())
	defer srv.Close()
	req, _ := http.NewRequest("POST", srv.URL+"/tunnel", strings.NewReader(`{"lan":"192.168.1.15:51820"}`))
	req.Header.Set(csrfHeader, "lorahub")
	r, err := http.DefaultClient.Do(req)
	if err != nil || r.StatusCode == 202 {
		t.Fatalf("hub accepted a tunnel change: %v %v", err, r.StatusCode)
	}
}

func TestHotspotMode(t *testing.T) {
	hub := true
	run := &fakeRun{}
	run.resp = nmState(&hub)
	s := testServer(t, run)
	if _, err := s.setAP(context.Background(), APRequest{Enabled: true, Mode: "hotspot", SSID: "field", Password: "longenough", OnlyWired: false}); err == nil {
		t.Fatal("hotspot accepted without dnsmasq")
	}
	writeFile(t, s, "/usr/sbin/dnsmasq", "")
	if _, err := s.setAP(context.Background(), APRequest{Enabled: true, Mode: "hotspot", SSID: "field", Password: "longenough", OnlyWired: false}); err != nil {
		t.Fatal(err)
	}
	if c := waitDone(t, s); c.State != "kept" {
		t.Fatalf("state %s: %s", c.State, c.Message)
	}
	add := run.called("nmcli connection add type wifi ifname wlan0 con-name lorahub-ap ipv4.method shared ipv4.addresses 10.42.0.1/24")
	if len(add) != 1 || !strings.Contains(add[0], "802-11-wireless.mode ap") || !strings.Contains(add[0], "connection.autoconnect yes") {
		t.Fatalf("hotspot profile %q", add)
	}
	if n := len(run.called("nmcli connection add type bridge")); n != 0 {
		t.Fatal("hotspot built a bridge")
	}
	if n := len(run.called("nmcli connection modify uuid u-wired")); n != 0 {
		t.Fatal("hotspot touched the wired profile")
	}
}

func TestGeneral(t *testing.T) {
	run := &fakeRun{}
	run.resp = func(cmd string) (string, error) {
		switch cmd {
		case "timedatectl show":
			return "Timezone=Europe/Bucharest\nNTP=yes\nNTPSynchronized=yes\n", nil
		case "timedatectl list-timezones":
			return "Europe/Bucharest\nEurope/London\nUTC\n", nil
		}
		return "", nil
	}
	s := testServer(t, run)
	writeFile(t, s, "/etc/hosts", "127.0.0.1\tlocalhost\n127.0.1.1\tbobcat-sat3\n")
	g := s.general(context.Background())
	if g.Timezone != "Europe/Bucharest" || !g.NTP || !g.Synced || g.Hostname != "bobcat-sat3" {
		t.Fatalf("general %+v", g)
	}
	for _, bad := range []GeneralRequest{{Hostname: "-bad"}, {Hostname: "has space"}, {Timezone: "Mars/Olympus"}} {
		if err := s.setGeneral(context.Background(), bad); err == nil {
			t.Errorf("%+v accepted", bad)
		}
	}
	off := false
	if err := s.setGeneral(context.Background(), GeneralRequest{Hostname: "gate-north", Timezone: "UTC", NTP: &off}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"hostnamectl set-hostname gate-north", "timedatectl set-timezone UTC", "timedatectl set-ntp no"} {
		if len(run.called(want)) != 1 {
			t.Errorf("missing %q", want)
		}
	}
	if b, _ := os.ReadFile(s.path("/etc/hosts")); !strings.Contains(string(b), "127.0.1.1\tgate-north") {
		t.Fatalf("hosts: %s", b)
	}
}

func TestLightsSchedule(t *testing.T) {
	cfg := LightsConfig{Mode: "quiet", Night: NightLights{Enabled: true, Mode: "off", From: "22:00", To: "07:00"}}
	at := func(hm string) time.Time { tm, _ := time.Parse("15:04", hm); return tm }
	for hm, want := range map[string]string{"21:59": "quiet", "22:00": "off", "03:00": "off", "06:59": "off", "07:00": "quiet", "12:00": "quiet"} {
		if got := effectiveMode(cfg, at(hm)); got != want {
			t.Errorf("%s: %s, want %s", hm, got, want)
		}
	}
	day := LightsConfig{Mode: "normal", Night: NightLights{Enabled: true, Mode: "errors", From: "08:00", To: "18:00"}}
	if effectiveMode(day, at("12:00")) != "errors" || effectiveMode(day, at("19:00")) != "normal" {
		t.Fatal("daytime window")
	}

	s := testServer(t, &fakeRun{})
	clock := time.Date(2026, 11, 11, 23, 30, 0, 0, time.Local)
	s.now = func() time.Time { return clock }
	if err := s.setLights(LightsConfig{Mode: "dim"}); err == nil {
		t.Fatal("unknown mode accepted")
	}
	if err := s.setLights(LightsConfig{Mode: "normal", Night: NightLights{Enabled: true, Mode: "off", From: "22", To: "07:00"}}); err == nil {
		t.Fatal("bad time accepted")
	}
	if err := s.setLights(cfg); err != nil {
		t.Fatal(err)
	}
	led := func() string { b, _ := os.ReadFile(s.path("/etc/lora/led_mode")); return strings.TrimSpace(string(b)) }
	if got := led(); got != "off idle=green rx=red tx=blue" {
		t.Fatalf("led_mode at 23:30 = %q", got)
	}
	clock = time.Date(2026, 11, 12, 9, 0, 0, 0, time.Local)
	s.applyLights()
	if got := led(); got != "quiet idle=green rx=red tx=blue" {
		t.Fatalf("led_mode at 09:00 = %q", got)
	}

	// Colours and the steady mode.
	if err := s.setLights(LightsConfig{Mode: "steady", Colors: LightColors{Idle: "cyan", RX: "pink", TX: "blue"}}); err == nil {
		t.Fatal("unknown colour accepted")
	}
	if err := s.setLights(LightsConfig{Mode: "steady", Colors: LightColors{Idle: "cyan", RX: "magenta", TX: "white"}}); err != nil {
		t.Fatal(err)
	}
	if got := led(); got != "steady idle=cyan rx=magenta tx=white" {
		t.Fatalf("led_mode = %q", got)
	}
	// Saving without colours keeps the ones saved before.
	if err := s.setLights(LightsConfig{Mode: "solid"}); err != nil || led() != "solid idle=cyan rx=magenta tx=white" {
		t.Fatalf("led_mode = %q, %v", led(), err)
	}

	// A preview shows one colour, holds off the schedule, then the settings come back.
	until, err := s.previewLights(PreviewRequest{Color: "yellow", Seconds: 1})
	if err != nil || led() != "solid idle=yellow rx=yellow tx=yellow" {
		t.Fatalf("preview: %q %v", led(), err)
	}
	s.applyLights()
	if led() != "solid idle=yellow rx=yellow tx=yellow" {
		t.Fatal("the schedule overwrote a preview")
	}
	clock = until.Add(time.Millisecond)
	s.endPreview()
	if got := led(); got != "solid idle=cyan rx=magenta tx=white" {
		t.Fatalf("after the preview: %q", got)
	}
	if _, err := s.previewLights(PreviewRequest{Color: "purple"}); err == nil {
		t.Fatal("unknown preview colour accepted")
	}
}

func TestSDCard(t *testing.T) {
	run := &fakeRun{}
	run.resp = func(cmd string) (string, error) {
		if cmd == "lsblk -J -b -o PATH,SIZE,FSTYPE,LABEL,MOUNTPOINT /dev/mmcblk1" {
			return `{"blockdevices":[{"path":"/dev/mmcblk1","size":"31914983424","fstype":null,"label":null,"mountpoint":null,
			 "children":[{"path":"/dev/mmcblk1p1","size":31910789120,"fstype":"vfat","label":"CARD","mountpoint":null}]}]}`, nil
		}
		return "", nil
	}
	s := hubServer(t, run)
	if c := s.sdCard(context.Background()); c.Present {
		t.Fatal("card found with no SD device")
	}
	writeFile(t, s, "/sys/block/mmcblk0/device/type", "MMC\n")
	writeFile(t, s, "/sys/block/mmcblk1/device/type", "SD\n")
	c := s.sdCard(context.Background())
	if !c.Present || c.Device != "/dev/mmcblk1" || c.Size != 31914983424 || len(c.Parts) != 1 || c.Parts[0].FSType != "vfat" || c.Mounted {
		t.Fatalf("card %+v", c)
	}
	if err := s.mountSD(context.Background(), SDMountRequest{Path: "/dev/mmcblk0p3"}); err == nil {
		t.Fatal("mounted a partition of the eMMC")
	}
	if err := s.mountSD(context.Background(), SDMountRequest{Path: "/dev/mmcblk1p1", ReadOnly: true}); err != nil {
		t.Fatal(err)
	}
	if got := run.called("mount -o nosuid,nodev,noexec,ro /dev/mmcblk1p1 /media/sdcard"); len(got) != 1 {
		t.Fatalf("mount calls %q", run.calls)
	}
	// Pretend the kernel mounted it.
	writeFile(t, s, "/proc/mounts", "/dev/mmcblk0p3 / ext4 rw 0 0\n/dev/mmcblk1p1 /media/sdcard vfat ro,nosuid 0 0\n")
	writeFile(t, s, "/media/sdcard/logs/a.txt", "hello")
	writeFile(t, s, "/media/sdcard/b.bin", "12345")
	writeFile(t, s, "/secret.txt", "not on the card")
	list, err := s.listSD("/")
	if err != nil || len(list) != 2 || !list[0].Dir || list[0].Name != "logs" || list[1].Name != "b.bin" || list[1].Size != 5 {
		t.Fatalf("list %+v %v", list, err)
	}
	for _, bad := range []string{"../secret.txt", "/../../secret.txt", "logs/../../secret.txt"} {
		if p, err := s.sdPath(bad); err == nil && !strings.HasPrefix(p, s.path("/media/sdcard")) {
			t.Errorf("%s resolved off the card to %s", bad, p)
		}
	}
	if _, _, err := s.backupToSD(context.Background()); err == nil {
		t.Fatal("backup written to a read-only card")
	}
	writeFile(t, s, "/proc/mounts", "/dev/mmcblk1p1 /media/sdcard vfat rw,nosuid 0 0\n")
	name, size, err := s.backupToSD(context.Background())
	if err != nil || !strings.HasPrefix(name, "lorahub-backup-") || size == 0 {
		t.Fatalf("backup %s %d %v", name, size, err)
	}

	srv := httptest.NewServer(s.API())
	defer srv.Close()
	r, err := http.Get(srv.URL + "/sd/download?path=logs/a.txt")
	if err != nil || r.StatusCode != 200 || !strings.Contains(r.Header.Get("Content-Disposition"), `filename="a.txt"`) {
		t.Fatalf("download: %v %v", err, r)
	}
	b, _ := io.ReadAll(r.Body)
	r.Body.Close()
	if string(b) != "hello" {
		t.Fatalf("downloaded %q", b)
	}
	if r, _ := http.Get(srv.URL + "/sd/download?path=../secret.txt"); r.StatusCode == 200 {
		b, _ := io.ReadAll(r.Body)
		if string(b) == "not on the card" {
			t.Fatal("download left the card")
		}
	}
}

func TestAPIRefusesChangesWithoutHeader(t *testing.T) {
	s := hubServer(t, &fakeRun{})
	srv := httptest.NewServer(s.API())
	defer srv.Close()
	r, err := http.Post(srv.URL+"/reboot", "application/json", strings.NewReader(`{}`))
	if err != nil || r.StatusCode != http.StatusForbidden {
		t.Fatalf("reboot without the header: %v %v", err, r.StatusCode)
	}
	_ = slog.Default
}
