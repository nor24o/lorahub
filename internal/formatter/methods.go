package formatter

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/nor24o/lorahub/internal/model"
)

// Decoding methods that work on the data bytes as a whole.
var methodKinds = map[string]bool{"tlv_custom": true, "bitstream": true, "csv": true, "kv": true, "regex": true, "json": true, "cbor": true, "msgpack": true}

// IsMethod reports whether kind is decoded by decodeMethod.
func IsMethod(kind string) bool { return methodKinds[kind] }

// applyPre transforms the data bytes before they are decoded.
func applyPre(o *model.DecodeOptions, data []byte) ([]byte, error) {
	if o == nil || o.Pre == "" {
		return data, nil
	}
	switch o.Pre {
	case "hex_text":
		return hex.DecodeString(strings.Join(strings.Fields(string(data)), ""))
	case "base64_text":
		s := strings.TrimSpace(string(data))
		b, err := base64.StdEncoding.DecodeString(s)
		if err != nil {
			return base64.RawStdEncoding.DecodeString(strings.TrimRight(s, "="))
		}
		return b, nil
	case "xor":
		key, err := hex.DecodeString(cleanMatchHex(o.PreKey))
		if err != nil || len(key) == 0 {
			return nil, errors.New("the XOR key must be hex")
		}
		out := make([]byte, len(data))
		for i, c := range data {
			out[i] = c ^ key[i%len(key)]
		}
		return out, nil
	case "reverse":
		out := make([]byte, len(data))
		for i, c := range data {
			out[len(data)-1-i] = c
		}
		return out, nil
	}
	return nil, fmt.Errorf("unknown preprocessing step %q", o.Pre)
}

func validateFields(name string, fields []model.Field) error {
	for _, f := range fields {
		for _, expr := range []string{f.Expr, f.When} {
			if expr == "" {
				continue
			}
			if _, err := compile("(" + expr + "\n)"); err != nil {
				return fmt.Errorf("%q field %s: %v", name, f.Name, err)
			}
		}
		if f.Type == "checksum" {
			if _, ok := checksumFuncs[strings.TrimSuffix(f.Algo, "_le")]; !ok {
				return fmt.Errorf("%q field %s: unknown checksum %q", name, f.Name, f.Algo)
			}
		}
		if f.Repeat < 0 || f.Repeat > maxRepeat {
			return fmt.Errorf("%q field %s: repeat must be 0 to %d", name, f.Name, maxRepeat)
		}
		if f.Bit < 0 || f.Bit > 63 || f.BitLen < 0 || f.BitLen > 64 {
			return fmt.Errorf("%q field %s: bit position must be 0 to 63 and bit length 0 to 64", name, f.Name)
		}
		if f.Decimals != nil && (*f.Decimals < 0 || *f.Decimals > 12) {
			return fmt.Errorf("%q field %s: decimals must be 0 to 12", name, f.Name)
		}
		if f.Invalid != "" {
			if _, err := hex.DecodeString(cleanMatchHex(f.Invalid)); err != nil {
				return fmt.Errorf("%q field %s: the no-reading value must be hex", name, f.Name)
			}
		}
	}
	return nil
}

func validateMethod(name, kind string, fields []model.Field, o *model.DecodeOptions) error {
	if err := validateFields(name, fields); err != nil {
		return err
	}
	if o != nil {
		if err := validateFields(name, o.Tags); err != nil {
			return err
		}
		switch o.Pre {
		case "", "hex_text", "base64_text", "reverse":
		case "xor":
			if k, err := hex.DecodeString(cleanMatchHex(o.PreKey)); err != nil || len(k) == 0 {
				return fmt.Errorf("%q: the XOR key must be hex", name)
			}
		default:
			return fmt.Errorf("%q: unknown preprocessing step %q", name, o.Pre)
		}
	}
	switch kind {
	case "regex":
		if o == nil || o.Pattern == "" {
			return fmt.Errorf("%q: enter a regular expression", name)
		}
		if _, err := regexp.Compile(o.Pattern); err != nil {
			return fmt.Errorf("%q: bad regular expression: %v", name, err)
		}
	case "tlv_custom":
		if o == nil || o.TagSize < 0 || o.TagSize > 4 || o.LenSize < 0 || o.LenSize > 2 {
			return fmt.Errorf("%q: tag size must be 1 to 4 bytes and length size 0 to 2", name)
		}
	}
	return nil
}

