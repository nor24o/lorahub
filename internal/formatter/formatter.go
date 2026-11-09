// Package formatter implements payload formatters: TTN-compatible
// JavaScript (decodeUplink/encodeDownlink), Cayenne LPP and a declarative
// byte-field slicer.
package formatter

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
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
	Vars     map[string]any // extra properties set on input (node_id, serial, payload)
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
		for k, v := range in.Vars {
			if b, ok := v.([]byte); ok {
				input.Set(k, bytesToJS(vm, b))
			} else {
				input.Set(k, v)
			}
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
	}
	if p, err := applyPre(f.Options, in.Bytes); err != nil {
		return &Result{Errors: []string{err.Error()}}
	} else {
		in.Bytes = p
	}
	if IsMethod(f.Type) {
		b := in.Bytes
		if f.DataOffset > 0 && f.DataOffset <= len(b) {
			b = b[f.DataOffset:]
		}
		d, w, e := decodeMethod(f.Type, f.Fields, f.Options, b)
		return &Result{Data: d, Warnings: w, Errors: e}
	}
	switch f.Type {
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
