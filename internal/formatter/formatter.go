// Package formatter implements payload formatters: TTN-compatible
// JavaScript (decodeUplink/encodeDownlink), Cayenne LPP and a declarative
// byte-field slicer.
package formatter

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/dop251/goja"
	"github.com/nor24o/lorahub/internal/model"
)

// Result is the outcome of a decode.
type Result struct {
	Data     map[string]any `json:"data,omitempty"`
	Warnings []string       `json:"warnings,omitempty"`
	Errors   []string       `json:"errors,omitempty"`
}

// Input is passed to uplink decoders.
type Input struct {
	Bytes    []byte
	FPort    int
	RecvTime time.Time
	Meta     map[string]any // device attributes etc.
}

const jsTimeout = 200 * time.Millisecond

var (
	progMu    sync.Mutex
	progCache = map[[32]byte]*goja.Program{}
)

func compile(code string) (*goja.Program, error) {
	k := sha256.Sum256([]byte(code))
	progMu.Lock()
	defer progMu.Unlock()
	if p, ok := progCache[k]; ok {
		return p, nil
	}
	p, err := goja.Compile("formatter.js", code, false)
	if err != nil {
		return nil, err
	}
	if len(progCache) > 256 {
		progCache = map[[32]byte]*goja.Program{}
	}
	progCache[k] = p
	return p, nil
}

func newVM(code string) (*goja.Runtime, error) {
	p, err := compile(code)
	if err != nil {
		return nil, fmt.Errorf("compile: %w", err)
	}
	vm := goja.New()
	vm.SetFieldNameMapper(goja.TagFieldNameMapper("json", true))
	console := vm.NewObject()
	logs := []string{}
	console.Set("log", func(args ...any) { logs = append(logs, fmt.Sprint(args...)) })
	vm.Set("console", console)
	timer := time.AfterFunc(jsTimeout, func() { vm.Interrupt("formatter timeout") })
	defer timer.Stop()
	if _, err := vm.RunProgram(p); err != nil {
		return nil, fmt.Errorf("run: %w", err)
	}
	return vm, nil
}

func bytesToJS(vm *goja.Runtime, b []byte) goja.Value {
	arr := make([]any, len(b))
	for i, v := range b {
		arr[i] = int64(v)
	}
	return vm.ToValue(arr)
}

func toStrings(v any) []string {
	var out []string
	if arr, ok := v.([]any); ok {
		for _, x := range arr {
			out = append(out, fmt.Sprint(x))
		}
	}
	return out
}

// DecodeJS runs a TTN-style decodeUplink(input), with fallbacks for the
// legacy Decoder(bytes, port) and decode(fPort, bytes) signatures.
func DecodeJS(code string, in Input) Result {
	vm, err := newVM(code)
	if err != nil {
		return Result{Errors: []string{err.Error()}}
	}
	timer := time.AfterFunc(jsTimeout, func() { vm.Interrupt("formatter timeout") })
	defer timer.Stop()

	jsBytes := bytesToJS(vm, in.Bytes)
	var out goja.Value
	if fn, ok := goja.AssertFunction(vm.Get("decodeUplink")); ok {
		input := vm.NewObject()
		input.Set("bytes", jsBytes)
		input.Set("fPort", in.FPort)
		input.Set("recvTime", vm.ToValue(in.RecvTime))
		input.Set("hex", strings.ToUpper(hex.EncodeToString(in.Bytes)))
		if in.Meta != nil {
			input.Set("attributes", in.Meta)
		}
		out, err = fn(goja.Undefined(), input)
		if err != nil {
			return Result{Errors: []string{err.Error()}}
		}
		exp, _ := out.Export().(map[string]any)
		r := Result{}
		if exp == nil {
			return Result{Errors: []string{"decodeUplink must return an object"}}
		}
		if d, ok := exp["data"].(map[string]any); ok {
			r.Data = d
		} else if exp["data"] != nil {
			r.Data = map[string]any{"value": exp["data"]}
		}
		r.Warnings = toStrings(exp["warnings"])
		r.Errors = toStrings(exp["errors"])
		return r
	}
	for _, name := range []string{"Decoder", "Decode", "decode"} {
		if fn, ok := goja.AssertFunction(vm.Get(name)); ok {
			var args []goja.Value
			if name == "Decoder" {
				args = []goja.Value{jsBytes, vm.ToValue(in.FPort)}
			} else {
				args = []goja.Value{vm.ToValue(in.FPort), jsBytes}
			}
			out, err = fn(goja.Undefined(), args...)
			if err != nil {
				return Result{Errors: []string{err.Error()}}
			}
			if m, ok := out.Export().(map[string]any); ok {
				return Result{Data: m}
			}
			return Result{Data: map[string]any{"value": out.Export()}}
		}
	}
	return Result{Errors: []string{"no decodeUplink(input) function defined"}}
}