// decodeMethod decodes data with one of the methodKinds.
func decodeMethod(kind string, fields []model.Field, o *model.DecodeOptions, data []byte) (map[string]any, []string, []string) {
	if o == nil {
		o = &model.DecodeOptions{}
	}
	switch kind {
	case "tlv_custom":
		return decodeTLV(o, data)
	case "bitstream":
		return decodeBitstream(fields, data)
	case "csv":
		return decodeCSV(o, data), nil, nil
	case "kv":
		return decodeKV(o, data), nil, nil
	case "regex":
		return decodeRegex(o, data)
	case "json":
		var v any
		if err := json.Unmarshal(data, &v); err != nil {
			return nil, nil, []string{"not valid JSON: " + err.Error()}
		}
		return objectOf(v), nil, nil
	case "cbor", "msgpack":
		r := &binReader{b: data}
		var v any
		var err error
		if kind == "cbor" {
			v, err = r.cbor(0)
		} else {
			v, err = r.msgpack(0)
		}
		if err != nil {
			return nil, nil, []string{kind + ": " + err.Error()}
		}
		return objectOf(v), nil, nil
	}
	return nil, nil, []string{"unknown decoder type " + kind}
}

func objectOf(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return map[string]any{"value": v}
}

func textOf(data []byte) string { return strings.Trim(string(data), "\x00 \t\r\n") }

// parseText turns text into a number or boolean when it clearly is one.
func parseText(s string) any {
	s = strings.TrimSpace(s)
	if v, err := strconv.ParseFloat(s, 64); err == nil && !math.IsNaN(v) && !math.IsInf(v, 0) {
		return v
	}
	switch strings.ToLower(s) {
	case "true":
		return true
	case "false":
		return false
	}
	return s
}

func decodeCSV(o *model.DecodeOptions, data []byte) map[string]any {
	sep := o.Separator
	if sep == "" {
		sep = ","
	}
	out := map[string]any{}
	for i, part := range strings.Split(textOf(data), sep) {
		if strings.TrimSpace(part) == "" {
			continue
		}
		name := fmt.Sprintf("col_%d", i+1)
		if i < len(o.Columns) {
			if o.Columns[i] == "" {
				continue // an unnamed column is left out
			}
			name = o.Columns[i]
		}
		out[name] = parseText(part)
	}
	return out
}

func decodeKV(o *model.DecodeOptions, data []byte) map[string]any {
	sep, kv := o.Separator, o.KVSeparator
	if sep == "" {
		sep = ";"
	}
	if kv == "" {
		kv = "="
	}
	text := strings.NewReplacer("\r\n", sep, "\n", sep).Replace(textOf(data))
	out := map[string]any{}
	for _, part := range strings.Split(text, sep) {
		k, v, ok := strings.Cut(part, kv)
		if k = strings.TrimSpace(k); ok && k != "" {
			out[k] = parseText(v)
		}
	}
	return out
}

func decodeRegex(o *model.DecodeOptions, data []byte) (map[string]any, []string, []string) {
	re, err := regexp.Compile(o.Pattern)
	if err != nil {
		return nil, nil, []string{"bad regular expression: " + err.Error()}
	}
	subject := textOf(data)
	if o.PatternOn == "hex" {
		subject = hexUpper(data)
	}
	m := re.FindStringSubmatch(subject)
	if m == nil {
		return nil, nil, nil
	}
	out := map[string]any{}
	names := re.SubexpNames()
	for i := 1; i < len(m); i++ {
		name := names[i]
		if name == "" {
			name = fmt.Sprintf("group_%d", i)
		}
		out[name] = parseText(m[i])
	}
	return out, nil, nil
}

// decodeTLV reads repeated tag, length and value records.
func decodeTLV(o *model.DecodeOptions, b []byte) (map[string]any, []string, []string) {
	tagSize := o.TagSize
	if tagSize == 0 {
		tagSize = 1
	}
	byTag := map[string]model.Field{}
	for _, t := range o.Tags {
		byTag[strings.ToUpper(cleanMatchHex(t.Tag))] = t
	}
	readLen := func(p []byte) int { return int(readUint(p, o.LittleEndian)) }
	out := map[string]any{}
	var warns []string
	seen := map[string]int{}
	for i := 0; i < len(b); {
		var tag string
		n := -1
		hdr := tagSize + o.LenSize
		if i+hdr > len(b) {
			warns = append(warns, fmt.Sprintf("incomplete record at byte %d", i))
			break
		}
		if o.LengthFirst {
			n = readLen(b[i : i+o.LenSize])
			tag = hexUpper(b[i+o.LenSize : i+hdr])
		} else {
			tag = hexUpper(b[i : i+tagSize])
			if o.LenSize > 0 {
				n = readLen(b[i+tagSize : i+hdr])
			}
		}
		i += hdr
		def, known := byTag[tag]
		if n < 0 { // no length on the wire: it follows from the tag's type
			if !known {
				warns = append(warns, "unknown tag "+tag+" and no length to skip it")
				break
			}
			if n = def.Length; n <= 0 {
				n = typeLen(def.Type)
			}
		}
		if i+n > len(b) {
			warns = append(warns, "record "+tag+" is longer than the packet")
			break
		}
		seg := b[i : i+n]
		i += n
		name := "tag_" + tag
		if known && def.Name != "" {
			name = def.Name
		}
		seen[name]++
		if seen[name] > 1 {
			name = fmt.Sprintf("%s_%d", name, seen[name])
		}
		if known {
			warns = append(warns, decodeSingle(def, name, seg, out)...)
		} else {
			out[name] = hexUpper(seg)
		}
	}
	return out, warns, nil
}

