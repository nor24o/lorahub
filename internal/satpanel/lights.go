package satpanel

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// The status light is driven by raw_lora_app, which reads /etc/lora/led_mode
// once a second: a mode word, then the colours (engine 2.2; 2.1 reads only the
// mode, 2.0 nothing):
//
//	steady idle=cyan rx=red tx=blue
//
// Modes:
//
//	normal  heartbeat in the idle colour, a flash for each packet
//	steady  idle colour lit all the time, a flash for each packet
//	quiet   heartbeat, no packet flashes
//	solid   idle colour lit all the time, no packet flashes
//	errors  dark unless something is wrong
//	off     always dark
//
// The Bobcat's light is one RGB LED with on/off channels, so it shows seven
// colours. The error signals keep their own: fast red for a radio error,
// yellow blinking for no network. The panel writes the file from the day mode,
// the colours and an optional night schedule.
var (
	ledModes  = []string{"normal", "steady", "quiet", "solid", "errors", "off"}
	ledColors = []string{"green", "red", "blue", "yellow", "cyan", "magenta", "white", "off"}
)

func oneOf(v string, list []string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func validMode(m string) bool { return oneOf(m, ledModes) }

// LightColors are the colours of the idle light and the packet flashes.
type LightColors struct {
	Idle string `json:"idle"`
	RX   string `json:"rx"`
	TX   string `json:"tx"`
}

// NightLights is the schedule: Mode between From and To (local time, HH:MM;
// the window may cross midnight).
type NightLights struct {
	Enabled bool   `json:"enabled"`
	Mode    string `json:"mode"`
	From    string `json:"from"`
	To      string `json:"to"`
}

// LightsConfig is what the page saves.
type LightsConfig struct {
	Mode   string      `json:"mode"`
	Colors LightColors `json:"colors"`
	Night  NightLights `json:"night"`
}

// Lights is what the page shows.
type Lights struct {
	LightsConfig
	Effective       string       `json:"effective"`             // the mode the schedule asks for now
	Engine          string       `json:"engine_mode,omitempty"` // what the engine reports
	EngineColors    *LightColors `json:"engine_colors,omitempty"`
	Supported       bool         `json:"supported"`        // the engine reads a mode (2.1 or later)
	ColorsSupported bool         `json:"colors_supported"` // and colours (2.2 or later)
	Modes           []string     `json:"modes"`
	ColorList       []string     `json:"color_list"`
	Preview         *time.Time   `json:"preview_until,omitempty"`
}

func defaultColors() LightColors { return LightColors{Idle: "green", RX: "red", TX: "blue"} }

func defaultLights() LightsConfig {
	return LightsConfig{Mode: "normal", Colors: defaultColors(), Night: NightLights{Mode: "off", From: "22:00", To: "07:00"}}
}

// line is what goes into the engine's file for a mode.
func (c LightsConfig) line(mode string) string {
	return mode + " idle=" + c.Colors.Idle + " rx=" + c.Colors.RX + " tx=" + c.Colors.TX
}

func (s *Server) loadLights() LightsConfig {
	cfg := defaultLights()
	if b, err := os.ReadFile(s.path(s.opt.LightsFile)); err == nil {
		json.Unmarshal(b, &cfg)
	}
	def := defaultColors()
	if !validMode(cfg.Mode) {
		cfg.Mode = "normal"
	}
	if !validMode(cfg.Night.Mode) {
		cfg.Night.Mode = "off"
	}
	for _, c := range []struct {
		v *string
		d string
	}{{&cfg.Colors.Idle, def.Idle}, {&cfg.Colors.RX, def.RX}, {&cfg.Colors.TX, def.TX}} {
		if !oneOf(*c.v, ledColors) {
			*c.v = c.d
		}
	}
	return cfg
}

func clockMinutes(hhmm string) (int, bool) {
	t, err := time.Parse("15:04", strings.TrimSpace(hhmm))
	if err != nil {
		return 0, false
	}
	return t.Hour()*60 + t.Minute(), true
}

// effectiveMode is the mode for the given local time.
func effectiveMode(cfg LightsConfig, now time.Time) string {
	if cfg.Night.Enabled {
		from, ok1 := clockMinutes(cfg.Night.From)
		to, ok2 := clockMinutes(cfg.Night.To)
		m := now.Hour()*60 + now.Minute()
		if ok1 && ok2 && from != to {
			in := (from < to && m >= from && m < to) || (from > to && (m >= from || m < to))
			if in {
				return cfg.Night.Mode
			}
		}
	}
	return cfg.Mode
}

func (s *Server) writeLEDLine(line string) {
	p := s.path(s.opt.LEDModeFile)
	if b, err := os.ReadFile(p); err == nil && strings.TrimSpace(string(b)) == line {
		return
	}
	if err := writeAtomic(p, []byte(line+"\n"), 0o644); err != nil {
		s.log.Warn("cannot set the lights", "err", err)
		return
	}
	s.log.Info("lights", "set", line)
}

// applyLights writes the engine's file from the saved settings, unless a
// colour is being previewed.
func (s *Server) applyLights() {
	s.lightsMu.Lock()
	defer s.lightsMu.Unlock()
	if s.now().Before(s.previewUntil) {
		return
	}
	cfg := s.loadLights()
	s.writeLEDLine(cfg.line(effectiveMode(cfg, s.now().In(s.localZone()))))
}

func (s *Server) lightsLoop(ctx context.Context) {
	if _, err := os.Stat(s.path(s.opt.LightsFile)); err == nil {
		s.applyLights()
	}
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		// Without saved settings the panel leaves the engine's file alone.
		if _, err := os.Stat(s.path(s.opt.LightsFile)); err == nil {
			s.applyLights()
		}
	}
}

