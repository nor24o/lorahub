package model

import (
	"encoding/hex"
	"strings"
	"testing"
)

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(strings.ReplaceAll(s, " ", ""))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func txt(s string) string {
	return hex.EncodeToString([]byte{byte(len(s))}) + hex.EncodeToString([]byte(s))
}

// The example in docs/node-protocol.md: a relay, a blink button and an interval.
func exampleDescription() string {
	return "D1 11" +
		" 01 11" + txt("Relay") +
		" 02 00" + txt("Blink") +
		" 03 23 000A 0E10 00" + txt("Interval") + txt("s")
}

func TestParseDescriptionExample(t *testing.T) {
	part, err := ParseDescription(mustHex(t, exampleDescription()))
	if err != nil {
		t.Fatal(err)
	}
	if part.Part != 1 || part.Parts != 1 || len(part.Commands) != 3 {
		t.Fatalf("part %+v", part)
	}
	relay, blink, interval := part.Commands[0], part.Commands[1], part.Commands[2]
	if relay.Code != 1 || relay.Kind != "switch" || relay.Format != "u8" || relay.Name != "Relay" {
		t.Errorf("relay %+v", relay)
	}
	if blink.Code != 2 || blink.Kind != "button" || blink.Value != nil {
		t.Errorf("blink %+v", blink)
	}
	if interval.Kind != "number" || interval.Format != "u16" || interval.Min != 10 || interval.Max != 3600 || interval.Unit != "s" {
		t.Errorf("interval %+v", interval)
	}
	// The whole uplink: header, profile byte (class C + description), description.
	if n := 6 + 1 + len(mustHex(t, exampleDescription())); n != 43 {
		t.Errorf("example uplink is %d bytes", n)
	}

	render := func(c DescribedCommand, v string) string {
		hc := c.HACommand()
		if err := hc.Normalize(); err != nil {
			t.Fatalf("%s: %v", c.Name, err)
		}
		b, err := hc.Render(v)
		if err != nil {
			t.Fatalf("%s %q: %v", c.Name, v, err)
		}
		return strings.ToUpper(hex.EncodeToString(b))
	}
	for _, tc := range []struct {
		c    DescribedCommand
		v    string
		want string
	}{{relay, "ON", "0101"}, {relay, "off", "0100"}, {blink, "", "02"}, {interval, "300", "03012C"}} {
		if got := render(tc.c, tc.v); got != tc.want {
			t.Errorf("%s %q = %s, want %s", tc.c.Name, tc.v, got, tc.want)
		}
	}
	if hc := interval.HACommand(); hc.Component() != "number" || hc.Source != "node" || hc.Min != 10 || hc.Max != 3600 || hc.Step != 1 {
		t.Errorf("interval command %+v", hc)
	}
}

func TestParseDescriptionKinds(t *testing.T) {
	b := mustHex(t, "D1 12"+
		" 04 24 FF9C 012C FF"+txt("Setpoint")+txt("C")+ // number, i16, -10.0 to 30.0 in tenths
		" 05 31 03 01"+txt("Eco")+" 02"+txt("Comfort")+" 03"+txt("Boost")+txt("Mode")+ // choice, u8
		" 06 01 2A"+txt("Reset")+ // button with the fixed value 2A
		" 07 40 0A"+txt("Text")) // raw, up to 10 bytes
	part, err := ParseDescription(b)
	if err != nil {
		t.Fatal(err)
	}
	if part.Part != 1 || part.Parts != 2 || len(part.Commands) != 4 {
		t.Fatalf("part %+v", part)
	}
	set, mode, reset, text := part.Commands[0], part.Commands[1], part.Commands[2], part.Commands[3]
	if set.Min != -100 || set.Max != 300 || set.Scale != -1 {
		t.Errorf("setpoint %+v", set)
	}
	hs := set.HACommand()
	if err := hs.Normalize(); err != nil {
		t.Fatal(err)
	}
	if hs.Min != -10 || hs.Max != 30 || hs.Step != 0.1 || hs.Scale != 10 {
		t.Errorf("setpoint command %+v", hs)
	}
	if out, _ := hs.Render("21.5"); strings.ToUpper(hex.EncodeToString(out)) != "0400D7" {
		t.Errorf("setpoint 21.5 = %X", out)
	}
	hm := mode.HACommand()
	if err := hm.Normalize(); err != nil {
		t.Fatal(err)
	}
	if hm.Component() != "select" || len(hm.Options) != 3 {
		t.Errorf("mode %+v", hm)
	}
	for v, want := range map[string]string{"Comfort": "0502", "boost": "0503", "1": "0501"} {
		if out, err := hm.Render(v); err != nil || strings.ToUpper(hex.EncodeToString(out)) != want {
			t.Errorf("mode %q = %X %v, want %s", v, out, err, want)
		}
	}
	if _, err := hm.Render("Turbo"); err == nil {
		t.Error("unknown option accepted")
	}
	if hr := reset.HACommand(); hr.Kind != "button" || hr.PayloadHex != "062A" {
		t.Errorf("reset %+v", hr)
	}
	ht := text.HACommand()
	if err := ht.Normalize(); err != nil || ht.Kind != "text" || ht.Encoding != "hex" || text.MaxLen != 10 {
		t.Errorf("text %+v %v", ht, err)
	}
	if out, _ := ht.Render("A1B2"); strings.ToUpper(hex.EncodeToString(out)) != "07A1B2" {
		t.Errorf("text = %X", out)
	}
}

