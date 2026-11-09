package formatter

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"hash/crc32"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/dop251/goja"
	"github.com/nor24o/lorahub/internal/model"
)

const maxRepeat = 256

// fieldDecoder holds the state of one packet's field decoding. The JavaScript
// runtime for expressions is created only when a field has one.
type fieldDecoder struct {
	b     []byte
	out   map[string]any
	warns []string
	vm    *goja.Runtime
	timer *time.Timer
}

func (d *fieldDecoder) close() {
	if d.timer != nil {
		d.timer.Stop()
	}
}

func (d *fieldDecoder) warn(format string, a ...any) {
	d.warns = append(d.warns, fmt.Sprintf(format, a...))
}

// eval runs a field expression with x (the value), i (the repeat index),
// bytes (the packet) and v (the values decoded so far).
func (d *fieldDecoder) eval(expr string, x any, i int) (any, error) {
	p, err := compile("(" + expr + "\n)")
	if err != nil {
		return nil, err
	}
	if d.vm == nil {
		d.vm = goja.New()
		d.vm.Set("bytes", bytesToJS(d.vm, d.b))
		d.vm.Set("v", d.out)
		vm := d.vm
		d.timer = time.AfterFunc(jsTimeout, func() { vm.Interrupt("expression timeout") })
	}
	d.vm.Set("x", x)
	d.vm.Set("i", i)
	r, err := d.vm.RunProgram(p)
	if err != nil {
		return nil, err
	}
	return r.Export(), nil
}

// parseIntType splits a name such as uint24_le. Without a suffix the type is
// big endian.
func parseIntType(t string) (signed bool, size int, little, ok bool) {
	name := t
	if s, found := strings.CutSuffix(name, "_le"); found {
		name, little = s, true
	} else {
		name = strings.TrimSuffix(name, "_be")
	}
	digits, found := strings.CutPrefix(name, "uint")
	if !found {
		if digits, found = strings.CutPrefix(name, "int"); !found {
			return false, 0, false, false
		}
		signed = true
	}
	bits, err := strconv.Atoi(digits)
	if err != nil || bits%8 != 0 || bits < 8 || bits > 64 {
		return false, 0, false, false
	}
	return signed, bits / 8, little, true
}

func readUint(seg []byte, little bool) uint64 {
	var v uint64
	for i := range seg {
		c := seg[i]
		if little {
			c = seg[len(seg)-1-i]
		}
		v = v<<8 | uint64(c)
	}
	return v
}

func signExtend(v uint64, bits int) int64 {
	if bits >= 64 {
		return int64(v)
	}
	shift := uint(64 - bits)
	return int64(v<<shift) >> shift
}

// typeLen returns the bytes a field type occupies by default.
func typeLen(t string) int {
	if _, size, _, ok := parseIntType(t); ok {
		return size
	}
	switch {
	case strings.HasPrefix(t, "float64"), strings.HasPrefix(t, "unix_ms"):
		return 8
	case strings.HasPrefix(t, "float32"), strings.HasPrefix(t, "unix_s"):
		return 4
	}
	return 1
}

// checksumFuncs computes a checksum and reports its width in bytes.
var checksumFuncs = map[string]struct {
	size int
	fn   func([]byte) uint64
}{
	"xor": {1, func(b []byte) uint64 {
		var x byte
		for _, c := range b {
			x ^= c
		}
		return uint64(x)
	}},
	"sum8": {1, func(b []byte) uint64 {
		var x byte
		for _, c := range b {
			x += c
		}
		return uint64(x)
	}},
	"crc8":         {1, func(b []byte) uint64 { return crcBits(b, 8, 0x07, 0, false) }},
	"crc8_maxim":   {1, func(b []byte) uint64 { return crcBits(b, 8, 0x31, 0, true) }},
	"crc16_ccitt":  {2, func(b []byte) uint64 { return crcBits(b, 16, 0x1021, 0xFFFF, false) }},
	"crc16_modbus": {2, func(b []byte) uint64 { return crcBits(b, 16, 0x8005, 0xFFFF, true) }},
	"crc32":        {4, func(b []byte) uint64 { return uint64(crc32.ChecksumIEEE(b)) }},
}

func reverseBits(v uint64, n int) uint64 {
	var r uint64
	for i := 0; i < n; i++ {
		if v&(1<<uint(i)) != 0 {
			r |= 1 << uint(n-1-i)
		}
	}
	return r
}

func crcBits(b []byte, width int, poly, init uint64, reflect bool) uint64 {
	mask := uint64(1)<<uint(width) - 1
	top := uint64(1) << uint(width-1)
	crc := init
	for _, raw := range b {
		c := uint64(raw)
		if reflect {
			c = reverseBits(c, 8)
		}
		crc ^= c << uint(width-8)
		for i := 0; i < 8; i++ {
			if crc&top != 0 {
				crc = (crc << 1) ^ poly
			} else {
				crc <<= 1
			}
			crc &= mask
		}
	}
	if reflect {
		crc = reverseBits(crc, width)
	}
	return crc
}

