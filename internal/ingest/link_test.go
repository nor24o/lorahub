package ingest

import (
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nor24o/lorahub/internal/model"
)

type fakeSink struct {
	mu      sync.Mutex
	uplinks []model.RxPacket
	status  []string
	acks    []map[string]any
}

func (f *fakeSink) HandleUplink(p model.RxPacket) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.uplinks = append(f.uplinks, p)
}
func (f *fakeSink) HandleStatus(eui, kind, addr string, st map[string]any) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status = append(f.status, eui)
	return true
}
func (f *fakeSink) HandleTxAck(eui string, ack map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.acks = append(f.acks, ack)
}
func (f *fakeSink) counts() (int, int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.uplinks), len(f.status), len(f.acks)
}

const testKey = "0123456789abcdef0123456789abcdef"

type rig struct {
	srv    *BobcatServer
	sink   *fakeSink
	engine *net.UDPConn // both the uplink and the control socket of a fake engine
	hub    *net.UDPAddr
	port   int
}

func newRig(t *testing.T) *rig {
	t.Helper()
	sink := &fakeSink{}
	srv, err := NewBobcatServer("127.0.0.1:0", 5556, nil, nil, sink, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Close() })
	srv.LinkKey = func() string { return testKey }
	c, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	port := c.LocalAddr().(*net.UDPAddr).Port
	srv.ResolvePort = func(string) int { return port }
	return &rig{srv, sink, c, srv.conn.LocalAddr().(*net.UDPAddr), port}
}

func (r *rig) send(t *testing.T, v any) {
	t.Helper()
	b, _ := json.Marshal(v)
	if _, err := r.engine.WriteToUDP(b, r.hub); err != nil {
		t.Fatal(err)
	}
}

func (r *rig) read(t *testing.T) string {
	t.Helper()
	buf := make([]byte, 4096)
	r.engine.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, _, err := r.engine.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("nothing received: %v", err)
	}
	return string(buf[:n])
}

func (r *rig) status(t *testing.T, seq int, auth bool) {
	t.Helper()
	r.send(t, map[string]any{"v": 2, "msg_type": "gateway_status", "gateway_id": "AABBCCDDEEFF0011", "seq": seq, "boot": 7,
		"gateway_status": map[string]any{"eui": "AABBCCDDEEFF0011", "link": 2, "auth": auth, "nonce": "0a0b0c0d", "port_down": r.port}})
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	for end := time.Now().Add(2 * time.Second); time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatal("timed out")
}

var testRx = []map[string]any{{"tmst": 1, "freq": 868.1, "stat": 1, "modu": "LORA", "datr": "SF7BW125", "codr": "4/5", "rssi": -70, "lsnr": 8, "size": 2, "data": "AQI="}}

func TestAckAndDuplicateFiltering(t *testing.T) {
	r := newRig(t)
	up := map[string]any{"v": 2, "msg_type": "uplink", "gateway_id": "AABBCCDDEEFF0011", "seq": 41, "boot": 7, "rxpk": testRx}
	r.send(t, up)
	if m := r.read(t); !strings.Contains(m, `"msg_type":"ack"`) || !strings.Contains(m, `"seq":41`) {
		t.Fatalf("ack: %s", m)
	}
	r.send(t, up) // the ack was lost and the engine repeats
	if m := r.read(t); !strings.Contains(m, `"seq":41`) {
		t.Fatalf("a repeat must be acknowledged again: %s", m)
	}
	waitFor(t, func() bool { n, _, _ := r.sink.counts(); return n == 1 })
	time.Sleep(30 * time.Millisecond)
	if n, _, _ := r.sink.counts(); n != 1 {
		t.Fatalf("processed %d times", n)
	}
	up["boot"] = 8 // the engine restarted and reuses the number
	r.send(t, up)
	r.read(t)
	waitFor(t, func() bool { n, _, _ := r.sink.counts(); return n == 2 })
}

func TestOldEnginesAreNotAcknowledged(t *testing.T) {
	r := newRig(t)
	r.send(t, map[string]any{"v": 1, "msg_type": "uplink", "gateway_id": "AABBCCDDEEFF0011", "seq": 1, "rxpk": testRx})
	waitFor(t, func() bool { n, _, _ := r.sink.counts(); return n == 1 })
	buf := make([]byte, 100)
	r.engine.SetReadDeadline(time.Now().Add(80 * time.Millisecond))
	if n, _, err := r.engine.ReadFromUDP(buf); err == nil {
		t.Fatalf("unexpected reply: %s", buf[:n])
	}
}

