package formatter

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dop251/goja"
	"github.com/nor24o/lorahub/internal/model"
)

// A SchemeDoc is an ordered list of decoder schemes. The first enabled scheme
// that matches a packet and yields data wins. The JSON layout is compatible
// with /etc/lora/decoders.json so existing decoder files import unchanged.
type SchemeDoc struct {
	Version int      `json:"version"`
	Updated string   `json:"updated,omitempty"`
	Schemes []Scheme `json:"schemes"`
}

// FlexString accepts a JSON string or number.
type FlexString string

// UnmarshalJSON implements json.Unmarshaler.
func (f *FlexString) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		*f = FlexString(s)
		return nil
	}
	var n json.Number
	if err := json.Unmarshal(b, &n); err != nil {
		return err
	}
	*f = FlexString(n.String())
	return nil
}

// Scheme describes how to recognise and decode one kind of packet.
//
// MatchType: prefix | size | regex | condition | default.
// Type: ctv | cayenne_lpp | fields | javascript | ascii.
// Field offsets refer to the whole packet; ctv, cayenne and javascript start
// at DataOffset.
type Scheme struct {
	ID             string        `json:"id"`
	Name           string        `json:"name"`
	Description    string        `json:"description,omitempty"`
	Enabled        *bool         `json:"enabled,omitempty"`
	MatchType      string        `json:"matchType"`
	MatchValue     FlexString    `json:"matchValue,omitempty"`
	MatchCondition string        `json:"matchCondition,omitempty"`
	Type           string        `json:"type"`
	DataOffset     int           `json:"dataOffset"`
	Serial         string        `json:"serial,omitempty"` // "" auto | header | none
	Fields         []model.Field `json:"fields,omitempty"`
	Code           string        `json:"code,omitempty"`   // javascript: decodeUplink(input)
	JSCode         string        `json:"jsCode,omitempty"` // js: function body using bytes and hex, returns an object
	SampleHex      string        `json:"sampleHex,omitempty"`
}

// IsEnabled reports whether the scheme takes part in matching.
func (s *Scheme) IsEnabled() bool { return s.Enabled == nil || *s.Enabled }

// SchemeTrace records what happened with one scheme during a decode.
type SchemeTrace struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Matched bool   `json:"matched"`
	Decoded bool   `json:"decoded"`
}

// SchemeResult is the outcome of decoding a packet through a SchemeDoc.
type SchemeResult struct {
	SchemeID   string         `json:"scheme_id,omitempty"`
	SchemeName string         `json:"scheme_name,omitempty"`
	Data       map[string]any `json:"data,omitempty"`
	Warnings   []string       `json:"warnings,omitempty"`
	Errors     []string       `json:"errors,omitempty"`
	Scheme     *Scheme        `json:"-"`
	Trace      []SchemeTrace  `json:"trace,omitempty"`
}

// DefaultSchemes returns the built-in generic decoders.
func DefaultSchemes() *SchemeDoc {
	return &SchemeDoc{Version: 1, Schemes: []Scheme{
		{ID: "scheme_tlv", Name: "Channel / type / value sensors", Description: "Channel, type and value triplets after the header",
			MatchType: "condition", MatchCondition: "return bytes.length >= 4;", Type: "ctv", DataOffset: 6},
		{ID: "scheme_ascii", Name: "ASCII text", Description: "Printable text such as test pings",
			MatchType: "condition", MatchCondition: "return bytes.length >= 2 && bytes.every(function(b){ return (b >= 32 && b <= 126) || b === 10 || b === 13; });", Type: "ascii"},
	}}
}

