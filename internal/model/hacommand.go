package model

import (
	"encoding/hex"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// HAValueToken marks where a dynamic command's value goes in its payload.
const HAValueToken = "{value}"

// HACommand is a control LoRaHub announces to Home Assistant for one node.
type HACommand struct {
	Name string `json:"name"`
	// Kind: button (default, fixed payload), number, text, switch (sends 1 for on
	// and 0 for off) or choice (sends the chosen option's value).
	Kind string `json:"kind,omitempty"`
	// PayloadHex is the payload in hex; the other kinds than button contain {value}.
	PayloadHex string `json:"payload_hex"`
	// Source is "node" for a command the node announced itself (model.NodeDescription).
	Source string `json:"source,omitempty"`

	// choice
	Options []HAOption `json:"options,omitempty"`

	// number
	Min   float64 `json:"min,omitempty"`
	Max   float64 `json:"max,omitempty"`
	Step  float64 `json:"step,omitempty"`
	Unit  string  `json:"unit,omitempty"`
	Scale float64 `json:"scale,omitempty"` // multiplies the value before encoding

	// Encoding is how the value becomes bytes: u8, i16le and so on for a
	// number, hex or ascii for text.
	Encoding string `json:"encoding,omitempty"`
}

// HAOption is one option of a choice.
type HAOption struct {
	Name  string  `json:"name"`
	Value float64 `json:"value"`
}

type haEncoding struct {
	size           int
	signed, little bool
}

var haEncodings = map[string]haEncoding{
	"u8": {1, false, false}, "i8": {1, true, false},
	"u16be": {2, false, false}, "u16le": {2, false, true}, "i16be": {2, true, false}, "i16le": {2, true, true},
	"u32be": {4, false, false}, "u32le": {4, false, true}, "i32be": {4, true, false}, "i32le": {4, true, true},
}

// Dynamic reports whether the payload depends on a value from Home Assistant.
func (c *HACommand) Dynamic() bool {
	return c.Kind == "number" || c.Kind == "text" || c.Kind == "switch" || c.Kind == "choice"
}

// Component is the Home Assistant entity type of the command.
func (c *HACommand) Component() string {
	if c.Kind == "choice" {
		return "select"
	}
	if c.Kind == "" {
		return "button"
	}
	return c.Kind
}

// Key identifies the command in entity IDs and MQTT topics.
func (c *HACommand) Key() string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' {
			return r
		}
		return '_'
	}, "cmd_"+c.Name)
}

func (c *HACommand) template() (left, right []byte, err error) {
	l, r, found := strings.Cut(c.PayloadHex, HAValueToken)
	if !found || strings.Contains(r, HAValueToken) {
		return nil, nil, fmt.Errorf("command %q: the payload needs {value} exactly once", c.Name)
	}
	if left, err = hex.DecodeString(l); err == nil {
		right, err = hex.DecodeString(r)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("command %q: the payload around {value} must be hex", c.Name)
	}
	return left, right, nil
}

// Normalize cleans a command and reports what is wrong with it.
func (c *HACommand) Normalize() error {
	c.Name = strings.TrimSpace(c.Name)
	if c.Name == "" {
		return fmt.Errorf("each command needs a name")
	}
	if c.Kind == "" {
		c.Kind = "button"
	}
	c.PayloadHex = strings.NewReplacer(" ", "", ":", "", "-", "").Replace(strings.TrimSpace(c.PayloadHex))
	c.PayloadHex = strings.ReplaceAll(strings.ToUpper(c.PayloadHex), strings.ToUpper(HAValueToken), HAValueToken)
	switch c.Kind {
	case "button":
		if b, err := hex.DecodeString(c.PayloadHex); err != nil || len(b) == 0 {
			return fmt.Errorf("command %q needs a hex payload", c.Name)
		}
		return nil
	case "number", "text", "switch", "choice":
		if c.PayloadHex == "" {
			c.PayloadHex = HAValueToken
		}
		if _, _, err := c.template(); err != nil {
			return err
		}
	default:
		return fmt.Errorf("command %q: unknown type %q", c.Name, c.Kind)
	}
	if c.Kind == "switch" || c.Kind == "choice" {
		if c.Encoding == "" {
			c.Encoding = "u8"
		}
		values := []float64{0, 1}
		if c.Kind == "choice" {
			if len(c.Options) == 0 || len(c.Options) > 32 {
				return fmt.Errorf("command %q: a choice has 1 to 32 options", c.Name)
			}
			values = values[:0]
			names := map[string]bool{}
			for i := range c.Options {
				o := &c.Options[i]
				o.Name = strings.TrimSpace(o.Name)
				if o.Name == "" || names[strings.ToLower(o.Name)] {
					return fmt.Errorf("command %q: each option needs its own name", c.Name)
				}
				names[strings.ToLower(o.Name)] = true
				values = append(values, o.Value)
			}
		}
		for _, v := range values {
			if _, err := encodeHANumber(v, c.Encoding); err != nil {
				return fmt.Errorf("command %q: %w", c.Name, err)
			}
		}
		return nil
	}
	if c.Kind == "text" {
		if c.Encoding == "" {
			c.Encoding = "hex"
		}
		if c.Encoding != "hex" && c.Encoding != "ascii" {
			return fmt.Errorf("command %q: text values are hex or ascii", c.Name)
		}
		return nil
	}
	if c.Encoding == "" {
		c.Encoding = "u8"
	}
	if c.Step == 0 {
		c.Step = 1
	}
	if c.Scale == 0 {
		c.Scale = 1
	}
	if c.Min >= c.Max || c.Step < 0 || c.Scale < 0 {
		return fmt.Errorf("command %q: minimum must be below maximum, step and scale above 0", c.Name)
	}
	for _, v := range []float64{c.Min, c.Max} {
		if _, err := encodeHANumber(v*c.Scale, c.Encoding); err != nil {
			return fmt.Errorf("command %q: %w", c.Name, err)
		}
	}
	return nil
}

