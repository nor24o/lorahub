package processor

import (
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nor24o/lorahub/internal/model"
	"github.com/nor24o/lorahub/internal/store"
)

type flakyDL struct {
	fakeDL
	failNext bool
}

func (f *flakyDL) Send(eui string, tx model.TxPacket) error {
	f.mu.Lock()
	fail := f.failNext
	f.failNext = false
	f.mu.Unlock()
	if fail {
		return fmt.Errorf("radio busy")
	}
	return f.fakeDL.Send(eui, tx)
}

func classCNode(t *testing.T, st *store.Store, gatewayKind string) {
	t.Helper()
	now := time.Now().UTC()
	st.SaveGateway(&model.Gateway{ID: "gw", EUI: "0000000000000001", Kind: gatewayKind, Downlink: true, CreatedAt: now})
	st.SaveDevice(&model.Device{ID: "lamp", AppID: "app", Mode: "node", Serial: "AABBCCDD", Class: "C", Adopted: true,
		LastGateway: "gw", LastFreq: 868100000, LastDataRate: "SF7BW125", CreatedAt: now})
}

func waitQueueEmpty(t *testing.T, st *store.Store) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if d, err := st.Device("app", "lamp"); err == nil && len(d.Queue) == 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("queue not empty")
}

func TestClassCCommandsGoOutInOrder(t *testing.T) {
	p, st, dl, _ := setup(t)
	classCNode(t, st, "semtech_udp")
	for i := byte(1); i <= 5; i++ {
		if _, err := p.EnqueueDownlink("app", "lamp", 1, []byte{i}, nil, false, false); err != nil {
			t.Fatal(err)
		}
	}
	waitQueueEmpty(t, st)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		dl.mu.Lock()
		n := len(dl.sent)
		dl.mu.Unlock()
		if n >= 5 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	dl.mu.Lock()
	defer dl.mu.Unlock()
	if len(dl.sent) != 5 {
		t.Fatalf("sent %d of 5", len(dl.sent))
	}
	for i, tx := range dl.sent {
		if tx.Payload[0] != byte(i+1) || !tx.Immediate {
			t.Fatalf("packet %d: %+v", i, tx)
		}
	}
}

func TestClassCIgnoresGroupWaitForUplink(t *testing.T) {
	p, st, dl, ch := setup(t)
	st.SaveApplication(&model.Application{ID: "app", Uplink: model.Formatter{Type: "cayenne_lpp"},
		RawDownlink: &model.RawDownlink{Mode: "after_uplink", DataRate: "SF9BW250", Power: 14}})
	classCNode(t, st, "semtech_udp")
	if _, err := p.EnqueueDownlink("app", "lamp", 1, []byte{7}, nil, false, false); err != nil {
		t.Fatal(err)
	}
	waitEvent(t, ch, "ns.down.sent")
	tx := dl.last()
	if tx == nil || !tx.Immediate || tx.DataRate != "SF9BW250" || tx.Frequency != 868100000 {
		t.Fatalf("%+v", tx)
	}
}

func TestNodeOwnDownlinkSettings(t *testing.T) {
	p, st, dl, ch := setup(t)
	second := &fakeDL{}
	p.RegisterDownlinker("second", second)
	st.SaveApplication(&model.Application{ID: "app", NodeID: "CAFE", DownlinkHeader: true, Uplink: model.Formatter{Type: "cayenne_lpp"},
		RawDownlink: &model.RawDownlink{Mode: "after_uplink", DataRate: "SF9BW250", Power: 14}})
	classCNode(t, st, "semtech_udp")
	st.SaveGateway(&model.Gateway{ID: "gw2", EUI: "0000000000000002", Kind: "second", Downlink: true, CreatedAt: time.Now().UTC()})
	st.UpdateDevice("app", "lamp", func(d *model.Device) error {
		d.RawDownlink = &model.RawDownlink{Mode: "immediate", Frequency: 869.525, DataRate: "SF10BW500", CodingRate: "4/8", Power: 10,
			InvertPol: true, Preamble: 12, NoCRC: true, NoHeader: true, Gateway: "gw2", Header: "off"}
		return nil
	})
	if _, err := p.EnqueueDownlink("app", "lamp", 1, []byte{7}, nil, false, false); err != nil {
		t.Fatal(err)
	}
	waitEvent(t, ch, "ns.down.sent")
	if dl.last() != nil {
		t.Fatal("sent through the wrong gateway")
	}
	tx := second.last()
	if tx == nil || !tx.Immediate || tx.Frequency != 869525000 || tx.DataRate != "SF10BW500" || tx.CodingRate != "4/8" || tx.Power != 10 ||
		!tx.InvertPol || tx.Preamble != 12 || !tx.NoCRC || !tx.NoHeader {
		t.Fatalf("%+v", tx)
	}
	if len(tx.Payload) != 1 || tx.Payload[0] != 7 {
		t.Fatalf("node header should be off: %X", tx.Payload)
	}
}

