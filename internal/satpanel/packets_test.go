package satpanel

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// uplink is a datagram as raw_lora_app 2.3 sends it.
func uplink(seq int, stat int, data string) []byte {
	return []byte(fmt.Sprintf(`{"v":2,"msg_type":"uplink","gateway_id":"0016C001F1225507","seq":%d,"boot":7,"ts_ms":%d,`+
		`"rxpk":[{"tmst":123,"time":"2026-11-22T18:00:00.000000Z","chan":2,"rfch":0,"freq":868.100000,"mid":0,"stat":%d,`+
		`"modu":"LORA","datr":"SF7BW125","codr":"4/5","rssis":-80.5,"lsnr":9.2,"foff":-120,"rssi":-79.0,"size":3,"data":"%s"}]}`,
		seq, time.Now().UnixMilli(), stat, data))
}

func TestPacketLogRepeatsAndRestart(t *testing.T) {
	file := filepath.Join(t.TempDir(), "packets.jsonl")
	l := newPacketLog(file, 100, 20*time.Millisecond, time.Now)
	l.datagram(uplink(5, 1, "yv6A")) // CA FE 80
	l.datagram(uplink(5, 1, "yv6A")) // the hub did not acknowledge: sent again
	l.datagram(uplink(5, 1, "yv6A"))
	l.datagram(uplink(6, -1, "AQID"))
	l.datagram([]byte(`{"v":2,"msg_type":"gateway_status","gateway_id":"X","seq":9,"rxpk":[]}`))
	l.datagram([]byte{0x02, 0x01, 0x02})
	v := l.since(0, 0)
	if len(v.Packets) != 2 {
		t.Fatalf("want 2 packets, got %d", len(v.Packets))
	}
	p := v.Packets[0]
	if p.DataHex != "CAFE80" || p.Repeats != 2 || p.CRC != "ok" || p.Freq != 868.1 || p.Datr != "SF7BW125" || p.RSSI != -79 || p.SNR != 9.2 {
		t.Fatalf("first packet: %+v", p)
	}
	if v.Packets[1].CRC != "bad" || v.Packets[1].DataHex != "010203" {
		t.Fatalf("second packet: %+v", v.Packets[1])
	}
	// Only what changed since the last look comes back.
	if n := len(l.since(v.Generation, v.Rev).Packets); n != 0 {
		t.Fatalf("nothing changed, got %d", n)
	}
	l.datagram(uplink(5, 1, "yv6A"))
	if w := l.since(v.Generation, v.Rev); len(w.Packets) != 1 || w.Packets[0].Repeats != 3 {
		t.Fatalf("the repeat should update packet 1: %+v", w.Packets)
	}

	time.Sleep(30 * time.Millisecond)
	l.flush(false)
	again := newPacketLog(file, 100, 20*time.Millisecond, time.Now)
	got := again.since(0, 0).Packets
	if len(got) != 2 || got[0].Repeats != 3 || got[0].ID != 1 || got[1].ID != 2 {
		t.Fatalf("after a restart: %+v", got)
	}
	again.datagram(uplink(1, 1, "AA=="))
	if all := again.since(0, 0).Packets; all[len(all)-1].ID != 3 {
		t.Fatalf("ids should go on from the file, got %d", all[len(all)-1].ID)
	}
}

func TestPacketLogKeepsTheLast(t *testing.T) {
	l := newPacketLog("", 3, time.Millisecond, time.Now)
	for i := 1; i <= 5; i++ {
		l.datagram(uplink(i, 1, "AA=="))
	}
	v := l.since(0, 0)
	if len(v.Packets) != 3 || v.Packets[0].ID != 3 || v.Total != 5 {
		t.Fatalf("want ids 3..5 of 5, got %d packets from %d, total %d", len(v.Packets), v.Packets[0].ID, v.Total)
	}
}

// The page listens for the engine's local copies, shows them, never answers,
// and clears and downloads the list.
func TestPacketsOverUDP(t *testing.T) {
	root := t.TempDir()
	s := New(Options{Root: root, PacketsListen: "127.0.0.1:0", PacketSettle: 10 * time.Millisecond,
		Run: &fakeRun{}, CheckPass: func(string) bool { return false }, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.packetsLoop(ctx)
	var addr string
	for i := 0; i < 100 && addr == ""; i++ {
		time.Sleep(10 * time.Millisecond)
		addr = s.packets.since(0, 0).Listening
	}
	if addr == "" {
		t.Fatal("not listening")
	}
	ua, _ := net.ResolveUDPAddr("udp", addr)
	engine, err := net.DialUDP("udp", nil, ua)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	engine.Write(uplink(41, 1, "yv6WuEDrAQI="))

	api := s.API()
	get := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		api.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		return w
	}
	var v packetsView
	for i := 0; i < 100; i++ {
		json.Unmarshal(get("/packets").Body.Bytes(), &v)
		if len(v.Packets) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(v.Packets) != 1 || v.Packets[0].DataHex != "CAFE96B840EB0102" {
		t.Fatalf("packets: %+v", v.Packets)
	}
	engine.SetReadDeadline(time.Now().Add(150 * time.Millisecond))
	if n, err := engine.Read(make([]byte, 100)); err == nil {
		t.Fatalf("the page answered the engine with %d bytes; it must stay silent", n)
	}
	if csv := get("/packets/download").Body.String(); !strings.Contains(csv, "received_at,frequency_mhz") || !strings.Contains(csv, "CAFE96B840EB0102") {
		t.Fatalf("csv: %q", csv)
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/packets/clear", strings.NewReader("{}"))
	r.Header.Set(csrfHeader, csrfValue)
	api.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("clear: %d %s", w.Code, w.Body)
	}
	var after packetsView
	json.Unmarshal(get(fmt.Sprintf("/packets?generation=%d&rev=%d", v.Generation, v.Rev)).Body.Bytes(), &after)
	if after.Generation == v.Generation || len(after.Packets) != 0 {
		t.Fatalf("after clearing: generation %d (was %d), %d packets", after.Generation, v.Generation, len(after.Packets))
	}
}

func TestNoPacketsOnTheHub(t *testing.T) {
	s := New(Options{Role: RoleHub, Root: t.TempDir(), Run: &fakeRun{}, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	w := httptest.NewRecorder()
	s.API().ServeHTTP(w, httptest.NewRequest("GET", "/packets", nil))
	if s.packets != nil || w.Code != http.StatusNotFound {
		t.Fatalf("the hub has its own live view; got %d", w.Code)
	}
}
