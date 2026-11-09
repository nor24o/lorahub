package formatter

import (
	"testing"
	"time"

	"github.com/nor24o/lorahub/internal/model"
)

func ptrF(v float64) *float64 { return &v }
func ptrI(v int) *int         { return &v }

func TestFieldTypes(t *testing.T) {
	cases := []struct {
		f    model.Field
		hex  string
		want any
	}{
		{model.Field{Type: "uint24_le"}, "010203", 197121.0},
		{model.Field{Type: "int24_le"}, "FFFFFF", -1.0},
		{model.Field{Type: "uint40_be"}, "0000000100", 256.0},
		{model.Field{Type: "int64_be"}, "FFFFFFFFFFFFFFFE", -2.0},
		{model.Field{Type: "float64_le"}, "000000000000F03F", 1.0},
		{model.Field{Type: "bcd", Length: 2}, "1234", 1234.0},
		{model.Field{Type: "bits", Length: 2, Bit: 4, BitLen: 8}, "0AB0", 171.0},
		{model.Field{Type: "bits_signed", Length: 1, Bit: 0, BitLen: 4}, "0F", -1.0},
		{model.Field{Type: "bits_le", Length: 2, Bit: 8, BitLen: 8}, "0102", 2.0},
		{model.Field{Type: "unix_s_be"}, "5F5E1000", "2020-09-13T12:26:40Z"},
		{model.Field{Type: "hex_le", Length: 2}, "1234", "3412"},
		{model.Field{Type: "base64", Length: 3}, "616263", "YWJj"},
		{model.Field{Type: "bool", Invert: true}, "01", false},
		{model.Field{Type: "ascii", Length: -1}, "414200", "AB"},
	}
	for _, c := range cases {
		c.f.Name = "v"
		d, w := DecodeFields([]model.Field{c.f}, mustHex(t, c.hex))
		if d["v"] != c.want {
			t.Errorf("%s %s: got %v, want %v (%v)", c.f.Type, c.hex, d["v"], c.want, w)
		}
	}
}

func TestFieldParameters(t *testing.T) {
	b := mustHex(t, "0102FFFF0A")
	d, w := DecodeFields([]model.Field{
		{Name: "state", Offset: 0, Type: "uint8", Map: map[string]string{"1": "open"}},
		{Name: "raw", Offset: 1, Type: "uint8", Map: map[string]string{"0x2": "two"}},
		{Name: "missing", Offset: 2, Type: "uint16_be", Invalid: "FFFF"},
		{Name: "last", Offset: -1, Type: "uint8", Decimals: ptrI(1), Scale: 0.33},
		{Name: "fahrenheit", Offset: 4, Type: "uint8", Expr: "x * 9 / 5 + 32"},
		{Name: "sum", Offset: 4, Type: "uint8", Expr: "x + v.fahrenheit"},
		{Name: "skipped", Offset: 4, Type: "uint8", When: "bytes[0] == 9"},
		{Name: "tooHigh", Offset: 4, Type: "uint8", Max: ptrF(5)},
	}, b)
	want := map[string]any{"state": "open", "raw": "two", "last": 3.3, "fahrenheit": 50.0, "sum": 60.0}
	if len(d) != len(want) {
		t.Fatalf("got %v, warnings %v", d, w)
	}
	for k, v := range want {
		if d[k] != v {
			t.Errorf("%s: got %v, want %v", k, d[k], v)
		}
	}
}

func TestRepeat(t *testing.T) {
	d, _ := DecodeFields([]model.Field{{Name: "t", Offset: 0, Type: "uint8", Repeat: 3, Stride: 2}}, mustHex(t, "0100020003"))
	if d["t_1"] != 1.0 || d["t_2"] != 2.0 || d["t_3"] != 3.0 {
		t.Fatalf("%v", d)
	}
}

func TestWrongLengthDoesNotPanic(t *testing.T) {
	d, w := DecodeFields([]model.Field{{Name: "a", Type: "uint32_be", Length: 2}, {Name: "b", Type: "float32_be", Length: 1}}, mustHex(t, "0102"))
	if len(d) != 0 || len(w) != 2 {
		t.Fatalf("%v %v", d, w)
	}
}

func TestChecksums(t *testing.T) {
	data := []byte("123456789")
	want := map[string]uint64{"crc8": 0xF4, "crc8_maxim": 0xA1, "crc16_ccitt": 0x29B1, "crc16_modbus": 0x4B37, "crc32": 0xCBF43926}
	for algo, v := range want {
		if got := checksumFuncs[algo].fn(data); got != v {
			t.Errorf("%s: %X", algo, got)
		}
	}
	pkt := append(append([]byte{}, data...), 0x37, 0x4B)
	fields := []model.Field{{Name: "t", Offset: 0, Type: "uint8"}, {Name: "ok", Offset: 9, Type: "checksum", Algo: "crc16_modbus_le", Required: true}}
	if d, w := DecodeFields(fields, pkt); d == nil || d["ok"] != true {
		t.Fatalf("valid checksum: %v %v", d, w)
	}
	pkt[9] ^= 1
	if d, _ := DecodeFields(fields, pkt); d != nil {
		t.Fatalf("a required checksum failure must return no values: %v", d)
	}
}

