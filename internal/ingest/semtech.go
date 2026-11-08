package ingest

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/nor24o/lorahub/internal/model"
)

// Semtech UDP packet identifiers.
const (
	pushData = 0x00
	pushAck  = 0x01
	pullData = 0x02
	pullResp = 0x03
	pullAck  = 0x04
	txAck    = 0x05
)

// SemtechServer implements the Semtech UDP packet forwarder protocol
// (server side) so any standard LoRa gateway can act as a satellite.
type SemtechServer struct {
	conn *net.UDPConn
	sink Sink
	log  *slog.Logger

	mu    sync.Mutex
	pull  map[string]*net.UDPAddr // eui -> PULL_DATA address
	ver   map[string]byte
	token uint16
}

// NewSemtechServer starts listening on addr.
func NewSemtechServer(addr string, sink Sink, log *slog.Logger) (*SemtechServer, error) {
	ua, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return nil, err
	}
	c, err := net.ListenUDP("udp", ua)
	if err != nil {
		return nil, err
	}
	s := &SemtechServer{conn: c, sink: sink, log: log, pull: map[string]*net.UDPAddr{}, ver: map[string]byte{}}
	go s.loop()
	return s, nil
}

// Close stops the server.
func (s *SemtechServer) Close() error { return s.conn.Close() }

func (s *SemtechServer) loop() {
	buf := make([]byte, 65535)
	for {
		n, addr, err := s.conn.ReadFromUDP(buf)
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			continue
		}
		if n < 4 {
			continue
		}
		pkt := make([]byte, n)
		copy(pkt, buf[:n])
		s.handle(pkt, addr)
	}
}

func (s *SemtechServer) handle(b []byte, addr *net.UDPAddr) {
	ver, token, typ := b[0], b[1:3], b[3]
	if ver != 1 && ver != 2 {
		return
	}
	switch typ {
	case pushData:
		if len(b) < 12 {
			return
		}
		eui := fmt.Sprintf("%016X", binary.BigEndian.Uint64(b[4:12]))
		s.conn.WriteToUDP([]byte{ver, token[0], token[1], pushAck}, addr)
		var body struct {
			Rxpk []Rxpk         `json:"rxpk"`
			Stat map[string]any `json:"stat"`
		}
		if err := json.Unmarshal(b[12:], &body); err != nil {
			s.log.Debug("semtech: bad PUSH_DATA json", "eui", eui, "err", err)
			return
		}
		if !s.sink.HandleStatus(eui, "semtech_udp", addr.String(), body.Stat) {
			return
		}
		for _, r := range body.Rxpk {
			p, err := RxpkToPacket(r, eui, "", "semtech_udp")
			if err != nil {
				continue
			}
			s.sink.HandleUplink(p)
		}
	case pullData:
		if len(b) < 12 {
			return
		}
		eui := fmt.Sprintf("%016X", binary.BigEndian.Uint64(b[4:12]))
		s.conn.WriteToUDP([]byte{ver, token[0], token[1], pullAck}, addr)
		s.mu.Lock()
		s.pull[eui] = addr
		s.ver[eui] = ver
		s.mu.Unlock()
		s.sink.HandleStatus(eui, "semtech_udp", addr.String(), nil)
	case txAck:
		if len(b) < 12 {
			return
		}
		eui := fmt.Sprintf("%016X", binary.BigEndian.Uint64(b[4:12]))
		ack := map[string]any{}
		if len(b) > 12 {
			var body struct {
				TxpkAck map[string]any `json:"txpk_ack"`
			}
			if json.Unmarshal(b[12:], &body) == nil && body.TxpkAck != nil {
				ack = body.TxpkAck
			}
		}
		s.sink.HandleTxAck(eui, ack)
	}
}

// CanSend reports whether a PULL_DATA address is known for eui.
func (s *SemtechServer) CanSend(eui string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.pull[strings.ToUpper(eui)]
	return ok
}

// Send sends a downlink via PULL_RESP.
func (s *SemtechServer) Send(eui string, tx model.TxPacket) error {
	eui = strings.ToUpper(eui)
	s.mu.Lock()
	addr, ok := s.pull[eui]
	ver := s.ver[eui]
	s.token++
	tok := s.token
	s.mu.Unlock()
	if !ok {
		return fmt.Errorf("gateway %s has no downlink path (no PULL_DATA received)", eui)
	}
	body, _ := json.Marshal(map[string]any{"txpk": TxToTxpk(tx)})
	pkt := append([]byte{ver, byte(tok >> 8), byte(tok), pullResp}, body...)
	_, err := s.conn.WriteToUDP(pkt, addr)
	return err
}

