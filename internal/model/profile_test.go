package model

import "testing"

func TestParseNodeProfile(t *testing.T) {
	cases := []struct {
		name string
		in   []byte
		used int
		want NodeProfile
	}{
		{"class A only", []byte{0x00, 0xAA}, 1, NodeProfile{Class: "A"}},
		{"class B with user bits", []byte{0x47}, 1, NodeProfile{Class: "B", User: 7}},
		{"class C inverted", []byte{0xA0}, 1, NodeProfile{Class: "C", InvertIQ: true}},
		{"radio block", []byte{0x90, 0x84, 0xAD, 0xD2, 0x90, 0x01}, 5, NodeProfile{Class: "C", Frequency: 869.525, DataRate: "SF9BW125", CodingRate: "4/5"}},
		{"wide and strong coding", []byte{0x10, 0x84, 0x76, 0x28, 0xC7}, 5, NodeProfile{Class: "A", Frequency: 868.1, DataRate: "SF12BW250", CodingRate: "4/8"}},
		{"same as uplink", []byte{0x90, 0, 0, 0, 0x01}, 5, NodeProfile{Class: "C", CodingRate: "4/6"}},
	}
	for _, c := range cases {
		got, n, err := ParseNodeProfile(c.in)
		if err != nil || n != c.used || *got != c.want {
			t.Errorf("%s: got %+v used %d err %v", c.name, got, n, err)
		}
	}
	bad := map[string][]byte{"empty": {}, "reserved class": {0xC0}, "short block": {0x90, 1, 2, 3}, "frequency too low": {0x90, 0, 0, 1, 0x90},
		"sf 4": {0x90, 0x84, 0xAD, 0xD2, 0x40}, "sf 13": {0x90, 0x84, 0xAD, 0xD2, 0xD0}, "reserved bandwidth": {0x90, 0x84, 0xAD, 0xD2, 0x9C}}
	for name, in := range bad {
		if p, _, err := ParseNodeProfile(in); err == nil {
			t.Errorf("%s accepted: %+v", name, p)
		}
	}
}

func TestNodeProfileApply(t *testing.T) {
	cfg := RawDownlink{Mode: "after_uplink", DelayMs: 1000, DataRate: "SF9BW250", Power: 14}
	(&NodeProfile{Class: "C", InvertIQ: true}).Apply(&cfg)
	if cfg.Mode != "immediate" || !cfg.InvertPol || cfg.DataRate != "SF9BW250" || cfg.Power != 14 {
		t.Fatalf("%+v", cfg)
	}
	(&NodeProfile{Class: "B", Frequency: 868.1, DataRate: "SF7BW125", CodingRate: "4/6"}).Apply(&cfg)
	if cfg.Mode != "after_uplink" || cfg.InvertPol || cfg.Frequency != 868.1 || cfg.DataRate != "SF7BW125" || cfg.CodingRate != "4/6" {
		t.Fatalf("%+v", cfg)
	}
}
