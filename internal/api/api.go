// Package api serves the REST API, live websocket stream and the embedded
// web console.
package api

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"
	"golang.org/x/crypto/bcrypt"

	"github.com/nor24o/lorahub/internal/band"
	"github.com/nor24o/lorahub/internal/config"
	"github.com/nor24o/lorahub/internal/events"
	"github.com/nor24o/lorahub/internal/formatter"
	"github.com/nor24o/lorahub/internal/ingest"
	"github.com/nor24o/lorahub/internal/integration"
	"github.com/nor24o/lorahub/internal/lorawan"
	"github.com/nor24o/lorahub/internal/model"
	"github.com/nor24o/lorahub/internal/processor"
	"github.com/nor24o/lorahub/internal/store"
)

// Version is set at build time.
var Version = "dev"

// Server is the HTTP server.
type Server struct {
	Cfg   *config.Config
	Store *store.Store
	Bus   *events.Bus
	Proc  *processor.Processor
	MQTT  *integration.MQTT
	Log   *slog.Logger
	Web   fs.FS
	// Host is the board's device settings API (internal/satpanel), mounted at
	// /api/host behind the console's login.
	Host http.Handler

	tx txState
}

const cookieName = "lorahub_session"

var idRe = regexp.MustCompile(`^[a-z0-9](?:[-]?[a-z0-9]){1,35}$`)

// Handler returns the root handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	// Public
	mux.HandleFunc("GET /api/auth/status", s.authStatus)
	mux.HandleFunc("POST /api/auth/setup", s.authSetup)
	mux.HandleFunc("POST /api/auth/login", s.authLogin)
	mux.HandleFunc("POST /api/auth/logout", s.authLogout)
	mux.HandleFunc("POST /api/v1/gateways/{eui}/uplink", s.httpIngest)

	// Protected
	p := func(pattern string, h http.HandlerFunc) { mux.Handle(pattern, s.auth(h)) }
	p("POST /api/auth/password", s.changePassword)
	p("GET /api/overview", s.overview)
	p("GET /api/system", s.system)
	p("GET /api/settings", s.getSettings)
	p("PUT /api/settings", s.putSettings)
	p("GET /api/bands", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, band.Names()) })

	p("GET /api/gateways", s.listGateways)
	p("POST /api/gateways", s.createGateway)
	p("GET /api/gateways/{id}", s.getGateway)
	p("PUT /api/gateways/{id}", s.updateGateway)
	p("DELETE /api/gateways/{id}", s.deleteGateway)
	p("POST /api/gateways/{id}/downlink", s.gatewayRawDownlink)

	p("GET /api/applications", s.listApps)
	p("POST /api/applications", s.createApp)
	p("GET /api/applications/{app}", s.getApp)
	p("PUT /api/applications/{app}", s.updateApp)
	p("DELETE /api/applications/{app}", s.deleteApp)

	p("GET /api/applications/{app}/devices", s.listDevices)
	p("POST /api/applications/{app}/broadcast", s.broadcast)
	p("POST /api/applications/{app}/devices", s.createDevice)
	p("GET /api/applications/{app}/devices/{dev}", s.getDevice)
	p("PUT /api/applications/{app}/devices/{dev}", s.updateDevice)
	p("DELETE /api/applications/{app}/devices/{dev}", s.deleteDevice)
	p("POST /api/applications/{app}/devices/{dev}/down", s.queueDown)
	p("DELETE /api/applications/{app}/devices/{dev}/down", s.clearDown)
	p("POST /api/applications/{app}/devices/{dev}/describe", s.requestDescription)
	p("POST /api/applications/{app}/devices/{dev}/reset", s.resetSession)
	p("GET /api/devices", s.allDevices)
	p("GET /api/downlinks/queued", s.queuedDownlinks)

	p("GET /api/events", s.listEvents)
	p("DELETE /api/events", s.clearEvents)
	p("GET /api/live", s.live)
	p("POST /api/formatters/test", s.testFormatter)
	p("POST /api/simulate", s.simulate)
	p("GET /api/tools/keys", s.genKeys)

	p("GET /api/keys", s.listKeys)
	p("POST /api/keys", s.createKey)
	p("DELETE /api/keys/{id}", s.deleteKey)

	p("GET /api/schemes", s.getSchemes)
	p("PUT /api/schemes", s.putSchemes)
	p("POST /api/schemes/test", s.testSchemes)
	p("GET /api/rules", s.listRules)
	p("POST /api/rules", s.saveRule)
	p("POST /api/rules/simulate", s.simulateRule)
	p("PUT /api/rules/{id}", s.saveRule)
	p("DELETE /api/rules/{id}", s.deleteRule)
	p("POST /api/rules/{id}/fire", s.fireRule)

	p("GET /api/concentrator", s.getConcentrator)
	p("PUT /api/concentrator", s.putConcentrator)
	p("POST /api/concentrator/restart", s.restartConcentrator)
	p("POST /api/concentrator/apply", s.applyConcentrator)
	p("POST /api/concentrator/service", s.concentratorService)
	p("POST /api/concentrator/tx", s.concentratorTX)
	p("GET /api/concentrator/tx/job", s.txJobStatus)
	p("GET /api/ha/entities", s.haEntities)
	p("POST /api/ha/republish", s.haRepublish)
	p("POST /api/ha/remove", s.haRemove)
	p("GET /api/logs", s.getLogs)
	if s.Host != nil {
		host := http.StripPrefix("/api/host", s.Host)
		mux.Handle("/api/host/", s.auth(host.ServeHTTP))
	}
	p("GET /api/vpn/wireguard", s.getWireGuard)
	p("POST /api/vpn/wireguard/service", s.wireGuardService)
	p("GET /api/vpn/tailscale", s.getTailscale)
	p("POST /api/vpn/tailscale/{action}", s.tailscaleAction)
	p("GET /api/gateways/{id}/remote", s.getRemote)
	p("PUT /api/gateways/{id}/remote/config", s.putRemoteConfig)

	// Static web console (SPA)
	files := http.FileServer(http.FS(s.Web))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			writeErr(w, 404, "not found")
			return
		}
		if r.URL.Path != "/" {
			if _, err := fs.Stat(s.Web, strings.TrimPrefix(r.URL.Path, "/")); err == nil {
				w.Header().Set("Cache-Control", "no-cache") // a new version must show at once
				files.ServeHTTP(w, r)
				return
			}
		}
		b, _ := fs.ReadFile(s.Web, "index.html")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Write(b)
	})
	return mux
}