// Forwarder is a Semtech UDP client that forwards one gateway's traffic
// upstream (to TTN, ChirpStack or another LoRaHub).
type Forwarder struct {
	EUI     string
	Addr    string
	conn    *net.UDPConn
	onDown  func(model.TxPacket)
	stop    chan struct{}
	log     *slog.Logger
	mu      sync.Mutex
	token   uint16
	LastAck time.Time
}

// NewForwarder dials an upstream server. onDown is called for PULL_RESP if
// non-nil.
func NewForwarder(eui, addr string, onDown func(model.TxPacket), log *slog.Logger) (*Forwarder, error) {
	ua, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return nil, err
	}
	c, err := net.DialUDP("udp", nil, ua)
	if err != nil {
		return nil, err
	}
	f := &Forwarder{EUI: strings.ToUpper(eui), Addr: addr, conn: c, onDown: onDown, stop: make(chan struct{}), log: log}
	go f.readLoop()
	go f.keepalive()
	return f, nil
}

func (f *Forwarder) header(typ byte) []byte {
	f.mu.Lock()
	f.token++
	t := f.token
	f.mu.Unlock()
	h := []byte{2, byte(t >> 8), byte(t), typ, 0, 0, 0, 0, 0, 0, 0, 0}
	var e uint64
	fmt.Sscanf(f.EUI, "%016X", &e)
	binary.BigEndian.PutUint64(h[4:], e)
	return h
}

func (f *Forwarder) keepalive() {
	t := time.NewTicker(10 * time.Second)
	defer t.Stop()
	f.conn.Write(f.header(pullData))
	for {
		select {
		case <-f.stop:
			return
		case <-t.C:
			f.conn.Write(f.header(pullData))
		}
	}
}

func (f *Forwarder) readLoop() {
	buf := make([]byte, 65535)
	for {
		n, err := f.conn.Read(buf)
		if err != nil {
			select {
			case <-f.stop:
				return
			default:
			}
			if errors.Is(err, net.ErrClosed) {
				return
			}
			time.Sleep(time.Second)
			continue
		}
		if n < 4 {
			continue
		}
		switch buf[3] {
		case pushAck, pullAck:
			f.mu.Lock()
			f.LastAck = time.Now()
			f.mu.Unlock()
		case pullResp:
			var body struct {
				Txpk struct {
					Imme bool    `json:"imme"`
					Tmst uint32  `json:"tmst"`
					Freq float64 `json:"freq"`
					RFCh int     `json:"rfch"`
					Powe int     `json:"powe"`
					Datr string  `json:"datr"`
					Codr string  `json:"codr"`
					IPol bool    `json:"ipol"`
					Data string  `json:"data"`
					NCRC bool    `json:"ncrc"`
					Prea int     `json:"prea"`
				} `json:"txpk"`
			}
			if json.Unmarshal(buf[4:n], &body) != nil {
				continue
			}
			ack := f.header(txAck)
			ack[1], ack[2] = buf[1], buf[2]
			if f.onDown == nil {
				f.conn.Write(append(ack, []byte(`{"txpk_ack":{"error":"TX_FREQ"}}`)...))
				continue
			}
			tp := body.Txpk
			payload, err := decodeB64(tp.Data)
			if err != nil {
				continue
			}
			f.onDown(model.TxPacket{Payload: payload, Frequency: uint64(tp.Freq*1e6 + 0.5), DataRate: tp.Datr, CodingRate: tp.Codr,
				Power: tp.Powe, InvertPol: tp.IPol, Immediate: tp.Imme, Timestamp: tp.Tmst, RFChain: tp.RFCh, NoCRC: tp.NCRC, Preamble: tp.Prea})
			f.conn.Write(append(ack, []byte(`{"txpk_ack":{"error":"NONE"}}`)...))
		}
	}
}

// Push forwards an uplink and/or status upstream.
func (f *Forwarder) Push(rxpk []map[string]any, stat map[string]any) {
	body := map[string]any{}
	if len(rxpk) > 0 {
		body["rxpk"] = rxpk
	}
	if stat != nil {
		body["stat"] = stat
	}
	b, _ := json.Marshal(body)
	f.conn.Write(append(f.header(pushData), b...))
}

// Close stops the forwarder.
func (f *Forwarder) Close() {
	close(f.stop)
	f.conn.Close()
}

// Connected reports whether an ACK was received recently.
func (f *Forwarder) Connected() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return time.Since(f.LastAck) < 35*time.Second
}