func TestAutomaticPairing(t *testing.T) {
	r := newRig(t)
	r.status(t, 1, false)
	if m := r.read(t); !strings.Contains(m, `"msg_type":"ack"`) {
		t.Fatalf("expected ack first: %s", m)
	}
	m := r.read(t)
	if !strings.Contains(m, `"cmd":"set_link_key"`) || !strings.Contains(m, testKey) {
		t.Fatalf("pairing: %s", m)
	}
	r.status(t, 2, true)
	r.read(t)
	buf := make([]byte, 200)
	r.engine.SetReadDeadline(time.Now().Add(80 * time.Millisecond))
	if n, _, err := r.engine.ReadFromUDP(buf); err == nil {
		t.Fatalf("a paired gateway must not be paired again: %s", buf[:n])
	}
}

func txPacket() model.TxPacket {
	return model.TxPacket{Payload: []byte{1}, Frequency: 868100000, DataRate: "SF7BW125", Power: 14, Immediate: true}
}

func TestDownlinkIsSignedConfirmedAndRetried(t *testing.T) {
	r := newRig(t)
	r.status(t, 1, true)
	r.read(t)
	waitFor(t, func() bool { _, n, _ := r.sink.counts(); return n == 1 })
	if err := r.srv.Send("AABBCCDDEEFF0011", txPacket()); err != nil {
		t.Fatal(err)
	}
	first := r.read(t)
	if !strings.HasPrefix(first, "L2:0a0b0c0d:") {
		t.Fatalf("not signed: %s", first)
	}
	body := first[strings.Index(first, ":{")+1:]
	if string(SignLink(testKey, "0a0b0c0d", []byte(body))) != first {
		t.Fatalf("bad signature: %s", first)
	}
	if second := r.read(t); second != first {
		t.Fatalf("expected a retry: %s", second)
	}
	var m struct {
		RequestID string `json:"request_id"`
	}
	json.Unmarshal([]byte(body), &m)
	r.send(t, map[string]any{"v": 2, "msg_type": "tx_ack", "gateway_id": "AABBCCDDEEFF0011", "request_id": m.RequestID, "status": "queued"})
	waitFor(t, func() bool { r.srv.mu.Lock(); defer r.srv.mu.Unlock(); return len(r.srv.waiting) == 0 })
	if _, _, n := r.sink.counts(); n != 0 {
		t.Fatal("queued is not a failure")
	}
	r.send(t, map[string]any{"v": 2, "msg_type": "tx_ack", "gateway_id": "AABBCCDDEEFF0011", "request_id": m.RequestID, "status": "busy"})
	waitFor(t, func() bool { _, _, n := r.sink.counts(); return n == 1 })
}

func TestDownlinkWithoutConfirmationIsReported(t *testing.T) {
	r := newRig(t)
	r.status(t, 1, true)
	r.read(t)
	waitFor(t, func() bool { _, n, _ := r.sink.counts(); return n == 1 })
	r.srv.Send("AABBCCDDEEFF0011", txPacket())
	waitFor(t, func() bool { _, _, n := r.sink.counts(); return n == 1 })
}

func TestStaleNonceIsRefreshedAndRepeated(t *testing.T) {
	r := newRig(t)
	r.status(t, 1, true)
	r.read(t)
	waitFor(t, func() bool { _, n, _ := r.sink.counts(); return n == 1 })
	r.srv.Send("AABBCCDDEEFF0011", txPacket())
	r.read(t)
	r.send(t, map[string]any{"v": 2, "msg_type": "auth_required", "gateway_id": "AABBCCDDEEFF0011", "nonce": "11112222"})
	if m := r.read(t); !strings.HasPrefix(m, "L2:11112222:") {
		t.Fatalf("not repeated with the new nonce: %s", m)
	}
}

func TestPlainDownlinkForOldEngines(t *testing.T) {
	r := newRig(t)
	r.send(t, map[string]any{"msg_type": "gateway_status", "gateway_status": map[string]any{"eui": "AABBCCDDEEFF0011"}})
	waitFor(t, func() bool { _, n, _ := r.sink.counts(); return n == 1 })
	r.srv.Send("AABBCCDDEEFF0011", txPacket())
	m := r.read(t)
	if strings.HasPrefix(m, "L2:") || strings.Contains(m, "request_id") {
		t.Fatalf("old engines get plain datagrams: %s", m)
	}
}
