package main

// End-to-end test: the whole hub as it runs on the Bobcat, two simulated
// raw_lora_app engines speaking link protocol 2 over UDP, an MQTT client in
// the role of Home Assistant and the console's HTTP API. Each step checks the
// exact bytes that reach the radios and what MQTT and Home Assistant receive.

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"sync"
	"testing"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"

	"github.com/nor24o/lorahub/internal/config"
	"github.com/nor24o/lorahub/internal/ingest"
)

const (
	gw1EUI    = "0016C001F1500B48" // the Bobcat's own radio
	gw2EUI    = "0016C001F1225507" // the LPS8 satellite
	gw1ID     = "eui-0016c001f1500b48"
	gw2ID     = "eui-0016c001f1225507"
	fakeNonce = "abcd0001"
	wait      = 3 * time.Second
)

// ---------- simulated radio engine ----------

type fakeGateway struct {
	t    *testing.T
	eui  string
	conn *net.UDPConn
	hub  *net.UDPAddr
	mu   sync.Mutex
	seq  uint32
	key  string
	tx   chan map[string]any
}

func newFakeGateway(t *testing.T, ip, eui string, hub *net.UDPAddr) *fakeGateway {
	c, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP(ip)})
	if err != nil {
		t.Fatalf("engine %s: %v", eui, err)
	}
	g := &fakeGateway{t: t, eui: eui, conn: c, hub: hub, tx: make(chan map[string]any, 64)}
	t.Cleanup(func() { c.Close() })
	go g.loop()
	return g
}

func (g *fakeGateway) send(m map[string]any) {
	g.mu.Lock()
	g.seq++
	m["v"], m["gateway_id"], m["seq"], m["boot"] = 2, g.eui, g.seq, 1
	g.mu.Unlock()
	b, _ := json.Marshal(m)
	g.conn.WriteToUDP(b, g.hub)
}

func (g *fakeGateway) paired() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.key != ""
}

func (g *fakeGateway) status() {
	g.send(map[string]any{"msg_type": "gateway_status", "gateway_status": map[string]any{"eui": g.eui, "link": 2, "auth": g.paired(), "nonce": fakeNonce,
		"port_down": g.conn.LocalAddr().(*net.UDPAddr).Port, "model": "test", "engine": "2.0", "rx": 0, "tx": 0}})
}

// uplink reports a received packet: hex payload, concentrator time in µs, radio settings and signal.
func (g *fakeGateway) uplink(payloadHex string, tmst uint32, freq float64, datr string, rssi, snr float64) {
	b, err := hex.DecodeString(payloadHex)
	if err != nil {
		g.t.Fatal(err)
	}
	g.send(map[string]any{"msg_type": "uplink", "rxpk": []map[string]any{{"tmst": tmst, "freq": freq, "chan": 0, "rfch": 0, "stat": 1, "modu": "LORA",
		"datr": datr, "codr": "4/5", "rssi": rssi, "lsnr": snr, "size": len(b), "data": base64.StdEncoding.EncodeToString(b)}}})
}

// uplinkFSK is an FSK packet as raw_lora_app reports it: datr is the bit rate.
func (g *fakeGateway) uplinkFSK(payloadHex string, tmst uint32, freq float64, bitRate int, rssi float64) {
	b, err := hex.DecodeString(payloadHex)
	if err != nil {
		g.t.Fatal(err)
	}
	g.send(map[string]any{"msg_type": "uplink", "rxpk": []map[string]any{{"tmst": tmst, "freq": freq, "chan": 9, "rfch": 1, "stat": 1, "modu": "FSK",
		"datr": bitRate, "rssi": rssi, "size": len(b), "data": base64.StdEncoding.EncodeToString(b)}}})
}

