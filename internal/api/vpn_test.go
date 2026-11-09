package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestParseWGDump(t *testing.T) {
	now := time.Unix(1794257100, 0)
	dump := "SECRETPRIVATEKEY\tbobcatpub=\t51820\toff\n" +
		"lps8pub=\t(none)\t192.168.1.17:54708\t10.77.0.2/32\t1794257012\t122300\t33152\toff\n" +
		"oldpub=\t(none)\t(none)\t10.77.0.3/32,10.77.1.0/24\t0\t0\t0\t25\n" +
		"stalepub=\t(none)\t1.2.3.4:5\t10.77.0.4/32\t1794250000\t1\t2\toff\n"
	st := wgStatus{Peers: []wgPeer{}}
	parseWGDump(dump, now, &st)
	if st.PublicKey != "bobcatpub=" || st.ListenPort != 51820 || len(st.Peers) != 3 {
		t.Fatalf("%+v", st)
	}
	p := st.Peers[0]
	if p.Endpoint != "192.168.1.17:54708" || p.AllowedIPs[0] != "10.77.0.2/32" || !p.Connected || p.RX != 122300 || p.TX != 33152 || p.LastHandshake == nil {
		t.Fatalf("%+v", p)
	}
	if q := st.Peers[1]; q.Endpoint != "" || q.Connected || q.LastHandshake != nil || len(q.AllowedIPs) != 2 {
		t.Fatalf("%+v", q)
	}
	if st.Peers[2].Connected {
		t.Fatal("a handshake that old is not connected")
	}
	b, _ := json.Marshal(st)
	if strings.Contains(string(b), "SECRET") {
		t.Fatal("private key in the API answer")
	}
}

func TestParseWGPeerNames(t *testing.T) {
	conf := "# hub\n[Interface]\nAddress = 10.77.0.1/24\n\n[Peer]\n# Dragino LPS8 satellite\nPublicKey = abc+/=\nAllowedIPs = 10.77.0.2/32\n\n[Peer]\nPublicKey = def=\n"
	got := parseWGPeerNames(conf)
	if got["abc+/="] != "Dragino LPS8 satellite" || got["def="] != "" || len(got) != 2 {
		t.Fatalf("%v", got)
	}
}

func TestParseTailscaleStatus(t *testing.T) {
	st := tsStatus{IPs: []string{}, Approved: []string{}, Peers: []tsPeer{}, Health: []string{}}
	err := parseTailscaleStatus([]byte(`{"Version":"1.104.5","BackendState":"Running","AuthURL":"","TailscaleIPs":["100.64.0.1"],"Health":[],
		"Self":{"HostName":"bobcat-285","DNSName":"bobcat-285.tail.ts.net.","Online":true,"TailscaleIPs":["100.64.0.1"],"PrimaryRoutes":["10.77.0.0/24"]},
		"Peer":{"k":{"HostName":"phone","OS":"android","Online":false,"TailscaleIPs":["100.64.0.2"],"LastSeen":"2026-11-09T10:00:00Z"}}}`), &st)
	if err != nil || st.State != "Running" || st.Hostname != "bobcat-285" || st.DNSName != "bobcat-285.tail.ts.net" || st.Approved[0] != "10.77.0.0/24" || len(st.Peers) != 1 || st.Peers[0].LastSeen == nil || st.Peers[0].IP != "100.64.0.2" {
		t.Fatalf("%+v %v", st, err)
	}
	waiting := tsStatus{IPs: []string{}, Approved: []string{}, Peers: []tsPeer{}, Health: []string{}}
	err = parseTailscaleStatus([]byte(`{"BackendState":"NeedsLogin","AuthURL":"https://login.tailscale.com/a/x","TailscaleIPs":null,"Self":{"HostName":"","Online":false},"Peer":null,"Health":["warning"]}`), &waiting)
	if err != nil || waiting.State != "NeedsLogin" || waiting.AuthURL == "" || waiting.IPs == nil || len(waiting.Health) != 1 {
		t.Fatalf("%+v %v", waiting, err)
	}
}
