// Package satpanel holds a Bobcat's device settings: telemetry of the board,
// the radio engine and the tunnel; wired and Wi-Fi networking (client, access
// point bridged to the cable, access point with its own network); host name and
// time; the SD card reader; and the status lights. Every network change is
// checked and undone when the board loses its way out.
//
// A satellite serves it as its own page (Handler, signed in with the root
// password). The hub mounts API() inside its console, behind its own login.
// Radio settings stay on the hub's Remote config page.
package satpanel

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/nor24o/lorahub/web"
)

//go:embed static
var static embed.FS

// Roles: a satellite checks changes against the hub through the tunnel; the
// hub checks them against the home router.
const (
	RoleSatellite = "satellite"
	RoleHub       = "hub"
)

// Options configure the panel. Zero values take the satellite's defaults.
type Options struct {
	Role        string
	Version     string
	LoraConfig  string // the engine's config.json
	WGInterface string
	WGConf      string
	RoamEnv     string
	Ethernet    string
	WiFi        string
	SDMount     string // where the SD card is mounted
	LEDModeFile string // read by raw_lora_app 2.1 once a second
	LightsFile  string // the day mode and night schedule
	Root        string // prefix for every file read, for tests
	SessionFile string // where sign-ins survive a restart (satellite only)
	// Backup, when set, writes a consistent copy of LoRaHub's database to dst
	// (the hub offers "back up to the SD card").
	Backup     func(ctx context.Context, dst string) error
	CheckFor   time.Duration // how long a change waits for the hub or router
	CheckEvery time.Duration
	ApplyDelay time.Duration
	APEvery    time.Duration
	LoginDelay time.Duration
	Run        Runner
	CheckPass  func(string) bool
	Log        *slog.Logger
	Now        func() time.Time
}

// Server serves the page and its API.
type Server struct {
	opt  Options
	run  Runner
	auth *auth
	log  *slog.Logger
	now  func() time.Time
	root string

	mu     sync.Mutex
	change *Change
	seq    int

	pingMu sync.Mutex
	pingAt time.Time
	pingOK bool
	pingMS float64
	pingTo string

	apOnce sync.Once
	apOK   bool

	nmMu    sync.Mutex
	conns   []nmConn
	connsAt time.Time

	lightsMu     sync.Mutex
	previewUntil time.Time // a colour is being shown until then
}