func TestOtherNodesKeepGroupIQ(t *testing.T) {
	p, st, dl, ch := setup(t)
	st.SaveApplication(&model.Application{ID: "app", NodeID: "CAFE", DownlinkHeader: true, Uplink: model.Formatter{Type: "cayenne_lpp"},
		RawDownlink: &model.RawDownlink{Mode: "immediate", DataRate: "SF9BW250", Power: 14}})
	classCNode(t, st, "semtech_udp")
	if _, err := p.EnqueueDownlink("app", "lamp", 1, []byte{7}, nil, false, false); err != nil {
		t.Fatal(err)
	}
	waitEvent(t, ch, "ns.down.sent")
	tx := dl.last()
	if tx == nil || tx.InvertPol || len(tx.Payload) != 7 || tx.Payload[0] != 0xCA {
		t.Fatalf("%+v", tx)
	}
}

func profileGroup(st *store.Store, on bool) {
	st.SaveApplication(&model.Application{ID: "node-cafe", NodeID: "CAFE", AutoRegister: true, NodeProfile: on, Uplink: model.Formatter{Type: "cayenne_lpp"},
		RawDownlink: &model.RawDownlink{Mode: "after_uplink", DelayMs: 1000, DataRate: "SF9BW250", Power: 14}})
}

func TestNodeProfileSetsClassAndDownlinkRadio(t *testing.T) {
	p, st, dl, ch := setup(t)
	profileGroup(st, true)
	payload, _ := hex.DecodeString("CAFE96B840EB" + "90" + "84ADD2" + "90" + "0167011002")
	p.HandleUplink(rx(payload, "0000000000000001", 7))
	e := waitEvent(t, ch, "as.up")
	msg := e.Data.(map[string]any)
	if msg["data_hex"] != "0167011002" {
		t.Fatalf("profile was not removed from the data: %v", msg["data_hex"])
	}
	if pr, ok := msg["profile"].(*model.NodeProfile); !ok || pr.Class != "C" || pr.Frequency != 869.525 {
		t.Fatalf("%+v", msg["profile"])
	}
	if dec, _ := msg["decoded"].(map[string]any); dec == nil || len(dec) == 0 {
		t.Fatalf("decoder should read the data after the profile: %+v", msg["decoded"])
	}
	d, err := st.Device("node-cafe", "96b840eb")
	if err != nil || d.Class != "C" || d.Profile == nil {
		t.Fatalf("%+v %v", d, err)
	}
	if _, err := p.EnqueueDownlink("node-cafe", "96b840eb", 1, []byte{1}, nil, false, false); err != nil {
		t.Fatal(err)
	}
	waitEvent(t, ch, "ns.down.sent")
	tx := dl.last()
	if tx == nil || !tx.Immediate || tx.Frequency != 869525000 || tx.DataRate != "SF9BW125" || tx.CodingRate != "4/5" || tx.InvertPol {
		t.Fatalf("%+v", tx)
	}
}

func TestNodeOwnSettingsBeatTheProfile(t *testing.T) {
	app := &model.Application{NodeProfile: true}
	own := &model.RawDownlink{Mode: "after_uplink", InvertPol: false}
	d := &model.Device{Class: "C", RawDownlink: own, Profile: &model.NodeProfile{Class: "C", InvertIQ: true}}
	if got := downlinkCfg(app, d); got.Mode != "after_uplink" || got.InvertPol {
		t.Fatalf("%+v", got)
	}
	d.RawDownlink = nil
	if got := downlinkCfg(app, d); got.Mode != "immediate" || !got.InvertPol {
		t.Fatalf("%+v", got)
	}
	app.NodeProfile = false
	if got := downlinkCfg(app, d); got.InvertPol {
		t.Fatalf("a profile only counts while the group uses them: %+v", got)
	}
}

func TestNodesWithoutProfileAreReadAsBefore(t *testing.T) {
	p, st, _, ch := setup(t)
	profileGroup(st, false)
	payload, _ := hex.DecodeString("CAFE96B840EB" + "0167011002")
	p.HandleUplink(rx(payload, "0000000000000001", 7))
	msg := waitEvent(t, ch, "as.up").Data.(map[string]any)
	if msg["data_hex"] != "0167011002" || msg["profile"] != nil {
		t.Fatalf("%+v", msg)
	}
}

