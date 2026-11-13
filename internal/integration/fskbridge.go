package integration

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"

	"github.com/nor24o/lorahub/internal/config"
)

// FSKOptions says how one packet from UDP goes on the air; zero values take the
// bridge settings. It mirrors processor.FSKSend.
type FSKOptions struct {
	Gateway   string  `json:"gateway"`
	Frequency float64 `json:"frequency"`
	BitRate   int     `json:"bit_rate"`
	Deviation int     `json:"deviation"`
	Power     int     `json:"power"`
	Preamble  int     `json:"preamble"`
}

// FSKBridge is the UDP side of the FSK bridge. Every FSK packet the gateways
// hear goes to settings.send_to; every datagram arriving on settings.listen
// goes on the air as FSK. In the json format a packet is
//
//	{"data_hex":"0102", "frequency":868.3, "bit_rate":50000, "gateways":[...]}   heard
//	{"data_hex":"0102", "frequency":868.3, "gateway":"..."}                     to send (all but data_hex optional)
//
// and in the raw format a datagram is just the packet's bytes.
type FSKBridge struct {
	log      *slog.Logger
	settings func() config.Settings
	send     func(data []byte, o FSKOptions) (string, error)

	mu     sync.Mutex
	listen string
	conn   *net.UDPConn
	out    *net.UDPConn
	outTo  string
	stats  FSKStats
}

// FSKStats are counters for the console.
type FSKStats struct {
	Listening  string `json:"listening"` // address actually bound, empty when off
	ListenErr  string `json:"listen_error,omitempty"`
	Forwarded  int    `json:"forwarded"` // FSK packets sent to send_to
	ForwardErr string `json:"forward_error,omitempty"`
	Received   int    `json:"received"` // datagrams on listen
	Sent       int    `json:"sent"`     // of those, put on the air
	SendErr    string `json:"send_error,omitempty"`
}

// NewFSKBridge makes the bridge; Apply opens its socket.
func NewFSKBridge(settings func() config.Settings, send func([]byte, FSKOptions) (string, error), log *slog.Logger) *FSKBridge {
	return &FSKBridge{log: log, settings: settings, send: send}
}

// Apply opens, moves or closes the listening socket to match the settings.
func (b *FSKBridge) Apply(s config.Settings) {
	want := ""
	if s.FSKBridge.Enabled {
		want = s.FSKBridge.Listen
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if want == b.listen && (want == "" || b.conn != nil) {
		return
	}
	if b.conn != nil {
		b.conn.Close()
		b.conn = nil
	}
	b.listen, b.stats.Listening, b.stats.ListenErr = want, "", ""
	if want == "" {
		return
	}
	addr, err := net.ResolveUDPAddr("udp", want)
	if err == nil {
		b.conn, err = net.ListenUDP("udp", addr)
	}
	if err != nil {
		b.stats.ListenErr = err.Error()
		b.log.Warn("FSK bridge: cannot listen", "addr", want, "err", err)
		return
	}
	b.stats.Listening = b.conn.LocalAddr().String()
	b.log.Info("FSK bridge listening", "addr", b.stats.Listening)
	go b.read(b.conn)
}

// Close stops the bridge.
func (b *FSKBridge) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.conn != nil {
		b.conn.Close()
		b.conn = nil
	}
	if b.out != nil {
		b.out.Close()
		b.out = nil
	}
	b.listen = ""
}

// Stats returns the counters.
func (b *FSKBridge) Stats() FSKStats {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.stats
}

// Up sends one FSK packet heard by the gateways to send_to.
func (b *FSKBridge) Up(data []byte, msg map[string]any) {
	s := b.settings().FSKBridge
	if !s.Enabled || s.SendTo == "" {
		return
	}
	out := data
	if s.Format != "raw" {
		out, _ = json.Marshal(msg)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.out == nil || b.outTo != s.SendTo {
		if b.out != nil {
			b.out.Close()
			b.out = nil
		}
		addr, err := net.ResolveUDPAddr("udp", s.SendTo)
		if err == nil {
			b.out, err = net.DialUDP("udp", nil, addr)
		}
		if err != nil {
			b.stats.ForwardErr = err.Error()
			return
		}
		b.outTo = s.SendTo
	}
	if _, err := b.out.Write(out); err != nil {
		b.stats.ForwardErr = err.Error()
		return
	}
	b.stats.Forwarded++
	b.stats.ForwardErr = ""
}

func (b *FSKBridge) read(conn *net.UDPConn) {
	buf := make([]byte, 2048)
	for {
		n, from, err := conn.ReadFromUDP(buf)
		if err != nil {
			return // closed by Apply or Close
		}
		data, o, err := parseFSKDatagram(buf[:n], b.settings().FSKBridge.Format)
		b.mu.Lock()
		b.stats.Received++
		b.mu.Unlock()
		if err == nil {
			_, err = b.send(data, o)
		}
		b.mu.Lock()
		if err != nil {
			b.stats.SendErr = err.Error()
			b.log.Warn("FSK bridge: not sent", "from", from, "err", err)
		} else {
			b.stats.Sent++
			b.stats.SendErr = ""
		}
		b.mu.Unlock()
	}
}

// parseFSKDatagram reads a datagram to send: the bytes themselves in the raw
// format, a JSON object with data_hex (and optional radio settings) in json.
func parseFSKDatagram(d []byte, format string) ([]byte, FSKOptions, error) {
	var o FSKOptions
	if format == "raw" {
		return append([]byte{}, d...), o, nil
	}
	var m struct {
		FSKOptions
		DataHex string `json:"data_hex"`
	}
	dec := json.NewDecoder(bytes.NewReader(d))
	if err := dec.Decode(&m); err != nil {
		return nil, o, fmt.Errorf("not a JSON object with data_hex: %v", err)
	}
	data, err := hex.DecodeString(strings.NewReplacer(" ", "", ":", "").Replace(m.DataHex))
	if err != nil || len(data) == 0 {
		return nil, o, fmt.Errorf("data_hex %q is not hex", m.DataHex)
	}
	return data, m.FSKOptions, nil
}