// New fills in defaults and returns the panel.
func New(o Options) *Server {
	def := func(v *string, d string) {
		if *v == "" {
			*v = d
		}
	}
	def(&o.Role, RoleSatellite)
	def(&o.LoraConfig, "/etc/lora/config.json")
	def(&o.WGInterface, "wg0")
	def(&o.WGConf, "/etc/wireguard/"+o.WGInterface+".conf")
	def(&o.RoamEnv, "/etc/wireguard/roam.env")
	def(&o.Ethernet, "end0")
	def(&o.WiFi, "wlan0")
	def(&o.SDMount, "/media/sdcard")
	def(&o.LEDModeFile, "/etc/lora/led_mode")
	def(&o.LightsFile, "/etc/lora/led_schedule.json")
	def(&o.Root, "/")
	if o.Role == RoleSatellite {
		def(&o.SessionFile, "/var/lib/lorahub-sat/sessions.json")
	}
	dur := func(v *time.Duration, d time.Duration) {
		if *v == 0 {
			*v = d
		}
	}
	dur(&o.CheckFor, 60*time.Second)
	dur(&o.CheckEvery, 3*time.Second)
	dur(&o.ApplyDelay, time.Second)
	dur(&o.APEvery, 5*time.Second)
	if o.LoginDelay == 0 {
		o.LoginDelay = failedDelay
	}
	if o.Run == nil {
		o.Run = execRunner{}
	}
	if o.CheckPass == nil {
		o.CheckPass = CheckRootPassword
	}
	if o.Log == nil {
		o.Log = slog.Default()
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	sessions := ""
	if o.SessionFile != "" {
		sessions = filepath.Join(o.Root, filepath.FromSlash(o.SessionFile))
	}
	return &Server{opt: o, run: o.Run, log: o.Log, now: o.Now, root: o.Root,
		auth: newAuth(o.CheckPass, o.Now, o.LoginDelay, sessions)}
}

func (s *Server) hub() bool { return s.opt.Role == RoleHub }

// Run does the background work until ctx ends: the access point that follows
// the cable, and the lights' night schedule.
func (s *Server) Run(ctx context.Context) {
	go s.apLoop(ctx)
	s.lightsLoop(ctx)
}

type httpError struct {
	code int
	err  error
}

func (e httpError) Error() string { return e.err.Error() }

func badRequest(err error) error { return httpError{http.StatusBadRequest, err} }

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func fail(w http.ResponseWriter, err error) {
	var he httpError
	switch {
	case errors.As(err, &he):
		writeErr(w, he.code, he.Error())
	case errors.Is(err, errBusy):
		writeErr(w, http.StatusConflict, err.Error())
	default:
		writeErr(w, http.StatusInternalServerError, err.Error())
	}
}

func readJSON(r *http.Request, v any) error {
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(v); err != nil {
		return badRequest(errors.New("the request is not valid JSON"))
	}
	return nil
}

// API returns the device settings API with paths relative to its mount point
// (/status, /network, ...). It does no sign-in check: the satellite page and
// the hub console wrap it in their own. Changes must carry an X-Requested-With
// header, which another web site cannot add to a request.
func (s *Server) API() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /status", s.getStatus)
	mux.HandleFunc("GET /network", s.getNetwork)
	mux.HandleFunc("POST /network/ethernet", s.postEthernet)
	mux.HandleFunc("GET /wifi/scan", s.getScan)
	mux.HandleFunc("POST /wifi/connect", s.postWiFiConnect)
	mux.HandleFunc("POST /wifi/disconnect", s.postWiFiDisconnect)
	mux.HandleFunc("POST /wifi/autoconnect", s.postWiFiAutoconnect)
	mux.HandleFunc("POST /wifi/forget", s.postWiFiForget)
	mux.HandleFunc("POST /wifi/radio", s.postWiFiRadio)
	mux.HandleFunc("POST /ap", s.postAP)
	if !s.hub() {
		mux.HandleFunc("POST /tunnel", s.postTunnel)
	}
	mux.HandleFunc("GET /general", s.getGeneral)
	mux.HandleFunc("GET /general/timezones", s.getTimezones)
	mux.HandleFunc("POST /general", s.postGeneral)
	mux.HandleFunc("GET /sd", s.getSD)
	mux.HandleFunc("POST /sd/mount", s.postSDMount)
	mux.HandleFunc("POST /sd/unmount", s.postSDUnmount)
	mux.HandleFunc("GET /sd/files", s.getSDFiles)
	mux.HandleFunc("GET /sd/download", s.getSDDownload)
	mux.HandleFunc("POST /sd/backup", s.postSDBackup)
	mux.HandleFunc("GET /lights", s.getLights)
	mux.HandleFunc("POST /lights", s.postLights)
	mux.HandleFunc("POST /lights/preview", s.postLightsPreview)
	mux.HandleFunc("POST /radio/restart", s.postRadioRestart)
	mux.HandleFunc("POST /reboot", s.postReboot)
	mux.HandleFunc("POST /poweroff", s.postPoweroff)
	mux.HandleFunc("GET /logs", s.getLogs)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Header.Get(csrfHeader) == "" {
			writeErr(w, http.StatusForbidden, "request refused")
			return
		}
		mux.ServeHTTP(w, r)
	})
}

