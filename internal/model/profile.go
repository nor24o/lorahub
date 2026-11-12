package model

import "fmt"

// NodeProfile is what a node's firmware says about itself in the bytes after
// SERIAL_ID, for groups with NodeProfile turned on.
//
//	profile byte   bit 7-6  class: 00 A, 01 B, 10 C, 11 reserved
//	               bit 5    1 = the node listens with inverted IQ
//	               bit 4    1 = a downlink radio block follows
//	               bit 3    1 = this packet is a description of the node's commands (describe.go)
//	               bit 2-0  free for the firmware, reported as "user"
//	radio block    byte 0-2 downlink frequency in 100 Hz, MSB first, 0 = same as uplink
//	               byte 3   bit 7-4 SF 5..12, 0 = same as uplink
//	                        bit 3-2 bandwidth: 0 125, 1 250, 2 500 kHz
//	                        bit 1-0 coding rate: 0 4/5, 1 4/6, 2 4/7, 3 4/8
//
// Uplink radio settings are not declared: every packet's are measured on reception.
type NodeProfile struct {
	Class      string  `json:"class"`
	InvertIQ   bool    `json:"invert_iq"`
	Describe   bool    `json:"describe,omitempty"` // the packet describes the node's commands
	User       int     `json:"user"`
	Frequency  float64 `json:"frequency,omitempty"`   // MHz
	DataRate   string  `json:"data_rate,omitempty"`   // empty = same as uplink
	CodingRate string  `json:"coding_rate,omitempty"` // empty = no radio block
}

// ParseNodeProfile reads a profile from the start of b and returns how many
// bytes it took.
func ParseNodeProfile(b []byte) (*NodeProfile, int, error) {
	if len(b) == 0 {
		return nil, 0, fmt.Errorf("packet ends before the profile byte")
	}
	class := b[0] >> 6
	if class == 3 {
		return nil, 0, fmt.Errorf("profile byte %02X uses the reserved class", b[0])
	}
	p := &NodeProfile{Class: string("ABC"[class]), InvertIQ: b[0]&0x20 != 0, Describe: b[0]&DescribeFlag != 0, User: int(b[0] & 0x07)}
	if b[0]&0x10 == 0 {
		return p, 1, nil
	}
	if len(b) < 5 {
		return nil, 0, fmt.Errorf("packet ends inside the profile's radio block")
	}
	if raw := int(b[1])<<16 | int(b[2])<<8 | int(b[3]); raw != 0 {
		p.Frequency = float64(raw) / 1e4
		if p.Frequency < 150 || p.Frequency > 960 {
			return nil, 0, fmt.Errorf("profile frequency %g MHz is out of range", p.Frequency)
		}
	}
	sf, bw := int(b[4]>>4), int(b[4]>>2&3)
	if sf != 0 {
		if sf < 5 || sf > 12 || bw == 3 {
			return nil, 0, fmt.Errorf("profile radio byte %02X is not valid", b[4])
		}
		p.DataRate = fmt.Sprintf("SF%dBW%d", sf, 125<<bw)
	}
	p.CodingRate = fmt.Sprintf("4/%d", 5+int(b[4]&3))
	return p, 5, nil
}

// Apply overlays what the firmware declared on a node's downlink settings.
func (p *NodeProfile) Apply(cfg *RawDownlink) {
	cfg.Mode = "after_uplink" // class A, and class B whose ping slots LoRaHub does not schedule
	if p.Class == "C" {
		cfg.Mode = "immediate"
	}
	cfg.InvertPol = p.InvertIQ
	if p.Frequency != 0 {
		cfg.Frequency = p.Frequency
	}
	if p.DataRate != "" {
		cfg.DataRate = p.DataRate
	}
	if p.CodingRate != "" {
		cfg.CodingRate = p.CodingRate
	}
}
