package integration

import (
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
