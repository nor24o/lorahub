// Package ingest receives packets from gateways: Semtech UDP packet
// forwarders (satellites), raw_lora_app instances (Bobcat local engine) and
// upstream forwarders. All of them feed a Sink.
package ingest

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/nor24o/lorahub/internal/band"
	"github.com/nor24o/lorahub/internal/model"
)

// Sink consumes received packets and gateway status.
type Sink interface {
	// HandleUplink is called for each received packet.
	HandleUplink(pkt model.RxPacket)
	// HandleStatus is called for gateway status/keepalive. Returns false if
	// the gateway is unknown and not accepted.
	HandleStatus(eui, kind, addr string, status map[string]any) bool
	// HandleTxAck is called when a gateway acknowledges a downlink.
	HandleTxAck(eui string, ack map[string]any)
}

// Rxpk is the Semtech UDP rxpk object.
type Rxpk struct {
	Time  string          `json:"time,omitempty"`
	Tmms  *int64          `json:"tmms,omitempty"`
	Tmst  uint32          `json:"tmst"`
	Chan  int             `json:"chan"`
	RFCh  int             `json:"rfch"`
	Freq  float64         `json:"freq"`
	Stat  int             `json:"stat"`
	Modu  string          `json:"modu"`
	Datr  json.RawMessage `json:"datr"`
	Codr  string          `json:"codr,omitempty"`
	RSSI  float64         `json:"rssi"`
	RSSIS *float64        `json:"rssis,omitempty"`
	LSNR  float64         `json:"lsnr"`
	FOff  int             `json:"foff,omitempty"`
	Size  int             `json:"size"`
	Data  string          `json:"data"`
	RSig  []struct {
		Ant   int      `json:"ant"`
		Chan  int      `json:"chan"`
		RSSIC float64  `json:"rssic"`
		RSSIS *float64 `json:"rssis"`
		LSNR  float64  `json:"lsnr"`
		FOff  int      `json:"foff"`
	} `json:"rsig,omitempty"`
	MID    *int     `json:"mid,omitempty"`
	SNRMin *float64 `json:"snr_min,omitempty"`
	SNRMax *float64 `json:"snr_max,omitempty"`
}

// RxpkToPacket converts an rxpk into a model.RxPacket.
func RxpkToPacket(r Rxpk, eui, gatewayID, source string) (model.RxPacket, error) {
	payload, err := base64.StdEncoding.DecodeString(r.Data)
	if err != nil {
		payload, err = base64.RawStdEncoding.DecodeString(strings.TrimRight(r.Data, "="))
		if err != nil {
			return model.RxPacket{}, fmt.Errorf("bad base64 payload: %w", err)
		}
	}
	now := time.Now().UTC()
	p := model.RxPacket{Payload: payload, GatewayEUI: strings.ToUpper(eui)}
	p.Settings.Frequency = uint64(r.Freq*1e6 + 0.5)
	p.Settings.Modulation = strings.ToUpper(r.Modu)
	p.Settings.CodingRate = r.Codr
	var ds string
	if json.Unmarshal(r.Datr, &ds) == nil {
		p.Settings.DataRate = strings.ToUpper(ds)
		if dr, err := band.ParseDatr(ds); err == nil {
			p.Settings.SF, p.Settings.BW = dr.SF, dr.BW
		}
	} else {
		var br float64
		json.Unmarshal(r.Datr, &br)
		p.Settings.BitRate = int(br)
		p.Settings.DataRate = fmt.Sprintf("%d", int(br))
		if p.Settings.Modulation == "" {
			p.Settings.Modulation = "FSK"
		}
	}
	m := &p.Meta
	m.GatewayIDs = model.GatewayIDs{GatewayID: gatewayID, EUI: p.GatewayEUI}
	m.Source = source
	m.Timestamp = r.Tmst
	m.RSSI = r.RSSI
	m.ChannelRSSI = r.RSSI
	m.SignalRSSI = r.RSSIS
	m.SNR = r.LSNR
	m.FreqOffset = r.FOff
	m.Channel = r.Chan
	m.RFChain = r.RFCh
	m.ReceivedAt = now
	switch r.Stat {
	case 1:
		m.CRCStatus = "ok"
	case -1:
		m.CRCStatus = "bad"
	default:
		m.CRCStatus = "none"
	}
	if len(r.RSig) > 0 {
		s := r.RSig[0]
		m.ChannelRSSI, m.SNR, m.Channel, m.FreqOffset = s.RSSIC, s.LSNR, s.Chan, s.FOff
		m.RSSI = s.RSSIC
		if s.RSSIS != nil {
			m.SignalRSSI = s.RSSIS
		}
	}
	if r.Time != "" {
		if t, err := time.Parse(time.RFC3339Nano, r.Time); err == nil {
			t = t.UTC()
			m.Time = &t
		}
	}
	if r.MID != nil || r.SNRMin != nil {
		m.Extra = map[string]any{}
		if r.MID != nil {
			m.Extra["modem_id"] = *r.MID
		}
		if r.SNRMin != nil {
			m.Extra["snr_min"] = *r.SNRMin
		}
		if r.SNRMax != nil {
			m.Extra["snr_max"] = *r.SNRMax
		}
	}
	return p, nil
}

// PacketToRxpk converts back to an rxpk (used for upstream forwarding).
func PacketToRxpk(p model.RxPacket) map[string]any {
	stat := 0
	switch p.Meta.CRCStatus {
	case "ok":
		stat = 1
	case "bad":
		stat = -1
	}
	r := map[string]any{
		"tmst": p.Meta.Timestamp,
		"chan": p.Meta.Channel,
		"rfch": p.Meta.RFChain,
		"freq": float64(p.Settings.Frequency) / 1e6,
		"stat": stat,
		"modu": p.Settings.Modulation,
		"codr": p.Settings.CodingRate,
		"rssi": int(p.Meta.RSSI),
		"lsnr": p.Meta.SNR,
		"foff": p.Meta.FreqOffset,
		"size": len(p.Payload),
		"data": base64.StdEncoding.EncodeToString(p.Payload),
	}
	if p.Settings.Modulation == "FSK" {
		r["datr"] = p.Settings.BitRate
	} else {
		r["datr"] = p.Settings.DataRate
	}
	if p.Meta.Time != nil {
		r["time"] = p.Meta.Time.Format(time.RFC3339Nano)
	}
	return r
}

// TxToTxpk converts a TxPacket to a Semtech txpk object.
func TxToTxpk(t model.TxPacket) map[string]any {
	m := map[string]any{
		"imme": t.Immediate,
		"freq": float64(t.Frequency) / 1e6,
		"rfch": t.RFChain,
		"powe": t.Power,
		"modu": "LORA",
		"datr": t.DataRate,
		"codr": t.CodingRate,
		"ipol": t.InvertPol,
		"size": len(t.Payload),
		"data": base64.StdEncoding.EncodeToString(t.Payload),
		"ncrc": t.NoCRC,
	}
	if !t.Immediate {
		m["tmst"] = t.Timestamp
	}
	if t.Preamble > 0 {
		m["prea"] = t.Preamble
	}
	if t.CodingRate == "" {
		m["codr"] = "4/5"
	}
	return m
}
