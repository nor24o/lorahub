package satpanel

// Packets the satellite's own radio heard. raw_lora_app on a Bobcat sends a
// copy of every datagram for the hub to 127.0.0.1:<port_up> as well, so the
// page sees each packet the moment it is received, also while the hub cannot
// be reached. Nothing is ever sent back: an answer would look like the hub's
// acknowledgement and the engine would stop sending the packet again.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// engineMaxTries is how often raw_lora_app sends an unacknowledged packet
// again before it gives up (LINK_MAX_TRIES, about 12.6 s in all).
const engineMaxTries = 6

// Packet is one packet the radio heard, as the engine reported it.
type Packet struct {
	ID         int64     `json:"id"`
	ReceivedAt time.Time `json:"received_at"`
	GatewayID  string    `json:"gateway_id,omitempty"`
	Seq        uint32    `json:"seq,omitempty"`
	Boot       uint32    `json:"boot,omitempty"`
	// Repeats counts the copies the engine sent again because the hub had not
	// acknowledged the packet in time; engineMaxTries means it never did.
	Repeats int      `json:"repeats"`
	Freq    float64  `json:"freq"` // MHz
	Modu    string   `json:"modu"`
	Datr    string   `json:"datr"`
	Codr    string   `json:"codr,omitempty"`
	RSSI    float64  `json:"rssi"`            // channel RSSI, as the hub shows it
	RSSIS   *float64 `json:"rssis,omitempty"` // signal RSSI
	SNR     float64  `json:"snr"`
	FOff    int      `json:"foff"`
	Chan    int      `json:"chan"`
	RFCh    int      `json:"rfch"`
	Tmst    uint32   `json:"tmst"`
	Time    string   `json:"time,omitempty"` // the engine's clock
	CRC     string   `json:"crc"`            // ok, bad or none
	Size    int      `json:"size"`
	DataHex string   `json:"data_hex"`

	rev   int64
	saved bool
}

// packetLog keeps the last packets in memory and in a file, so they survive a
// restart of the page or the board.
type packetLog struct {
	keep   int
	file   string // "" = memory only
	settle time.Duration
	now    func() time.Time

	mu      sync.Mutex
	list    []*Packet // oldest first
	nextID  int64
	rev     int64
	gen     int64 // changes when the list is cleared
	total   int64 // packets since the page started
	lines   int   // lines in the file
	listen  string
	err     string
	fileErr string
	heard   time.Time // anything from the engine, status messages included
}

func newPacketLog(file string, keep int, settle time.Duration, now func() time.Time) *packetLog {
	l := &packetLog{keep: keep, file: file, settle: settle, now: now, nextID: 1, gen: now().UnixNano()}
	l.load()
	return l
}

// load reads the file written before the last restart.
func (l *packetLog) load() {
	if l.file == "" {
		return
	}
	f, err := os.Open(l.file)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 64<<10)
	at := map[int64]int{} // a packet written again (its final repeat count) replaces the first line
	for sc.Scan() {
		var p Packet
		if json.Unmarshal(sc.Bytes(), &p) != nil || p.ID == 0 {
			continue
		}
		p.saved = true
		l.lines++
		if i, ok := at[p.ID]; ok {
			l.list[i] = &p
			continue
		}
		at[p.ID] = len(l.list)
		l.list = append(l.list, &p)
		if p.ID >= l.nextID {
			l.nextID = p.ID + 1
		}
	}
	if len(l.list) > l.keep {
		l.list = l.list[len(l.list)-l.keep:]
	}
	for _, p := range l.list {
		l.rev++
		p.rev = l.rev
	}
}