// ---------------- helpers ----------------

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func readJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, 4<<20))
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("invalid JSON body: %w", err)
	}
	return nil
}

func storeErr(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, 404, "not found")
		return
	}
	writeErr(w, 400, err.Error())
}

// ---------------- auth ----------------

func (s *Server) auth(h http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie(cookieName); err == nil {
			if _, ok := s.Store.SessionUser(c.Value); ok {
				h(w, r)
				return
			}
		}
		if a := r.Header.Get("Authorization"); strings.HasPrefix(a, "Bearer ") {
			if s.Store.CheckAPIKey(strings.TrimPrefix(a, "Bearer ")) {
				h(w, r)
				return
			}
		}
		if k := r.URL.Query().Get("api_key"); k != "" && s.Store.CheckAPIKey(k) {
			h(w, r)
			return
		}
		writeErr(w, 401, "authentication required")
	})
}

func (s *Server) currentUser(r *http.Request) string {
	if c, err := r.Cookie(cookieName); err == nil {
		if u, ok := s.Store.SessionUser(c.Value); ok {
			return u
		}
	}
	return ""
}

func (s *Server) authStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"setup_required": s.Store.UserCount() == 0, "user": s.currentUser(r), "version": Version})
}

type creds struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (s *Server) setSession(w http.ResponseWriter, user string) {
	tok, _ := s.Store.CreateSession(user, 30*24*time.Hour)
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: tok, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: 30 * 24 * 3600})
}

func (s *Server) authSetup(w http.ResponseWriter, r *http.Request) {
	if s.Store.UserCount() > 0 {
		writeErr(w, 403, "already set up")
		return
	}
	var c creds
	if err := readJSON(r, &c); err != nil || c.Username == "" || len(c.Password) < 6 {
		writeErr(w, 400, "username and a password of at least 6 characters are required")
		return
	}
	h, _ := bcrypt.GenerateFromPassword([]byte(c.Password), bcrypt.DefaultCost)
	if err := s.Store.SetUser(c.Username, string(h)); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	s.setSession(w, c.Username)
	writeJSON(w, 200, map[string]string{"user": c.Username})
}

func (s *Server) authLogin(w http.ResponseWriter, r *http.Request) {
	var c creds
	if err := readJSON(r, &c); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	h, err := s.Store.UserHash(c.Username)
	if err != nil || bcrypt.CompareHashAndPassword([]byte(h), []byte(c.Password)) != nil {
		time.Sleep(500 * time.Millisecond)
		writeErr(w, 401, "invalid username or password")
		return
	}
	s.setSession(w, c.Username)
	writeJSON(w, 200, map[string]string{"user": c.Username})
}