func (s *Server) lights(ctx context.Context) Lights {
	cfg := s.loadLights()
	l := Lights{LightsConfig: cfg, Effective: effectiveMode(cfg, s.now().In(s.localZone())), Modes: ledModes, ColorList: ledColors}
	s.lightsMu.Lock()
	if s.now().Before(s.previewUntil) {
		t := s.previewUntil.UTC()
		l.Preview = &t
	}
	s.lightsMu.Unlock()
	if st, err := s.engine(ctx); err == nil {
		if m, ok := st["led_mode"].(string); ok {
			l.Engine, l.Supported = m, true
		}
		if idle, ok := st["led_idle"].(string); ok {
			rx, _ := st["led_rx"].(string)
			tx, _ := st["led_tx"].(string)
			l.EngineColors, l.ColorsSupported = &LightColors{Idle: idle, RX: rx, TX: tx}, true
		}
	}
	return l
}

func (s *Server) setLights(cfg LightsConfig) error {
	if !validMode(cfg.Mode) || (cfg.Night.Enabled && !validMode(cfg.Night.Mode)) {
		return badRequest(errors.New("a mode is normal, steady, quiet, solid, errors or off"))
	}
	if cfg.Colors == (LightColors{}) {
		cfg.Colors = s.loadLights().Colors
	}
	for _, c := range []string{cfg.Colors.Idle, cfg.Colors.RX, cfg.Colors.TX} {
		if !oneOf(c, ledColors) {
			return badRequest(errors.New("a colour is green, red, blue, yellow, cyan, magenta, white or off"))
		}
	}
	if cfg.Night.Enabled {
		if _, ok := clockMinutes(cfg.Night.From); !ok {
			return badRequest(errors.New("the night starts at a time like 22:00"))
		}
		if _, ok := clockMinutes(cfg.Night.To); !ok {
			return badRequest(errors.New("the night ends at a time like 07:00"))
		}
	}
	if !validMode(cfg.Night.Mode) {
		cfg.Night.Mode = "off"
	}
	b, _ := json.MarshalIndent(cfg, "", "  ")
	p := s.path(s.opt.LightsFile)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	if err := writeAtomic(p, b, 0o644); err != nil {
		return err
	}
	s.lightsMu.Lock()
	s.previewUntil = time.Time{} // saving ends a preview
	s.lightsMu.Unlock()
	s.applyLights()
	return nil
}

// PreviewRequest lights one colour for a few seconds, to see it on the board.
type PreviewRequest struct {
	Color   string `json:"color"`
	Seconds int    `json:"seconds"`
}

func (s *Server) previewLights(req PreviewRequest) (time.Time, error) {
	if !oneOf(req.Color, ledColors) {
		return time.Time{}, badRequest(errors.New("a colour is green, red, blue, yellow, cyan, magenta, white or off"))
	}
	if req.Seconds <= 0 {
		req.Seconds = 5
	}
	if req.Seconds > 30 {
		req.Seconds = 30
	}
	d := time.Duration(req.Seconds) * time.Second
	s.lightsMu.Lock()
	until := s.now().Add(d)
	s.previewUntil = until
	s.writeLEDLine("solid idle=" + req.Color + " rx=" + req.Color + " tx=" + req.Color)
	s.lightsMu.Unlock()
	time.AfterFunc(d, s.endPreview)
	return until, nil
}

// endPreview goes back to the saved settings, or to the engine's default when
// nothing was saved.
func (s *Server) endPreview() {
	s.lightsMu.Lock()
	if s.now().Before(s.previewUntil) {
		s.lightsMu.Unlock()
		return // a newer preview is running
	}
	s.previewUntil = time.Time{}
	_, err := os.Stat(s.path(s.opt.LightsFile))
	s.lightsMu.Unlock()
	if err == nil {
		s.applyLights()
		return
	}
	s.lightsMu.Lock()
	s.writeLEDLine(defaultLights().line("normal"))
	s.lightsMu.Unlock()
}

func (s *Server) getLights(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	writeJSON(w, 200, s.lights(ctx))
}

func (s *Server) postLights(w http.ResponseWriter, r *http.Request) {
	var cfg LightsConfig
	if err := readJSON(r, &cfg); err != nil {
		fail(w, err)
		return
	}
	if err := s.setLights(cfg); err != nil {
		fail(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	time.Sleep(1200 * time.Millisecond) // the engine reads the file once a second
	writeJSON(w, 200, s.lights(ctx))
}

func (s *Server) postLightsPreview(w http.ResponseWriter, r *http.Request) {
	var req PreviewRequest
	if err := readJSON(r, &req); err != nil {
		fail(w, err)
		return
	}
	until, err := s.previewLights(req)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"until": until.UTC()})
}