func (g *fakeGateway) loop() {
	buf := make([]byte, 65535)
	for {
		n, from, err := g.conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		text := string(buf[:n])
		if strings.HasPrefix(text, "L2:") {
			if len(text) < 78 {
				g.t.Errorf("%s: short signed command %q", g.eui, text)
				continue
			}
			nonce, body := text[3:11], text[77:]
			g.mu.Lock()
			key := g.key
			g.mu.Unlock()
			if string(ingest.SignLink(key, nonce, []byte(body))) != text {
				g.t.Errorf("%s: command with a wrong signature", g.eui)
			}
			text = body
		}
		var m map[string]any
		if json.Unmarshal([]byte(text), &m) != nil {
			continue
		}
		switch {
		case m["cmd"] == "set_link_key":
			g.mu.Lock()
			g.key, _ = m["key"].(string)
			g.mu.Unlock()
			g.status()
		case m["data"] != nil:
			g.tx <- m
			b, _ := json.Marshal(map[string]any{"v": 2, "msg_type": "tx_ack", "gateway_id": g.eui, "request_id": m["request_id"], "status": "queued"})
			g.conn.WriteToUDP(b, from)
		}
	}
}

func (g *fakeGateway) nextTX(t *testing.T) map[string]any {
	t.Helper()
	select {
	case m := <-g.tx:
		return m
	case <-time.After(wait):
		t.Fatalf("%s transmitted nothing", g.eui)
		return nil
	}
}

func (g *fakeGateway) noTX(t *testing.T, d time.Duration) {
	t.Helper()
	select {
	case m := <-g.tx:
		t.Fatalf("%s transmitted %s although it should not", g.eui, txHex(m))
	case <-time.After(d):
	}
}

func txHex(m map[string]any) string {
	b, _ := base64.StdEncoding.DecodeString(fmt.Sprint(m["data"]))
	return strings.ToUpper(hex.EncodeToString(b))
}

// ---------- MQTT client in the role of Home Assistant ----------

type mqttMsg struct {
	topic, payload string
	retained       bool
}

type recorder struct {
	mu   sync.Mutex
	msgs []mqttMsg
}

func (r *recorder) mark() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.msgs)
}

func (r *recorder) find(from int, topic string, match func(string) bool) (mqttMsg, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, m := range r.msgs[min(from, len(r.msgs)):] {
		if m.topic == topic && (match == nil || match(m.payload)) {
			return m, true
		}
	}
	return mqttMsg{}, false
}

func (r *recorder) wait(t *testing.T, from int, topic string, match func(string) bool) mqttMsg {
	t.Helper()
	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) {
		if m, ok := r.find(from, topic, match); ok {
			return m
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no MQTT message on %s", topic)
	return mqttMsg{}
}

func (r *recorder) none(t *testing.T, from int, topic string, d time.Duration) {
	t.Helper()
	r.noneMatching(t, from, topic, nil, d)
}

func (r *recorder) noneMatching(t *testing.T, from int, topic string, match func(string) bool, d time.Duration) {
	t.Helper()
	time.Sleep(d)
	if m, ok := r.find(from, topic, match); ok {
		t.Fatalf("unexpected MQTT message on %s: %s", topic, m.payload)
	}
}

func jsonOf(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatalf("not JSON: %s", s)
	}
	return m
}

// ---------- the test ----------

type e2e struct {
	h        *hub
	base     string
	web      *http.Client
	mq       paho.Client
	rec      *recorder
	gw1, gw2 *fakeGateway
}

func freePort(t *testing.T) int {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func (e *e2e) call(t *testing.T, method, path string, body any) (int, map[string]any) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, e.base+path, rd)
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.web.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var out map[string]any
	json.Unmarshal(b, &out)
	return resp.StatusCode, out
}

func (e *e2e) must(t *testing.T, method, path string, body any) map[string]any {
	t.Helper()
	code, out := e.call(t, method, path, body)
	if code >= 300 {
		t.Fatalf("%s %s: %d %v", method, path, code, out)
	}
	return out
}

// queued is the Overview's list of queued downlinks.
func (e *e2e) queued(t *testing.T) []map[string]any {
	t.Helper()
	resp, err := e.web.Get(e.base + "/api/downlinks/queued")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || resp.StatusCode != 200 {
		t.Fatalf("%d %v", resp.StatusCode, err)
	}
	return out
}

