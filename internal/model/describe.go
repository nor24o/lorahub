package model

import (
	"encoding/hex"
	"fmt"
	"math"
	"strings"
	"time"
)

// Self-description: a node tells LoRaHub which downlink commands it accepts,
// and LoRaHub turns them into controls (in Home Assistant too).
//
// A description is an uplink whose profile byte has bit 3 set. After the
// profile byte (and its radio block, when bit 4 is set) come:
//
//	D1        format 1
//	PN        bits 7-4 part number 1-15, bits 3-0 number of parts 1-15
//	entries   up to the end of the packet; an entry never spans two parts
//
// Each entry starts with the command's code, the first data byte of its
// downlink (FF is reserved: it asks a node to describe itself), and a kind
// byte (bits 7-4 type, bits 3-0 value format). Then, by type:
//
//	0 button  [value]                 a fixed value, sent when a format is given
//	1 switch                          off sends 0, on sends 1 (u8 without a format)
//	2 number  min max scale           scale is a signed power of ten: shown = raw × 10^scale
//	3 choice  n, n × (value, name)    the options
//	4 raw     max length (0 = any)    the value is typed in hex
//
// and every entry ends with its name (a length byte and 1-24 ASCII
// characters); a number also has its unit after the name (length byte and 0-8
// characters). Value formats: 0 none, 1 u8, 2 i8, 3 u16, 4 i16, 5 u32, 6 i32,
// all big-endian.
//
// The downlink for a command is its code followed by the value:
// NODE_ID | SERIAL_ID | [control] | code | value.
const (
	DescribeFlag    = 0x08 // profile byte bit 3
	DescribeFormat  = 0xD1
	DescribeRequest = 0xFF // downlink data byte asking a node to describe itself
)

var (
	describeKinds     = []string{"button", "switch", "number", "choice", "raw"}
	describeEncodings = []string{"", "u8", "i8", "u16be", "i16be", "u32be", "i32be"}
	describeFormats   = []string{"", "u8", "i8", "u16", "i16", "u32", "i32"}
	describeSizes     = []int{0, 1, 1, 2, 2, 4, 4}
)

// DescribedOption is one option of a choice.
type DescribedOption struct {
	Value int64  `json:"value"`
	Name  string `json:"name"`
}

// DescribedCommand is one command a node announced.
type DescribedCommand struct {
	Code    int               `json:"code"`
	Kind    string            `json:"kind"`             // button, switch, number, choice, raw
	Format  string            `json:"format,omitempty"` // u8, i8, u16, i16, u32, i32 (big-endian)
	Value   *int64            `json:"value,omitempty"`  // a button's fixed value
	Min     int64             `json:"min,omitempty"`    // number, raw units
	Max     int64             `json:"max,omitempty"`
	Scale   int               `json:"scale,omitempty"` // shown = raw × 10^scale
	Unit    string            `json:"unit,omitempty"`
	MaxLen  int               `json:"max_len,omitempty"` // raw: bytes, 0 = any
	Options []DescribedOption `json:"options,omitempty"`
	Name    string            `json:"name"`
}

// NodeDescription is the last complete description a node sent.
type NodeDescription struct {
	Commands []DescribedCommand `json:"commands"`
	Parts    int                `json:"parts"`
	Received time.Time          `json:"received"`
	Hex      []string           `json:"hex"` // each part as received, from D1 on
}

// DescriptionPart is one packet of a description.
type DescriptionPart struct {
	Part, Parts int
	Commands    []DescribedCommand
}

type descReader struct {
	b   []byte
	pos int
}

func (r *descReader) left() int { return len(r.b) - r.pos }

func (r *descReader) byte(what string) (byte, error) {
	if r.left() < 1 {
		return 0, fmt.Errorf("the description ends before %s", what)
	}
	r.pos++
	return r.b[r.pos-1], nil
}

func (r *descReader) value(format int, what string) (int64, error) {
	n := describeSizes[format]
	if r.left() < n {
		return 0, fmt.Errorf("the description ends inside %s", what)
	}
	var u uint64
	for _, c := range r.b[r.pos : r.pos+n] {
		u = u<<8 | uint64(c)
	}
	r.pos += n
	if format == 2 || format == 4 || format == 6 { // signed
		shift := 64 - 8*uint(n)
		return int64(u<<shift) >> shift, nil
	}
	return int64(u), nil
}

func (r *descReader) text(minLen, maxLen int, what string) (string, error) {
	n, err := r.byte(what)
	if err != nil {
		return "", err
	}
	if int(n) < minLen || int(n) > maxLen {
		return "", fmt.Errorf("%s has %d characters; %d to %d are allowed", what, n, minLen, maxLen)
	}
	if r.left() < int(n) {
		return "", fmt.Errorf("the description ends inside %s", what)
	}
	s := r.b[r.pos : r.pos+int(n)]
	r.pos += int(n)
	for _, c := range s {
		if c < 0x20 || c > 0x7E {
			return "", fmt.Errorf("%s is not plain text", what)
		}
	}
	return string(s), nil
}

// ParseDescription reads one description packet, starting at the D1 byte.
func ParseDescription(b []byte) (*DescriptionPart, error) {
	if len(b) < 3 || b[0] != DescribeFormat {
		return nil, fmt.Errorf("not a description (it must start with D1 and hold at least one command)")
	}
	part, parts := int(b[1]>>4), int(b[1]&0x0F)
	if part < 1 || parts < 1 || part > parts {
		return nil, fmt.Errorf("part byte %02X: part %d of %d is not valid", b[1], part, parts)
	}
	out := &DescriptionPart{Part: part, Parts: parts}
	r := &descReader{b: b, pos: 2}
	codes := map[int]bool{}
	for r.left() > 0 {
		c, err := readEntry(r)
		if err != nil {
			return nil, fmt.Errorf("command %d: %w", len(out.Commands)+1, err)
		}
		if codes[c.Code] {
			return nil, fmt.Errorf("code %02X is used twice", c.Code)
		}
		codes[c.Code] = true
		out.Commands = append(out.Commands, c)
	}
	return out, nil
}