func bcdValue(seg []byte) (float64, bool) {
	var v float64
	for _, c := range seg {
		hi, lo := c>>4, c&0x0F
		if hi > 9 || lo > 9 {
			return 0, false
		}
		v = v*100 + float64(hi)*10 + float64(lo)
	}
	return v, true
}

// roundValue applies the field's precision. Without one, values keep six
// decimals, except very large ones that a float64 could not scale safely.
func roundValue(f *model.Field, num float64) float64 {
	if f.Decimals != nil {
		p := math.Pow10(*f.Decimals)
		return math.Round(num*p) / p
	}
	if math.Abs(num) >= 1e9 {
		return num
	}
	return math.Round(num*1e6) / 1e6
}

// number finishes a numeric value: label, scale, offset, expression, valid
// range and rounding. It reports false when the value was discarded.
func (d *fieldDecoder) number(f *model.Field, name string, raw float64, i int) bool {
	if len(f.Map) > 0 {
		key := strconv.FormatFloat(raw, 'f', -1, 64)
		label, ok := f.Map[key]
		if !ok && raw >= 0 && raw == math.Trunc(raw) {
			label, ok = f.Map[fmt.Sprintf("0x%X", uint64(raw))]
		}
		if ok {
			d.out[name] = label
			return true
		}
	}
	num := raw
	if f.Scale != 0 {
		num *= f.Scale
	}
	num += f.Add
	if f.Expr != "" {
		r, err := d.eval(f.Expr, num, i)
		if err != nil {
			d.warn("field %s: %v", name, err)
			return false
		}
		switch x := r.(type) {
		case int64:
			num = float64(x)
		case float64:
			num = x
		case nil:
			return false
		default:
			d.out[name] = x
			return true
		}
	}
	if (f.Min != nil && num < *f.Min) || (f.Max != nil && num > *f.Max) {
		d.warn("field %s: value %v outside its valid range", name, num)
		return false
	}
	d.out[name] = roundValue(f, num)
	return true
}

// other finishes a non-numeric value: expression and label.
func (d *fieldDecoder) other(f *model.Field, name string, val any, i int) bool {
	if f.Expr != "" {
		r, err := d.eval(f.Expr, val, i)
		if err != nil {
			d.warn("field %s: %v", name, err)
			return false
		}
		if r == nil {
			return false
		}
		val = r
	}
	if s, ok := val.(string); ok {
		if label, found := f.Map[s]; found {
			val = label
		}
	}
	d.out[name] = val
	return true
}

func hexUpper(b []byte) string { return strings.ToUpper(hex.EncodeToString(b)) }

// value decodes one instance of a field from seg.
func (d *fieldDecoder) value(f *model.Field, name string, seg []byte, start, i int) bool {
	if signed, size, little, ok := parseIntType(f.Type); ok {
		if len(seg) < size {
			d.warn("field %s out of range", name)
			return false
		}
		v := readUint(seg[:size], little)
		if signed {
			return d.number(f, name, float64(signExtend(v, size*8)), i)
		}
		return d.number(f, name, float64(v), i)
	}
	t := f.Type
	switch {
	case strings.HasPrefix(t, "float32"), strings.HasPrefix(t, "float64"):
		size := 4
		if strings.HasPrefix(t, "float64") {
			size = 8
		}
		if len(seg) < size {
			d.warn("field %s out of range", name)
			return false
		}
		v := readUint(seg[:size], strings.HasSuffix(t, "_le"))
		num := float64(math.Float32frombits(uint32(v)))
		if size == 8 {
			num = math.Float64frombits(v)
		}
		if math.IsNaN(num) || math.IsInf(num, 0) {
			d.warn("field %s is not a number", name)
			return false
		}
		return d.number(f, name, num, i)
	case strings.HasPrefix(t, "unix_s"), strings.HasPrefix(t, "unix_ms"):
		size := typeLen(t)
		if len(seg) < size {
			d.warn("field %s out of range", name)
			return false
		}
		v := int64(readUint(seg[:size], strings.HasSuffix(t, "_le")))
		at := time.Unix(v, 0)
		if size == 8 {
			at = time.UnixMilli(v)
		}
		return d.other(f, name, at.UTC().Format(time.RFC3339), i)
	case t == "bits", t == "bits_le", t == "bits_signed", t == "bits_signed_le":
		n := f.BitLen
		if n <= 0 {
			n = 1
		}
		if len(seg) > 8 || f.Bit < 0 || f.Bit+n > len(seg)*8 {
			d.warn("field %s: bits do not fit in %d byte(s)", name, len(seg))
			return false
		}
		v := readUint(seg, strings.HasSuffix(t, "_le")) >> uint(f.Bit)
		if n < 64 {
			v &= uint64(1)<<uint(n) - 1
		}
		if strings.Contains(t, "signed") {
			return d.number(f, name, float64(signExtend(v, n)), i)
		}
		return d.number(f, name, float64(v), i)
	case t == "bcd":
		v, ok := bcdValue(seg)
		if !ok {
			d.warn("field %s is not valid BCD", name)
			return false
		}
		return d.number(f, name, v, i)
	case t == "bool":
		return d.other(f, name, (seg[0] != 0) != f.Invert, i)
	case t == "bit":
		return d.other(f, name, ((seg[0]>>uint(f.Bit))&1 == 1) != f.Invert, i)
	case t == "ascii", t == "utf8":
		return d.other(f, name, strings.TrimRight(string(seg), "\x00"), i)
	case t == "base64":
		return d.other(f, name, base64.StdEncoding.EncodeToString(seg), i)
	case t == "hex_le":
		rev := make([]byte, len(seg))
		for k, c := range seg {
			rev[len(seg)-1-k] = c
		}
		return d.other(f, name, hexUpper(rev), i)
	case t == "checksum":
		return d.checksum(f, name, seg, start)
	}
	return d.other(f, name, hexUpper(seg), i)
}