func (e *e2e) publish(topic, payload string) {
	e.mq.Publish(topic, 0, false, payload).Wait()
}

func start(t *testing.T) *e2e {
	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	cfg.HTTP.Listen = "127.0.0.1:0"
	cfg.SemtechUDP.Enabled = false
	cfg.Bobcat.Listen = "127.0.0.1:0"
	cfg.MQTTBroker.Listen = fmt.Sprintf("127.0.0.1:%d", freePort(t))
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	h, err := startHub(cfg, log, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.Close)
	s := h.proc.Settings()
	s.DedupWindowMs = 150
	s.HomeAssistant.Enabled = true
	if err := h.proc.SetSettings(s); err != nil {
		t.Fatal(err)
	}

	jar, _ := cookiejar.New(nil)
	e := &e2e{h: h, base: "http://" + h.httpAddr, web: &http.Client{Jar: jar, Timeout: 5 * time.Second}, rec: &recorder{}}
	e.must(t, "POST", "/api/auth/setup", map[string]string{"username": "e2e", "password": "e2e-local-only"})

	o := paho.NewClientOptions().AddBroker("tcp://" + cfg.MQTTBroker.Listen).SetClientID("e2e-home-assistant")
	e.mq = paho.NewClient(o)
	if tok := e.mq.Connect(); !tok.WaitTimeout(wait) || tok.Error() != nil {
		t.Fatalf("mqtt: %v", tok.Error())
	}
	t.Cleanup(func() { e.mq.Disconnect(100) })
	e.mq.Subscribe("#", 0, func(_ paho.Client, m paho.Message) {
		e.rec.mu.Lock()
		e.rec.msgs = append(e.rec.msgs, mqttMsg{m.Topic(), string(m.Payload()), m.Retained()})
		e.rec.mu.Unlock()
	}).Wait()

	hubAddr := h.bobcat.LocalAddr().(*net.UDPAddr)
	e.gw1 = newFakeGateway(t, "127.0.0.1", gw1EUI, hubAddr)
	e.gw2 = newFakeGateway(t, "127.0.0.2", gw2EUI, hubAddr)
	e.gw1.status()
	e.gw2.status()
	deadline := time.Now().Add(wait)
	for !(e.gw1.paired() && e.gw2.paired() && len(h.store.Gateways()) == 2) {
		if time.Now().After(deadline) {
			t.Fatalf("gateways did not register and pair: %d registered", len(h.store.Gateways()))
		}
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond) // the paired status reaches the hub
	return e
}

