package formatter

import (
	"encoding/binary"
	"fmt"
	"math"
)

type lppType struct {
	name   string
	size   int
	scale  float64
	signed bool
}

var lppTypes = map[byte]lppType{
	0:   {"digital_input", 1, 1, false},
	1:   {"digital_output", 1, 1, false},
	2:   {"analog_input", 2, 0.01, true},
	3:   {"analog_output", 2, 0.01, true},
	100: {"generic", 4, 1, false},
	101: {"illuminance", 2, 1, false},
	102: {"presence", 1, 1, false},
	103: {"temperature", 2, 0.1, true},
	104: {"relative_humidity", 1, 0.5, false},
	113: {"accelerometer", 6, 0.001, true},
	115: {"barometric_pressure", 2, 0.1, false},
	116: {"voltage", 2, 0.01, false},
	117: {"current", 2, 0.001, false},
	118: {"frequency", 4, 1, false},
	120: {"percentage", 1, 1, false},
	121: {"altitude", 2, 1, true},
	125: {"concentration", 2, 1, false},
	128: {"power", 2, 1, false},
	130: {"distance", 4, 0.001, false},
	131: {"energy", 4, 0.001, false},
	132: {"direction", 2, 1, false},
	133: {"unix_time", 4, 1, false},
	134: {"gyrometer", 6, 0.01, true},
	135: {"colour", 3, 1, false},
	136: {"gps", 9, 0, true},
	142: {"switch", 1, 1, false},
}

func round(v float64) float64 { return math.Round(v*1e6) / 1e6 }

// DecodeCayenne decodes a Cayenne LPP payload into "<type>_<channel>" keys.
func DecodeCayenne(b []byte) (map[string]any, error) {
	out := map[string]any{}
	i := 0
	for i+2 <= len(b) {
		ch, t := b[i], b[i+1]
		lt, ok := lppTypes[t]
		if !ok {
			return out, fmt.Errorf("unknown LPP type %d at offset %d", t, i+1)
		}
		if i+2+lt.size > len(b) {
			return out, fmt.Errorf("truncated LPP %s at offset %d", lt.name, i)
		}
		d := b[i+2 : i+2+lt.size]
		key := fmt.Sprintf("%s_%d", lt.name, ch)
		switch t {
		case 113, 134:
			out[key] = map[string]any{
				"x": round(float64(int16(binary.BigEndian.Uint16(d[0:2]))) * lt.scale),
				"y": round(float64(int16(binary.BigEndian.Uint16(d[2:4]))) * lt.scale),
				"z": round(float64(int16(binary.BigEndian.Uint16(d[4:6]))) * lt.scale),
			}
		case 136:
			s24 := func(x []byte) float64 {
				v := int32(uint32(x[0])<<16 | uint32(x[1])<<8 | uint32(x[2]))
				if v&0x800000 != 0 {
					v -= 0x1000000
				}
				return float64(v)
			}
			out[key] = map[string]any{
				"latitude":  round(s24(d[0:3]) * 0.0001),
				"longitude": round(s24(d[3:6]) * 0.0001),
				"altitude":  round(s24(d[6:9]) * 0.01),
			}
		case 135:
			out[key] = map[string]any{"r": d[0], "g": d[1], "b": d[2]}
		default:
			var v uint64
			for _, x := range d {
				v = v<<8 | uint64(x)
			}
			var f float64
			if lt.signed {
				shift := 64 - 8*uint(lt.size)
				f = float64(int64(v<<shift) >> shift)
			} else {
				f = float64(v)
			}
			out[key] = round(f * lt.scale)
		}
		i += 2 + lt.size
	}
	return out, nil
}
