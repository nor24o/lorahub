package processor

import (
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/nor24o/lorahub/internal/events"
	"github.com/nor24o/lorahub/internal/model"
	"github.com/nor24o/lorahub/internal/store"
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
	g, err := st.Gateway("eui-0016c001f1225507")
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

func TestReversedIDIsCorrectedEverywhere(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	const oldID, newID = "eui-075522f101c01600", "eui-0016c001f1225507"
	now := time.Now().UTC()
	st.SaveGateway(&model.Gateway{ID: oldID, EUI: "0016C001F1225507", Name: "Dragino LPS8 225507", Kind: "bobcat", Downlink: true, ControlPort: 1700, RxCount: 9, CreatedAt: now})
	st.SaveGateway(&model.Gateway{ID: "eui-0016c001f1500b48", EUI: "0016C001F1500B48", Kind: "bobcat", CreatedAt: now})
	st.SaveApplication(&model.Application{ID: "app"})
	st.SaveDevice(&model.Device{ID: "n1", AppID: "app", Mode: "node", LastGateway: oldID})
	st.SaveDevice(&model.Device{ID: "n2", AppID: "app", Mode: "node", LastGateway: "eui-0016c001f1500b48"})
	st.AddEvent(&model.Event{Name: "gs.up", Time: now, Gateways: []string{oldID}, Summary: "via " + oldID,
		Data: map[string]any{"gateway_id": oldID, "eui": "075522F101C01600"}})
	st.AddEvent(&model.Event{Name: "gs.up", Time: now, Gateways: []string{"eui-0016c001f1500b48"}, Summary: "other", Data: map[string]any{"eui": "0016C001F1500B48"}})

	p := New(st, events.New(), slog.New(slog.NewTextHandler(io.Discard, nil)))

	if _, err := st.Gateway(oldID); err == nil {
		t.Fatal("the old ID still exists")
	}
	g, err := st.Gateway(newID)
	if err != nil || g.ControlPort != 1700 || g.RxCount != 9 || g.Name != "Dragino LPS8 225507" {
		t.Fatalf("%+v %v", g, err)
	}
	if n := len(st.Gateways()); n != 2 {
		t.Fatalf("%d gateways", n)
	}
	if d, _ := st.Device("app", "n1"); d.LastGateway != newID {
		t.Fatalf("node: %s", d.LastGateway)
	}
	if d, _ := st.Device("app", "n2"); d.LastGateway != "eui-0016c001f1500b48" {
		t.Fatalf("other node changed: %s", d.LastGateway)
	}
	if old, _ := st.Events(store.EventFilter{GatewayID: oldID}); len(old) != 0 {
		t.Fatalf("events still under the old ID: %+v", old)
	}
	evs, err := st.Events(store.EventFilter{GatewayID: newID})
	if err != nil || len(evs) != 1 {
		t.Fatalf("%v %v", evs, err)
	}
	data, _ := json.Marshal(evs[0].Data)
	if evs[0].Summary != "via "+newID || !strings.Contains(string(data), newID) || !strings.Contains(string(data), "0016C001F1225507") || strings.Contains(string(data), "075522") {
		t.Fatalf("%+v %s", evs[0], data)
	}
	if r := p.RenamedGateways(); len(r) != 1 || r[0] != oldID {
		t.Fatalf("%v", r)
	}
	// A correct gateway is left alone on the next start.
	if q := New(st, events.New(), slog.New(slog.NewTextHandler(io.Discard, nil))); len(q.RenamedGateways()) != 0 {
		t.Fatal("renamed twice")
	}
}

func TestCurrentStatusReplacesOldFields(t *testing.T) {
	p, st, _, _ := setup(t)
	p.HandleStatus("0016C001F1500B48", "bobcat", "127.0.0.1", map[string]any{"heartbeat": true, "rx": 1.0})
	p.HandleStatus("0016C001F1500B48", "bobcat", "127.0.0.1", map[string]any{"link": 2.0, "rx": 2.0})
	g, _ := st.GatewayByEUI("0016C001F1500B48")
	if strings.Contains(string(g.LastStatus), "heartbeat") || !strings.Contains(string(g.LastStatus), `"rx":2`) {
		t.Fatalf("%s", g.LastStatus)
	}
}
