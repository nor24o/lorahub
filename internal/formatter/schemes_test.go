package formatter

import (
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"
)

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestSchemeNoSerialTLV(t *testing.T) {
	var d SchemeDoc
	raw := `{"schemes":[{"id":"a","name":"AA0B","enabled":true,"matchType":"prefix","matchValue":"aa0b","type":"ctv","dataOffset":4}]}`
	if err := json.Unmarshal([]byte(raw), &d); err != nil {
		t.Fatal(err)
	}
	r := d.Decode(mustHex(t, "aa0b64b2011107aa02050fa5"), time.Now(), nil, false)
	if r == nil || r.SchemeID != "a" {
		t.Fatalf("no match: %+v", r)
	}
	if r.Data["temperature"] != 19.62 || r.Data["battery"] != 4.005 || r.Data["seq"] != 0xb2 {
		t.Fatalf("decoded %+v", r.Data)
	}
}

func TestSchemeCayenneAfterSerial(t *testing.T) {
	d := SchemeDoc{Schemes: []Scheme{{ID: "c", MatchType: "prefix", MatchValue: "cafe", Type: "cayenne_lpp", DataOffset: 6}}}
	r := d.Decode(mustHex(t, "CAFE96B840EB0102014902710001001C0425036700B904880000000000000000"), time.Now(), nil, false)
	if r == nil || r.Data["temperature"] != 18.5 || r.Data["battery"] != 3.29 {
		t.Fatalf("decoded %+v", r)
	}
}

func TestSchemeOrderAndFallThrough(t *testing.T) {
	off := false
	d := SchemeDoc{Schemes: []Scheme{
		{ID: "disabled", Enabled: &off, MatchType: "default", Type: "ascii"},
		{ID: "unreadable", MatchType: "default", Type: "ctv", DataOffset: 0},
		{ID: "text", MatchType: "condition", MatchCondition: "return bytes.length > 1;", Type: "ascii"},
	}}
	r := d.Decode([]byte("\xff\xffPING"), time.Now(), nil, true)
	if r.SchemeID != "text" || r.Data["text"] != "PING" {
		t.Fatalf("got %+v", r)
	}
	if len(r.Trace) != 2 {
		t.Fatalf("trace %+v", r.Trace)
	}
}

func TestSchemeMatchTypes(t *testing.T) {
	b := mustHex(t, "AABBCC")
	cases := []struct {
		s    Scheme
		want bool
	}{
		{Scheme{MatchType: "prefix", MatchValue: "aa bb"}, true},
		{Scheme{MatchType: "prefix", MatchValue: "bb"}, false},
		{Scheme{MatchType: "size", MatchValue: "3"}, true},
		{Scheme{MatchType: "size", MatchValue: "4"}, false},
		{Scheme{MatchType: "regex", MatchValue: "BBCC$"}, true},
		{Scheme{MatchType: "condition", MatchCondition: "return bytes[2] === 0xCC && hex === 'aabbcc';"}, true},
		{Scheme{MatchType: "condition", MatchCondition: "return #bytes >= 4"}, false},
		{Scheme{MatchType: "default"}, true},
	}
	for _, c := range cases {
		if got := c.s.Matches(b); got != c.want {
			t.Errorf("%s %q: got %v", c.s.MatchType, c.s.MatchValue, got)
		}
	}
}

func TestSchemeJSBody(t *testing.T) {
	d := SchemeDoc{Schemes: []Scheme{{ID: "j", MatchType: "prefix", MatchValue: "aa0c", Type: "js",
		JSCode: "return { seq: (bytes[2] << 8) | bytes[3], head: hex.substring(0, 4) };"}}}
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	r := d.Decode(mustHex(t, "AA0C0102"), time.Now(), nil, false)
	if r == nil || r.Data["head"] != "aa0c" || r.Data["seq"] != int64(258) {
		t.Fatalf("decoded %+v", r)
	}
}

func TestSchemeValidate(t *testing.T) {
	bad := SchemeDoc{Schemes: []Scheme{{Name: "x", MatchType: "regex", MatchValue: "(", Type: "ascii"}}}
	if bad.Validate() == nil {
		t.Fatal("bad regex accepted")
	}
	if err := DefaultSchemes().Validate(); err != nil {
		t.Fatal(err)
	}
}