func run(t *testing.T, kind string, fields []model.Field, o *model.DecodeOptions, data []byte) map[string]any {
	t.Helper()
	d, w, e := decodeMethod(kind, fields, o, data)
	if len(e) > 0 {
		t.Fatalf("%s: %v %v", kind, e, w)
	}
	return d
}

func TestTextMethods(t *testing.T) {
	d := run(t, "csv", nil, &model.DecodeOptions{Columns: []string{"t", "", "h"}}, []byte("21.5,x,40\r\n"))
	if d["t"] != 21.5 || d["h"] != 40.0 || len(d) != 2 {
		t.Fatalf("csv %v", d)
	}
	d = run(t, "kv", nil, nil, []byte("T=21.5;ok=true;name=a b"))
	if d["T"] != 21.5 || d["ok"] != true || d["name"] != "a b" {
		t.Fatalf("kv %v", d)
	}
	d = run(t, "kv", nil, &model.DecodeOptions{Separator: ",", KVSeparator: ":"}, []byte("a:1,b:2"))
	if d["a"] != 1.0 || d["b"] != 2.0 {
		t.Fatalf("kv custom %v", d)
	}
	d = run(t, "regex", nil, &model.DecodeOptions{Pattern: `T(?P<temp>[\d.]+)H(\d+)`}, []byte("xT19.5H40"))
	if d["temp"] != 19.5 || d["group_2"] != 40.0 {
		t.Fatalf("regex %v", d)
	}
	d = run(t, "regex", nil, &model.DecodeOptions{Pattern: `^AA(?P<x>..)`, PatternOn: "hex"}, mustHex(t, "AA12"))
	if d["x"] != 12.0 {
		t.Fatalf("regex hex %v", d)
	}
	d = run(t, "json", nil, nil, []byte(`{"a":1,"b":{"c":"x"}}`))
	if d["a"] != 1.0 {
		t.Fatalf("json %v", d)
	}
	if _, _, e := decodeMethod("json", nil, nil, []byte("{")); len(e) == 0 {
		t.Fatal("bad JSON must report an error")
	}
}

func TestTLV(t *testing.T) {
	o := &model.DecodeOptions{TagSize: 1, LenSize: 1, Tags: []model.Field{{Tag: "01", Name: "temp", Type: "int16_be", Scale: 0.1}, {Tag: "02", Name: "state", Type: "uint8", Map: map[string]string{"1": "on"}}}}
	d := run(t, "tlv_custom", nil, o, mustHex(t, "0102FFF6"+"020101"+"0901AB"+"0102000A"))
	if d["temp"] != -1.0 || d["state"] != "on" || d["tag_09"] != "AB" || d["temp_2"] != 1.0 {
		t.Fatalf("tlv %v", d)
	}
	lf := &model.DecodeOptions{TagSize: 2, LenSize: 2, LengthFirst: true, LittleEndian: true, Tags: []model.Field{{Tag: "ABCD", Name: "x", Type: "uint8"}}}
	if d := run(t, "tlv_custom", nil, lf, mustHex(t, "0100ABCD07")); d["x"] != 7.0 {
		t.Fatalf("ltv %v", d)
	}
	fixed := &model.DecodeOptions{TagSize: 1, Tags: []model.Field{{Tag: "10", Name: "a", Type: "uint16_be"}, {Tag: "11", Name: "b", Type: "uint8"}}}
	if d := run(t, "tlv_custom", nil, fixed, mustHex(t, "100102"+"1105")); d["a"] != 258.0 || d["b"] != 5.0 {
		t.Fatalf("fixed %v", d)
	}
	if _, w, _ := decodeMethod("tlv_custom", nil, o, mustHex(t, "01FF00")); len(w) == 0 {
		t.Fatal("an over-long record must warn")
	}
}

func TestBitstream(t *testing.T) {
	// 1010 | 000011111111 | 1
	d := run(t, "bitstream", []model.Field{{Name: "type", BitLen: 4}, {Name: "value", BitLen: 12, Scale: 0.5}, {Name: "flag", BitLen: 1, Type: "bool"}}, nil, mustHex(t, "A0FF80"))
	if d["type"] != 10.0 || d["value"] != 127.5 || d["flag"] != true {
		t.Fatalf("%v", d)
	}
	d = run(t, "bitstream", []model.Field{{BitLen: 4}, {Name: "s", BitLen: 4, Type: "int"}}, nil, mustHex(t, "0F"))
	if d["s"] != -1.0 || len(d) != 1 {
		t.Fatalf("%v", d)
	}
}