// EncodeJS runs encodeDownlink({data, fPort}) and returns bytes and fPort.
func EncodeJS(code string, data any, fPort int) ([]byte, int, error) {
	vm, err := newVM(code)
	if err != nil {
		return nil, 0, err
	}
	timer := time.AfterFunc(jsTimeout, func() { vm.Interrupt("formatter timeout") })
	defer timer.Stop()
	fn, ok := goja.AssertFunction(vm.Get("encodeDownlink"))
	if !ok {
		return nil, 0, errors.New("no encodeDownlink(input) function defined")
	}
	input := vm.NewObject()
	input.Set("data", data)
	input.Set("fPort", fPort)
	out, err := fn(goja.Undefined(), input)
	if err != nil {
		return nil, 0, err
	}
	exp, _ := out.Export().(map[string]any)
	if exp == nil {
		return nil, 0, errors.New("encodeDownlink must return an object")
	}
	if errs := toStrings(exp["errors"]); len(errs) > 0 {
		return nil, 0, errors.New(strings.Join(errs, "; "))
	}
	if p, ok := exp["fPort"]; ok {
		fPort = int(toFloat(p))
	}
	arr, ok := exp["bytes"].([]any)
	if !ok {
		return nil, 0, errors.New("encodeDownlink must return {bytes: [...]}")
	}
	b := make([]byte, len(arr))
	for i, v := range arr {
		b[i] = byte(int64(toFloat(v)))
	}
	return b, fPort, nil
}

func toFloat(v any) float64 {
	switch x := v.(type) {
	case int64:
		return float64(x)
	case float64:
		return x
	case int:
		return float64(x)
	}
	return 0
}

// Decode applies a formatter configuration.
func Decode(f model.Formatter, in Input) *Result {
	switch f.Type {
	case "", "none", "inherit":
		return nil
	case "javascript":
		r := DecodeJS(f.Code, in)
		return &r
	case "cayenne_lpp":
		b := in.Bytes
		if f.DataOffset > 0 && f.DataOffset <= len(b) {
			b = b[f.DataOffset:]
		}
		d, err := DecodeCayenne(b)
		r := Result{Data: d}
		if err != nil {
			r.Warnings = []string{err.Error()}
		}
		return &r
	case "fields":
		d, warns := DecodeFields(f.Fields, in.Bytes)
		return &Result{Data: d, Warnings: warns}
	}
	return &Result{Errors: []string{"unknown formatter type " + f.Type}}
}

// DecodeFields applies a list of byte-range fields.
func DecodeFields(fields []model.Field, b []byte) (map[string]any, []string) {
	out := map[string]any{}
	var warns []string
	for _, f := range fields {
		if f.Name == "" {
			continue
		}
		l := f.Length
		if l <= 0 {
			l = typeLen(f.Type)
		}
		if f.Offset < 0 || f.Offset+l > len(b) {
			warns = append(warns, fmt.Sprintf("field %s out of range", f.Name))
			continue
		}
		seg := b[f.Offset : f.Offset+l]
		var num float64
		isNum := true
		switch f.Type {
		case "uint8":
			num = float64(seg[0])
		case "int8":
			num = float64(int8(seg[0]))
		case "uint16_be", "uint16":
			num = float64(binary.BigEndian.Uint16(seg))
		case "uint16_le":
			num = float64(binary.LittleEndian.Uint16(seg))
		case "int16_be", "int16":
			num = float64(int16(binary.BigEndian.Uint16(seg)))
		case "int16_le":
			num = float64(int16(binary.LittleEndian.Uint16(seg)))
		case "uint24_be":
			num = float64(uint32(seg[0])<<16 | uint32(seg[1])<<8 | uint32(seg[2]))
		case "int24_be":
			v := int32(uint32(seg[0])<<16 | uint32(seg[1])<<8 | uint32(seg[2]))
			if v&0x800000 != 0 {
				v -= 0x1000000
			}
			num = float64(v)
		case "uint32_be", "uint32":
			num = float64(binary.BigEndian.Uint32(seg))
		case "uint32_le":
			num = float64(binary.LittleEndian.Uint32(seg))
		case "int32_be", "int32":
			num = float64(int32(binary.BigEndian.Uint32(seg)))
		case "int32_le":
			num = float64(int32(binary.LittleEndian.Uint32(seg)))
		case "float32_be", "float32":
			num = float64(math.Float32frombits(binary.BigEndian.Uint32(seg)))
		case "float32_le":
			num = float64(math.Float32frombits(binary.LittleEndian.Uint32(seg)))
		case "bool":
			out[f.Name] = seg[0] != 0
			isNum = false
		case "bit":
			out[f.Name] = (seg[0]>>uint(f.Bit))&1 == 1
			isNum = false
		case "ascii":
			out[f.Name] = strings.TrimRight(string(seg), "\x00")
			isNum = false
		default: // hex
			out[f.Name] = strings.ToUpper(hex.EncodeToString(seg))
			isNum = false
		}
		if isNum {
			if f.Scale != 0 {
				num *= f.Scale
			}
			num += f.Add
			out[f.Name] = math.Round(num*1e6) / 1e6
		}
	}
	return out, warns
}

func typeLen(t string) int {
	switch {
	case strings.HasPrefix(t, "uint8"), strings.HasPrefix(t, "int8"), t == "bool", t == "bit":
		return 1
	case strings.Contains(t, "16"):
		return 2
	case strings.Contains(t, "24"):
		return 3
	case strings.Contains(t, "32"):
		return 4
	}
	return 1
}