// datagram takes one datagram from the engine: uplinks are kept, repeats of a
// packet already kept are counted, anything else (status, heartbeats) ignored.
func (l *packetLog) datagram(b []byte) {
	b = bytes.TrimSpace(b)
	if len(b) == 0 {
		return
	}
	l.mu.Lock()
	l.heard = l.now()
	l.mu.Unlock()
	if b[0] != '{' {
		return
	}
	var m struct {
		MsgType   string            `json:"msg_type"`
		GatewayID string            `json:"gateway_id"`
		Seq       uint32            `json:"seq"`
		Boot      uint32            `json:"boot"`
		TsMS      int64             `json:"ts_ms"`
		Rxpk      []json.RawMessage `json:"rxpk"`
	}
	if json.Unmarshal(b, &m) != nil || len(m.Rxpk) == 0 || (m.MsgType != "" && m.MsgType != "uplink") {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if m.Seq != 0 {
		if p := l.recent(m.GatewayID, m.Boot, m.Seq); p != nil {
			if p.Repeats < engineMaxTries {
				p.Repeats++
				l.rev++
				p.rev = l.rev
			}
			return
		}
	}
	at := l.now()
	if m.TsMS > 0 {
		at = time.UnixMilli(m.TsMS)
	}
	for _, raw := range m.Rxpk {
		p, ok := parseRxpk(raw)
		if !ok {
			continue
		}
		p.ID, p.ReceivedAt, p.GatewayID, p.Seq, p.Boot = l.nextID, at.UTC(), m.GatewayID, m.Seq, m.Boot
		l.nextID++
		l.rev++
		p.rev = l.rev
		l.total++
		l.list = append(l.list, p)
	}
	if over := len(l.list) - l.keep; over > 0 {
		l.list = append([]*Packet(nil), l.list[over:]...)
	}
}

// recent finds a packet kept within the engine's resend time with the same
// sequence number. Called with mu held.
func (l *packetLog) recent(gw string, boot, seq uint32) *Packet {
	limit := l.now().Add(-2 * l.settle)
	for i := len(l.list) - 1; i >= 0 && i >= len(l.list)-64; i-- {
		p := l.list[i]
		if p.ReceivedAt.Before(limit) {
			break
		}
		if p.Seq == seq && p.Boot == boot && p.GatewayID == gw {
			return p
		}
	}
	return nil
}

func parseRxpk(raw json.RawMessage) (*Packet, bool) {
	var r struct {
		Time  string          `json:"time"`
		Tmst  uint32          `json:"tmst"`
		Chan  int             `json:"chan"`
		RFCh  int             `json:"rfch"`
		Freq  float64         `json:"freq"`
		Stat  int             `json:"stat"`
		Modu  string          `json:"modu"`
		Datr  json.RawMessage `json:"datr"`
		Codr  string          `json:"codr"`
		RSSI  float64         `json:"rssi"`
		RSSIS *float64        `json:"rssis"`
		LSNR  float64         `json:"lsnr"`
		FOff  int             `json:"foff"`
		Size  int             `json:"size"`
		Data  string          `json:"data"`
	}
	if json.Unmarshal(raw, &r) != nil {
		return nil, false
	}
	data, err := base64.StdEncoding.DecodeString(r.Data)
	if err != nil {
		data, err = base64.RawStdEncoding.DecodeString(r.Data)
		if err != nil {
			return nil, false
		}
	}
	datr := strings.Trim(string(r.Datr), `"`)
	crc := "none"
	switch r.Stat {
	case 1:
		crc = "ok"
	case -1:
		crc = "bad"
	}
	if r.Size == 0 {
		r.Size = len(data)
	}
	return &Packet{Freq: r.Freq, Modu: r.Modu, Datr: datr, Codr: r.Codr, RSSI: r.RSSI, RSSIS: r.RSSIS, SNR: r.LSNR,
		FOff: r.FOff, Chan: r.Chan, RFCh: r.RFCh, Tmst: r.Tmst, Time: r.Time, CRC: crc, Size: r.Size,
		DataHex: strings.ToUpper(hex.EncodeToString(data))}, true
}

// flush writes packets whose repeat count is final (the engine has stopped
// sending them) to the file; all of them when final is set.
func (l *packetLog) flush(final bool) {
	if l.file == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	cut := l.now().Add(-l.settle)
	var buf bytes.Buffer
	var done []*Packet
	for _, p := range l.list {
		if p.saved || (!final && p.ReceivedAt.After(cut)) {
			continue
		}
		b, _ := json.Marshal(p)
		buf.Write(b)
		buf.WriteByte('\n')
		done = append(done, p)
	}
	if len(done) == 0 {
		return
	}
	if l.lines+len(done) > 2*l.keep {
		l.rewrite(cut, final) // keeps the file at most twice the list
		return
	}
	if err := appendFile(l.file, buf.Bytes()); err != nil {
		l.fileErr = err.Error()
		return
	}
	l.fileErr = ""
	l.lines += len(done)
	for _, p := range done {
		p.saved = true
	}
}

// rewrite replaces the file with the list. Packets the engine may still send
// again stay unsaved and are written once more when final. Called with mu held.
func (l *packetLog) rewrite(cut time.Time, final bool) {
	var buf bytes.Buffer
	for _, p := range l.list {
		b, _ := json.Marshal(p)
		buf.Write(b)
		buf.WriteByte('\n')
	}
	if err := os.MkdirAll(filepath.Dir(l.file), 0o700); err != nil {
		l.fileErr = err.Error()
		return
	}
	tmp := l.file + ".tmp"
	if err := os.WriteFile(tmp, buf.Bytes(), 0o600); err != nil {
		l.fileErr = err.Error()
		return
	}
	if err := os.Rename(tmp, l.file); err != nil {
		l.fileErr = err.Error()
		return
	}
	l.fileErr = ""
	l.lines = len(l.list)
	for _, p := range l.list {
		p.saved = final || !p.ReceivedAt.After(cut)
	}
}

func appendFile(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func (l *packetLog) clear() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.list = nil
	l.gen = l.now().UnixNano()
	if l.file != "" {
		l.rewrite(l.now(), true)
	}
}

func (l *packetLog) setListen(addr, err string) {
	l.mu.Lock()
	l.listen, l.err = addr, err
	l.mu.Unlock()
}

type packetsView struct {
	Listening  string    `json:"listening,omitempty"`
	Error      string    `json:"error,omitempty"`
	FileError  string    `json:"file_error,omitempty"`
	Generation int64     `json:"generation"`
	Rev        int64     `json:"rev"`
	Total      int64     `json:"total"`
	Kept       int       `json:"kept"`
	Keep       int       `json:"keep"`
	MaxTries   int       `json:"max_tries"`
	Settle     float64   `json:"settle_s"`
	EngineSeen *float64  `json:"engine_seen_s,omitempty"` // seconds since anything came from the engine
	Packets    []*Packet `json:"packets"`
}

// since returns the packets added or changed after rev, oldest first; all of
// them when gen is not the current list's.
func (l *packetLog) since(gen, rev int64) packetsView {
	l.mu.Lock()
	defer l.mu.Unlock()
	if gen != l.gen {
		rev = 0
	}
	v := packetsView{Listening: l.listen, Error: l.err, FileError: l.fileErr, Generation: l.gen, Rev: l.rev,
		Total: l.total, Kept: len(l.list), Keep: l.keep, MaxTries: engineMaxTries, Settle: l.settle.Seconds(), Packets: []*Packet{}}
	if !l.heard.IsZero() {
		ago := l.now().Sub(l.heard).Seconds()
		v.EngineSeen = &ago
	}
	for _, p := range l.list {
		if p.rev > rev {
			c := *p
			v.Packets = append(v.Packets, &c)
		}
	}
	return v
}

func (l *packetLog) snapshot() []Packet {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]Packet, len(l.list))
	for i, p := range l.list {
		out[i] = *p
	}
	return out
}

