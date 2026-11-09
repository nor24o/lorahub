package ingest

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net"
	"strings"
	"time"
)

// LinkVersion is the raw_lora_app link protocol this hub speaks. Gateways that
// report it get acknowledged uplinks, signed commands and confirmed downlinks.
// Older engines keep working with plain datagrams.
const LinkVersion = 2

// StubNonce is used for a first attempt when the current nonce is unknown; the
// engine answers auth_required with one that works.
const StubNonce = "00000000"

const (
	seenWindow  = 30 * time.Second
	txRetryWait = 120 * time.Millisecond
	txRetries   = 2
	pairEvery   = 20 * time.Second
)

// SignLink wraps a command for a gateway that shares the link key.
func SignLink(key, nonce string, payload []byte) []byte {
	m := hmac.New(sha256.New, []byte(key))
	m.Write([]byte(nonce + ":"))
	m.Write(payload)
	return []byte("L2:" + nonce + ":" + hex.EncodeToString(m.Sum(nil)) + ":" + string(payload))
}

// NewRequestID returns an identifier for one command.
func NewRequestID() string {
	b := make([]byte, 6)
	rand.Read(b)
	return hex.EncodeToString(b)
}

type linkState struct {
	version  int
	auth     bool
	nonce    string
	lastPair time.Time
}

type txWait struct {
	eui     string
	addr    *net.UDPAddr
	payload []byte
	tries   int
	timer   *time.Timer
}

func statusInt(st map[string]any, key string) int {
	v, _ := st[key].(float64)
	return int(v)
}

// ack confirms a datagram to the engine that sent it.
func (s *BobcatServer) ack(addr *net.UDPAddr, seq uint32) {
	b, _ := json.Marshal(map[string]any{"v": LinkVersion, "msg_type": "ack", "seq": seq})
	s.conn.WriteToUDP(b, addr)
}

// duplicate reports whether a datagram was already handled. A retransmission
// happens when an acknowledgement was lost.
func (s *BobcatServer) duplicate(key string) bool {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.seen == nil {
		s.seen = map[string]time.Time{}
	}
	if len(s.seen) > 4096 {
		for k, at := range s.seen {
			if now.Sub(at) > seenWindow {
				delete(s.seen, k)
			}
		}
	}
	if at, ok := s.seen[key]; ok && now.Sub(at) < seenWindow {
		return true
	}
	s.seen[key] = now
	return false
}

func (s *BobcatServer) linkOf(eui string) *linkState {
	if s.links == nil {
		s.links = map[string]*linkState{}
	}
	l := s.links[eui]
	if l == nil {
		l = &linkState{}
		s.links[eui] = l
	}
	return l
}

// noteStatus records a gateway's link state and pairs it when it has no key.
func (s *BobcatServer) noteStatus(eui, ip string, st map[string]any) {
	v := statusInt(st, "link")
	if v < LinkVersion {
		return
	}
	auth, _ := st["auth"].(bool)
	nonce, _ := st["nonce"].(string)
	now := time.Now()
	s.mu.Lock()
	l := s.linkOf(eui)
	l.version, l.auth = v, auth
	if nonce != "" {
		l.nonce = nonce
	}
	pair := !auth && now.Sub(l.lastPair) > pairEvery
	if pair {
		l.lastPair = now
	}
	s.mu.Unlock()
	if !pair || s.LinkKey == nil || (s.AutoPair != nil && !s.AutoPair()) {
		return
	}
	key := s.LinkKey()
	port := statusInt(st, "port_down")
	if key == "" || port == 0 {
		return
	}
	b, _ := json.Marshal(map[string]any{"v": LinkVersion, "cmd": "set_link_key", "key": key, "request_id": NewRequestID()})
	if _, err := s.conn.WriteToUDP(b, &net.UDPAddr{IP: net.ParseIP(ip), Port: port}); err == nil {
		s.log.Info("pairing gateway", "eui", eui)
	}
}

// wire returns the bytes to send to a gateway, signed when it requires that.
func (s *BobcatServer) wire(eui string, payload []byte) []byte {
	s.mu.Lock()
	l := s.links[eui]
	auth, nonce := l != nil && l.auth, ""
	if l != nil {
		nonce = l.nonce
	}
	s.mu.Unlock()
	if !auth || s.LinkKey == nil {
		return payload
	}
	key := s.LinkKey()
	if key == "" {
		return payload
	}
	if nonce == "" {
		nonce = StubNonce
	}
	return SignLink(key, nonce, payload)
}

func (s *BobcatServer) linked(eui string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	l := s.links[eui]
	return l != nil && l.version >= LinkVersion
}

// sendConfirmed transmits a downlink and repeats it until the engine confirms
// that it has it. The engine ignores a repeat of a request it already holds.
func (s *BobcatServer) sendConfirmed(eui string, addr *net.UDPAddr, msg map[string]any) error {
	id := NewRequestID()
	msg["request_id"] = id
	payload, _ := json.Marshal(msg)
	w := &txWait{eui: eui, addr: addr, payload: payload}
	s.mu.Lock()
	if s.waiting == nil {
		s.waiting = map[string]*txWait{}
	}
	s.waiting[id] = w
	w.timer = time.AfterFunc(txRetryWait, func() { s.retryTx(id) })
	s.mu.Unlock()
	_, err := s.conn.WriteToUDP(s.wire(eui, payload), addr)
	return err
}

func (s *BobcatServer) retryTx(id string) {
	s.mu.Lock()
	w := s.waiting[id]
	if w == nil {
		s.mu.Unlock()
		return
	}
	if w.tries >= txRetries {
		delete(s.waiting, id)
		s.mu.Unlock()
		s.sink.HandleTxAck(w.eui, map[string]any{"error": "the gateway did not confirm the downlink", "request_id": id})
		return
	}
	w.tries++
	w.timer = time.AfterFunc(txRetryWait, func() { s.retryTx(id) })
	s.mu.Unlock()
	s.conn.WriteToUDP(s.wire(w.eui, w.payload), w.addr)
}

// onTxAck handles the engine's report about a downlink.
func (s *BobcatServer) onTxAck(eui, id, status string) {
	s.mu.Lock()
	if w := s.waiting[id]; w != nil {
		w.timer.Stop()
		delete(s.waiting, id)
	}
	s.mu.Unlock()
	switch status {
	case "queued", "sent", "aborted":
	default:
		s.sink.HandleTxAck(eui, map[string]any{"error": txProblem(status), "request_id": id})
	}
}

func txProblem(status string) string {
	switch status {
	case "full":
		return "the gateway's transmit queue is full"
	case "busy":
		return "the gateway's radio stayed busy"
	case "bad_payload":
		return "the gateway could not read the payload"
	}
	return "the gateway could not transmit (" + status + ")"
}

// onAuthRequired takes the engine's fresh nonce and repeats what is waiting.
func (s *BobcatServer) onAuthRequired(eui, nonce string) {
	eui = strings.ToUpper(eui)
	s.mu.Lock()
	s.linkOf(eui).nonce = nonce
	var again []*txWait
	for _, w := range s.waiting {
		if w.eui == eui {
			again = append(again, w)
		}
	}
	s.mu.Unlock()
	for _, w := range again {
		s.conn.WriteToUDP(s.wire(w.eui, w.payload), w.addr)
	}
}
