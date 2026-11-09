package model

import (
	"bytes"
	"testing"
)

func TestHACommandRender(t *testing.T) {
	cases := []struct {
		name  string
		c     HACommand
		value string
		want  []byte
	}{
		{"button", HACommand{Name: "a", Kind: "button", PayloadHex: "0101"}, "", []byte{1, 1}},
		{"old button without a kind", HACommand{Name: "a", PayloadHex: "0100"}, "", []byte{1, 0}},
		{"u8", HACommand{Name: "a", Kind: "number", PayloadHex: "02{value}", Min: 0, Max: 100, Scale: 1, Encoding: "u8"}, "50", []byte{2, 50}},
		{"scaled little endian with tail", HACommand{Name: "a", Kind: "number", PayloadHex: "03{value}FF", Min: 0, Max: 50, Scale: 10, Encoding: "u16le"}, "21.5", []byte{3, 215, 0, 0xFF}},
		{"big endian", HACommand{Name: "a", Kind: "number", PayloadHex: "{value}", Min: 0, Max: 70000, Scale: 1, Encoding: "u32be"}, "66051", []byte{0, 1, 2, 3}},
		{"signed", HACommand{Name: "a", Kind: "number", PayloadHex: "{value}", Min: -20, Max: 20, Scale: 1, Encoding: "i16be"}, "-2", []byte{0xFF, 0xFE}},
		{"hex text", HACommand{Name: "a", Kind: "text", PayloadHex: "04{value}", Encoding: "hex"}, "ABCD", []byte{4, 0xAB, 0xCD}},
		{"ascii text", HACommand{Name: "a", Kind: "text", PayloadHex: "05{value}", Encoding: "ascii"}, "Hi", []byte{5, 'H', 'i'}},
	}
	for _, tc := range cases {
		got, err := tc.c.Render(tc.value)
		if err != nil || !bytes.Equal(got, tc.want) {
			t.Errorf("%s: got %X err %v, want %X", tc.name, got, err, tc.want)
		}
	}
}

func TestHACommandRenderRejectsBadValues(t *testing.T) {
	num := HACommand{Name: "a", Kind: "number", PayloadHex: "02{value}", Min: 0, Max: 100, Scale: 1, Encoding: "u8"}
	for _, v := range []string{"101", "-1", "abc", "", "NaN"} {
		if b, err := num.Render(v); err == nil {
			t.Errorf("value %q accepted: %X", v, b)
		}
	}
	text := HACommand{Name: "a", Kind: "text", PayloadHex: "{value}", Encoding: "hex"}
	for _, v := range []string{"", "ABC", "zz"} {
		if b, err := text.Render(v); err == nil {
			t.Errorf("text %q accepted: %X", v, b)
		}
	}
}

func TestHACommandNormalize(t *testing.T) {
	c := HACommand{Name: " Dim ", Kind: "number", PayloadHex: "0a {VALUE}", Max: 100}
	if err := c.Normalize(); err != nil {
		t.Fatal(err)
	}
	if c.Name != "Dim" || c.PayloadHex != "0A{value}" || c.Encoding != "u8" || c.Step != 1 || c.Scale != 1 {
		t.Fatalf("%+v", c)
	}
	blank := HACommand{Name: "x", Kind: "text"}
	if err := blank.Normalize(); err != nil || blank.PayloadHex != HAValueToken || blank.Encoding != "hex" {
		t.Fatalf("%+v %v", blank, err)
	}
	old := HACommand{Name: "x", PayloadHex: "01 01"}
	if err := old.Normalize(); err != nil || old.Kind != "button" || old.PayloadHex != "0101" {
		t.Fatalf("%+v %v", old, err)
	}
	bad := []HACommand{
		{Kind: "button", PayloadHex: "01"},
		{Name: "x", Kind: "button", PayloadHex: "0"},
		{Name: "x", Kind: "button"},
		{Name: "x", Kind: "dial", PayloadHex: "01"},
		{Name: "x", Kind: "number", PayloadHex: "02", Max: 10},
		{Name: "x", Kind: "number", PayloadHex: "{value}{value}", Max: 10},
		{Name: "x", Kind: "number", PayloadHex: "0{value}", Max: 10},
		{Name: "x", Kind: "number", PayloadHex: "{value}"},
		{Name: "x", Kind: "number", PayloadHex: "{value}", Max: 300},
		{Name: "x", Kind: "number", PayloadHex: "{value}", Max: 10, Scale: 100},
		{Name: "x", Kind: "number", PayloadHex: "{value}", Max: 10, Encoding: "u64"},
		{Name: "x", Kind: "text", PayloadHex: "{value}", Encoding: "utf16"},
	}
	for i := range bad {
		if err := bad[i].Normalize(); err == nil {
			t.Errorf("accepted %+v", bad[i])
		}
	}
}

func TestHACommandKey(t *testing.T) {
	c := HACommand{Name: "Open valve"}
	if c.Key() != "cmd_Open_valve" {
		t.Fatal(c.Key())
	}
}
