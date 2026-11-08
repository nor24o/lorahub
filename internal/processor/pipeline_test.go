package processor

import (
	"encoding/hex"
	"testing"
	"time"

	"github.com/nor24o/lorahub/internal/formatter"
	"github.com/nor24o/lorahub/internal/model"
)

func pipelineSetup(t *testing.T) (*Processor, func() []*model.Device) {
	t.Helper()
	p, st, _, _ := setup(t)
	s := p.Settings()
	s.AutoCreateGroups, s.LoRaWANEnabled = true, false
	if err := p.SetSettings(s); err != nil {
		t.Fatal(err)
	}
	doc := &formatter.SchemeDoc{Schemes: []formatter.Scheme{
		{ID: "aa0b", Name: "AA0B", MatchType: "prefix", MatchValue: "aa0b", Type: "ctv", DataOffset: 4},
		{ID: "cafe", Name: "CAFE", MatchType: "prefix", MatchValue: "cafe", Type: "cayenne_lpp", DataOffset: 6},
	}}
	if err := p.SetSchemes(doc); err != nil {
		t.Fatal(err)
	}
	return p, func() []*model.Device { return st.FindDevices(func(*model.Device) bool { return true }) }
}

func waitDevices(t *testing.T, list func() []*model.Device, uplinks uint64) []*model.Device {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var n uint64
		ds := list()
		for _, d := range ds {
			n += d.UplinkCount
		}
		if n >= uplinks {
			return ds
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d uplinks", uplinks)
	return nil
}

func send(p *Processor, s string) {
	b, _ := hex.DecodeString(s)
	p.HandleUplink(rx(b, "AA555A0000000001", 7))
	time.Sleep(90 * time.Millisecond) // beyond the dedup window
}

func TestChangingSequenceIsOneNode(t *testing.T) {
	p, list := pipelineSetup(t)
	send(p, "aa0b64b2011107aa02050fa5")
	send(p, "aa0b64b3011107ab02050fa5")
	ds := waitDevices(t, list, 2)
	if len(ds) != 1 || ds[0].ID != "aa0b" || ds[0].Serial != "" {
		t.Fatalf("devices: %+v", ds)
	}
	if ds[0].LastScheme != "AA0B" {
		t.Fatalf("scheme %q", ds[0].LastScheme)
	}
}

func TestGenericSchemeTypeHeaderHasNoSerial(t *testing.T) {
	p, list := pipelineSetup(t)
	doc := &formatter.SchemeDoc{Schemes: []formatter.Scheme{{ID: "g", Name: "generic", MatchType: "condition", MatchCondition: "return bytes.length >= 4;", Type: "ctv", DataOffset: 4}}}
	if err := p.SetSchemes(doc); err != nil {
		t.Fatal(err)
	}
	send(p, "aa0c64f7011108c302050fe3")
	send(p, "aa0c64f8011108c402050fe3")
	if ds := waitDevices(t, list, 2); len(ds) != 1 || ds[0].ID != "aa0c" {
		t.Fatalf("devices: %+v", ds)
	}
}

func TestSerialNodesStaySeparate(t *testing.T) {
	p, list := pipelineSetup(t)
	send(p, "CAFE96B840EB0102014902710001001C0425036700B904880000000000000000")
	send(p, "CAFE96B840EC0102014902710001001C0425036700B904880000000000000000")
	if ds := waitDevices(t, list, 2); len(ds) != 2 {
		t.Fatalf("devices: %d", len(ds))
	}
}

func TestRuleFiresWithCooldown(t *testing.T) {
	p, list := pipelineSetup(t)
	send(p, "aa0b64b2011107aa02050fa5")
	ds := waitDevices(t, list, 1)
	src := ds[0]
	r, err := p.SaveRule(model.Rule{Name: "hot", Enabled: true, SourceApp: src.AppID, SourceDev: src.ID, Field: "temperature", Operator: ">", Threshold: 10,
		TargetApp: src.AppID, TargetDev: src.ID, PayloadHex: "0101", CooldownSec: 3600})
	if err != nil {
		t.Fatal(err)
	}
	send(p, "aa0b64b3011107aa02050fa5")
	send(p, "aa0b64b4011107aa02050fa5")
	waitDevices(t, list, 3)
	time.Sleep(50 * time.Millisecond)
	got := p.Rules()[0]
	if got.ID != r.ID || got.LastTriggered == nil || got.LastStatus == "" {
		t.Fatalf("rule did not fire: %+v", got)
	}
	first := *got.LastTriggered
	send(p, "aa0b64b5011107aa02050fa5")
	waitDevices(t, list, 4)
	if !p.Rules()[0].LastTriggered.Equal(first) {
		t.Fatal("cooldown ignored")
	}
}

func TestRuleValidation(t *testing.T) {
	p, _ := pipelineSetup(t)
	if _, err := p.SaveRule(model.Rule{SourceApp: "a", SourceDev: "b", TargetApp: "a", TargetDev: "c", Operator: "~", PayloadHex: "01"}); err == nil {
		t.Fatal("bad operator accepted")
	}
	if _, err := p.SaveRule(model.Rule{SourceApp: "a", SourceDev: "b", TargetApp: "a", TargetDev: "c", Operator: "always", PayloadHex: "zz"}); err == nil {
		t.Fatal("bad hex accepted")
	}
}
