package integration

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"sort"
	"strings"
	"testing"

	"github.com/nor24o/lorahub/internal/config"
	"github.com/nor24o/lorahub/internal/model"
)

func testSettings() config.Settings {
	s := config.DefaultSettings()
	s.MQTTTopicPrefix, s.HomeAssistant.DiscoveryPrefix = "lorahub", "homeassistant"
	return s
}

func byKey(list []haEntity) map[string]haEntity {
	out := map[string]haEntity{}
	for _, e := range list {
		out[e.component+"/"+e.key] = e
	}
	return out
}

func TestEntitiesSensorNode(t *testing.T) {
	d := &model.Device{ID: "aa0b", AppID: "node-aa0b", Class: "A"}
	got := byKey(entities(d, map[string]any{"temperature": 19.6, "battery": 4.0, "seq": 3.0, "node_class": "A", "ch1_temperature": 19.6}, testSettings()))
	temp, ok := got["sensor/temperature"]
	if !ok || temp.cfg["device_class"] != "temperature" || temp.cfg["state_topic"] != "lorahub/node-aa0b/aa0b/state" {
		t.Fatalf("temperature: %+v", temp)
	}
	if got["sensor/seq"].cfg["entity_category"] != "diagnostic" {
		t.Fatal("seq must be diagnostic")
	}
	for _, k := range []string{"sensor/node_class", "sensor/ch1_temperature", "switch/switch"} {
		if _, bad := got[k]; bad {
			t.Fatalf("unexpected entity %s", k)
		}
	}
	if _, ok := got["sensor/last_seen"]; !ok {
		t.Fatal("missing diagnostics")
	}
}

func TestEntitiesActuator(t *testing.T) {
	d := &model.Device{ID: "aa0c", AppID: "g", Class: "C"}
	sw, ok := byKey(entities(d, map[string]any{"state": "ON"}, testSettings()))["switch/switch"]
	if !ok || sw.cfg["command_topic"] != "lorahub/g/aa0c/set" || sw.cfg["optimistic"] != false {
		t.Fatalf("switch: %+v", sw)
	}
}

func TestExposedNeedsAdoptionAndOptIn(t *testing.T) {
	s := testSettings()
	if exposed(&model.Device{HAEnabled: true}, s) || exposed(&model.Device{Adopted: true}, s) || exposed(nil, s) {
		t.Fatal("exposed without adoption and opt-in")
	}
	if !exposed(&model.Device{Adopted: true, HAEnabled: true}, s) {
		t.Fatal("should be exposed")
	}
	s.HomeAssistant.ExposeMode = "all"
	if !exposed(&model.Device{}, s) {
		t.Fatal("expose mode all must include every node")
	}
}

func TestExposureSettings(t *testing.T) {
	d := &model.Device{ID: "a", AppID: "g", Class: "C", HAFields: map[string]model.HAField{
		"humidity": {Disabled: true}, "temperature": {Name: "Soil", Unit: "K"}}}
	dec := map[string]any{"temperature": 19.6, "humidity": 40.0, "pressure": 1000.0, "state": "ON"}
	s := testSettings()
	s.HomeAssistant.ExcludeFields = []string{"press*"}
	s.HomeAssistant.Diagnostics = []string{"rssi"}
	s.HomeAssistant.ExpireAfter = 600
	got := byKey(entities(d, dec, s))
	for _, k := range []string{"sensor/humidity", "sensor/pressure", "sensor/snr"} {
		if _, bad := got[k]; bad {
			t.Fatalf("%s should be hidden", k)
		}
	}
	temp := got["sensor/temperature"]
	if temp.cfg["name"] != "Soil" || temp.cfg["unit_of_measurement"] != "K" || temp.cfg["expire_after"] != 600 {
		t.Fatalf("override not applied: %+v", temp.cfg)
	}
	if _, ok := got["sensor/rssi"]; !ok {
		t.Fatal("selected diagnostic missing")
	}
	if _, ok := got["switch/switch"]; !ok {
		t.Fatal("switch missing")
	}
	if _, has := got["switch/switch"].cfg["expire_after"]; has {
		t.Fatal("switches must not expire")
	}
	s.MQTTCommands.AcceptSet = false
	if _, ok := byKey(entities(d, dec, s))["switch/switch"]; ok {
		t.Fatal("switch exposed although commands are off")
	}
}