func (s *Server) authLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(cookieName); err == nil {
		s.Store.DeleteSession(c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", MaxAge: -1})
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) changePassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	u := s.currentUser(r)
	h, err := s.Store.UserHash(u)
	if err != nil || bcrypt.CompareHashAndPassword([]byte(h), []byte(req.Current)) != nil {
		writeErr(w, 403, "current password is wrong")
		return
	}
	if len(req.New) < 6 {
		writeErr(w, 400, "new password must be at least 6 characters")
		return
	}
	nh, _ := bcrypt.GenerateFromPassword([]byte(req.New), bcrypt.DefaultCost)
	s.Store.SetUser(u, string(nh))
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// ---------------- overview / system / settings ----------------

func (s *Server) gatewayOnline(g *model.Gateway) bool {
	after := g.OfflineAfter
	if after <= 0 {
		after = s.Proc.Settings().GatewayOfflineSec
	}
	return g.LastSeen != nil && time.Since(*g.LastSeen) < time.Duration(after)*time.Second
}

func (s *Server) overview(w http.ResponseWriter, r *http.Request) {
	gws := s.Store.Gateways()
	online := 0
	for _, g := range gws {
		if s.gatewayOnline(g) {
			online++
		}
	}
	devs := s.Store.Devices("")
	active := 0
	for _, d := range devs {
		if d.LastSeen != nil && time.Since(*d.LastSeen) < 24*time.Hour {
			active++
		}
	}
	since := time.Now().Add(-24 * time.Hour)
	hist := map[string]map[int64]int{}
	for _, k := range []string{"gs.up", "as.up", "ns.down.sent"} {
		hist[k], _ = s.Store.EventHistogram(k, since, time.Hour)
	}
	writeJSON(w, 200, map[string]any{
		"gateways": len(gws), "gateways_online": online,
		"applications": len(s.Store.Applications()),
		"devices":      len(devs), "devices_active_24h": active,
		"uplinks_24h":    s.Store.CountEvents("as.up", since),
		"gateway_rx_24h": s.Store.CountEvents("gs.up", since),
		"downlinks_24h":  s.Store.CountEvents("ns.down.sent", since),
		"unmatched_24h":  s.Store.CountEvents("ns.up.unmatched", since),
		"histogram":      hist,
	})
}

func (s *Server) system(w http.ResponseWriter, r *http.Request) {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	host, _ := os.Hostname()
	info := map[string]any{
		"version": Version, "go": runtime.Version(), "arch": runtime.GOARCH, "hostname": host,
		"uptime_s": int(time.Since(s.Proc.Started).Seconds()), "goroutines": runtime.NumGoroutine(),
		"heap_mb": float64(ms.HeapAlloc) / 1e6,
		"listeners": map[string]any{
			"http": s.Cfg.HTTP.Listen, "semtech_udp": enabledAddr(s.Cfg.SemtechUDP.Enabled, s.Cfg.SemtechUDP.Listen),
			"bobcat_raw": enabledAddr(s.Cfg.Bobcat.Enabled, s.Cfg.Bobcat.Listen), "mqtt": enabledAddr(s.Cfg.MQTTBroker.Enabled, s.Cfg.MQTTBroker.Listen),
		},
		"mqtt_clients": s.MQTT.BrokerClients(), "external_mqtt_connected": s.MQTT.ExternalConnected(),
		"addresses": localAddrs(),
	}
	if b, err := os.ReadFile("/proc/loadavg"); err == nil {
		info["loadavg"] = strings.Fields(string(b))[:3]
	}
	if b, err := os.ReadFile("/sys/class/thermal/thermal_zone0/temp"); err == nil {
		if v, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil {
			info["cpu_temp_c"] = float64(v) / 1000
		}
	}
	if b, err := os.ReadFile("/proc/meminfo"); err == nil {
		sc := bufio.NewScanner(strings.NewReader(string(b)))
		for sc.Scan() {
			f := strings.Fields(sc.Text())
			if len(f) >= 2 && (f[0] == "MemTotal:" || f[0] == "MemAvailable:") {
				v, _ := strconv.Atoi(f[1])
				info[strings.ToLower(strings.TrimSuffix(f[0], ":"))+"_mb"] = v / 1024
			}
		}
	}
	writeJSON(w, 200, info)
}

func enabledAddr(on bool, a string) string {
	if !on {
		return ""
	}
	return a
}

func localAddrs() []string {
	var out []string
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if ipn, ok := a.(*net.IPNet); ok && !ipn.IP.IsLoopback() && ipn.IP.To4() != nil {
			out = append(out, ipn.IP.String())
		}
	}
	return out
}

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.Proc.Settings())
}

func (s *Server) putSettings(w http.ResponseWriter, r *http.Request) {
	before := s.Proc.Settings()
	st := before
	if err := readJSON(r, &st); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if err := s.Proc.SetSettings(st); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if s.MQTT != nil {
		go s.MQTT.ReconcileHA(before, s.Proc.Settings())
	}
	writeJSON(w, 200, s.Proc.Settings())
}

// ---------------- gateways ----------------

type gatewayView struct {
	*model.Gateway
	Online     bool            `json:"online"`
	Forwarders map[string]bool `json:"forwarder_status,omitempty"`
}

func (s *Server) viewGateway(g *model.Gateway) gatewayView {
	return gatewayView{Gateway: g, Online: s.gatewayOnline(g), Forwarders: s.Proc.ForwarderStatus(g.ID)}
}

func (s *Server) listGateways(w http.ResponseWriter, r *http.Request) {
	out := []gatewayView{}
	for _, g := range s.Store.Gateways() {
		out = append(out, s.viewGateway(g))
	}
	writeJSON(w, 200, out)
}

func (s *Server) getGateway(w http.ResponseWriter, r *http.Request) {
	g, err := s.Store.Gateway(r.PathValue("id"))
	if err != nil {
		storeErr(w, err)
		return
	}
	writeJSON(w, 200, s.viewGateway(g))
}

func validateGateway(g *model.Gateway) error {
	for _, p := range []int{g.ControlPort, g.StatusPort} {
		if p < 0 || p > 65535 {
			return fmt.Errorf("ports must be between 1 and 65535")
		}
	}
	if g.DutyCyclePct < 0 || g.DutyCyclePct > 100 {
		return fmt.Errorf("duty cycle must be between 0 and 100 percent")
	}
	if g.AntennaGain < 0 || g.AntennaGain > 30 {
		return fmt.Errorf("antenna gain must be between 0 and 30 dBi")
	}
	if g.MinRSSI > 0 {
		return fmt.Errorf("minimum RSSI must be negative, or 0 for no limit")
	}
	if g.OfflineAfter < 0 {
		return fmt.Errorf("offline timeout cannot be negative")
	}
	if !idRe.MatchString(g.ID) {
		return fmt.Errorf("gateway ID must be 2-36 lowercase letters, digits or dashes")
	}
	if _, err := lorawan.ParseEUI(g.EUI); err != nil {
		return fmt.Errorf("gateway EUI must be 16 hex characters")
	}
	g.EUI = strings.ToUpper(strings.NewReplacer(":", "", "-", "", " ", "").Replace(g.EUI))
	if g.Band == "" {
		g.Band = "EU868"
	}
	return nil
}

