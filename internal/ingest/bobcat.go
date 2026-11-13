package ingest

import (
	"encoding/base64"
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

// BobcatServer receives the native protocol of raw_lora_app (the SX1302
// engine of the Bobcat firmware): raw {"rxpk":[...]} JSON datagrams plus
// binary heartbeat/boot packets and {"msg_type":"gateway_status"} JSON.
// Several raw_lora_app instances (local + remote Bobcats) can point at the
// same LoRaHub; each is identified by its source IP.
type BobcatServer struct {
	conn         *net.UDPConn
	sink         Sink
	log          *slog.Logger
	downlinkPort int

	mu      sync.Mutex
	byIP    map[string]string // ip -> eui
	addrOf  map[string]string // eui -> ip
	pending map[string][]pendingRx
	static  map[string]string
	resolve func(ip string) string

	// ResolveAddr returns the last known IP of a gateway EUI (from the
	// database) so downlinks work right after a LoRaHub restart.
	ResolveAddr func(eui string) string
	// ResolvePort returns a gateway's own downlink port; 0 means the default.
	ResolvePort func(eui string) int
	// LinkKey returns the key shared with paired gateways.
	LinkKey func() string
	// AutoPair reports whether unpaired gateways receive the key.
	AutoPair func() bool

	links   map[string]*linkState
	seen    map[string]time.Time
	waiting map[string]*txWait
}

type pendingRx struct {
	r  Rxpk
	at time.Time
}

const (
	bobcatHeartbeat = 0x02
	bobcatBoot      = 0x03
)

// NewBobcatServer listens on addr. resolve is consulted for unknown IPs
// (e.g. last known address persisted in the database).
func NewBobcatServer(addr string, downlinkPort int, static map[string]string, resolve func(ip string) string, sink Sink, log *slog.Logger) (*BobcatServer, error) {
	ua, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return nil, err
	}
	c, err := net.ListenUDP("udp", ua)
	if err != nil {
		return nil, err
	}
	s := &BobcatServer{conn: c, sink: sink, log: log, downlinkPort: downlinkPort,
		byIP: map[string]string{}, addrOf: map[string]string{}, pending: map[string][]pendingRx{},
		static: map[string]string{}, resolve: resolve}
	for ip, eui := range static {
		s.static[ip] = strings.ToUpper(eui)
	}
	go s.loop()
	return s, nil
}

// Close stops the server.
func (s *BobcatServer) Close() error { return s.conn.Close() }

// LocalAddr is the address the server listens on.
func (s *BobcatServer) LocalAddr() net.Addr { return s.conn.LocalAddr() }

func (s *BobcatServer) loop() {
	buf := make([]byte, 65535)
	for {
		n, addr, err := s.conn.ReadFromUDP(buf)
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			continue
		}
		if n == 0 {
			continue
		}
		b := make([]byte, n)
		copy(b, buf[:n])
		s.handle(b, addr)
	}
}

func (s *BobcatServer) learn(ip, eui string) {
	eui = strings.ToUpper(eui)
	s.mu.Lock()
	s.byIP[ip] = eui
	s.addrOf[eui] = ip
	pend := s.pending[ip]
	delete(s.pending, ip)
	s.mu.Unlock()
	for _, p := range pend {
		if time.Since(p.at) < 30*time.Second {
			s.emit(p.r, eui)
		}
	}
}

func (s *BobcatServer) euiFor(ip string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.byIP[ip]; ok {
		return e
	}
	if e, ok := s.static[ip]; ok {
		s.byIP[ip], s.addrOf[e] = e, ip
		return e
	}
	if s.resolve != nil {
		if e := s.resolve(ip); e != "" {
			s.byIP[ip], s.addrOf[e] = e, ip
			return e
		}
	}
	return ""
}