// Validate normalises a document and rejects schemes that can never work.
func (d *SchemeDoc) Validate() error {
	seen := map[string]bool{}
	for i := range d.Schemes {
		s := &d.Schemes[i]
		if s.ID == "" {
			s.ID = fmt.Sprintf("scheme_%d_%d", time.Now().UnixMilli(), i)
		}
		if seen[s.ID] {
			return fmt.Errorf("duplicate scheme id %q", s.ID)
		}
		seen[s.ID] = true
		if s.DataOffset < 0 {
			return fmt.Errorf("scheme %q: data offset cannot be negative", s.Name)
		}
		switch s.MatchType {
		case "prefix":
			if _, err := hex.DecodeString(cleanMatchHex(string(s.MatchValue))); err != nil || s.MatchValue == "" {
				return fmt.Errorf("scheme %q: prefix must be hex", s.Name)
			}
		case "size":
			if _, err := strconv.Atoi(strings.TrimSpace(string(s.MatchValue))); err != nil {
				return fmt.Errorf("scheme %q: size must be a number", s.Name)
			}
		case "regex":
			if _, err := regexp.Compile(string(s.MatchValue)); err != nil {
				return fmt.Errorf("scheme %q: bad regular expression: %v", s.Name, err)
			}
		case "condition", "default":
		default:
			return fmt.Errorf("scheme %q: unknown match type %q", s.Name, s.MatchType)
		}
		switch s.Type {
		case "ctv", "tlv", "cayenne_lpp", "fields", "javascript", "js", "ascii":
		default:
			return fmt.Errorf("scheme %q: unknown decoder type %q", s.Name, s.Type)
		}
	}
	return nil
}

func cleanMatchHex(s string) string {
	return strings.ToLower(strings.NewReplacer(" ", "", ":", "", "-", "").Replace(s))
}

var schemeRe sync.Map // pattern -> *regexp.Regexp

// Matches reports whether the scheme applies to a packet.
func (s *Scheme) Matches(b []byte) bool {
	h := hex.EncodeToString(b)
	switch s.MatchType {
	case "prefix":
		v := cleanMatchHex(string(s.MatchValue))
		return v != "" && strings.HasPrefix(h, v)
	case "size":
		n, err := strconv.Atoi(strings.TrimSpace(string(s.MatchValue)))
		return err == nil && n == len(b)
	case "regex":
		pat := "(?i)" + string(s.MatchValue)
		var re *regexp.Regexp
		if v, ok := schemeRe.Load(pat); ok {
			re = v.(*regexp.Regexp)
		} else {
			var err error
			if re, err = regexp.Compile(pat); err != nil {
				return false
			}
			schemeRe.Store(pat, re)
		}
		return re.MatchString(h)
	case "condition":
		return evalCondition(s.MatchCondition, b, h)
	case "default":
		return true
	}
	return false
}

func evalCondition(code string, b []byte, h string) bool {
	if strings.TrimSpace(code) == "" {
		return false
	}
	vm, err := newVM("function __match(bytes, hex) {\n" + code + "\n}")
	if err != nil {
		return false
	}
	timer := time.AfterFunc(jsTimeout, func() { vm.Interrupt("condition timeout") })
	defer timer.Stop()
	fn, ok := goja.AssertFunction(vm.Get("__match"))
	if !ok {
		return false
	}
	out, err := fn(goja.Undefined(), bytesToJS(vm, b), vm.ToValue(h))
	return err == nil && out.ToBoolean()
}

func runBody(code string, b []byte) (map[string]any, []string, []string) {
	vm, err := newVM("function __decode(bytes, hex) {\n" + code + "\n}")
	if err != nil {
		return nil, nil, []string{err.Error()}
	}
	timer := time.AfterFunc(jsTimeout, func() { vm.Interrupt("decoder timeout") })
	defer timer.Stop()
	fn, ok := goja.AssertFunction(vm.Get("__decode"))
	if !ok {
		return nil, nil, []string{"decoder did not compile to a function"}
	}
	out, err := fn(goja.Undefined(), bytesToJS(vm, b), vm.ToValue(hex.EncodeToString(b)))
	if err != nil {
		return nil, nil, []string{err.Error()}
	}
	data, _ := out.Export().(map[string]any)
	if data == nil {
		return nil, nil, []string{"decoder must return an object"}
	}
	return data, nil, nil
}