func (s *Server) createGateway(w http.ResponseWriter, r *http.Request) {
	var g model.Gateway
	if err := readJSON(r, &g); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if err := validateGateway(&g); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if _, err := s.Store.Gateway(g.ID); err == nil {
		writeErr(w, 409, "gateway ID already exists")
		return
	}
	if g.Kind == "http" && g.APIKey == "" {
		g.APIKey = "gwk_" + store.NewToken(16)
	}
	g.CreatedAt, g.UpdatedAt = time.Now().UTC(), time.Now().UTC()
	if err := s.Store.SaveGateway(&g); err != nil {
		storeErr(w, err)
		return
	}
	s.Proc.ReloadForwarders()
	writeJSON(w, 201, s.viewGateway(&g))
}

func (s *Server) updateGateway(w http.ResponseWriter, r *http.Request) {
	cur, err := s.Store.Gateway(r.PathValue("id"))
	if err != nil {
		storeErr(w, err)
		return
	}
	merged, err := mergeJSON(cur, r)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	upd := *merged
	upd.ID = cur.ID
	if err := validateGateway(&upd); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if upd.Kind == "http" && upd.APIKey == "" {
		upd.APIKey = "gwk_" + store.NewToken(16)
	}
	// keep runtime counters from the live copy
	err = s.Store.UpdateGateway(cur.ID, true, func(g *model.Gateway) {
		rt := *g
		*g = upd
		g.LastSeen, g.LastAddr, g.RxCount, g.TxCount, g.LastStatus, g.CreatedAt = rt.LastSeen, rt.LastAddr, rt.RxCount, rt.TxCount, rt.LastStatus, rt.CreatedAt
		g.UpdatedAt = time.Now().UTC()
	})
	if err != nil {
		storeErr(w, err)
		return
	}
	s.Proc.ReloadForwarders()
	g, _ := s.Store.Gateway(cur.ID)
	writeJSON(w, 200, s.viewGateway(g))
}