func TestCBOR(t *testing.T) {
	// {"t": 21.5, "n": -2, "ok": true, "b": h'0A', "l": [1, 2]}
	d := run(t, "cbor", nil, nil, mustHex(t, "A56174FB4035800000000000616E21626F6BF56162410A616C820102"))
	if d["t"] != 21.5 || d["n"] != -2.0 || d["ok"] != true || d["b"] != "0A" {
		t.Fatalf("%v", d)
	}
	if l := d["l"].([]any); len(l) != 2 || l[1] != 2.0 {
		t.Fatalf("%v", d)
	}
	if v := float16(0x3E00); v != 1.5 { // RFC 8949 appendix A
		t.Fatalf("float16 %v", v)
	}
	if d := run(t, "cbor", nil, nil, mustHex(t, "BF616101FF")); d["a"] != 1.0 {
		t.Fatalf("indefinite map %v", d)
	}
}

func TestMsgpack(t *testing.T) {
	// {"t": 1.5, "n": -5, "s": "hi", "u": 300}
	d := run(t, "msgpack", nil, nil, mustHex(t, "84A174CB3FF8000000000000A16EFBA173A26869A175CD012C"))
	if d["t"] != 1.5 || d["n"] != -5.0 || d["s"] != "hi" || d["u"] != 300.0 {
		t.Fatalf("%v", d)
	}
}

func TestCorruptBinaryIsSafe(t *testing.T) {
	for _, h := range []string{"9BFFFFFFFFFFFFFFFF", "BBFFFFFFFFFFFFFFFF", "5BFFFFFFFFFFFFFFFF", "DDFFFFFFFF", "DFFFFFFFFF", "C6FFFFFFFF", "81"} {
		for _, kind := range []string{"cbor", "msgpack"} {
			decodeMethod(kind, nil, nil, mustHex(t, h)) // must return, not panic or allocate
		}
	}
	deep := make([]byte, 5000)
	for i := range deep {
		deep[i] = 0x81
	}
	if _, _, e := decodeMethod("cbor", nil, nil, deep); len(e) == 0 {
		t.Fatal("deep nesting must be refused")
	}
}

func TestPreprocessing(t *testing.T) {
	s := Scheme{ID: "a", MatchType: "default", Type: "fields", DataOffset: 2, Fields: []model.Field{{Name: "v", Offset: 2, Type: "uint8"}},
		Options: &model.DecodeOptions{Pre: "xor", PreKey: "FF"}}
	d, _, e := s.Run(mustHex(t, "AABBF0"), time.Now(), nil)
	if len(e) > 0 || d["v"] != 15.0 {
		t.Fatalf("xor %v %v", d, e)
	}
	s = Scheme{ID: "b", MatchType: "default", Type: "cbor", Options: &model.DecodeOptions{Pre: "hex_text"}}
	if d, _, e := s.Run([]byte("A1616101"), time.Now(), nil); len(e) > 0 || d["a"] != 1.0 {
		t.Fatalf("hex text %v %v", d, e)
	}
	s = Scheme{ID: "c", MatchType: "default", Type: "kv", Options: &model.DecodeOptions{Pre: "base64_text"}}
	if d, _, _ := s.Run([]byte("YT0x"), time.Now(), nil); d["a"] != 1.0 {
		t.Fatalf("base64 %v", d)
	}
	r := Decode(model.Formatter{Type: "fields", Fields: []model.Field{{Name: "v", Type: "uint8"}}, Options: &model.DecodeOptions{Pre: "reverse"}}, Input{Bytes: mustHex(t, "0102")})
	if r.Data["v"] != 2.0 {
		t.Fatalf("reverse %v", r)
	}
	r = Decode(model.Formatter{Type: "kv"}, Input{Bytes: []byte("a=1")})
	if r.Data["a"] != 1.0 {
		t.Fatalf("formatter method %v", r)
	}
}

func TestValidateRejectsBadDefinitions(t *testing.T) {
	bad := []Scheme{
		{ID: "a", MatchType: "default", Type: "regex"},
		{ID: "b", MatchType: "default", Type: "regex", Options: &model.DecodeOptions{Pattern: "("}},
		{ID: "c", MatchType: "default", Type: "fields", Fields: []model.Field{{Name: "x", Expr: "1 +"}}},
		{ID: "d", MatchType: "default", Type: "fields", Fields: []model.Field{{Name: "x", Type: "checksum", Algo: "md5"}}},
		{ID: "e", MatchType: "default", Type: "kv", Options: &model.DecodeOptions{Pre: "xor", PreKey: "zz"}},
		{ID: "f", MatchType: "default", Type: "fields", Fields: []model.Field{{Name: "x", Repeat: 9999}}},
		{ID: "g", MatchType: "default", Type: "tlv_custom", Options: &model.DecodeOptions{TagSize: 9}},
	}
	for _, s := range bad {
		doc := SchemeDoc{Schemes: []Scheme{s}}
		if err := doc.Validate(); err == nil {
			t.Errorf("scheme %s accepted", s.ID)
		}
	}
	ok := SchemeDoc{Schemes: []Scheme{{ID: "a", MatchType: "default", Type: "cbor"}, {ID: "b", MatchType: "default", Type: "bitstream", Fields: []model.Field{{Name: "x", BitLen: 3}}}}}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
}