func TestBadProfileKeepsTheData(t *testing.T) {
	p, st, _, ch := setup(t)
	profileGroup(st, true)
	payload, _ := hex.DecodeString("CAFE96B840EB" + "C0" + "0167")
	p.HandleUplink(rx(payload, "0000000000000001", 7))
	msg := waitEvent(t, ch, "as.up").Data.(map[string]any)
	if msg["data_hex"] != "C00167" || msg["profile_error"] == nil || msg["profile"] != nil {
		t.Fatalf("%+v", msg)
	}
}

func controlGroup(st *store.Store, control bool) {
	st.SaveApplication(&model.Application{ID: "app", NodeID: "CAFE", DownlinkHeader: true, DownlinkControl: control, Uplink: model.Formatter{Type: "cayenne_lpp"}})
}

func TestUnicastControlByte(t *testing.T) {
	p, st, dl, ch := setup(t)
	controlGroup(st, true)
	classCNode(t, st, "semtech_udp")
	st.UpdateDevice("app", "lamp", func(d *model.Device) error { d.DownSeq = 15; return nil })
	want := []string{"CAFEAABBCCDD8F07", "CAFEAABBCCDD9007"}
	for i, confirm := range []bool{false, true} {
		if _, err := p.EnqueueDownlink("app", "lamp", 1, []byte{7}, nil, confirm, false); err != nil {
			t.Fatal(err)
		}
		waitEvent(t, ch, "ns.down.sent")
		if got := fmt.Sprintf("%X", dl.last().Payload); got != want[i] {
			t.Fatalf("packet %d: %s, want %s", i, got, want[i])
		}
	}
	d, _ := st.Device("app", "lamp")
	if d.DownSeq != 1 {
		t.Fatalf("sequence should wrap at 15: %d", d.DownSeq)
	}
	st.UpdateDevice("app", "lamp", func(d *model.Device) error {
		d.RawDownlink = &model.RawDownlink{Mode: "immediate", Control: "off"}
		return nil
	})
	if _, err := p.EnqueueDownlink("app", "lamp", 1, []byte{7}, nil, false, false); err != nil {
		t.Fatal(err)
	}
	waitEvent(t, ch, "ns.down.sent")
	if got := fmt.Sprintf("%X", dl.last().Payload); got != "CAFEAABBCCDD07" {
		t.Fatalf("control off for this node: %s", got)
	}
	// The group sends no address of its own, but the control byte brings it.
	st.SaveApplication(&model.Application{ID: "app", NodeID: "CAFE", DownlinkControl: true, Uplink: model.Formatter{Type: "cayenne_lpp"}})
	st.UpdateDevice("app", "lamp", func(d *model.Device) error { d.RawDownlink = nil; return nil })
	if _, err := p.EnqueueDownlink("app", "lamp", 1, []byte{7}, nil, false, false); err != nil {
		t.Fatal(err)
	}
	waitEvent(t, ch, "ns.down.sent")
	if got := fmt.Sprintf("%X", dl.last().Payload); got != "CAFEAABBCCDD8107" { // sequence 1: the message without a control byte took no number
		t.Fatalf("control byte without its address: %s", got)
	}
}

