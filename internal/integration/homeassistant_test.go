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
	if exposed(&model.Device{HAEnabled: true}) || exposed(&model.Device{Adopted: true}) || exposed(nil) {
		t.Fatal("exposed without adoption and opt-in")
	}
	if !exposed(&model.Device{Adopted: true, HAEnabled: true}) {
		t.Fatal("should be exposed")
	}
}
