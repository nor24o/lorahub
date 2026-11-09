package model

import "testing"

func TestRawDownlinkValidate(t *testing.T) {
	good := []RawDownlink{
		{},
		{Mode: "after_uplink", DelayMs: 1000, Frequency: 868.1, DataRate: "SF12BW125", CodingRate: "4/8", Power: 27, InvertPol: true, Preamble: 8, Header: "off"},
		{Mode: "immediate", DataRate: "SF5BW500", Header: "on"},
	}
	for i := range good {
		if err := good[i].Validate(); err != nil {
			t.Errorf("%+v: %v", good[i], err)
		}
	}
	if good[0].Mode != "immediate" {
		t.Fatal("default mode")
	}
	bad := []RawDownlink{
		{Mode: "later"}, {DelayMs: -1}, {DelayMs: 20000}, {Frequency: 868100000}, {DataRate: "SF4BW125"}, {DataRate: "SF9BW62"},
		{CodingRate: "4/9"}, {Power: 28}, {Power: -1}, {Preamble: 3}, {Header: "maybe"},
	}
	for i := range bad {
		if err := bad[i].Validate(); err == nil {
			t.Errorf("accepted %+v", bad[i])
		}
	}
}

func TestNodeHeaderOn(t *testing.T) {
	on, off := &Application{DownlinkHeader: true}, &Application{}
	if !on.NodeHeaderOn(&Device{}) || off.NodeHeaderOn(&Device{}) {
		t.Fatal("follows the group by default")
	}
	if on.NodeHeaderOn(&Device{RawDownlink: &RawDownlink{Header: "off"}}) || !off.NodeHeaderOn(&Device{RawDownlink: &RawDownlink{Header: "on"}}) {
		t.Fatal("the node's own setting wins")
	}
	if !on.NodeHeaderOn(&Device{RawDownlink: &RawDownlink{}}) {
		t.Fatal("empty follows the group")
	}
	control := &Application{DownlinkControl: true}
	if !control.NodeHeaderOn(&Device{}) || !control.NodeHeaderOn(&Device{RawDownlink: &RawDownlink{Header: "off"}}) {
		t.Fatal("the control byte always brings NODE_ID and SERIAL_ID")
	}
	if off.NodeHeaderOn(&Device{RawDownlink: &RawDownlink{Control: "on"}}) == false {
		t.Fatal("a node's own control byte brings the address too")
	}
}