// Render builds the payload for a value. A button ignores the value.
func (c *HACommand) Render(value string) ([]byte, error) {
	if !c.Dynamic() {
		return hex.DecodeString(c.PayloadHex)
	}
	left, right, err := c.template()
	if err != nil {
		return nil, err
	}
	var mid []byte
	if c.Kind == "switch" {
		on, ok := map[string]float64{"ON": 1, "1": 1, "TRUE": 1, "OFF": 0, "0": 0, "FALSE": 0}[strings.ToUpper(strings.TrimSpace(value))]
		if !ok {
			return nil, fmt.Errorf("a switch takes ON or OFF, not %q", value)
		}
		if mid, err = encodeHANumber(on, c.Encoding); err != nil {
			return nil, err
		}
	} else if c.Kind == "choice" {
		o := c.Option(value)
		if o == nil {
			return nil, fmt.Errorf("%q is not one of the options", value)
		}
		if mid, err = encodeHANumber(o.Value, c.Encoding); err != nil {
			return nil, err
		}
	} else if c.Kind == "number" {
		v, perr := strconv.ParseFloat(strings.TrimSpace(value), 64)
		if perr != nil || math.IsNaN(v) || v < c.Min || v > c.Max {
			return nil, fmt.Errorf("value %q is outside %g to %g", value, c.Min, c.Max)
		}
		if mid, err = encodeHANumber(v*c.Scale, c.Encoding); err != nil {
			return nil, err
		}
	} else if c.Encoding == "ascii" {
		mid = []byte(value)
	} else if mid, err = hex.DecodeString(strings.TrimSpace(value)); err != nil {
		return nil, fmt.Errorf("value must be hex")
	}
	if len(mid) == 0 {
		return nil, fmt.Errorf("empty value")
	}
	return append(append(left, mid...), right...), nil
}

// Option finds a choice's option by name (any case) or by value.
func (c *HACommand) Option(v string) *HAOption {
	v = strings.TrimSpace(v)
	for i := range c.Options {
		if strings.EqualFold(c.Options[i].Name, v) {
			return &c.Options[i]
		}
	}
	for i := range c.Options {
		if strconv.FormatFloat(c.Options[i].Value, 'f', -1, 64) == v {
			return &c.Options[i]
		}
	}
	return nil
}

func encodeHANumber(v float64, encoding string) ([]byte, error) {
	e, ok := haEncodings[encoding]
	if !ok {
		return nil, fmt.Errorf("unknown number format %q", encoding)
	}
	bits := e.size * 8
	lo, hi := 0.0, math.Ldexp(1, bits)-1
	if e.signed {
		lo, hi = -math.Ldexp(1, bits-1), math.Ldexp(1, bits-1)-1
	}
	n := math.Round(v)
	if n < lo || n > hi {
		return nil, fmt.Errorf("%g does not fit in %s", n, encoding)
	}
	u := uint64(int64(n))
	out := make([]byte, e.size)
	for i := range out {
		at := e.size - 1 - i
		if e.little {
			at = i
		}
		out[at] = byte(u >> (8 * i))
	}
	return out, nil
}