// Run decodes a packet with this scheme, ignoring its match rule.
func (s *Scheme) Run(b []byte, at time.Time, vars map[string]any) (map[string]any, []string, []string) {
	start := s.DataOffset
	if start > len(b) {
		start = len(b)
	}
	switch s.Type {
	case "ctv", "tlv", "cayenne_lpp":
		return decodeCTV(b, s.DataOffset), nil, nil
	case "fields":
		d, w := DecodeFields(s.Fields, b)
		return d, w, nil
	case "ascii":
		var sb strings.Builder
		for _, c := range b[start:] {
			if (c >= 32 && c <= 126) || c == 9 || c == 10 || c == 13 {
				sb.WriteByte(c)
			}
		}
		if sb.Len() == 0 {
			return nil, nil, nil
		}
		return map[string]any{"text": sb.String()}, nil, nil
	case "js":
		return runBody(s.JSCode, b)
	case "javascript":
		v := map[string]any{"payload": b}
		for k, x := range vars {
			v[k] = x
		}
		r := DecodeJS(s.Code, Input{Bytes: b[start:], FPort: 1, RecvTime: at, Vars: v})
		return r.Data, r.Warnings, r.Errors
	}
	return nil, nil, []string{"unknown decoder type " + s.Type}
}

// Decode runs the packet through the schemes in order. It returns nil when
// nothing matched and no trace was requested.
func (d *SchemeDoc) Decode(b []byte, at time.Time, vars map[string]any, trace bool) *SchemeResult {
	res := &SchemeResult{}
	for i := range d.Schemes {
		s := &d.Schemes[i]
		if !s.IsEnabled() {
			continue
		}
		matched := s.Matches(b)
		t := SchemeTrace{ID: s.ID, Name: s.Name, Matched: matched}
		if matched && res.Data == nil {
			data, w, e := s.Run(b, at, vars)
			if len(e) > 0 && len(res.Errors) == 0 {
				res.Errors = e
			}
			if len(data) > 0 {
				t.Decoded = true
				res.SchemeID, res.SchemeName, res.Scheme, res.Data, res.Warnings = s.ID, s.Name, s, data, w
				res.Errors = e
			}
		}
		if trace {
			res.Trace = append(res.Trace, t)
		} else if res.Data != nil {
			return res
		}
	}
	if res.Data == nil && !trace && len(res.Errors) == 0 {
		return nil
	}
	return res
}

func round6(v float64) float64 { return math.Round(v*1e6) / 1e6 }

func be16(b []byte) int { return int(b[0])<<8 | int(b[1]) }

func sbe16(b []byte) int {
	v := be16(b)
	if v >= 0x8000 {
		v -= 0x10000
	}
	return v
}

func sbe24(b []byte) int {
	v := int(b[0])<<16 | int(b[1])<<8 | int(b[2])
	if v >= 0x800000 {
		v -= 0x1000000
	}
	return v
}

