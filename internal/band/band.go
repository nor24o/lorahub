// Package band contains the minimal regional parameters needed to schedule
// class A downlinks (RX1/RX2) and compute time on air.
package band

import (
	"fmt"
	"math"
	"strings"
)

// DataRate is a LoRa modulation setting.
type DataRate struct {
	SF int `json:"spreading_factor"`
	BW int `json:"bandwidth"` // kHz
}

func (d DataRate) String() string { return fmt.Sprintf("SF%dBW%d", d.SF, d.BW) }

// ParseDatr parses "SF7BW125".
func ParseDatr(s string) (DataRate, error) {
	var d DataRate
	_, err := fmt.Sscanf(strings.ToUpper(s), "SF%dBW%d", &d.SF, &d.BW)
	return d, err
}

// Band describes a region.
type Band struct {
	Name         string           `json:"name"`
	RX2Freq      uint64           `json:"rx2_frequency"`
	RX2DR        int              `json:"rx2_dr"`
	RX2DataRate  DataRate         `json:"rx2_data_rate"`
	RX1Power     int              `json:"rx1_power"`
	RX2Power     int              `json:"rx2_power"`
	UplinkDRs    []DataRate       `json:"-"`
	DownlinkDRs  map[int]DataRate `json:"-"`
	JoinDelay1   int              `json:"join_accept_delay1_s"`
	ReceiveDelay int              `json:"receive_delay1_s"`
	MaxEIRP      int              `json:"max_eirp"`
}

var bands = map[string]*Band{
	"EU868": {
		Name: "EU868", RX2Freq: 869525000, RX2DR: 0, RX2DataRate: DataRate{12, 125},
		RX1Power: 14, RX2Power: 27, JoinDelay1: 5, ReceiveDelay: 1, MaxEIRP: 16,
		UplinkDRs: []DataRate{{12, 125}, {11, 125}, {10, 125}, {9, 125}, {8, 125}, {7, 125}, {7, 250}},
	},
	"US915": {
		Name: "US915", RX2Freq: 923300000, RX2DR: 8, RX2DataRate: DataRate{12, 500},
		RX1Power: 20, RX2Power: 20, JoinDelay1: 5, ReceiveDelay: 1, MaxEIRP: 30,
		UplinkDRs: []DataRate{{10, 125}, {9, 125}, {8, 125}, {7, 125}, {8, 500}},
	},
	"AU915": {
		Name: "AU915", RX2Freq: 923300000, RX2DR: 8, RX2DataRate: DataRate{12, 500},
		RX1Power: 20, RX2Power: 20, JoinDelay1: 5, ReceiveDelay: 1, MaxEIRP: 30,
		UplinkDRs: []DataRate{{12, 125}, {11, 125}, {10, 125}, {9, 125}, {8, 125}, {7, 125}, {8, 500}},
	},
	"AS923": {
		Name: "AS923", RX2Freq: 923200000, RX2DR: 2, RX2DataRate: DataRate{10, 125},
		RX1Power: 14, RX2Power: 14, JoinDelay1: 5, ReceiveDelay: 1, MaxEIRP: 16,
		UplinkDRs: []DataRate{{12, 125}, {11, 125}, {10, 125}, {9, 125}, {8, 125}, {7, 125}, {7, 250}},
	},
	"IN865": {
		Name: "IN865", RX2Freq: 866550000, RX2DR: 2, RX2DataRate: DataRate{10, 125},
		RX1Power: 20, RX2Power: 20, JoinDelay1: 5, ReceiveDelay: 1, MaxEIRP: 30,
		UplinkDRs: []DataRate{{12, 125}, {11, 125}, {10, 125}, {9, 125}, {8, 125}, {7, 125}},
	},
}

// Get returns a band by name (defaults to EU868).
func Get(name string) *Band {
	if b, ok := bands[strings.ToUpper(name)]; ok {
		return b
	}
	return bands["EU868"]
}

// Names lists supported bands.
func Names() []string { return []string{"EU868", "US915", "AU915", "AS923", "IN865"} }

// RX1 returns the RX1 frequency and data rate for an uplink (RX1DROffset 0).
func (b *Band) RX1(upFreq uint64, up DataRate) (uint64, DataRate) {
	switch b.Name {
	case "US915", "AU915":
		var ch int
		base125, base500 := 902300000.0, 903000000.0
		if b.Name == "AU915" {
			base125, base500 = 915200000.0, 915900000.0
		}
		if up.BW == 500 {
			ch = 64 + int(math.Round((float64(upFreq)-base500)/1600000))
		} else {
			ch = int(math.Round((float64(upFreq) - base125) / 200000))
		}
		f := uint64(923300000 + (ch%8)*600000)
		dr := DataRate{SF: up.SF, BW: 500}
		if up.BW == 500 {
			dr.SF = 7
		}
		if b.Name == "AU915" && up.BW == 125 && up.SF > 10 {
			dr.SF = 12 // AU915 DR0/DR1 map to DR8 (SF12BW500)
		}
		return f, dr
	default:
		return upFreq, up
	}
}

// AirtimeMs computes LoRa time on air in milliseconds.
func AirtimeMs(payloadLen int, dr DataRate, codingRate string, preamble int, explicitHeader, crc bool) float64 {
	if dr.SF == 0 || dr.BW == 0 {
		return 0
	}
	cr := 1
	fmt.Sscanf(codingRate, "4/%d", &cr)
	if cr >= 5 {
		cr -= 4
	} else {
		cr = 1
	}
	if preamble == 0 {
		preamble = 8
	}
	tSym := math.Pow(2, float64(dr.SF)) / float64(dr.BW*1000) * 1000
	de := 0.0
	if tSym > 16 {
		de = 1
	}
	h := 0.0
	if !explicitHeader {
		h = 1
	}
	c := 0.0
	if crc {
		c = 1
	}
	num := 8*float64(payloadLen) - 4*float64(dr.SF) + 28 + 16*c - 20*h
	den := 4 * (float64(dr.SF) - 2*de)
	nPayload := 8 + math.Max(math.Ceil(num/den)*float64(cr+4), 0)
	tPreamble := (float64(preamble) + 4.25) * tSym
	return tPreamble + nPayload*tSym
}