func TestCommandsAndLastPayload(t *testing.T) {
	d := &model.Device{ID: "a", AppID: "g", Class: "A", HASwitch: true, HACommands: []model.HACommand{{Name: "Open valve", PayloadHex: "0101"}}}
	got := byKey(entities(d, map[string]any{"temperature": 20.0}, testSettings()))
	if b, ok := got["button/cmd_Open_valve"]; !ok || b.cfg["payload_press"] != "0101" || b.cfg["command_topic"] != "lorahub/g/a/set" || b.cfg["state_topic"] != nil {
		t.Fatalf("button: %+v", b)
	}
	if x, ok := got["text/downlink"]; !ok || x.cfg["command_topic"] != "lorahub/g/a/set" || x.cfg["state_topic"] != "lorahub/g/a/command_box" || x.cfg["value_template"] == nil {
		t.Fatalf("text: %+v", x)
	}
	sw, ok := got["switch/switch"]
	if !ok {
		t.Fatal("a node marked as having an output needs a switch")
	}
	if sw.cfg["state_topic"] != nil || sw.cfg["optimistic"] != true || sw.cfg["command_topic"] != "lorahub/g/a/set" {
		t.Fatalf("a node without a state field must keep the switch as set: %+v", sw.cfg)
	}
	hex, ok := got["sensor/hex"]
	if !ok || hex.cfg["json_attributes_topic"] != "lorahub/g/a/state" || !strings.Contains(hex.cfg["value_template"].(string), "h[:16]") {
		t.Fatalf("last payload: %+v", hex)
	}
	s := testSettings()
	s.HomeAssistant.LastPayload = false
	s.HomeAssistant.ExpireAfter = 60
	got = byKey(entities(d, map[string]any{"temperature": 20.0}, s))
	if _, ok := got["sensor/hex"]; ok {
		t.Fatal("last payload should be off")
	}
	if _, has := got["sensor/temperature"].cfg["expire_after"]; !has {
		t.Fatal("sensors expire")
	}
	if _, has := got["text/downlink"].cfg["expire_after"]; has {
		t.Fatal("commands do not expire")
	}
	s.MQTTCommands.AcceptSet = false
	got = byKey(entities(d, map[string]any{"temperature": 20.0}, s))
	for _, k := range []string{"text/downlink", "button/cmd_Open_valve", "switch/switch"} {
		if _, ok := got[k]; ok {
			t.Fatalf("%s shown although commands are off", k)
		}
	}
}

func controlTestDevice(class string) *model.Device {
	return &model.Device{ID: "a", AppID: "g", Class: class, HACommands: []model.HACommand{
		{Name: "Open valve", Kind: "button", PayloadHex: "0101"},
		{Name: "Dimmer", Kind: "number", PayloadHex: "02{value}", Min: 10, Max: 100, Step: 5, Unit: "%", Scale: 1, Encoding: "u8"},
		{Name: "Message", Kind: "text", PayloadHex: "03{value}", Encoding: "ascii"},
	}}
}