func (s *Server) deleteGateway(w http.ResponseWriter, r *http.Request) {
	if err := s.Store.DeleteGateway(r.PathValue("id")); err != nil {
		storeErr(w, err)
		return
	}
	s.Proc.ReloadForwarders()
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// gatewayRawDownlink transmits an arbitrary packet immediately (testing).
func (s *Server) gatewayRawDownlink(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PayloadHex string  `json:"payload_hex"`
		Frequency  float64 `json:"frequency"`
		DataRate   string  `json:"data_rate"`
		CodingRate string  `json:"coding_rate"`
		Power      int     `json:"power"`
		InvertPol  bool    `json:"invert_polarity"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	b, err := hex.DecodeString(strings.ReplaceAll(req.PayloadHex, " ", ""))
	if err != nil || len(b) == 0 {
		writeErr(w, 400, "payload_hex must be non-empty hex")
		return
	}
	if req.Frequency == 0 || req.DataRate == "" {
		writeErr(w, 400, "frequency and data_rate are required")
		return
	}
	if err := s.Proc.SendRaw(r.PathValue("id"), model.TxPacket{Payload: b, Frequency: uint64(req.Frequency*1e6 + 0.5), DataRate: req.DataRate,
		CodingRate: req.CodingRate, Power: req.Power, InvertPol: req.InvertPol, Immediate: true}); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// httpIngest accepts uplinks from HTTP satellites:
// POST /api/v1/gateways/{eui}/uplink  Authorization: Bearer <gateway key>
// body: {"rxpk":[...semtech rxpk...], "stat":{...}}
func (s *Server) httpIngest(w http.ResponseWriter, r *http.Request) {
	eui := strings.ToUpper(r.PathValue("eui"))
	g, err := s.Store.GatewayByEUI(eui)
	key := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if err != nil || g.Kind != "http" || g.APIKey == "" || key != g.APIKey {
		writeErr(w, 401, "unknown gateway or invalid gateway API key")
		return
	}
	var body struct {
		Rxpk []ingest.Rxpk  `json:"rxpk"`
		Stat map[string]any `json:"stat"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	s.Proc.HandleStatus(eui, "http", host, body.Stat)
	n := 0
	for _, rx := range body.Rxpk {
		pkt, err := ingest.RxpkToPacket(rx, eui, g.ID, "http")
		if err != nil {
			continue
		}
		s.Proc.HandleUplink(pkt)
		n++
	}
	writeJSON(w, 200, map[string]int{"accepted": n})
}

// ---------------- applications ----------------

func (s *Server) listApps(w http.ResponseWriter, r *http.Request) {
	type appView struct {
		*model.Application
		DeviceCount int `json:"device_count"`
	}
	out := []appView{}
	for _, a := range s.Store.Applications() {
		out = append(out, appView{a, len(s.Store.Devices(a.ID))})
	}
	writeJSON(w, 200, out)
}

func (s *Server) getApp(w http.ResponseWriter, r *http.Request) {
	a, err := s.Store.Application(r.PathValue("app"))
	if err != nil {
		storeErr(w, err)
		return
	}
	writeJSON(w, 200, a)
}

func normalizeWebhooks(a *model.Application) {
	for i := range a.Webhooks {
		if a.Webhooks[i].ID == "" {
			a.Webhooks[i].ID = store.NewToken(4)
		}
		if len(a.Webhooks[i].Events) == 0 {
			a.Webhooks[i].Events = []string{"up"}
		}
	}
}

func (s *Server) validateGroup(a *model.Application) error {
	a.NodeID = normHex(a.NodeID)
	if a.NodeID == "" {
		return nil
	}
	b, err := hex.DecodeString(a.NodeID)
	pf := s.Proc.Settings().PacketFormat
	if err != nil || len(b) != pf.NodeIDLength {
		return fmt.Errorf("node ID must be %d bytes of hex (%d characters)", pf.NodeIDLength, pf.NodeIDLength*2)
	}
	for _, o := range s.Store.Applications() {
		if o.ID != a.ID && strings.EqualFold(o.NodeID, a.NodeID) {
			return fmt.Errorf("node ID %s is already used by group %s", a.NodeID, o.ID)
		}
	}
	return nil
}

func (s *Server) createApp(w http.ResponseWriter, r *http.Request) {
	var a model.Application
	if err := readJSON(r, &a); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if a.ID == "" && a.NodeID != "" {
		a.ID = "node-" + strings.ToLower(normHex(a.NodeID))
	}
	if err := s.validateGroup(&a); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if !idRe.MatchString(a.ID) {
		writeErr(w, 400, "application ID must be 2-36 lowercase letters, digits or dashes")
		return
	}
	if _, err := s.Store.Application(a.ID); err == nil {
		writeErr(w, 409, "application ID already exists")
		return
	}
	if a.Uplink.Type == "" {
		a.Uplink.Type = "none"
	}
	normalizeWebhooks(&a)
	a.CreatedAt, a.UpdatedAt = time.Now().UTC(), time.Now().UTC()
	if err := s.Store.SaveApplication(&a); err != nil {
		storeErr(w, err)
		return
	}
	writeJSON(w, 201, a)
}

func (s *Server) updateApp(w http.ResponseWriter, r *http.Request) {
	cur, err := s.Store.Application(r.PathValue("app"))
	if err != nil {
		storeErr(w, err)
		return
	}
	merged, err := mergeJSON(cur, r)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	upd := *merged
	upd.ID, upd.CreatedAt, upd.UpdatedAt = cur.ID, cur.CreatedAt, time.Now().UTC()
	if err := s.validateGroup(&upd); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	normalizeWebhooks(&upd)
	if err := s.Store.SaveApplication(&upd); err != nil {
		storeErr(w, err)
		return
	}
	writeJSON(w, 200, upd)
}

func (s *Server) deleteApp(w http.ResponseWriter, r *http.Request) {
	nodes := s.Store.Devices(r.PathValue("app"))
	if err := s.Store.DeleteApplication(r.PathValue("app")); err != nil {
		storeErr(w, err)
		return
	}
	if s.MQTT != nil {
		for _, d := range nodes {
			s.MQTT.NodeChanged(d, nil) // its nodes leave Home Assistant with it
		}
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// ---------------- devices ----------------

func (s *Server) allDevices(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.Store.Devices(""))
}

func (s *Server) listDevices(w http.ResponseWriter, r *http.Request) {
	if _, err := s.Store.Application(r.PathValue("app")); err != nil {
		storeErr(w, err)
		return
	}
	writeJSON(w, 200, s.Store.Devices(r.PathValue("app")))
}

func (s *Server) getDevice(w http.ResponseWriter, r *http.Request) {
	d, err := s.Store.Device(r.PathValue("app"), r.PathValue("dev"))
	if err != nil {
		storeErr(w, err)
		return
	}
	writeJSON(w, 200, d)
}

func normHex(s string) string {
	return strings.ToUpper(strings.NewReplacer(" ", "", ":", "", "-", "").Replace(strings.TrimSpace(s)))
}

func validateDevice(d *model.Device) error {
	if d.Mode == "" {
		d.Mode = "node"
	}
	d.Serial = normHex(d.Serial)
	if d.Mode == "node" && d.ID == "" {
		d.ID = strings.ToLower(d.Serial)
	}
	if !idRe.MatchString(d.ID) {
		return fmt.Errorf("device ID must be 2-36 lowercase letters, digits or dashes")
	}
	if d.RawDownlink != nil {
		if err := d.RawDownlink.Validate(); err != nil {
			return err
		}
	}
	if len(d.HACommands) > 20 {
		return fmt.Errorf("a node can have at most 20 commands")
	}
	names := map[string]bool{}
	for i := range d.HACommands {
		c := &d.HACommands[i]
		if err := c.Normalize(); err != nil {
			return err
		}
		key := strings.ToLower(c.Key())
		if names[key] {
			return fmt.Errorf("command %q appears twice", c.Name)
		}
		names[key] = true
	}
	d.DevEUI, d.JoinEUI, d.AppKey = normHex(d.DevEUI), normHex(d.JoinEUI), normHex(d.AppKey)
	if d.Band == "" {
		d.Band = "EU868"
	}
	if d.Class == "" {
		d.Class = "A"
	}
	switch d.Mode {
	case "node":
		if _, err := hex.DecodeString(d.Serial); err != nil { // empty: identified by NODE_ID alone
			return fmt.Errorf("serial ID must be hex (e.g. 96B840EB)")
		}
	case "otaa":
		if _, err := lorawan.ParseEUI(d.DevEUI); err != nil {
			return fmt.Errorf("DevEUI must be 16 hex characters")
		}
		if d.JoinEUI == "" {
			d.JoinEUI = "0000000000000000"
		}
		if _, err := lorawan.ParseEUI(d.JoinEUI); err != nil {
			return fmt.Errorf("JoinEUI must be 16 hex characters")
		}
		if _, err := lorawan.ParseKey(d.AppKey); err != nil {
			return fmt.Errorf("AppKey must be 32 hex characters")
		}
	case "abp":
		if d.Session == nil {
			return fmt.Errorf("ABP devices need dev_addr, nwk_s_key and app_s_key")
		}
		d.Session.DevAddr, d.Session.NwkSKey, d.Session.AppSKey = normHex(d.Session.DevAddr), normHex(d.Session.NwkSKey), normHex(d.Session.AppSKey)
		if _, err := lorawan.ParseDevAddr(d.Session.DevAddr); err != nil {
			return fmt.Errorf("DevAddr must be 8 hex characters")
		}
		if _, err := lorawan.ParseKey(d.Session.NwkSKey); err != nil {
			return fmt.Errorf("NwkSKey must be 32 hex characters")
		}
		if _, err := lorawan.ParseKey(d.Session.AppSKey); err != nil {
			return fmt.Errorf("AppSKey must be 32 hex characters")
		}
	case "raw":
		if d.RawMatch == nil || d.RawMatch.Value == "" {
			return fmt.Errorf("raw devices need a payload match rule")
		}
		if d.RawMatch.Type != "regex_hex" {
			d.RawMatch.Value = normHex(d.RawMatch.Value)
			if _, err := hex.DecodeString(d.RawMatch.Value); err != nil {
				return fmt.Errorf("match value must be hex")
			}
		} else if _, err := regexp.Compile(d.RawMatch.Value); err != nil {
			return fmt.Errorf("invalid regex: %v", err)
		}
	default:
		return fmt.Errorf("mode must be node, otaa, abp or raw")
	}
	if d.Uplink.Type == "" {
		d.Uplink.Type = "inherit"
	}
	if d.Downlink.Type == "" {
		d.Downlink.Type = "inherit"
	}
	return nil
}

func (s *Server) createDevice(w http.ResponseWriter, r *http.Request) {
	appID := r.PathValue("app")
	if _, err := s.Store.Application(appID); err != nil {
		storeErr(w, err)
		return
	}
	var d model.Device
	if err := readJSON(r, &d); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	d.AppID = appID
	if err := validateDevice(&d); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if _, err := s.Store.Device(appID, d.ID); err == nil {
		writeErr(w, 409, "a node with this ID already exists in this group")
		return
	}
	if d.Mode == "node" && len(s.Store.FindDevices(func(o *model.Device) bool { return o.AppID == appID && strings.EqualFold(o.Serial, d.Serial) })) > 0 {
		writeErr(w, 409, "a node with serial "+d.Serial+" already exists in this group")
		return
	}
	if d.Mode == "abp" {
		d.Session.StartedAt = time.Now().UTC()
	} else if d.Mode == "otaa" {
		d.Session = nil
	}
	d.Queue = nil
	d.CreatedAt, d.UpdatedAt = time.Now().UTC(), time.Now().UTC()
	if err := s.Store.SaveDevice(&d); err != nil {
		storeErr(w, err)
		return
	}
	writeJSON(w, 201, d)
}

func (s *Server) updateDevice(w http.ResponseWriter, r *http.Request) {
	appID, devID := r.PathValue("app"), r.PathValue("dev")
	cur, err := s.Store.Device(appID, devID)
	if err != nil {
		storeErr(w, err)
		return
	}
	merged, err := mergeJSON(cur, r)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	upd := *merged
	upd.ID, upd.AppID = cur.ID, cur.AppID
	if err := validateDevice(&upd); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if upd.HAEnabled && !cur.HAEnabled && cur.LastDecoded == nil {
		writeErr(w, 400, "this node has no decoded data yet: set up a decoder and wait for a packet before exposing it to Home Assistant")
		return
	}
	// check DevEUI uniqueness through SaveDevice semantics
	for _, o := range s.Store.FindDevices(func(o *model.Device) bool {
		return o.Mode != "raw" && upd.Mode != "raw" && upd.DevEUI != "" && strings.EqualFold(o.DevEUI, upd.DevEUI) && !(o.AppID == appID && o.ID == devID)
	}) {
		writeErr(w, 400, "DevEUI already used by "+o.AppID+"/"+o.ID)
		return
	}
	d, err := s.Store.UpdateDevice(appID, devID, func(dv *model.Device) error {
		rt := *dv
		*dv = upd
		// runtime fields are owned by the server
		dv.LastSeen, dv.UplinkCount, dv.LastRSSI, dv.LastSNR, dv.LastGateway, dv.LastDecoded = rt.LastSeen, rt.UplinkCount, rt.LastRSSI, rt.LastSNR, rt.LastGateway, rt.LastDecoded
		dv.Queue, dv.UsedNonces, dv.CreatedAt, dv.LastScheme, dv.Profile, dv.DownSeq = rt.Queue, rt.UsedNonces, rt.CreatedAt, rt.LastScheme, rt.Profile, rt.DownSeq
		dv.Described = rt.Described // what the node announced; only the node changes it
		if dv.Mode == "otaa" {
			dv.Session = rt.Session
		}
		if dv.Mode == "abp" && rt.Session != nil && upd.Session != nil && upd.Session.DevAddr == rt.Session.DevAddr && upd.Session.NwkSKey == rt.Session.NwkSKey {
			dv.Session.FCntUp, dv.Session.FCntDown, dv.Session.HasUplink = rt.Session.FCntUp, rt.Session.FCntDown, rt.Session.HasUplink
		}
		dv.UpdatedAt = time.Now().UTC()
		return nil
	})
	if err != nil {
		storeErr(w, err)
		return
	}
	if s.MQTT != nil {
		s.MQTT.NodeChanged(cur, d)
	}
	writeJSON(w, 200, d)
}

func (s *Server) deleteDevice(w http.ResponseWriter, r *http.Request) {
	cur, _ := s.Store.Device(r.PathValue("app"), r.PathValue("dev"))
	if err := s.Store.DeleteDevice(r.PathValue("app"), r.PathValue("dev")); err != nil {
		storeErr(w, err)
		return
	}
	if s.MQTT != nil && cur != nil {
		s.MQTT.NodeChanged(cur, nil)
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) queueDown(w http.ResponseWriter, r *http.Request) {
	var req struct {
		FPort      int    `json:"f_port"`
		FRMPayload string `json:"frm_payload"` // base64
		Hex        string `json:"frm_payload_hex"`
		Decoded    any    `json:"decoded_payload"`
		Confirmed  bool   `json:"confirmed"`
		Replace    bool   `json:"replace"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	var b []byte
	var err error
	switch {
	case req.Hex != "":
		b, err = hex.DecodeString(normHex(req.Hex))
	case req.FRMPayload != "":
		b, err = base64.StdEncoding.DecodeString(req.FRMPayload)
	case req.Decoded == nil:
		err = fmt.Errorf("frm_payload_hex, frm_payload or decoded_payload is required")
	}
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if req.FPort == 0 {
		req.FPort = 1
	}
	item, err := s.Proc.EnqueueDownlink(r.PathValue("app"), r.PathValue("dev"), req.FPort, b, req.Decoded, req.Confirmed, req.Replace)
	if err != nil {
		storeErr(w, err)
		return
	}
	writeJSON(w, 200, item)
}

func (s *Server) clearDown(w http.ResponseWriter, r *http.Request) {
	d, err := s.Store.UpdateDevice(r.PathValue("app"), r.PathValue("dev"), func(dv *model.Device) error { dv.Queue = nil; return nil })
	if err != nil {
		storeErr(w, err)
		return
	}
	writeJSON(w, 200, d)
}

func (s *Server) resetSession(w http.ResponseWriter, r *http.Request) {
	d, err := s.Store.UpdateDevice(r.PathValue("app"), r.PathValue("dev"), func(dv *model.Device) error {
		switch dv.Mode {
		case "otaa":
			dv.Session = nil
			dv.UsedNonces = nil
		case "abp":
			if dv.Session != nil {
				dv.Session.FCntUp, dv.Session.FCntDown, dv.Session.HasUplink = 0, 0, false
			}
		}
		return nil
	})
	if err != nil {
		storeErr(w, err)
		return
	}
	writeJSON(w, 200, d)
}

// ---------------- events / live ----------------

func eventFilter(r *http.Request) store.EventFilter {
	q := r.URL.Query()
	f := store.EventFilter{AppID: q.Get("app"), DevID: q.Get("dev"), GatewayID: q.Get("gateway"), Prefix: q.Get("prefix")}
	if n := q.Get("names"); n != "" {
		f.Names = strings.Split(n, ",")
	}
	f.Limit, _ = strconv.Atoi(q.Get("limit"))
	f.BeforeID, _ = strconv.ParseInt(q.Get("before"), 10, 64)
	return f
}

func (s *Server) listEvents(w http.ResponseWriter, r *http.Request) {
	ev, err := s.Store.Events(eventFilter(r))
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, ev)
}

func (s *Server) clearEvents(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	s.Store.ClearEvents(q.Get("app"), q.Get("dev"), q.Get("gateway"))
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func matchEvent(f store.EventFilter, e *model.Event) bool {
	if f.AppID != "" && e.AppID != f.AppID {
		return false
	}
	if f.DevID != "" && e.DevID != f.DevID {
		return false
	}
	if f.Prefix != "" && !strings.HasPrefix(e.Name, f.Prefix) {
		return false
	}
	if f.GatewayID != "" {
		found := false
		for _, g := range e.Gateways {
			if g == f.GatewayID {
				found = true
			}
		}
		if !found {
			return false
		}
	}
	if len(f.Names) > 0 {
		for _, n := range f.Names {
			if n == e.Name {
				return true
			}
		}
		return false
	}
	return true
}

// live streams events over a websocket.
func (s *Server) live(w http.ResponseWriter, r *http.Request) {
	f := eventFilter(r)
	includeStatus := r.URL.Query().Get("status") == "1"
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	defer c.CloseNow()
	ch, unsub := s.Bus.Subscribe(256, func(e *model.Event) bool {
		if e.Name == "gs.status" && !includeStatus {
			return false
		}
		return matchEvent(f, e)
	})
	defer unsub()
	ctx := c.CloseRead(r.Context())
	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ping.C:
			pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := c.Ping(pctx)
			cancel()
			if err != nil {
				return
			}
		case e, ok := <-ch:
			if !ok {
				return
			}
			b, _ := json.Marshal(e)
			wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := c.Write(wctx, websocket.MessageText, b)
			cancel()
			if err != nil {
				return
			}
		}
	}
}

// ---------------- tools ----------------

func (s *Server) testFormatter(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Formatter  model.Formatter `json:"formatter"`
		PayloadHex string          `json:"payload_hex"`
		FPort      int             `json:"f_port"`
		Direction  string          `json:"direction"`
		Decoded    any             `json:"decoded_payload"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if req.Direction == "down" {
		b, fport, err := formatter.EncodeJS(req.Formatter.Code, req.Decoded, req.FPort)
		if err != nil {
			writeJSON(w, 200, map[string]any{"errors": []string{err.Error()}})
			return
		}
		writeJSON(w, 200, map[string]any{"bytes_hex": strings.ToUpper(hex.EncodeToString(b)), "f_port": fport})
		return
	}
	b, err := hex.DecodeString(normHex(req.PayloadHex))
	if err != nil {
		writeErr(w, 400, "payload_hex must be hex")
		return
	}
	start := time.Now()
	res := formatter.Decode(req.Formatter, formatter.Input{Bytes: b, FPort: req.FPort, RecvTime: time.Now()})
	if res == nil {
		res = &formatter.Result{}
	}
	writeJSON(w, 200, map[string]any{"data": res.Data, "warnings": res.Warnings, "errors": res.Errors, "duration_ms": time.Since(start).Seconds() * 1000})
}

func (s *Server) simulate(w http.ResponseWriter, r *http.Request) {
	var req processor.SimulateRequest
	if err := readJSON(r, &req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if err := s.Proc.Simulate(req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) genKeys(w http.ResponseWriter, r *http.Request) {
	h := func(n int) string { return strings.ToUpper(hex.EncodeToString(lorawan.RandomBytes(n))) }
	writeJSON(w, 200, map[string]string{"eui": h(8), "key": h(16), "dev_addr": "26" + h(3)})
}

func (s *Server) listKeys(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.Store.APIKeys())
}

func (s *Server) createKey(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	readJSON(r, &req)
	if req.Name == "" {
		req.Name = "API key"
	}
	k, err := s.Store.CreateAPIKey(req.Name)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 201, k)
}

func (s *Server) deleteKey(w http.ResponseWriter, r *http.Request) {
	s.Store.DeleteAPIKey(r.PathValue("id"))
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// ---------------- concentrator (raw_lora_app) ----------------

func (s *Server) getConcentrator(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{"config_path": s.Cfg.Concentrator.ConfigPath, "service": s.serviceState()}
	if b, err := os.ReadFile(s.Cfg.Concentrator.ConfigPath); err == nil {
		out["config"] = json.RawMessage(b)
	} else {
		out["config_error"] = err.Error()
	}
	if s.Cfg.Concentrator.StatusAddr != "" {
		if st, err := readStatus(s.Cfg.Concentrator.StatusAddr); err == nil {
			out["status"] = st
		} else {
			out["status_error"] = "raw_lora_app status server unreachable: " + err.Error()
		}
	}
	writeJSON(w, 200, out)
}

func (s *Server) putConcentrator(w http.ResponseWriter, r *http.Request) {
	var cfg map[string]any
	if err := readJSON(r, &cfg); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	b, _ := json.MarshalIndent(cfg, "", "    ")
	path := s.Cfg.Concentrator.ConfigPath
	if old, err := os.ReadFile(path); err == nil {
		os.WriteFile(path+".bak", old, 0o644)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// applyConcentrator saves a radio configuration and restarts the engine with
// it. When the radio does not start listening, the previous configuration is
// put back, so a setting the hardware cannot do never leaves the gateway deaf.
func (s *Server) applyConcentrator(w http.ResponseWriter, r *http.Request) {
	var cfg map[string]any
	if err := readJSON(r, &cfg); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	path := s.Cfg.Concentrator.ConfigPath
	old, err := os.ReadFile(path)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	b, _ := json.MarshalIndent(cfg, "", "    ")
	os.WriteFile(path+".bak", old, 0o644)
	if err := os.WriteFile(path, b, 0o644); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	ok, why := s.restartAndWait()
	if ok {
		writeJSON(w, 200, map[string]any{"ok": true, "service": s.serviceState()})
		return
	}
	os.WriteFile(path+".failed", b, 0o644)
	os.WriteFile(path, old, 0o644)
	back, _ := s.restartAndWait()
	msg := "The radio did not start with these settings, so the previous ones were put back"
	if !back {
		msg = "The radio did not start with these settings, and not with the previous ones either"
	}
	if why != "" {
		msg += ". The engine said: " + why
	}
	writeErr(w, 409, msg)
}

// restartAndWait restarts the radio engine and waits until it listens.
func (s *Server) restartAndWait() (bool, string) {
	if out, err := runCmd(30*time.Second, "systemctl", "restart", s.Cfg.Concentrator.Service); err != nil {
		return false, strings.TrimSpace(out)
	}
	if s.Cfg.Concentrator.StatusAddr == "" {
		return true, ""
	}
	for deadline := time.Now().Add(25 * time.Second); time.Now().Before(deadline); {
		time.Sleep(time.Second)
		if b, err := readStatus(s.Cfg.Concentrator.StatusAddr); err == nil {
			var st struct {
				RX string `json:"rx_status"`
			}
			if json.Unmarshal(b, &st) == nil && st.RX == "LISTENING" {
				return true, ""
			}
		}
	}
	out, _ := runCmd(5*time.Second, "journalctl", "-u", s.Cfg.Concentrator.Service, "-n", "60", "--no-pager", "-o", "cat")
	return false, engineErrors(out)
}

// engineErrors picks the engine's own error lines out of its log.
func engineErrors(log string) string {
	var lines []string
	for _, l := range strings.Split(log, "\n") {
		if l = strings.TrimSpace(l); strings.Contains(l, "ERROR") && !slices.Contains(lines, l) {
			lines = append(lines, l)
		}
	}
	if len(lines) > 3 {
		lines = lines[len(lines)-3:]
	}
	return strings.Join(lines, " · ")
}

func (s *Server) restartConcentrator(w http.ResponseWriter, r *http.Request) {
	cmd := s.Cfg.Concentrator.RestartCommand
	if cmd == "" {
		writeErr(w, 400, "no restart command configured")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "sh", "-c", cmd).CombinedOutput()
	if err != nil {
		writeErr(w, 500, fmt.Sprintf("%v: %s", err, out))
		return
	}
	writeJSON(w, 200, map[string]string{"output": string(out)})
}