func TestEndToEnd(t *testing.T) {
	e := start(t)
	up := "lorahub/node-cafe/96b840eb/up"
	set := "lorahub/node-cafe/96b840eb/set"
	disc := "homeassistant/sensor/lorahub_node_cafe_96b840eb/temperature_1/config"

	t.Run("uplink is decoded and published", func(t *testing.T) {
		e.must(t, "POST", "/api/applications", map[string]any{"node_id": "CAFE", "auto_register_nodes": true, "downlink_header": true, "uplink_formatter": map[string]any{"type": "cayenne_lpp"}})
		m := e.rec.mark()
		e.gw1.uplink("CAFE96B840EB0167011002", 1_000_000, 868.1, "SF7BW125", -60, 8)
		msg := jsonOf(t, e.rec.wait(t, m, up, nil).payload)
		dec, _ := msg["decoded"].(map[string]any)
		if msg["node_id"] != "CAFE" || msg["serial"] != "96B840EB" || msg["data_hex"] != "0167011002" || dec["temperature_1"] != 27.2 || msg["gateway_count"] != 1.0 {
			t.Fatalf("%v", msg)
		}
		e.rec.wait(t, m, "lorahub/node-cafe/96b840eb/state", func(p string) bool { return jsonOf(t, p)["temperature_1"] == 27.2 })
	})

	t.Run("two gateways hearing one packet give one message", func(t *testing.T) {
		m := e.rec.mark()
		e.gw1.uplink("CAFE96B840EB0167011003", 2_000_000, 868.3, "SF7BW125", -80, 2)
		e.gw2.uplink("CAFE96B840EB0167011003", 7_000_000, 868.3, "SF7BW125", -50, 9)
		msg := jsonOf(t, e.rec.wait(t, m, up, nil).payload)
		gws, _ := msg["gateways"].([]any)
		if msg["gateway_count"] != 2.0 || len(gws) != 2 || gws[0].(map[string]any)["gateway_id"] != "eui-0016c001f1225507" {
			t.Fatalf("merged message, best signal first: %v", msg)
		}
		time.Sleep(300 * time.Millisecond)
		n := 0
		e.rec.mu.Lock()
		for _, x := range e.rec.msgs[m:] {
			if x.topic == up {
				n++
			}
		}
		e.rec.mu.Unlock()
		if n != 1 {
			t.Fatalf("%d messages for one packet", n)
		}
	})

	t.Run("unknown NODE_ID makes a new group but stays out of Home Assistant", func(t *testing.T) {
		m := e.rec.mark()
		e.gw1.uplink("BEEF112233440167011002", 3_000_000, 868.1, "SF7BW125", -60, 8)
		e.rec.wait(t, m, "lorahub/node-beef/11223344/up", nil)
		e.rec.none(t, m, "homeassistant/sensor/lorahub_node_beef_11223344/temperature_1/config", 300*time.Millisecond)
	})

	t.Run("exposing a node announces it with values already set", func(t *testing.T) {
		m := e.rec.mark()
		e.must(t, "PUT", "/api/applications/node-cafe/devices/96b840eb", map[string]any{"adopted": true, "ha_enabled": true, "class": "A"})
		cfg := jsonOf(t, e.rec.wait(t, m, disc, func(p string) bool { return p != "" }).payload)
		if cfg["state_topic"] != "lorahub/node-cafe/96b840eb/state" {
			t.Fatalf("%v", cfg)
		}
		e.rec.wait(t, m, "lorahub/node-cafe/96b840eb/command_box", func(p string) bool { return p == `{"value":""}` })
		e.rec.wait(t, m, "homeassistant/text/lorahub_node_cafe_96b840eb/downlink/config", nil)
	})

	t.Run("class A downlink waits for the node and goes out in its receive window", func(t *testing.T) {
		e.publish(set, "0101")
		e.gw1.noTX(t, 400*time.Millisecond)
		e.gw2.noTX(t, 10*time.Millisecond)
		if q := e.queued(t); len(q) != 1 || q[0]["device_id"] != "96b840eb" || q[0]["payload_hex"] != "CAFE96B840EB0101" || q[0]["position"] != 1.0 || q[0]["waits_for"] != "packet" {
			t.Fatalf("Overview list of queued downlinks: %v", q)
		}
		m := e.rec.mark()
		e.gw2.uplink("CAFE96B840EB0167011004", 9_000_000, 868.5, "SF7BW125", -50, 9)
		tx := e.gw2.nextTX(t)
		if txHex(tx) != "CAFE96B840EB0101" || tx["imme"] != false || tx["tmst"] != 10_000_000.0 || tx["freq"] != 868.5 || tx["ipol"] != false {
			t.Fatalf("%v", tx)
		}
		e.rec.wait(t, m, up, func(p string) bool { return jsonOf(t, p)["data_hex"] == "0167011004" })
		if q := e.queued(t); len(q) != 0 {
			t.Fatalf("sent but still listed: %v", q)
		}
	})

	t.Run("a downlink heard back by the other gateway is not an uplink", func(t *testing.T) {
		m := e.rec.mark()
		before, _ := e.h.store.Device("node-cafe", "96b840eb")
		e.gw1.uplink("CAFE96B840EB0101", 10_100_000, 868.5, "SF7BW125", -40, 10)
		e.rec.noneMatching(t, m, up, func(p string) bool { return jsonOf(t, p)["data_hex"] == "0101" }, 500*time.Millisecond)
		after, _ := e.h.store.Device("node-cafe", "96b840eb")
		if after.UplinkCount != before.UplinkCount || after.LastGateway != before.LastGateway {
			t.Fatal("the echo changed the node")
		}
	})

	t.Run("class C downlink goes out at once", func(t *testing.T) {
		e.must(t, "PUT", "/api/applications/node-cafe/devices/96b840eb", map[string]any{"class": "C"})
		e.publish(set, "OFF")
		tx := e.gw2.nextTX(t) // the gateway that heard the node best last time
		if txHex(tx) != "CAFE96B840EB0100" || tx["imme"] != true || tx["freq"] != 868.5 {
			t.Fatalf("%v", tx)
		}
	})

	t.Run("buttons and sliders from Home Assistant", func(t *testing.T) {
		m := e.rec.mark()
		e.must(t, "PUT", "/api/applications/node-cafe/devices/96b840eb", map[string]any{"ha_commands": []map[string]any{
			{"name": "Open", "kind": "button", "payload_hex": "0A0B"},
			{"name": "Dimmer", "kind": "number", "payload_hex": "02{value}", "min": 0, "max": 100}}})
		b := jsonOf(t, e.rec.wait(t, m, "homeassistant/button/lorahub_node_cafe_96b840eb/cmd_Open/config", nil).payload)
		if b["payload_press"] != "0A0B" || b["command_topic"] != set {
			t.Fatalf("%v", b)
		}
		e.rec.wait(t, m, "homeassistant/number/lorahub_node_cafe_96b840eb/cmd_Dimmer/config", nil)
		e.rec.wait(t, m, "lorahub/node-cafe/96b840eb/cmd/cmd_Dimmer/state", func(p string) bool { return p == `{"value":"0"}` })
		e.publish(set, "0A0B") // what Home Assistant sends when the button is pressed
		if got := txHex(e.gw2.nextTX(t)); got != "CAFE96B840EB0A0B" {
			t.Fatal(got)
		}
		e.publish("lorahub/node-cafe/96b840eb/cmd/cmd_Dimmer", "50")
		if got := txHex(e.gw2.nextTX(t)); got != "CAFE96B840EB0232" {
			t.Fatal(got)
		}
		e.rec.wait(t, m, "lorahub/node-cafe/96b840eb/cmd/cmd_Dimmer/state", func(p string) bool { return p == `{"value":"50"}` })
	})

	t.Run("a node's own radio settings and inverted IQ", func(t *testing.T) {
		e.must(t, "PUT", "/api/applications/node-cafe/devices/96b840eb", map[string]any{"raw_downlink": map[string]any{
			"mode": "immediate", "frequency": 869.525, "data_rate": "SF10BW125", "invert_polarity": true, "gateway": gw1ID}})
		e.publish(set, "01")
		tx := e.gw1.nextTX(t)
		if txHex(tx) != "CAFE96B840EB01" || tx["freq"] != 869.525 || tx["datr"] != "SF10BW125" || tx["ipol"] != true || tx["imme"] != true {
			t.Fatalf("%v", tx)
		}
		e.must(t, "PUT", "/api/applications/node-cafe/devices/96b840eb", map[string]any{"raw_downlink": nil})
	})

	t.Run("a test packet from the console changes nothing", func(t *testing.T) {
		m := e.rec.mark()
		e.must(t, "POST", "/api/simulate", map[string]any{"gateway_id": gw1ID, "payload_hex": "0167FF00", "application_id": "node-cafe", "device_id": "96b840eb"})
		msg := jsonOf(t, e.rec.wait(t, m, up, nil).payload)
		if msg["simulated"] != true {
			t.Fatalf("%v", msg)
		}
		e.rec.none(t, m, "lorahub/node-cafe/96b840eb/state", 300*time.Millisecond)
		d, _ := e.h.store.Device("node-cafe", "96b840eb")
		if dec, _ := d.LastDecoded.(map[string]any); dec["temperature_1"] == -25.6 {
			t.Fatal("the test packet became the node's value")
		}
	})

	t.Run("control byte numbers each downlink", func(t *testing.T) {
		e.must(t, "PUT", "/api/applications/node-cafe", map[string]any{"downlink_control": true})
		e.publish(set, "01")
		e.publish(set, "02")
		a, b := txHex(e.gw2.nextTX(t)), txHex(e.gw2.nextTX(t))
		if a != "CAFE96B840EB8001" || b != "CAFE96B840EB8102" {
			t.Fatal(a, b)
		}
	})

	t.Run("group broadcast reaches class C now and class A in its window", func(t *testing.T) {
		m := e.rec.mark()
		e.gw1.uplink("CAFE000000020167011002", 20_000_000, 868.1, "SF7BW125", -60, 8)
		e.rec.wait(t, m, "lorahub/node-cafe/00000002/up", nil)
		e.must(t, "PUT", "/api/applications/node-cafe/devices/00000002", map[string]any{"adopted": true, "class": "A"})
		res := e.must(t, "POST", "/api/applications/node-cafe/broadcast", map[string]any{"scope": "group", "data_hex": "FF",
			"radio": map[string]any{"frequency": 869.525, "data_rate": "SF9BW125"}, "class_a_minutes": 10})
		if res["class_a_queued"] != 1.0 || res["payload_hex"] != "CAFEFFFFFFFFA0FF" {
			t.Fatalf("%v", res)
		}
		for _, g := range []*fakeGateway{e.gw1, e.gw2} {
			if tx := g.nextTX(t); txHex(tx) != "CAFEFFFFFFFFA0FF" || tx["imme"] != true || tx["freq"] != 869.525 {
				t.Fatalf("%s: %v", g.eui, tx)
			}
		}
		e.gw1.uplink("CAFE000000020167011005", 30_000_000, 868.1, "SF7BW125", -60, 8)
		if tx := e.gw1.nextTX(t); txHex(tx) != "CAFEFFFFFFFFA0FF" || tx["imme"] != false || tx["tmst"] != 31_000_000.0 {
			t.Fatalf("class A copy: %v", tx)
		}
	})

	t.Run("profile byte from the firmware sets class and downlink radio", func(t *testing.T) {
		e.must(t, "PUT", "/api/applications/node-cafe", map[string]any{"node_profile": true})
		m := e.rec.mark()
		e.gw2.uplink("CAFE96B840EB"+"90"+"84ADD2"+"90"+"0167011006", 40_000_000, 868.1, "SF7BW125", -50, 9)
		msg := jsonOf(t, e.rec.wait(t, m, up, nil).payload)
		prof, _ := msg["profile"].(map[string]any)
		if msg["data_hex"] != "0167011006" || prof["class"] != "C" || prof["frequency"] != 869.525 {
			t.Fatalf("%v", msg)
		}
		e.publish(set, "03")
		tx := e.gw2.nextTX(t)
		if txHex(tx) != "CAFE96B840EB8203" || tx["freq"] != 869.525 || tx["datr"] != "SF9BW125" || tx["imme"] != true {
			t.Fatalf("%v", tx)
		}
	})

	relayCfg := "homeassistant/switch/lorahub_node_cafe_96b840eb/cmd_Relay/config"
	t.Run("a node describes its commands and gets them as controls", func(t *testing.T) {
		// Asked from the console: FF after the header and control byte, at once for class C.
		e.must(t, "POST", "/api/applications/node-cafe/devices/96b840eb/describe", nil)
		if got := txHex(e.gw2.nextTX(t)); got != "CAFE96B840EB83FF" {
			t.Fatal(got)
		}
		// The answer: profile byte 88 (class C, description), then the example of docs/node-protocol.md.
		desc := "D111" + "0111" + "05" + hex.EncodeToString([]byte("Relay")) + "0200" + "05" + hex.EncodeToString([]byte("Blink")) +
			"0323000A0E1000" + "08" + hex.EncodeToString([]byte("Interval")) + "01" + hex.EncodeToString([]byte("s"))
		m := e.rec.mark()
		e.gw2.uplink(strings.ToUpper("CAFE96B840EB88"+desc), 50_000_000, 868.1, "SF7BW125", -50, 9)
		sw := jsonOf(t, e.rec.wait(t, m, relayCfg, func(p string) bool { return p != "" }).payload)
		if sw["command_topic"] != "lorahub/node-cafe/96b840eb/cmd/cmd_Relay" || sw["payload_on"] != "ON" {
			t.Fatalf("relay switch %v", sw)
		}
		num := jsonOf(t, e.rec.wait(t, m, "homeassistant/number/lorahub_node_cafe_96b840eb/cmd_Interval/config", nil).payload)
		if num["min"] != 10.0 || num["max"] != 3600.0 || num["unit_of_measurement"] != "s" {
			t.Fatalf("interval number %v", num)
		}
		btn := jsonOf(t, e.rec.wait(t, m, "homeassistant/button/lorahub_node_cafe_96b840eb/cmd_Blink/config", nil).payload)
		// Its Relay switch sends 0101 / 0100, as the generic Output switch of a class C node would: Output goes.
		e.rec.wait(t, m, "homeassistant/switch/lorahub_node_cafe_96b840eb/switch/config", func(p string) bool { return p == "" })
		published := jsonOf(t, e.rec.wait(t, m, "lorahub/node-cafe/96b840eb/description", nil).payload)
		if cmds, _ := published["commands"].([]any); len(cmds) != 3 {
			t.Fatalf("description on MQTT: %v", published)
		}
		e.rec.noneMatching(t, m, up, func(p string) bool { return strings.Contains(jsonOf(t, p)["payload_hex"].(string), "D111") }, 300*time.Millisecond)
		d := e.must(t, "GET", "/api/applications/node-cafe/devices/96b840eb", nil)
		if dd, _ := d["described"].(map[string]any); dd == nil || len(dd["commands"].([]any)) != 3 {
			t.Fatalf("stored description: %v", d["described"])
		}

		// Each control sends the code the node announced, with its value.
		e.publish("lorahub/node-cafe/96b840eb/cmd/cmd_Relay", "ON")
		if got := txHex(e.gw2.nextTX(t)); got != "CAFE96B840EB840101" {
			t.Fatal("relay on:", got)
		}
		e.publish("lorahub/node-cafe/96b840eb/cmd/cmd_Interval", "300")
		if got := txHex(e.gw2.nextTX(t)); got != "CAFE96B840EB8503012C" {
			t.Fatal("interval 300:", got)
		}
		e.publish(set, btn["payload_press"].(string))
		if got := txHex(e.gw2.nextTX(t)); got != "CAFE96B840EB8602" {
			t.Fatal("blink:", got)
		}
		e.rec.wait(t, m, "lorahub/node-cafe/96b840eb/cmd/cmd_Relay/state", func(p string) bool { return p == `{"value":"ON"}` })

		// Editing the node in the console keeps what it announced.
		e.must(t, "PUT", "/api/applications/node-cafe/devices/96b840eb", map[string]any{"name": "Relay board"})
		if d := e.must(t, "GET", "/api/applications/node-cafe/devices/96b840eb", nil); d["described"] == nil {
			t.Fatal("an edit dropped the description")
		}
	})

	t.Run("deleting the group removes it from Home Assistant", func(t *testing.T) {
		m := e.rec.mark()
		e.must(t, "DELETE", "/api/applications/node-cafe", nil)
		for _, topic := range []string{disc, relayCfg, "homeassistant/button/lorahub_node_cafe_96b840eb/cmd_Open/config", "homeassistant/text/lorahub_node_cafe_96b840eb/downlink/config"} {
			e.rec.wait(t, m, topic, func(p string) bool { return p == "" })
		}
	})

	t.Run("FSK bridge passes FSK packets to and from UDP untouched", func(t *testing.T) {
		app, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)}) // the program the packets are for
		if err != nil {
			t.Fatal(err)
		}
		defer app.Close()
		e.must(t, "PUT", "/api/settings", map[string]any{"fsk_bridge": map[string]any{"enabled": true, "send_to": app.LocalAddr().String(),
			"listen": "127.0.0.1:0", "format": "json", "frequency": 868.3, "bit_rate": 50000, "deviation": 25, "power": 14, "preamble": 5}})
		var bridge *net.UDPAddr
		for deadline := time.Now().Add(wait); bridge == nil; {
			if st, _ := e.must(t, "GET", "/api/fsk", nil)["stats"].(map[string]any); st != nil && st["listening"] != "" && st["listening"] != nil {
				bridge, _ = net.ResolveUDPAddr("udp", st["listening"].(string))
			} else if time.Now().After(deadline) {
				t.Fatal("the bridge did not open its port")
			} else {
				time.Sleep(20 * time.Millisecond)
			}
		}
		next := func() []byte {
			app.SetReadDeadline(time.Now().Add(wait))
			buf := make([]byte, 2048)
			n, _, err := app.ReadFromUDP(buf)
			if err != nil {
				t.Fatal("nothing from the bridge:", err)
			}
			return buf[:n]
		}

		// Heard by both gateways: one datagram, with both receptions, and no node group for "A1B2".
		e.gw1.uplinkFSK("A1B2C3D4E5", 70_000_000, 868.3, 50000, -60)
		e.gw2.uplinkFSK("A1B2C3D4E5", 70_000_100, 868.3, 50000, -80)
		var up map[string]any
		json.Unmarshal(next(), &up)
		if gws, _ := up["gateways"].([]any); up["data_hex"] != "A1B2C3D4E5" || up["bit_rate"] != 50000.0 || len(gws) != 2 {
			t.Fatalf("FSK packet to UDP: %v", up)
		}
		app.SetReadDeadline(time.Now().Add(400 * time.Millisecond))
		if n, _, err := app.ReadFromUDP(make([]byte, 64)); err == nil {
			t.Fatalf("a second datagram for the same packet (%d bytes)", n)
		}
		if code, _ := e.call(t, "GET", "/api/applications/node-a1b2", nil); code != 404 {
			t.Fatal("an FSK packet made a node group")
		}

		// From UDP to the air: through the gateway that heard the last FSK packet best.
		app.WriteToUDP([]byte(`{"data_hex":"0102030405"}`), bridge)
		tx := e.gw1.nextTX(t)
		if txHex(tx) != "0102030405" || tx["modu"] != "FSK" || tx["datr"] != "50000" || tx["f_dev"] != 25.0 || tx["freq"] != 868.3 || tx["imme"] != true {
			t.Fatalf("FSK downlink %v", tx)
		}
		app.WriteToUDP([]byte(`{"data_hex":"AA","gateway":"`+gw2ID+`","frequency":868.8,"bit_rate":38400}`), bridge)
		if tx := e.gw2.nextTX(t); txHex(tx) != "AA" || tx["freq"] != 868.8 || tx["datr"] != "38400" {
			t.Fatalf("FSK downlink with its own settings %v", tx)
		}

		// Raw format: the bytes alone, both ways.
		e.must(t, "PUT", "/api/settings", map[string]any{"fsk_bridge": map[string]any{"format": "raw"}})
		e.gw1.uplinkFSK("0BADC0DE", 80_000_000, 868.3, 50000, -60)
		if got := hex.EncodeToString(next()); got != "0badc0de" {
			t.Fatal("raw FSK packet to UDP:", got)
		}
		app.WriteToUDP([]byte{0x10, 0x20}, bridge)
		if tx := e.gw1.nextTX(t); txHex(tx) != "1020" || tx["modu"] != "FSK" {
			t.Fatalf("raw FSK downlink %v", tx)
		}
		e.must(t, "PUT", "/api/settings", map[string]any{"fsk_bridge": map[string]any{"enabled": false}})
	})
}