// packetsAddr is where the engine sends its local copies: port_up on the
// loopback address.
func (s *Server) packetsAddr() string {
	if s.opt.PacketsListen != "" {
		return s.opt.PacketsListen
	}
	port := 5555
	if p, ok := s.loraConfig()["port_up"].(float64); ok && p > 0 {
		port = int(p)
	}
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
}

// packetsLoop listens for the engine's copies until ctx ends, and keeps
// trying when the port is taken (by LoRaHub, when a hub runs on this board).
func (s *Server) packetsLoop(ctx context.Context) {
	l := s.packets
	go func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				l.flush(true)
				return
			case <-t.C:
				l.flush(false)
			}
		}
	}()
	for ctx.Err() == nil {
		addr := s.packetsAddr()
		ua, err := net.ResolveUDPAddr("udp", addr)
		var c *net.UDPConn
		if err == nil {
			c, err = net.ListenUDP("udp", ua)
		}
		if err != nil {
			l.setListen("", fmt.Sprintf("cannot listen on %s for the radio engine's packets: %v", addr, err))
			select {
			case <-ctx.Done():
				return
			case <-time.After(30 * time.Second):
			}
			continue
		}
		l.setListen(c.LocalAddr().String(), "")
		stop := context.AfterFunc(ctx, func() { c.Close() })
		buf := make([]byte, 65535)
		for {
			n, _, err := c.ReadFromUDP(buf)
			if err != nil {
				break
			}
			l.datagram(buf[:n])
		}
		stop()
		c.Close()
	}
}

func (s *Server) getPackets(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	gen, _ := strconv.ParseInt(q.Get("generation"), 10, 64)
	rev, _ := strconv.ParseInt(q.Get("rev"), 10, 64)
	writeJSON(w, 200, s.packets.since(gen, rev))
}

func (s *Server) postPacketsClear(w http.ResponseWriter, r *http.Request) {
	s.packets.clear()
	s.log.Info("packet list cleared")
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// getPacketsDownload hands out the kept packets as CSV or JSON.
func (s *Server) getPacketsDownload(w http.ResponseWriter, r *http.Request) {
	list := s.packets.snapshot()
	name := "packets-" + s.system().Hostname + "-" + s.now().Format("20060102-150405")
	if r.URL.Query().Get("format") == "json" {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition", `attachment; filename="`+name+`.json"`)
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		enc.Encode(list)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`.csv"`)
	cw := csv.NewWriter(w)
	cw.Write([]string{"received_at", "frequency_mhz", "modulation", "data_rate", "coding_rate", "rssi_dbm", "snr_db",
		"size", "crc", "data_hex", "sent_again", "gateway_id", "seq", "boot", "chan", "rfch", "foff", "tmst"})
	for _, p := range list {
		f := func(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }
		cw.Write([]string{p.ReceivedAt.Format(time.RFC3339Nano), f(p.Freq), p.Modu, p.Datr, p.Codr, f(p.RSSI), f(p.SNR),
			strconv.Itoa(p.Size), p.CRC, p.DataHex, strconv.Itoa(p.Repeats), p.GatewayID, strconv.FormatUint(uint64(p.Seq), 10),
			strconv.FormatUint(uint64(p.Boot), 10), strconv.Itoa(p.Chan), strconv.Itoa(p.RFCh), strconv.Itoa(p.FOff),
			strconv.FormatUint(uint64(p.Tmst), 10)})
	}
	cw.Flush()
}