// parseChannels reads channel, type, value triplets (IPSO / Cayenne codes)
// from start. Parsing stops at the first byte sequence it cannot interpret.
func parseChannels(b []byte, start int) (map[string]any, int) {
	out := map[string]any{}
	count := 0
	i := start
	for i >= 0 && i+1 < len(b) {
		ch := int(b[i])
		if ch < 1 || ch > 32 {
			break
		}
		typ := b[i+1]
		i += 2
		have := func(n int) bool { return i+n <= len(b) }
		switch typ {
		case 0x11: // temperature, 0.1 or 0.01 degrees depending on magnitude
			if !have(2) {
				return out, count
			}
			raw := sbe16(b[i:])
			i += 2
			t := float64(raw) / 10
			if raw > 500 || raw < -500 {
				t = float64(raw) / 100
			}
			out["temperature"], out[fmt.Sprintf("ch%d_temperature", ch)] = round6(t), round6(t)
		case 0x05: // battery in mV
			if !have(2) {
				return out, count
			}
			mv := be16(b[i:])
			i += 2
			out["battery"], out["battery_mv"] = round6(float64(mv)/1000), mv
		case 0x00: // digital input
			if !have(1) {
				return out, count
			}
			out[fmt.Sprintf("ch%d_digital_in", ch)] = int(b[i])
			i++
		case 0x01, 0x10: // digital output / switch
			if !have(1) {
				return out, count
			}
			st := "OFF"
			if b[i] == 1 {
				st = "ON"
			}
			i++
			out["state"], out[fmt.Sprintf("ch%d_state", ch)] = st, st
		case 0x02, 0x03, 0x12: // analog, 0.01 signed
			if !have(2) {
				return out, count
			}
			v := round6(float64(sbe16(b[i:])) / 100)
			i += 2
			out[fmt.Sprintf("ch%d_analog_in", ch)] = v
			if _, has := out["battery"]; ch == 1 && v >= 1.8 && v <= 5.5 && !has {
				out["battery"] = v
			}
		case 0x65: // illuminance
			if !have(2) {
				return out, count
			}
			out["illuminance"] = be16(b[i:])
			i += 2
		case 0x66: // presence
			if !have(1) {
				return out, count
			}
			out["presence"] = int(b[i])
			i++
		case 0x67: // temperature, 0.1 degrees
			if !have(2) {
				return out, count
			}
			t := round6(float64(sbe16(b[i:])) / 10)
			i += 2
			out["temperature"], out[fmt.Sprintf("ch%d_temperature", ch)] = t, t
		case 0x68: // humidity, 0.5 %
			if !have(1) {
				return out, count
			}
			v := float64(b[i]) * 0.5
			i++
			out["humidity"], out[fmt.Sprintf("ch%d_humidity", ch)] = v, v
		case 0x71: // accelerometer
			if !have(6) {
				return out, count
			}
			out["accel_x"] = round6(float64(sbe16(b[i:])) * 0.001)
			out["accel_y"] = round6(float64(sbe16(b[i+2:])) * 0.001)
			out["accel_z"] = round6(float64(sbe16(b[i+4:])) * 0.001)
			i += 6
		case 0x73: // barometer
			if !have(2) {
				return out, count
			}
			out["pressure"] = round6(float64(be16(b[i:])) * 0.1)
			i += 2
		case 0x88: // GPS
			if !have(9) {
				return out, count
			}
			out["latitude"] = round6(float64(sbe24(b[i:])) * 0.0001)
			out["longitude"] = round6(float64(sbe24(b[i+3:])) * 0.0001)
			out["altitude"] = round6(float64(sbe24(b[i+6:])) * 0.01)
			i += 9
		default:
			return out, count
		}
		count++
	}
	return out, count
}

// decodeCTV decodes channel data at offset, probing the usual header sizes
// when that offset holds nothing readable. It returns nil when no channel
// could be read, so the next scheme gets a chance.
func decodeCTV(b []byte, offset int) map[string]any {
	out, n := parseChannels(b, offset)
	eff := offset
	if n == 0 {
		for _, c := range []int{6, 4, 2, 0} {
			if o, k := parseChannels(b, c); k > n {
				out, n, eff = o, k, c
			}
		}
	}
	if n == 0 {
		return nil
	}
	if eff == 4 && len(b) >= 4 { // NODE_ID | device type | sequence header
		t := b[2]
		out["device_type"] = fmt.Sprintf("0x%02X", t)
		out["seq"] = int(b[3])
		switch t >> 4 {
		case 1:
			out["node_class"] = "A"
		case 2:
			out["node_class"] = "B"
		case 3:
			out["node_class"] = "C"
		}
		caps := map[byte]string{1: "Sensor", 2: "Actuator", 3: "Combo", 4: "Meter", 5: "Security", 6: "Dimmer"}
		if c, ok := caps[t&0x0F]; ok {
			out["capability"] = c
		} else {
			out["capability"] = "Generic"
		}
	}
	return out
}