// checksum compares the field's bytes with the checksum of the bytes from
// f.From up to the field. An algorithm name ending in _le reads it low byte
// first.
func (d *fieldDecoder) checksum(f *model.Field, name string, seg []byte, start int) bool {
	algo, little := strings.CutSuffix(f.Algo, "_le")
	c, ok := checksumFuncs[algo]
	from := f.From
	if from < 0 {
		from += len(d.b)
	}
	if !ok || len(seg) < c.size || from < 0 || from > start {
		d.warn("field %s: cannot compute checksum %q", name, f.Algo)
		return false
	}
	match := readUint(seg[:c.size], little) == c.fn(d.b[from:start])
	d.out[name] = match
	if !match {
		d.warn("field %s: checksum does not match", name)
	}
	return match
}

// field decodes all instances of a field. It reports false when the field is
// required and could not be read.
func (d *fieldDecoder) field(f *model.Field) bool {
	if f.Name == "" {
		return true
	}
	count := 1
	if f.Repeat > 1 {
		count = min(f.Repeat, maxRepeat)
	}
	for i := 0; i < count; i++ {
		name := f.Name
		if f.Repeat > 1 {
			name = fmt.Sprintf("%s_%d", f.Name, i+1)
		}
		if f.When != "" {
			r, err := d.eval(f.When, nil, i)
			if err != nil {
				d.warn("field %s: %v", name, err)
				if f.Required {
					return false
				}
				continue
			}
			if !truthy(r) {
				continue
			}
		}
		start := f.Offset
		if start < 0 {
			start += len(d.b)
		}
		l := f.Length
		if l == 0 {
			if f.Type == "checksum" {
				l = checksumFuncs[strings.TrimSuffix(f.Algo, "_le")].size
			} else {
				l = typeLen(f.Type)
			}
		} else if l < 0 { // -1 reaches the last byte, -3 leaves two bytes after it
			l += len(d.b) - start + 1
		}
		stride := f.Stride
		if stride <= 0 {
			stride = l
		}
		start += i * stride
		if start < 0 || l <= 0 || start+l > len(d.b) {
			if i == 0 || f.Required {
				d.warn("field %s out of range", name)
			}
			if f.Required {
				return false
			}
			break
		}
		seg := d.b[start : start+l]
		if f.Invalid != "" && hexUpper(seg) == strings.ToUpper(cleanMatchHex(f.Invalid)) {
			continue // the sensor reports "no reading"
		}
		if !d.value(f, name, seg, start, i) && f.Required {
			return false
		}
	}
	return true
}

func truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case int64:
		return x != 0
	case float64:
		return x != 0
	case string:
		return x != ""
	}
	return true
}

// DecodeFields applies a list of byte-range fields. When a required field
// fails, no values are returned, so the next decoder gets a chance.
func DecodeFields(fields []model.Field, b []byte) (map[string]any, []string) {
	d := &fieldDecoder{b: b, out: map[string]any{}}
	defer d.close()
	for i := range fields {
		if !d.field(&fields[i]) {
			return nil, d.warns
		}
	}
	return d.out, d.warns
}

// decodeSingle runs a field definition over a value of its own, as used by
// tag / length / value decoding.
func decodeSingle(f model.Field, name string, seg []byte, out map[string]any) []string {
	f.Name, f.Offset, f.Length, f.Repeat = name, 0, len(seg), 0
	d := &fieldDecoder{b: seg, out: out}
	defer d.close()
	d.field(&f)
	return d.warns
}
