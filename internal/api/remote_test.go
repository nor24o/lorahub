package api

import (
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/nor24o/lorahub/internal/ingest"
)

// fakeEngine answers like raw_lora_app on a UDP port.
func fakeEngine(t *testing.T, seen chan<- map[string]any) int {
	t.Helper()
	c, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	go func() {
		buf := make([]byte, 4096)
		for {
			n, from, err := c.ReadFromUDP(buf)
			if err != nil {
				return
			}
			var m map[string]any
			json.Unmarshal(buf[:n], &m)
			seen <- m
			reply := `{"msg_type":"config_resp","rf0_freq":867500000,"port_down":1700}`
			if m["cmd"] == "set_config" {
				reply = `{"msg_type":"config_ack","status":"ok","message":"done"}`
			}
			c.WriteToUDP([]byte(`{"msg_type":"gateway_status"}`), from) // unrelated traffic must be skipped
			c.WriteToUDP([]byte(reply), from)
		}
	}()
	return c.LocalAddr().(*net.UDPAddr).Port
}

func plain(v any) func(string) []byte {
	b, _ := json.Marshal(v)
	return func(string) []byte { return b }
}

func TestControlExchange(t *testing.T) {
	seen := make(chan map[string]any, 4)
	port := fakeEngine(t, seen)
	m, err := controlExchange("127.0.0.1", port, plain(map[string]string{"cmd": "get_config"}), "config_resp", time.Second)
	if err != nil || m["port_down"] != float64(1700) {
		t.Fatalf("get_config: %v %v", m, err)
	}
	if got := <-seen; got["cmd"] != "get_config" {
		t.Fatalf("sent %v", got)
	}
	ack, err := controlExchange("127.0.0.1", port, plain(map[string]any{"cmd": "set_config", "config": map[string]any{"sync_word": "public"}}), "config_ack", time.Second)
	if err != nil || ack["status"] != "ok" {
		t.Fatalf("set_config: %v %v", ack, err)
	}
	got := <-seen
	if got["config"].(map[string]any)["sync_word"] != "public" {
		t.Fatalf("sent %v", got)
	}
}

func TestControlExchangeNoAnswer(t *testing.T) {
	c, _ := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	defer c.Close()
	if _, err := controlExchange("127.0.0.1", c.LocalAddr().(*net.UDPAddr).Port, plain(map[string]string{"cmd": "get_config"}), "config_resp", 100*time.Millisecond); err == nil {
		t.Fatal("expected a timeout")
	}
}

func TestValidateRemote(t *testing.T) {
	good := []map[string]any{{"rf0_freq": 867.5e6}, {"sync_word": "private"}, {"target_ip": "192.168.1.15", "port_up": 5555.0}, {"tx_default_power": 14.0}}
	for _, c := range good {
		if err := validateRemote(c, false); err != nil {
			t.Errorf("%v rejected: %v", c, err)
		}
	}
	bad := []map[string]any{{}, {"rf0_freq": 867.5}, {"sync_word": "x"}, {"target_ip": "gateway.local"}, {"port_up": 0.0}, {"tx_default_power": 40.0}, {"spi_path": "/dev/spidev0.0"}}
	for _, c := range bad {
		if err := validateRemote(c, false); err == nil {
			t.Errorf("%v accepted", c)
		}
	}
}

func TestValidateFullConfig(t *testing.T) {
	if err := validateRemote(map[string]any{"spi_path": "/dev/spidev0.0", "chan_if": []any{0.0}}, true); err != nil {
		t.Fatal(err)
	}
	if err := validateRemote(map[string]any{"link_key": "x"}, true); err == nil {
		t.Fatal("the link key must not be settable as a normal setting")
	}
	if err := validateRemote(map[string]any{"rf0_freq": 1.0}, true); err == nil {
		t.Fatal("known settings are still range-checked")
	}
}

func TestSignedExchangeRefreshesNonce(t *testing.T) {
	c, _ := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	defer c.Close()
	go func() {
		buf := make([]byte, 4096)
		n, from, _ := c.ReadFromUDP(buf)
		if !strings.HasPrefix(string(buf[:n]), "L2:"+ingest.StubNonce+":") {
			return
		}
		c.WriteToUDP([]byte(`{"msg_type":"auth_required","nonce":"abcd0123"}`), from)
		n, from, _ = c.ReadFromUDP(buf)
		if strings.HasPrefix(string(buf[:n]), "L2:abcd0123:") {
			c.WriteToUDP([]byte(`{"msg_type":"config_resp","ok":true}`), from)
		}
	}()
	wire := func(nonce string) []byte { return ingest.SignLink("key", nonce, []byte(`{"cmd":"get_config"}`)) }
	m, err := controlExchange("127.0.0.1", c.LocalAddr().(*net.UDPAddr).Port, wire, "config_resp", time.Second)
	if err != nil || m["ok"] != true {
		t.Fatalf("%v %v", m, err)
	}
}

func TestWrongKeyIsReported(t *testing.T) {
	c, _ := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	defer c.Close()
	go func() {
		buf := make([]byte, 4096)
		for {
			_, from, err := c.ReadFromUDP(buf)
			if err != nil {
				return
			}
			c.WriteToUDP([]byte(`{"msg_type":"auth_required","nonce":"abcd0123"}`), from)
		}
	}()
	wire := func(nonce string) []byte { return ingest.SignLink("key", nonce, []byte(`{}`)) }
	_, err := controlExchange("127.0.0.1", c.LocalAddr().(*net.UDPAddr).Port, wire, "config_resp", time.Second)
	if err == nil || !strings.Contains(err.Error(), "link key") {
		t.Fatalf("%v", err)
	}
}

func TestValidateTX(t *testing.T) {
	ok := txRequest{Modu: "LORA", Freq: 868.1, Power: 14}
	if err := validateTX(&ok); err != nil || ok.Count != 1 {
		t.Fatalf("%v count %d", err, ok.Count)
	}
	for _, r := range []txRequest{
		{Modu: "X", Freq: 868.1}, {Modu: "LORA", Freq: 868100000}, {Modu: "LORA", Freq: 868.1, Power: 40},
		{Modu: "LORA", Freq: 868.1, Count: 5, IntervalMs: 10}, {Modu: "LORA", Freq: 868.1, Count: 5000, IntervalMs: 1000},
		{Modu: "LORA", Freq: 868.1, RFChain: 2},
	} {
		r := r
		if err := validateTX(&r); err == nil {
			t.Errorf("%+v accepted", r)
		}
	}
}

func TestTXPayload(t *testing.T) {
	req := txRequest{PayloadHex: "AABB", AppendCounter: true}
	gen, err := txPayload(&req)
	if err != nil {
		t.Fatal(err)
	}
	b := gen(258)
	if len(b) != 4 || b[2] != 1 || b[3] != 2 {
		t.Fatalf("%x", b)
	}
	req = txRequest{RandomLen: 254, AppendCounter: true}
	if _, err := txPayload(&req); err == nil {
		t.Fatal("over-long payload accepted")
	}
}
