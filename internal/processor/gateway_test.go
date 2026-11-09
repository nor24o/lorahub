package processor

import (
	"strings"
	"testing"
	"time"

	"github.com/nor24o/lorahub/internal/model"
)

func TestTxPowerFor(t *testing.T) {
	cases := []struct {
		g    model.Gateway
		in   int
		want int
	}{
		{model.Gateway{}, 14, 14},
		{model.Gateway{TxPower: 10}, 14, 10},
		{model.Gateway{AntennaGain: 2.1}, 14, 11},
		{model.Gateway{TxPower: 10, AntennaGain: 3}, 14, 7},
		{model.Gateway{AntennaGain: 20}, 14, 0},
	}
	for _, c := range cases {
		if got := TxPowerFor(&c.g, c.in); got != c.want {
			t.Errorf("%+v: got %d, want %d", c.g, got, c.want)
		}
	}
}

func TestReceptionFilter(t *testing.T) {
	snr := -5.0
	g := &model.Gateway{RSSIOffset: -3, MinRSSI: -100, MinSNR: &snr}
	m := &model.RxMetadata{RSSI: -90, SNR: 1}
	if rejectedByFilter(g, m) || m.RSSI != -93 {
		t.Fatalf("offset not applied or packet dropped: %+v", m)
	}
	if !rejectedByFilter(g, &model.RxMetadata{RSSI: -98, SNR: 1}) {
		t.Fatal("packet below minimum RSSI after offset must be dropped")
	}
	if !rejectedByFilter(g, &model.RxMetadata{RSSI: -50, SNR: -6}) {
		t.Fatal("packet below minimum SNR must be dropped")
	}
	if rejectedByFilter(&model.Gateway{}, &model.RxMetadata{RSSI: -140, SNR: -30}) {
		t.Fatal("no limits set: nothing may be dropped")
	}
}

func TestDutyCycleLimit(t *testing.T) {
	p := &Processor{}
	g := &model.Gateway{ID: "gw", DutyCyclePct: 0.1} // 3600 ms per hour
	tx := model.TxPacket{Payload: make([]byte, 50), DataRate: "SF12BW125", CodingRate: "4/5"}
	var err error
	sent := 0
	for i := 0; i < 20 && err == nil; i++ {
		if err = p.reserveAirtime(g, tx); err == nil {
			sent++
		}
	}
	if err == nil || !strings.Contains(err.Error(), "duty-cycle") {
		t.Fatalf("limit never reached: %v", err)
	}
	if sent < 1 || sent > 3 {
		t.Fatalf("sent %d packets", sent)
	}
	if err := p.reserveAirtime(&model.Gateway{ID: "free"}, tx); err != nil {
		t.Fatalf("no limit configured: %v", err)
	}
}

func TestReverseEUI(t *testing.T) {
	if got := reverseEUI("0016C001F1225507"); got != "075522F101C01600" {
		t.Fatal(got)
	}
	if reverseEUI("ABC") != "" {
		t.Fatal("short EUI")
	}
}

func TestLinkStatusCorrectsGateway(t *testing.T) {
	p, st, _, _ := setup(t)
	now := time.Now().UTC()
	if err := st.SaveGateway(&model.Gateway{ID: "eui-075522f101c01600", EUI: "075522F101C01600", Name: "Bobcat C01600", Kind: "bobcat", Downlink: true, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if !p.HandleStatus("0016C001F1225507", "bobcat", "192.168.1.17", map[string]any{"port_down": 1700.0, "status_port": 5557.0, "model": "dragino-lps8"}) {
		t.Fatal("status refused")
	}
	g, err := st.Gateway("eui-075522f101c01600")
	if err != nil {
		t.Fatal(err)
	}
	if g.EUI != "0016C001F1225507" || g.ControlPort != 1700 || g.StatusPort != 5557 || g.Name != "Dragino LPS8 225507" {
		t.Fatalf("%+v", g)
	}
	if n := len(st.Gateways()); n != 1 {
		t.Fatalf("%d gateways", n)
	}
	st.UpdateGateway(g.ID, true, func(x *model.Gateway) { x.Name = "Roof" })
	p.HandleStatus("0016C001F1225507", "bobcat", "192.168.1.17", map[string]any{"model": "dragino-lps8"})
	if g, _ := st.Gateway(g.ID); g.Name != "Roof" {
		t.Fatalf("a name the owner chose must stay: %s", g.Name)
	}
}

func TestLinkKeyIsGenerated(t *testing.T) {
	p, _, _, _ := setup(t)
	if len(p.Settings().LinkKey) != 48 || !p.Settings().LinkAutoPair {
		t.Fatalf("%q", p.Settings().LinkKey)
	}
}
