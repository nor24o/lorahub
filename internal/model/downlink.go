package model

import (
	"fmt"
	"regexp"
)

var dataRateRe = regexp.MustCompile(`^SF([5-9]|1[0-2])BW(125|250|500)$`)

// Who a downlink is for, in bits 6-5 of its control byte.
const (
	ControlUnicast byte = 0x00
	ControlGroup   byte = 0x20
	ControlAll     byte = 0x40
)

// ControlByte builds the byte after SERIAL_ID in a downlink: bit 7 marks it as
// coming from the hub, bits 6-5 are the audience, bit 4 asks the node to
// confirm and bits 3-0 are a sequence number for spotting repeats.
func ControlByte(audience byte, confirm bool, seq byte) byte {
	b := 0x80 | audience | seq&0x0F
	if confirm {
		b |= 0x10
	}
	return b
}

// NodeControlOn reports whether a node's downlinks carry the control byte.
func (a *Application) NodeControlOn(d *Device) bool {
	if d.RawDownlink != nil && d.RawDownlink.Control != "" {
		return d.RawDownlink.Control == "on"
	}
	return a.DownlinkControl
}

// NodeHeaderOn reports whether NODE_ID+SERIAL goes in front of a node's
// downlinks. The control byte always comes with it: saying "this node only"
// means nothing without the address of that node.
func (a *Application) NodeHeaderOn(d *Device) bool {
	if a.NodeControlOn(d) {
		return true
	}
	if d.RawDownlink != nil && d.RawDownlink.Header != "" {
		return d.RawDownlink.Header == "on"
	}
	return a.DownlinkHeader
}

// Validate checks the settings a node uses for its own downlinks.
func (r *RawDownlink) Validate() error {
	switch r.Mode {
	case "":
		r.Mode = "immediate"
	case "immediate", "after_uplink":
	default:
		return fmt.Errorf("when to send must be immediate or after_uplink")
	}
	if r.DelayMs < 0 || r.DelayMs > 16000 {
		return fmt.Errorf("delay after packet must be 0 to 16000 ms")
	}
	if r.Frequency != 0 && (r.Frequency < 150 || r.Frequency > 960) {
		return fmt.Errorf("frequency must be in MHz, between 150 and 960")
	}
	if r.DataRate != "" && !dataRateRe.MatchString(r.DataRate) {
		return fmt.Errorf("data rate must look like SF9BW125")
	}
	switch r.CodingRate {
	case "", "4/5", "4/6", "4/7", "4/8":
	default:
		return fmt.Errorf("coding rate must be 4/5, 4/6, 4/7 or 4/8")
	}
	if r.Power < 0 || r.Power > 27 {
		return fmt.Errorf("TX power must be 0 to 27 dBm")
	}
	if r.Preamble != 0 && (r.Preamble < 6 || r.Preamble > 65535) {
		return fmt.Errorf("preamble must be at least 6 symbols")
	}
	switch r.Header {
	case "", "on", "off":
	default:
		return fmt.Errorf("node header must be on, off or empty")
	}
	switch r.Control {
	case "", "on", "off":
	default:
		return fmt.Errorf("control byte must be on, off or empty")
	}
	return nil
}