func TestDynamicControlsOnlyOnClassC(t *testing.T) {
	dec := map[string]any{"temperature": 20.0}
	got := byKey(entities(controlTestDevice("C"), dec, testSettings()))
	n, ok := got["number/cmd_Dimmer"]
	if !ok || n.cfg["command_topic"] != "lorahub/g/a/cmd/cmd_Dimmer" || n.cfg["state_topic"] != "lorahub/g/a/cmd/cmd_Dimmer/state" ||
		n.cfg["min"] != 10.0 || n.cfg["max"] != 100.0 || n.cfg["step"] != 5.0 || n.cfg["unit_of_measurement"] != "%" {
		t.Fatalf("number: %+v", n)
	}
	if x, ok := got["text/cmd_Message"]; !ok || x.cfg["pattern"] != nil {
		t.Fatalf("ascii text: %+v", x)
	}
	if _, ok := got["button/cmd_Open_valve"]; !ok {
		t.Fatal("button missing on class C")
	}
	d := controlTestDevice("A")
	got = byKey(entities(d, dec, testSettings()))
	for _, k := range []string{"number/cmd_Dimmer", "text/cmd_Message"} {
		if _, bad := got[k]; bad {
			t.Fatalf("%s shown on a class A node", k)
		}
	}
	if _, ok := got["button/cmd_Open_valve"]; !ok {
		t.Fatal("a fixed button is fine on class A")
	}
	for _, e := range allEntities(d, dec, testSettings()) {
		if e.key == "cmd_Dimmer" && hiddenReason(d, e, testSettings()) != "class C nodes only" {
			t.Fatalf("reason: %q", hiddenReason(d, e, testSettings()))
		}
	}
}

func TestHandleControl(t *testing.T) {
	var sent [][]byte
	saved := map[string]string{}
	d := controlTestDevice("C")
	m := &MQTT{log: slog.New(slog.NewTextHandler(io.Discard, nil)), settings: testSettings, haSent: map[string]bool{},
		downlink:         func(_, _ string, _ int, b []byte, _ any, _, _ bool) error { sent = append(sent, b); return nil },
		Device:           func(_, _ string) (*model.Device, error) { return d, nil },
		SaveControlValue: func(_, _, key, value string) { saved[key] = value }}
	m.handleDown("lorahub/g/a/cmd/cmd_Dimmer", []byte("50"))
	if len(sent) != 1 || !bytes.Equal(sent[0], []byte{2, 50}) || saved["cmd_Dimmer"] != "50" {
		t.Fatalf("sent %X saved %v", sent, saved)
	}
	m.handleDown("lorahub/g/a/cmd/cmd_Dimmer", []byte("101"))   // above the maximum
	m.handleDown("lorahub/g/a/cmd/cmd_Open_valve", []byte("1")) // buttons use /set
	m.handleDown("lorahub/g/a/cmd/nope", []byte("1"))
	if len(sent) != 1 {
		t.Fatalf("bad commands were sent: %X", sent)
	}
	d.Class = "A"
	m.handleDown("lorahub/g/a/cmd/cmd_Dimmer", []byte("20"))
	if len(sent) != 1 {
		t.Fatal("dynamic command sent to a class A node")
	}
}

func TestControlValueNeverUnknown(t *testing.T) {
	d := controlTestDevice("C")
	c := findControl(d, "cmd_Dimmer")
	if v := controlValue(d, c); v != "10" {
		t.Fatalf("default %q", v)
	}
	d.HAValues = map[string]string{"cmd_Dimmer": "40"}
	if v := controlValue(d, c); v != "40" {
		t.Fatalf("saved %q", v)
	}
	d.HAValues["cmd_Dimmer"] = "400" // the range was narrowed since
	if v := controlValue(d, c); v != "10" {
		t.Fatalf("out of range %q", v)
	}
}