func TestParseDescriptionErrors(t *testing.T) {
	for name, s := range map[string]string{
		"not a description":   "D2 11 01 11" + txt("Relay"),
		"no command":          "D1 11",
		"part beyond parts":   "D1 32 01 11" + txt("Relay"),
		"reserved code":       "D1 11 FF 11" + txt("Relay"),
		"unknown type":        "D1 11 01 91" + txt("Relay"),
		"unknown format":      "D1 11 01 1F" + txt("Relay"),
		"number, no format":   "D1 11 03 20 00" + txt("Interval") + txt("s"),
		"min not below max":   "D1 11 03 21 10 10 00" + txt("Interval") + txt("s"),
		"truncated":           "D1 11 03 23 000A 0E",
		"name too long":       "D1 11 01 11" + txt(strings.Repeat("x", 25)),
		"empty name":          "D1 11 01 11 00",
		"name not text":       "D1 11 01 11 02 41 07",
		"code used twice":     "D1 11 01 11" + txt("Relay") + " 01 00" + txt("Blink"),
		"duplicate option":    "D1 11 05 31 02 01" + txt("Eco") + " 02" + txt("eco") + txt("Mode"),
		"too many options":    "D1 11 05 31 21" + txt("Mode"),
		"scale out of range":  "D1 11 03 21 00 10 07" + txt("Interval") + txt("s"),
		"leftover byte":       "D1 11 01 11" + txt("Relay") + " 02",
		"choice, no options":  "D1 11 05 31 00" + txt("Mode"),
		"button value cut":    "D1 11 06 03 2A",
		"raw without length":  "D1 11 07 40",
		"unit too long":       "D1 11 03 21 00 10 00" + txt("Interval") + txt("123456789"),
		"bad option name":     "D1 11 05 31 01 01 00" + txt("Mode"),
		"part zero":           "D1 01 01 11" + txt("Relay"),
		"zero parts":          "D1 10 01 11" + txt("Relay"),
		"only the format":     "D1",
		"number cut at max":   "D1 11 03 23 000A",
		"number cut at scale": "D1 11 03 23 000A 0E10",
	} {
		if _, err := ParseDescription(mustHex(t, s)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestProfileDescribeBit(t *testing.T) {
	p, n, err := ParseNodeProfile([]byte{0x88})
	if err != nil || n != 1 || p.Class != "C" || !p.Describe || p.User != 0 {
		t.Fatalf("%+v %d %v", p, n, err)
	}
	p, _, _ = ParseNodeProfile([]byte{0x47})
	if p.Describe || p.User != 7 {
		t.Fatalf("class B with user bits: %+v", p)
	}
}

func TestDeviceCommandsMerge(t *testing.T) {
	part, err := ParseDescription(mustHex(t, exampleDescription()))
	if err != nil {
		t.Fatal(err)
	}
	d := &Device{HACommands: []HACommand{{Name: "Blink", Kind: "button", PayloadHex: "0299"}}, Described: &NodeDescription{Commands: part.Commands}}
	cmds := d.Commands()
	if len(cmds) != 3 {
		t.Fatalf("%d commands: %+v", len(cmds), cmds)
	}
	if cmds[0].PayloadHex != "0299" || cmds[0].Source != "" {
		t.Errorf("the command set in LoRaHub must win: %+v", cmds[0])
	}
	if cmds[1].Name != "Relay" || cmds[1].Source != "node" || cmds[2].Name != "Interval" {
		t.Errorf("announced commands %+v", cmds[1:])
	}
}
