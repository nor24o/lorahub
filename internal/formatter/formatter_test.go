package formatter

import (
	"encoding/hex"
	"testing"

	"github.com/nor24o/lorahub/internal/model"
)

func TestJS(t *testing.T) {
	code := `function decodeUplink(input){ return {data:{t:((input.bytes[0]<<8)|input.bytes[1])/10, port: input.fPort}, warnings:["w"]}; }
function encodeDownlink(input){ return {bytes:[input.data.on?1:0, 7], fPort: 9}; }`
	r := DecodeJS(code, Input{Bytes: []byte{0x00, 0xFA}, FPort: 3})
	if len(r.Errors) > 0 || toFloat(r.Data["t"]) != 25 || len(r.Warnings) != 1 {
		t.Fatalf("%+v", r)
	}
	b, p, err := EncodeJS(code, map[string]any{"on": true}, 1)
	if err != nil || p != 9 || hex.EncodeToString(b) != "0107" {
		t.Fatal(b, p, err)
	}
	r = DecodeJS(`function decodeUplink(i){ while(true){} }`, Input{})
	if len(r.Errors) == 0 {
		t.Fatal("expected timeout")
	}
}

func TestCayenne(t *testing.T) {
	b, _ := hex.DecodeString("03670110056700FF01880672D7FB8EEC000E10")
	d, err := DecodeCayenne(b)
	if err != nil || d["temperature_3"].(float64) != 27.2 || d["temperature_5"].(float64) != 25.5 {
		t.Fatal(d, err)
	}
}

func TestFields(t *testing.T) {
	d, _ := DecodeFields([]model.Field{{Name: "v", Offset: 1, Type: "int16_be", Scale: 0.1}, {Name: "id", Offset: 0, Length: 1, Type: "hex"}}, []byte{0xAA, 0xFF, 0x38})
	if d["v"].(float64) != -20 || d["id"] != "AA" {
		t.Fatal(d)
	}
}