// Handler returns the satellite's own page and /api, signed in with the
// Bobcat's root password.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	page, _ := fs.Sub(static, "static")
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		serveFile(w, page, "index.html", "text/html; charset=utf-8")
	})
	mux.HandleFunc("GET /sat.js", func(w http.ResponseWriter, r *http.Request) {
		serveFile(w, page, "sat.js", "text/javascript; charset=utf-8")
	})
	mux.HandleFunc("GET /host-ui.js", func(w http.ResponseWriter, r *http.Request) {
		serveFile(w, web.FS(), "host-ui.js", "text/javascript; charset=utf-8")
	})
	mux.HandleFunc("GET /help.js", func(w http.ResponseWriter, r *http.Request) {
		serveFile(w, web.FS(), "help.js", "text/javascript; charset=utf-8")
	})
	mux.HandleFunc("GET /style.css", func(w http.ResponseWriter, r *http.Request) {
		serveFile(w, web.FS(), "style.css", "text/css; charset=utf-8")
	})
	mux.HandleFunc("GET /api/session", s.getSession)
	mux.HandleFunc("POST /api/login", s.postLogin)
	mux.HandleFunc("POST /api/logout", s.postLogout)
	api := http.StripPrefix("/api", s.API())
	mux.Handle("/api/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.auth.valid(sessionToken(r)) {
			writeErr(w, http.StatusUnauthorized, "sign in first")
			return
		}
		api.ServeHTTP(w, r)
	}))
	return headers(mux)
}

func headers(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; frame-ancestors 'none'")
		h.ServeHTTP(w, r)
	})
}

func serveFile(w http.ResponseWriter, fsys fs.FS, name, ctype string) {
	b, err := fs.ReadFile(fsys, name)
	if err != nil {
		http.NotFound(w, nil)
		return
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(b)
}

func (s *Server) hostname() string { return s.system().Hostname }

func (s *Server) getSession(w http.ResponseWriter, r *http.Request) {
	if !s.auth.valid(sessionToken(r)) {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "sign in first", "hostname": s.hostname(), "version": s.opt.Version})
		return
	}
	writeJSON(w, 200, map[string]any{"user": loginUser, "hostname": s.hostname(), "version": s.opt.Version, "role": s.opt.Role})
}

func (s *Server) postLogin(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get(csrfHeader) != csrfValue {
		writeErr(w, http.StatusForbidden, "request refused")
		return
	}
	ip := clientIP(r)
	if wait := s.auth.locked(ip); wait > 0 {
		writeErr(w, http.StatusTooManyRequests, "too many wrong passwords; try again in "+strconv.Itoa(int(wait.Seconds())+1)+" s")
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if err := readJSON(r, &req); err != nil {
		fail(w, err)
		return
	}
	tok, ok := s.auth.login(ip, req.Password)
	if !ok {
		s.log.Warn("wrong password on the satellite page", "from", ip)
		writeErr(w, http.StatusUnauthorized, "wrong password")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: tok, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: int(sessionTTL.Seconds())})
	s.log.Info("signed in to the satellite page", "from", ip)
	writeJSON(w, 200, map[string]string{"user": loginUser})
}

func (s *Server) postLogout(w http.ResponseWriter, r *http.Request) {
	s.auth.logout(sessionToken(r))
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) getStatus(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	out := map[string]any{
		"role":       s.opt.Role,
		"system":     s.system(),
		"interfaces": interfaces(),
		"tunnel":     s.tunnel(ctx),
		"radio":      radioSummary(s.loraConfig()),
		"version":    s.opt.Version,
	}
	if svc, _ := s.run.Run(ctx, "", "systemctl", "is-active", "raw-lora"); svc != "" {
		out["engine_service"] = trimSpace(svc)
	}
	if st, err := s.engine(ctx); err == nil {
		out["engine"] = st
	} else {
		out["engine_error"] = "the radio engine does not answer on its status port: " + err.Error()
	}
	writeJSON(w, 200, out)
}

func trimSpace(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r' || s[len(s)-1] == ' ') {
		s = s[:len(s)-1]
	}
	return s
}

func (s *Server) getNetwork(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	writeJSON(w, 200, s.network(ctx))
}

func (s *Server) answerChange(w http.ResponseWriter, c Change, err error) {
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, c)
}

// post decodes a JSON body into a T and hands it on with a context that gives
// slow nmcli calls room.
func post[T any](w http.ResponseWriter, r *http.Request, f func(ctx context.Context, req T) (Change, error), s *Server) {
	var req T
	if err := readJSON(r, &req); err != nil {
		fail(w, err)
		return
	}
	ctx, cancel := waitCtx(r.Context())
	defer cancel()
	c, err := f(ctx, req)
	s.answerChange(w, c, err)
}