func (s *BobcatServer) handle(b []byte, addr *net.UDPAddr) {
	ip := addr.IP.String()
	switch {
	case b[0] == '{':
		var probe struct {
			V             int             `json:"v"`
			MsgType       string          `json:"msg_type"`
			GatewayID     string          `json:"gateway_id"`
			Seq           uint32          `json:"seq"`
			Boot          uint32          `json:"boot"`
			RequestID     string          `json:"request_id"`
			Status        string          `json:"status"`
			Nonce         string          `json:"nonce"`
			GatewayStatus map[string]any  `json:"gateway_status"`
			Rxpk          []Rxpk          `json:"rxpk"`
			Stat          json.RawMessage `json:"stat"`
		}
		if err := json.Unmarshal(b, &probe); err != nil {
			return
		}
		gatewayID := strings.ToUpper(probe.GatewayID)
		if probe.V >= LinkVersion && (probe.MsgType == "uplink" || probe.MsgType == "gateway_status") {
			s.ack(addr, probe.Seq)
			if s.duplicate(fmt.Sprintf("%s/%d/%d", gatewayID, probe.Boot, probe.Seq)) {
				return
			}
		}
		switch probe.MsgType {
		case "tx_ack":
			s.onTxAck(gatewayID, probe.RequestID, probe.Status)
			return
		case "auth_required":
			s.onAuthRequired(gatewayID, probe.Nonce)
			return
		}
		if probe.MsgType == "gateway_status" && probe.GatewayStatus != nil {
			eui, _ := probe.GatewayStatus["eui"].(string)
			if eui == "" || strings.Trim(eui, "0") == "" {
				return
			}
			eui = strings.ToUpper(eui)
			s.learn(ip, eui)
			s.noteStatus(eui, ip, probe.GatewayStatus)
			s.sink.HandleStatus(eui, "bobcat", ip, probe.GatewayStatus)
			return
		}
		if len(probe.Rxpk) == 0 {
			return
		}
		eui := gatewayID
		if len(eui) == 16 && strings.Trim(eui, "0") != "" {
			s.learn(ip, eui)
		} else {
			eui = s.euiFor(ip)
		}
		if eui == "" {
			s.mu.Lock()
			if len(s.pending[ip]) < 64 {
				for _, r := range probe.Rxpk {
					s.pending[ip] = append(s.pending[ip], pendingRx{r, time.Now()})
				}
			}
			s.mu.Unlock()
			s.log.Info("bobcat: uplink from unannounced raw_lora_app, waiting for heartbeat", "ip", ip)
			return
		}
		for _, r := range probe.Rxpk {
			s.emit(r, eui)
		}
	case b[0] == bobcatHeartbeat && len(b) >= 43:
		eui := fmt.Sprintf("%016X", binary.LittleEndian.Uint64(b[1:9]))
		if strings.Trim(eui, "0") == "" {
			return
		}
		s.learn(ip, eui)
		st := map[string]any{
			"uptime_s":       binary.LittleEndian.Uint32(b[9:13]),
			"rx":             binary.LittleEndian.Uint32(b[13:17]),
			"tx":             binary.LittleEndian.Uint32(b[17:21]),
			"crc_err":        binary.LittleEndian.Uint32(b[21:25]),
			"tx_err":         binary.LittleEndian.Uint32(b[25:29]),
			"tx_queue_drop":  binary.LittleEndian.Uint32(b[29:33]),
			"tx_queue_depth": b[33],
			"sync_word":      fmt.Sprintf("0x%02X", b[34]),
			"rf0_freq":       binary.LittleEndian.Uint32(b[35:39]),
			"rf1_freq":       binary.LittleEndian.Uint32(b[39:43]),
			"heartbeat":      true,
		}
		s.sink.HandleStatus(eui, "bobcat", ip, st)
	case b[0] == bobcatBoot && len(b) >= 9:
		eui := fmt.Sprintf("%016X", binary.LittleEndian.Uint64(b[1:9]))
		if strings.Trim(eui, "0") == "" {
			return
		}
		hal := ""
		if len(b) > 9 {
			hal = strings.TrimRight(string(b[9:]), "\x00")
		}
		s.learn(ip, eui)
		s.sink.HandleStatus(eui, "bobcat", ip, map[string]any{"boot": true, "hal_version": hal})
	}
}

func (s *BobcatServer) emit(r Rxpk, eui string) {
	p, err := RxpkToPacket(r, eui, "", "bobcat")
	if err != nil {
		return
	}
	s.sink.HandleUplink(p)
}

func (s *BobcatServer) addrFor(eui string) (string, bool) {
	eui = strings.ToUpper(eui)
	s.mu.Lock()
	ip, ok := s.addrOf[eui]
	s.mu.Unlock()
	if !ok && s.ResolveAddr != nil {
		if ip = s.ResolveAddr(eui); ip != "" && net.ParseIP(ip) != nil {
			return ip, true
		}
	}
	return ip, ok
}

// CanSend reports whether the gateway's address is known.
func (s *BobcatServer) CanSend(eui string) bool {
	_, ok := s.addrFor(eui)
	return ok
}

// Send transmits a downlink through raw_lora_app's flat-JSON downlink port.
func (s *BobcatServer) Send(eui string, tx model.TxPacket) error {
	ip, ok := s.addrFor(eui)
	if !ok {
		return fmt.Errorf("no address known for bobcat gateway %s", eui)
	}
	m := map[string]any{
		"imme": tx.Immediate,
		"freq": float64(tx.Frequency) / 1e6,
		"rfch": tx.RFChain,
		"powe": tx.Power,
		"modu": "LORA",
		"datr": tx.DataRate,
		"codr": tx.CodingRate,
		"ipol": tx.InvertPol,
		"ncrc": tx.NoCRC,
		"data": base64.StdEncoding.EncodeToString(tx.Payload),
	}
	if tx.CodingRate == "" {
		m["codr"] = "4/5"
	}
	if !tx.Immediate {
		m["tmst"] = tx.Timestamp
	}
	if tx.Preamble > 0 {
		m["prea"] = tx.Preamble
	}
	if tx.NoHeader {
		m["nhdr"] = true
	}
	setFSK(m, tx, true) // raw_lora_app reads the FSK bit rate as text
	port := s.downlinkPort
	if s.ResolvePort != nil {
		if p := s.ResolvePort(eui); p > 0 {
			port = p
		}
	}
	ua := &net.UDPAddr{IP: net.ParseIP(ip), Port: port}
	eui = strings.ToUpper(eui)
	if s.linked(eui) {
		return s.sendConfirmed(eui, ua, m)
	}
	b, _ := json.Marshal(m)
	_, err := s.conn.WriteToUDP(b, ua)
	return err
}

func decodeB64(s string) ([]byte, error) {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return base64.RawStdEncoding.DecodeString(strings.TrimRight(s, "="))
	}
	return b, nil
}