// decodeBitstream reads fields one after another, most significant bit first.
// A field without a name skips its bits.
func decodeBitstream(fields []model.Field, b []byte) (map[string]any, []string, []string) {
	d := &fieldDecoder{b: b, out: map[string]any{}}
	defer d.close()
	pos := 0
	for i := range fields {
		f := &fields[i]
		n := f.BitLen
		if n <= 0 {
			n = 1
		}
		if n > 64 || pos+n > len(b)*8 {
			d.warn("field %s runs past the end of the data", f.Name)
			break
		}
		var v uint64
		for k := pos; k < pos+n; k++ {
			v = v<<1 | uint64(b[k/8]>>uint(7-k%8))&1
		}
		pos += n
		if f.Name == "" {
			continue
		}
		switch {
		case f.Type == "bool":
			d.other(f, f.Name, (v != 0) != f.Invert, 0)
		case strings.HasPrefix(f.Type, "int"), strings.Contains(f.Type, "signed"):
			d.number(f, f.Name, float64(signExtend(v, n)), 0)
		default:
			d.number(f, f.Name, float64(v), 0)
		}
	}
	return d.out, d.warns, nil
}

const maxNesting = 16

// binReader decodes the binary object formats. Lengths are checked against
// the bytes left, so a corrupt packet cannot request a huge allocation.
type binReader struct {
	b []byte
	i int
}

var errTruncated = errors.New("data ends too early")

func (r *binReader) take(n uint64) ([]byte, error) {
	if n > uint64(len(r.b)-r.i) {
		return nil, errTruncated
	}
	p := r.b[r.i : r.i+int(n)]
	r.i += int(n)
	return p, nil
}

func (r *binReader) uintN(size int) (uint64, error) {
	p, err := r.take(uint64(size))
	if err != nil {
		return 0, err
	}
	return readUint(p, false), nil
}

func float16(v uint16) float64 {
	exp, frac := int(v>>10)&0x1F, float64(v&0x3FF)
	var out float64
	switch exp {
	case 0:
		out = math.Ldexp(frac, -24)
	case 31:
		if frac != 0 {
			return math.NaN()
		}
		out = math.Inf(1)
	default:
		out = math.Ldexp(frac+1024, exp-25)
	}
	if v&0x8000 != 0 {
		return -out
	}
	return out
}

// jsonSafe replaces floats JSON cannot carry.
func jsonSafe(v float64) any {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return nil
	}
	return v
}