func TestGroupBroadcastReachesBothClasses(t *testing.T) {
	p, st, dl, ch := setup(t)
	controlGroup(st, true)
	classCNode(t, st, "semtech_udp")
	st.SaveDevice(&model.Device{ID: "sensor", AppID: "app", Mode: "node", Serial: "11223344", Class: "A", Adopted: true, CreatedAt: time.Now().UTC()})
	res, err := p.SendBroadcast(Broadcast{AppID: "app", Data: []byte{7}, Radio: model.RawDownlink{Frequency: 869.525, DataRate: "SF9BW125"}, ClassAFor: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if res.ClassAQueued != 1 || len(res.Gateways) != 1 || res.PayloadHex != "CAFEFFFFFFFFA007" {
		t.Fatalf("%+v", res)
	}
	waitEvent(t, ch, "as.down.broadcast")
	tx := dl.last()
	if tx == nil || !tx.Immediate || tx.Frequency != 869525000 || tx.DataRate != "SF9BW125" || fmt.Sprintf("%X", tx.Payload) != "CAFEFFFFFFFFA007" {
		t.Fatalf("%+v", tx)
	}
	sensor, _ := st.Device("app", "sensor")
	if len(sensor.Queue) != 1 || sensor.Queue[0].ExpiresAt == nil || fmt.Sprintf("%X", sensor.Queue[0].Payload) != "CAFEFFFFFFFFA007" {
		t.Fatalf("%+v", sensor.Queue)
	}
	if lamp, _ := st.Device("app", "lamp"); len(lamp.Queue) != 0 {
		t.Fatalf("a class C node gets it on the air: %+v", lamp.Queue)
	}
}

func TestBroadcastToEveryone(t *testing.T) {
	p, st, dl, ch := setup(t)
	controlGroup(st, false)
	classCNode(t, st, "semtech_udp")
	res, err := p.SendBroadcast(Broadcast{AppID: "app", Everyone: true, Data: []byte{7}, Radio: model.RawDownlink{Frequency: 868.1, DataRate: "SF7BW125", InvertPol: true}})
	if err != nil || res.PayloadHex != "FFFFFFFFFFFFC007" || res.ClassAQueued != 0 {
		t.Fatalf("%+v %v", res, err)
	}
	waitEvent(t, ch, "as.down.broadcast")
	if tx := dl.last(); tx == nil || !tx.InvertPol {
		t.Fatalf("%+v", tx)
	}
}

func TestBroadcastGoesThroughEachGatewayInTurn(t *testing.T) {
	p, st, dl, ch := setup(t)
	controlGroup(st, true)
	classCNode(t, st, "semtech_udp")
	st.SaveGateway(&model.Gateway{ID: "gw2", EUI: "0000000000000002", Kind: "semtech_udp", Downlink: true, CreatedAt: time.Now().UTC()})
	st.SaveGateway(&model.Gateway{ID: "off", EUI: "0000000000000003", Kind: "semtech_udp", Downlink: false, CreatedAt: time.Now().UTC()})
	res, err := p.SendBroadcast(Broadcast{AppID: "app", Data: []byte{7}, Radio: model.RawDownlink{Frequency: 868.1, DataRate: "SF7BW125"}})
	if err != nil || len(res.Gateways) != 2 {
		t.Fatalf("%+v %v", res, err)
	}
	waitEvent(t, ch, "as.down.broadcast")
	waitEvent(t, ch, "as.down.broadcast")
	dl.mu.Lock()
	defer dl.mu.Unlock()
	if len(dl.sent) != 2 {
		t.Fatalf("%d", len(dl.sent))
	}
	res2, err := p.SendBroadcast(Broadcast{AppID: "app", Data: []byte{7}, Gateways: []string{"gw2"}, Radio: model.RawDownlink{Frequency: 868.1, DataRate: "SF7BW125"}})
	if err != nil || len(res2.Gateways) != 1 || res2.Gateways[0] != "gw2" || res2.PayloadHex != "CAFEFFFFFFFFA107" {
		t.Fatalf("the chosen gateway and next sequence number: %+v %v", res2, err)
	}
}

func TestBroadcastRefused(t *testing.T) {
	p, st, _, _ := setup(t)
	controlGroup(st, false)
	classCNode(t, st, "semtech_udp")
	radio := model.RawDownlink{Frequency: 868.1, DataRate: "SF7BW125"}
	if _, err := p.SendBroadcast(Broadcast{AppID: "app", Data: []byte{1}, Radio: radio}); err == nil {
		t.Fatal("group broadcast without the control byte")
	}
	controlGroup(st, true)
	for name, b := range map[string]Broadcast{
		"no data":      {AppID: "app", Radio: radio},
		"no frequency": {AppID: "app", Data: []byte{1}, Radio: model.RawDownlink{DataRate: "SF7BW125"}},
		"no data rate": {AppID: "app", Data: []byte{1}, Radio: model.RawDownlink{Frequency: 868.1}},
		"too long":     {AppID: "app", Data: make([]byte, 240), Radio: radio},
		"unknown":      {AppID: "nope", Data: []byte{1}, Radio: radio},
	} {
		if _, err := p.SendBroadcast(b); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestExpiredBroadcastIsNotSentInClassAWindow(t *testing.T) {
	p, st, dl, ch := setup(t)
	st.SaveApplication(&model.Application{ID: "node-cafe", NodeID: "CAFE", AutoRegister: true, Uplink: model.Formatter{Type: "cayenne_lpp"},
		RawDownlink: &model.RawDownlink{Mode: "after_uplink", DelayMs: 1000, Power: 14}})
	first, _ := hex.DecodeString("CAFE96B840EB0167011002")
	p.HandleUplink(rx(first, "0000000000000001", 7))
	waitEvent(t, ch, "as.up")
	past := time.Now().Add(-time.Minute)
	st.UpdateDevice("node-cafe", "96b840eb", func(d *model.Device) error {
		d.Adopted, d.Class = true, "A"
		d.Queue = []model.QueuedDownlink{{ID: "old", FPort: 1, Payload: []byte{1}, CreatedAt: past, ExpiresAt: &past}}
		return nil
	})
	second, _ := hex.DecodeString("CAFE96B840EB0167011003")
	p.HandleUplink(rx(second, "0000000000000001", 7))
	waitEvent(t, ch, "ns.down.failed")
	if d, _ := st.Device("node-cafe", "96b840eb"); len(d.Queue) != 0 {
		t.Fatalf("%+v", d.Queue)
	}
	if dl.last() != nil {
		t.Fatal("an expired broadcast was transmitted")
	}
}

func TestClassCAvoidsAGatewayThatWentSilent(t *testing.T) {
	p, st, dl, ch := setup(t)
	other := &fakeDL{}
	p.RegisterDownlinker("bobcat", other)
	classCNode(t, st, "semtech_udp")
	old, recent := time.Now().UTC().Add(-10*time.Minute), time.Now().UTC().Add(-5*time.Second)
	st.UpdateGateway("gw", true, func(g *model.Gateway) { g.LastSeen = &old })
	st.SaveGateway(&model.Gateway{ID: "gw2", EUI: "0000000000000002", Kind: "bobcat", Downlink: true, LastSeen: &recent,
		LastStatus: []byte(`{"link":2}`), CreatedAt: time.Now().UTC()})
	if _, err := p.EnqueueDownlink("app", "lamp", 1, []byte{7}, nil, false, false); err != nil {
		t.Fatal(err)
	}
	e := waitEvent(t, ch, "ns.down.sent")
	if dl.last() != nil || other.last() == nil || !strings.Contains(e.Summary, "instead of gw") {
		t.Fatalf("%s", e.Summary)
	}
}

func TestClassAStillWaitsForGroupUplinkMode(t *testing.T) {
	app := &model.Application{RawDownlink: &model.RawDownlink{Mode: "after_uplink"}}
	if got := downlinkCfg(app, &model.Device{Class: "A", Adopted: true}).Mode; got != "after_uplink" {
		t.Fatal(got)
	}
	node := &model.Device{Class: "C", RawDownlink: &model.RawDownlink{Mode: "after_uplink"}}
	if got := downlinkCfg(app, node).Mode; got != "after_uplink" {
		t.Fatalf("a setting on the node itself wins: %s", got)
	}
}

func TestClassCOldCommandsDoNotFire(t *testing.T) {
	p, st, dl, ch := setup(t)
	classCNode(t, st, "semtech_udp")
	st.UpdateDevice("app", "lamp", func(d *model.Device) error {
		d.Queue = []model.QueuedDownlink{{ID: "old1", FPort: 1, Payload: []byte{1}, CreatedAt: time.Now().Add(-5 * time.Minute)},
			{ID: "old2", FPort: 1, Payload: []byte{2}, CreatedAt: time.Now().Add(-4 * time.Minute)}}
		return nil
	})
	if _, err := p.EnqueueDownlink("app", "lamp", 1, []byte{9}, nil, false, false); err != nil {
		t.Fatal(err)
	}
	waitEvent(t, ch, "ns.down.sent")
	waitQueueEmpty(t, st)
	dl.mu.Lock()
	defer dl.mu.Unlock()
	if len(dl.sent) != 1 || dl.sent[0].Payload[0] != 9 {
		t.Fatalf("%+v", dl.sent)
	}
}

func TestClassCFailedCommandDoesNotBlockTheNext(t *testing.T) {
	p, st, _, ch := setup(t)
	dl := &flakyDL{failNext: true}
	p.RegisterDownlinker("flaky", dl)
	classCNode(t, st, "flaky")
	if _, err := p.EnqueueDownlink("app", "lamp", 1, []byte{1}, nil, false, false); err != nil {
		t.Fatal(err)
	}
	waitEvent(t, ch, "ns.down.failed")
	waitQueueEmpty(t, st)
	if _, err := p.EnqueueDownlink("app", "lamp", 1, []byte{2}, nil, false, false); err != nil {
		t.Fatal(err)
	}
	waitEvent(t, ch, "ns.down.sent")
	dl.mu.Lock()
	defer dl.mu.Unlock()
	if len(dl.sent) != 1 || dl.sent[0].Payload[0] != 2 {
		t.Fatalf("%+v", dl.sent)
	}
}
