package api

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nor24o/lorahub/internal/model"
)

func TestMergeJSONReplacesWhatIsSent(t *testing.T) {
	cur := &model.Device{ID: "a", Name: "Lamp", Class: "C",
		HAFields:    map[string]model.HAField{"temperature": {Name: "Room"}, "humidity": {Disabled: true}},
		HACommands:  []model.HACommand{{Name: "On", PayloadHex: "01"}, {Name: "Off", PayloadHex: "00"}},
		RawDownlink: &model.RawDownlink{Mode: "immediate", Frequency: 869.525, InvertPol: true}}
	body := `{"ha_fields":{"temperature":{"name":"Room"}},"ha_commands":[{"name":"On","payload_hex":"01"}],"raw_downlink":{"mode":"after_uplink"}}`
	got, err := mergeJSON(cur, httptest.NewRequest("PUT", "/", strings.NewReader(body)))
	if err != nil {
		t.Fatal(err)
	}
	if _, kept := got.HAFields["humidity"]; kept || len(got.HACommands) != 1 {
		t.Fatalf("removed entries came back: %+v %+v", got.HAFields, got.HACommands)
	}
	if got.RawDownlink.Frequency != 0 || got.RawDownlink.InvertPol {
		t.Fatalf("settings left out of a sent object must not survive: %+v", got.RawDownlink)
	}
	if got.Name != "Lamp" || got.Class != "C" {
		t.Fatalf("fields not sent must keep their values: %+v", got)
	}
	if len(cur.HACommands) != 2 || cur.HACommands[1].Name != "Off" || len(cur.HAFields) != 2 || cur.RawDownlink.Frequency != 869.525 {
		t.Fatal("the previous state was changed, so Home Assistant cannot see what was removed")
	}
	got, _ = mergeJSON(cur, httptest.NewRequest("PUT", "/", strings.NewReader(`{"raw_downlink":null,"ha_commands":[]}`)))
	if got.RawDownlink != nil || len(got.HACommands) != 0 || len(got.HAFields) != 2 {
		t.Fatalf("%+v", got)
	}
	if _, err := mergeJSON(cur, httptest.NewRequest("PUT", "/", strings.NewReader(`[1]`))); err == nil {
		t.Fatal("a body that is not an object must be refused")
	}
}