func (r *binReader) readList(n uint64, item func() (any, error)) (any, error) {
	if n > uint64(len(r.b)-r.i) {
		return nil, errTruncated
	}
	out := make([]any, 0, n)
	for k := uint64(0); k < n; k++ {
		v, err := item()
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

func (r *binReader) readMap(n uint64, item func() (any, error)) (any, error) {
	if n > uint64(len(r.b)-r.i) {
		return nil, errTruncated
	}
	out := make(map[string]any, n)
	for k := uint64(0); k < n; k++ {
		key, err := item()
		if err != nil {
			return nil, err
		}
		val, err := item()
		if err != nil {
			return nil, err
		}
		out[fmt.Sprint(key)] = val
	}
	return out, nil
}

func (r *binReader) cbor(depth int) (any, error) {
	if depth > maxNesting {
		return nil, errors.New("nested too deeply")
	}
	head, err := r.take(1)
	if err != nil {
		return nil, err
	}
	major, info := head[0]>>5, head[0]&0x1F
	next := func() (any, error) { return r.cbor(depth + 1) }
	var arg uint64
	indefinite := false
	switch {
	case info < 24:
		arg = uint64(info)
	case info <= 27:
		if arg, err = r.uintN(1 << (info - 24)); err != nil {
			return nil, err
		}
	case info == 31 && (major == 4 || major == 5):
		indefinite = true
	case info == 31:
		return nil, errors.New("chunked strings are not supported")
	default:
		return nil, errors.New("reserved value")
	}
	switch major {
	case 0:
		return float64(arg), nil
	case 1:
		return -1 - float64(arg), nil
	case 2:
		p, err := r.take(arg)
		if err != nil {
			return nil, err
		}
		return hexUpper(p), nil
	case 3:
		p, err := r.take(arg)
		if err != nil {
			return nil, err
		}
		return string(p), nil
	case 4:
		if !indefinite {
			return r.readList(arg, next)
		}
		out := []any{}
		for {
			if r.i >= len(r.b) {
				return nil, errTruncated
			}
			if r.b[r.i] == 0xFF {
				r.i++
				return out, nil
			}
			v, err := next()
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
	case 5:
		if !indefinite {
			return r.readMap(arg, next)
		}
		out := map[string]any{}
		for {
			if r.i >= len(r.b) {
				return nil, errTruncated
			}
			if r.b[r.i] == 0xFF {
				r.i++
				return out, nil
			}
			k, err := next()
			if err != nil {
				return nil, err
			}
			v, err := next()
			if err != nil {
				return nil, err
			}
			out[fmt.Sprint(k)] = v
		}
	case 6: // a tag only annotates the value after it
		return next()
	}
	switch info {
	case 20:
		return false, nil
	case 21:
		return true, nil
	case 22, 23:
		return nil, nil
	case 25:
		return jsonSafe(float16(uint16(arg))), nil
	case 26:
		return jsonSafe(float64(math.Float32frombits(uint32(arg)))), nil
	case 27:
		return jsonSafe(math.Float64frombits(arg)), nil
	}
	return float64(arg), nil
}

func (r *binReader) msgpack(depth int) (any, error) {
	if depth > maxNesting {
		return nil, errors.New("nested too deeply")
	}
	head, err := r.take(1)
	if err != nil {
		return nil, err
	}
	c := head[0]
	next := func() (any, error) { return r.msgpack(depth + 1) }
	text := func(n uint64) (any, error) {
		p, err := r.take(n)
		if err != nil {
			return nil, err
		}
		return string(p), nil
	}
	sized := func(size int, fn func(uint64) (any, error)) (any, error) {
		n, err := r.uintN(size)
		if err != nil {
			return nil, err
		}
		return fn(n)
	}
	list := func(n uint64) (any, error) { return r.readList(n, next) }
	dict := func(n uint64) (any, error) { return r.readMap(n, next) }
	binary_ := func(n uint64) (any, error) {
		p, err := r.take(n)
		if err != nil {
			return nil, err
		}
		return hexUpper(p), nil
	}
	switch {
	case c <= 0x7F:
		return float64(c), nil
	case c >= 0xE0:
		return float64(int8(c)), nil
	case c >= 0x80 && c <= 0x8F:
		return dict(uint64(c & 0x0F))
	case c >= 0x90 && c <= 0x9F:
		return list(uint64(c & 0x0F))
	case c >= 0xA0 && c <= 0xBF:
		return text(uint64(c & 0x1F))
	}
	switch c {
	case 0xC0:
		return nil, nil
	case 0xC2:
		return false, nil
	case 0xC3:
		return true, nil
	case 0xC4, 0xC5, 0xC6:
		return sized(1<<(c-0xC4), binary_)
	case 0xCA:
		p, err := r.take(4)
		if err != nil {
			return nil, err
		}
		return jsonSafe(float64(math.Float32frombits(binary.BigEndian.Uint32(p)))), nil
	case 0xCB:
		p, err := r.take(8)
		if err != nil {
			return nil, err
		}
		return jsonSafe(math.Float64frombits(binary.BigEndian.Uint64(p))), nil
	case 0xCC, 0xCD, 0xCE, 0xCF:
		n, err := r.uintN(1 << (c - 0xCC))
		if err != nil {
			return nil, err
		}
		return float64(n), nil
	case 0xD0, 0xD1, 0xD2, 0xD3:
		size := 1 << (c - 0xD0)
		n, err := r.uintN(size)
		if err != nil {
			return nil, err
		}
		return float64(signExtend(n, size*8)), nil
	case 0xD9, 0xDA, 0xDB:
		return sized(1<<(c-0xD9), text)
	case 0xDC, 0xDD:
		return sized(2<<(c-0xDC), list)
	case 0xDE, 0xDF:
		return sized(2<<(c-0xDE), dict)
	}
	return nil, fmt.Errorf("unsupported type 0x%02X", c)
}