func readEntry(r *descReader) (DescribedCommand, error) {
	var c DescribedCommand
	code, err := r.byte("a command code")
	if err != nil {
		return c, err
	}
	if code == DescribeRequest {
		return c, fmt.Errorf("code FF is reserved for the describe request")
	}
	kb, err := r.byte("the kind byte")
	if err != nil {
		return c, err
	}
	typ, format := int(kb>>4), int(kb&0x0F)
	if typ >= len(describeKinds) {
		return c, fmt.Errorf("kind byte %02X: unknown type %d", kb, typ)
	}
	if format >= len(describeFormats) {
		return c, fmt.Errorf("kind byte %02X: unknown value format %d", kb, format)
	}
	c.Code, c.Kind, c.Format = int(code), describeKinds[typ], describeFormats[format]
	switch c.Kind {
	case "button":
		if format != 0 {
			v, err := r.value(format, "the button's value")
			if err != nil {
				return c, err
			}
			c.Value = &v
		}
	case "switch":
		if format == 0 {
			c.Format = "u8"
		}
	case "number":
		if format == 0 {
			return c, fmt.Errorf("a number needs a value format")
		}
		if c.Min, err = r.value(format, "the minimum"); err != nil {
			return c, err
		}
		if c.Max, err = r.value(format, "the maximum"); err != nil {
			return c, err
		}
		if c.Min >= c.Max {
			return c, fmt.Errorf("the minimum %d is not below the maximum %d", c.Min, c.Max)
		}
		s, err := r.byte("the scale")
		if err != nil {
			return c, err
		}
		if c.Scale = int(int8(s)); c.Scale < -6 || c.Scale > 6 {
			return c, fmt.Errorf("scale 10^%d is out of range (-6 to 6)", c.Scale)
		}
	case "choice":
		if format == 0 {
			return c, fmt.Errorf("a choice needs a value format")
		}
		n, err := r.byte("the number of options")
		if err != nil {
			return c, err
		}
		if n < 1 || n > 32 {
			return c, fmt.Errorf("a choice has 1 to 32 options, not %d", n)
		}
		names := map[string]bool{}
		for i := 0; i < int(n); i++ {
			v, err := r.value(format, "an option's value")
			if err != nil {
				return c, err
			}
			name, err := r.text(1, 16, "an option's name")
			if err != nil {
				return c, err
			}
			if names[strings.ToLower(name)] {
				return c, fmt.Errorf("option %q appears twice", name)
			}
			names[strings.ToLower(name)] = true
			c.Options = append(c.Options, DescribedOption{Value: v, Name: name})
		}
	case "raw":
		c.Format = ""
		n, err := r.byte("the maximum length")
		if err != nil {
			return c, err
		}
		c.MaxLen = int(n)
	}
	if c.Name, err = r.text(1, 24, "the name"); err != nil {
		return c, err
	}
	if c.Kind == "number" {
		if c.Unit, err = r.text(0, 8, "the unit"); err != nil {
			return c, err
		}
	}
	return c, nil
}

func pow10(v int64, scale int) float64 {
	if scale < 0 {
		return float64(v) / math.Pow10(-scale) // divide, so 215 at 10^-1 is exactly 21.5
	}
	return float64(v) * math.Pow10(scale)
}

func encodingOf(format string) string {
	for i, f := range describeFormats {
		if f == format {
			return describeEncodings[i]
		}
	}
	return "u8"
}

// HACommand turns an announced command into a LoRaHub command.
func (c DescribedCommand) HACommand() HACommand {
	code := fmt.Sprintf("%02X", c.Code)
	enc := encodingOf(c.Format)
	cmd := HACommand{Name: c.Name, Source: "node", PayloadHex: code + HAValueToken, Encoding: enc}
	switch c.Kind {
	case "button":
		cmd.Kind, cmd.PayloadHex, cmd.Encoding = "button", code, ""
		if c.Value != nil {
			if b, err := encodeHANumber(float64(*c.Value), enc); err == nil {
				cmd.PayloadHex += strings.ToUpper(hex.EncodeToString(b))
			}
		}
	case "switch":
		cmd.Kind = "switch"
	case "number":
		cmd.Kind, cmd.Unit = "number", c.Unit
		cmd.Min, cmd.Max = pow10(c.Min, c.Scale), pow10(c.Max, c.Scale)
		cmd.Step, cmd.Scale = pow10(1, c.Scale), 1/pow10(1, c.Scale)
	case "choice":
		cmd.Kind = "choice"
		for _, o := range c.Options {
			cmd.Options = append(cmd.Options, HAOption{Name: o.Name, Value: float64(o.Value)})
		}
	case "raw":
		cmd.Kind, cmd.Encoding = "text", "hex"
	}
	return cmd
}

// Commands are a node's controls: the ones set in LoRaHub first, then those
// the node announced itself; on a name clash the one set in LoRaHub wins.
func (d *Device) Commands() []HACommand {
	out := append([]HACommand(nil), d.HACommands...)
	if d.Described == nil {
		return out
	}
	seen := map[string]bool{}
	for i := range out {
		seen[out[i].Key()] = true
	}
	for _, dc := range d.Described.Commands {
		c := dc.HACommand()
		if c.Normalize() != nil || seen[c.Key()] {
			continue
		}
		seen[c.Key()] = true
		out = append(out, c)
	}
	return out
}