func TestSweepWithdrawsStaleEntities(t *testing.T) {
	s := testSettings()
	s.HomeAssistant.Enabled = true
	s.HomeAssistant.Diagnostics = []string{"rssi"}
	live := &model.Device{ID: "b2", AppID: "node-b", Adopted: true, HAEnabled: true, Class: "A", LastDecoded: map[string]any{"temperature": 20.0},
		HAFields: map[string]model.HAField{"humidity": {Disabled: true}}, HACommands: []model.HACommand{{Name: "Open", Kind: "button", PayloadHex: "01"}}}
	m := &MQTT{log: slog.New(slog.NewTextHandler(io.Discard, nil)), settings: func() config.Settings { return s }, haSent: map[string]bool{},
		Devices: func() []*model.Device { return []*model.Device{live} }, Gateways: func() []string { return nil }}
	keep := []string{
		"homeassistant/sensor/lorahub_node_b_b2/temperature/config",
		"homeassistant/sensor/lorahub_node_b_b2/pressure/config", // sent in other packets
		"homeassistant/button/lorahub_node_b_b2/cmd_Open/config",
		"homeassistant/sensor/lorahub_node_b_b2/rssi/config",
		"homeassistant/sensor/other_integration/x/config",
	}
	gone := []string{
		"homeassistant/sensor/lorahub_node_cafe_96b840eb/temperature/config", // group deleted
		"homeassistant/button/lorahub_node_b_b2/cmd_Close/config",            // command removed
		"homeassistant/sensor/lorahub_node_b_b2/humidity/config",             // field turned off
		"homeassistant/sensor/lorahub_node_b_b2/snr/config",                  // diagnostic turned off
		"homeassistant/switch/lorahub_node_b_b2/switch/config",               // output removed
		"homeassistant/sensor/lorahub_node_b_b2_temperature/config",          // an older version's topic
	}
	for _, t := range append(append([]string{}, keep...), gone...) {
		m.noteRetained(t, []byte("{}"))
	}
	removed := m.sweep(false)
	sort.Strings(removed)
	want := append([]string{}, gone...)
	sort.Strings(want)
	if strings.Join(removed, "\n") != strings.Join(want, "\n") {
		t.Fatalf("removed:\n%s\nwant:\n%s", strings.Join(removed, "\n"), strings.Join(want, "\n"))
	}
	if again := m.sweep(false); len(again) != 0 {
		t.Fatalf("second sweep removed %v", again)
	}
	if strict := m.sweep(true); len(strict) != 1 || strict[0] != "homeassistant/sensor/lorahub_node_b_b2/pressure/config" {
		t.Fatalf("the strict sweep also takes fields missing from the last packet: %v", strict)
	}
	live.HAEnabled = false
	if left := m.sweep(false); len(left) != 3 {
		t.Fatalf("a node no longer exposed loses everything: %v", left)
	}
}

func TestFlatStateHasEveryEntityValue(t *testing.T) {
	msg := upMessage{Decoded: map[string]any{"temperature": 21.5, "gps": map[string]any{"lat": 1.0}}, PayloadHex: "CAFE", Size: 2, GatewayCount: 2, ReceivedAt: "2026-11-09T10:00:00Z",
		Gateways: []map[string]any{{"gateway_id": "g1", "rssi": -70.0, "snr": 8.0}}, Radio: map[string]any{"frequency": 868100000.0, "data_rate": "SF7BW125"}}
	b, gw := flatState(&msg)
	var st map[string]any
	if err := json.Unmarshal(b, &st); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"temperature", "gps_lat", "hex", "rssi", "snr", "freq", "datr", "gateway_count", "last_seen"} {
		if st[k] == nil {
			t.Errorf("%s missing from state", k)
		}
	}
	if gw != "g1" || st["freq"] != 868.1 {
		t.Fatalf("%v %v", gw, st["freq"])
	}
}

func TestDeviceStateWithoutHistory(t *testing.T) {
	d := &model.Device{LastDecoded: map[string]any{"temperature": 19.0}, LastRSSI: -80, LastSNR: 5, LastFreq: 868300000, LastDataRate: "SF9BW125"}
	var st map[string]any
	json.Unmarshal(deviceState(d), &st)
	if st["temperature"] != 19.0 || st["rssi"] != -80.0 || st["freq"] != 868.3 {
		t.Fatalf("%v", st)
	}
}