func ok(w http.ResponseWriter, err error) {
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) postEthernet(w http.ResponseWriter, r *http.Request) {
	post(w, r, s.setEthernet, s)
}

func (s *Server) getScan(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := waitCtx(r.Context())
	defer cancel()
	writeJSON(w, 200, s.scan(ctx, true))
}

func (s *Server) postWiFiConnect(w http.ResponseWriter, r *http.Request) {
	post(w, r, s.connectWiFi, s)
}

type keepRequest struct {
	Keep bool `json:"keep"`
}

func (s *Server) postWiFiDisconnect(w http.ResponseWriter, r *http.Request) {
	post(w, r, func(ctx context.Context, req keepRequest) (Change, error) { return s.disconnectWiFi(ctx, req.Keep) }, s)
}

// RadioRequest switches the Wi-Fi radio on or off.
type RadioRequest struct {
	On   bool `json:"on"`
	Keep bool `json:"keep"`
}

func (s *Server) postWiFiRadio(w http.ResponseWriter, r *http.Request) {
	post(w, r, s.setWiFiRadio, s)
}

func (s *Server) postWiFiAutoconnect(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UUID string `json:"uuid"`
		On   bool   `json:"on"`
	}
	if err := readJSON(r, &req); err != nil {
		fail(w, err)
		return
	}
	ctx, cancel := waitCtx(r.Context())
	defer cancel()
	ok(w, s.setAutoconnect(ctx, req.UUID, req.On))
}

func (s *Server) postWiFiForget(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UUID string `json:"uuid"`
	}
	if err := readJSON(r, &req); err != nil {
		fail(w, err)
		return
	}
	ctx, cancel := waitCtx(r.Context())
	defer cancel()
	ok(w, s.forgetWiFi(ctx, req.UUID))
}

func (s *Server) postAP(w http.ResponseWriter, r *http.Request) {
	post(w, r, s.setAP, s)
}

func (s *Server) postTunnel(w http.ResponseWriter, r *http.Request) {
	post(w, r, s.setTunnel, s)
}

func (s *Server) postRadioRestart(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if _, err := s.run.Run(ctx, "", "systemctl", "restart", "raw-lora"); err != nil {
		fail(w, err)
		return
	}
	s.log.Info("radio engine restarted from the device page")
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) postReboot(w http.ResponseWriter, r *http.Request) {
	s.log.Info("reboot requested from the device page", "from", clientIP(r))
	writeJSON(w, 200, map[string]bool{"ok": true})
	go func() {
		time.Sleep(time.Second)
		s.run.Run(context.Background(), "", "systemctl", "reboot")
	}()
}

func (s *Server) postPoweroff(w http.ResponseWriter, r *http.Request) {
	s.log.Info("power off requested from the device page", "from", clientIP(r))
	writeJSON(w, 200, map[string]bool{"ok": true})
	go func() {
		time.Sleep(time.Second)
		s.run.Run(context.Background(), "", "systemctl", "poweroff")
	}()
}

// logUnits are the journals the Logs tab can show.
var logUnits = map[string][]string{
	"radio":   {"raw-lora"},
	"tunnel":  {"wg-quick@wg0", "wg-roam"},
	"network": {"NetworkManager"},
	"page":    {"lorahub-sat"},
	"hub":     {"lorahub"},
}

func (s *Server) getLogs(w http.ResponseWriter, r *http.Request) {
	units, ok := logUnits[r.URL.Query().Get("unit")]
	if !ok {
		writeErr(w, 400, "unit must be radio, tunnel, network, page or hub")
		return
	}
	n := 200
	if v, err := strconv.Atoi(r.URL.Query().Get("lines")); err == nil && v > 0 && v <= 2000 {
		n = v
	}
	args := []string{"--no-pager", "-o", "short-iso", "-n", strconv.Itoa(n)}
	for _, u := range units {
		args = append(args, "-u", u)
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	out, err := s.run.Run(ctx, "", "journalctl", args...)
	if err != nil && out == "" {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]string{"text": out})
}
