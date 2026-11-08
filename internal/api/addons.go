package api

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

func runCmd(timeout time.Duration, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	return string(out), err
}

func (s *Server) serviceState() string {
	out, _ := runCmd(3*time.Second, "systemctl", "is-active", s.Cfg.Concentrator.Service)
	if st := strings.TrimSpace(out); st != "" && !strings.Contains(st, "\n") {
		return st
	}
	return "unknown"
}

var serviceCmds = map[string]bool{"start": true, "stop": true, "restart": true}

func (s *Server) concentratorService(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Cmd string `json:"cmd"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if !serviceCmds[req.Cmd] {
		writeErr(w, 400, "cmd must be start, stop or restart")
		return
	}
	if out, err := runCmd(30*time.Second, "systemctl", req.Cmd, s.Cfg.Concentrator.Service); err != nil {
		writeErr(w, 500, fmt.Sprintf("%v: %s", err, out))
		return
	}
	writeJSON(w, 200, map[string]string{"service": s.serviceState()})
}

func (s *Server) getLogs(w http.ResponseWriter, r *http.Request) {
	unit := map[string]string{"concentrator": s.Cfg.Concentrator.Service, "lorahub": "lorahub"}[r.URL.Query().Get("source")]
	if unit == "" {
		writeErr(w, 400, "source must be concentrator or lorahub")
		return
	}
	lines, _ := strconv.Atoi(r.URL.Query().Get("lines"))
	if lines <= 0 || lines > 2000 {
		lines = 300
	}
	out, err := runCmd(8*time.Second, "journalctl", "-u", unit, "-n", strconv.Itoa(lines), "--no-pager", "-o", "short-iso")
	if err != nil {
		writeErr(w, 500, fmt.Sprintf("cannot read the system log: %v", err))
		return
	}
	writeJSON(w, 200, map[string]string{"log": out})
}

// concentratorTX hands a test transmission straight to raw_lora_app.
func (s *Server) concentratorTX(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Abort      bool    `json:"abort"`
		Modu       string  `json:"modu"`
		Freq       float64 `json:"freq"`
		Datr       string  `json:"datr"`
		Codr       string  `json:"codr"`
		Power      int     `json:"powe"`
		RFChain    int     `json:"rfch"`
		InvertPol  bool    `json:"ipol"`
		Preamble   int     `json:"prea"`
		PayloadHex string  `json:"payload_hex"`
		ASCII      string  `json:"ascii"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if req.RFChain != 0 && req.RFChain != 1 {
		writeErr(w, 400, "RF chain must be 0 or 1")
		return
	}
	msg := map[string]any{}
	if req.Abort {
		msg = map[string]any{"abort": true, "rfch": req.RFChain}
	} else {
		if req.Modu != "LORA" && req.Modu != "FSK" && req.Modu != "CW" {
			writeErr(w, 400, "mode must be LORA, FSK or CW")
			return
		}
		if req.Freq < 100 || req.Freq > 1100 {
			writeErr(w, 400, "frequency must be given in MHz")
			return
		}
		if req.Power < 0 || req.Power > 27 {
			writeErr(w, 400, "power must be 0 to 27 dBm")
			return
		}
		data := []byte(req.ASCII)
		if req.PayloadHex != "" {
			var err error
			if data, err = hex.DecodeString(strings.ReplaceAll(req.PayloadHex, " ", "")); err != nil {
				writeErr(w, 400, "payload must be hex")
				return
			}
		}
		if len(data) > 255 {
			writeErr(w, 400, "payload is longer than 255 bytes")
			return
		}
		msg = map[string]any{"imme": true, "freq": req.Freq, "rfch": req.RFChain, "powe": req.Power, "modu": req.Modu, "codr": req.Codr,
			"ipol": req.InvertPol, "size": len(data), "data": base64.StdEncoding.EncodeToString(data)}
		if req.Modu == "FSK" {
			rate, err := strconv.Atoi(req.Datr)
			if err != nil {
				writeErr(w, 400, "FSK data rate must be a number in bit/s")
				return
			}
			msg["datr"] = rate
		} else {
			msg["datr"] = req.Datr
		}
		if req.Preamble > 0 {
			msg["prea"] = req.Preamble
		}
	}
	b, _ := json.Marshal(msg)
	c, err := net.Dial("udp", fmt.Sprintf("127.0.0.1:%d", s.Cfg.Bobcat.DownlinkPort))
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	defer c.Close()
	if _, err := c.Write(b); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"sent": msg})
}

var gpioLine = regexp.MustCompile(`^\s*gpio-(\d+)\s+\(([^|)]*)\|?([^)]*)\)\s+(in|out)\s+(hi|lo)`)
var gpioChip = regexp.MustCompile(`^gpiochip(\d+):`)

type gpioInfo struct {
	Global    int    `json:"gpio"`
	Pin       string `json:"pin"`
	Label     string `json:"label"`
	Direction string `json:"direction"`
	Value     int    `json:"value"`
	CanSet    bool   `json:"can_set"`
}

func readTrim(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func gpioBases() []int {
	dirs, _ := filepath.Glob("/sys/class/gpio/gpiochip*")
	var out []int
	for _, d := range dirs {
		if v, err := strconv.Atoi(readTrim(d + "/base")); err == nil {
			out = append(out, v)
		}
	}
	sort.Ints(out)
	return out
}

func (s *Server) concentratorFile() map[string]any {
	var cfg map[string]any
	if b, err := os.ReadFile(s.Cfg.Concentrator.ConfigPath); err == nil {
		json.Unmarshal(b, &cfg)
	}
	return cfg
}

func (s *Server) readGPIO() []gpioInfo {
	allowed := map[int]bool{}
	for _, g := range s.Cfg.Concentrator.GPIOAllowed {
		allowed[g] = true
	}
	cfg := s.concentratorFile()
	protected := map[int]bool{}
	for _, k := range []string{"reset_gpio", "pa_gpio"} {
		if v, err := strconv.Atoi(fmt.Sprint(cfg[k])); err == nil {
			protected[v] = true
		}
	}
	bases := gpioBases()
	bank := -1
	out := []gpioInfo{}
	for _, l := range strings.Split(readTrim("/sys/kernel/debug/gpio"), "\n") {
		if m := gpioChip.FindStringSubmatch(l); m != nil {
			bank, _ = strconv.Atoi(m[1])
			continue
		}
		m := gpioLine.FindStringSubmatch(l)
		if m == nil || bank < 0 || bank >= len(bases) {
			continue
		}
		line, _ := strconv.Atoi(m[1])
		g := gpioInfo{Global: bases[bank] + line, Pin: fmt.Sprintf("GPIO%d_%c%d", bank, 'A'+line/8, line%8), Label: strings.TrimSpace(m[3]), Direction: m[4]}
		if m[5] == "hi" {
			g.Value = 1
		}
		g.CanSet = allowed[g.Global] && !protected[g.Global] && (g.Label == "" || g.Label == "sysfs")
		out = append(out, g)
	}
	return out
}

func (s *Server) getHardware(w http.ResponseWriter, r *http.Request) {
	leds := []map[string]any{}
	dirs, _ := filepath.Glob("/sys/class/leds/*")
	for _, d := range dirs {
		v, _ := strconv.Atoi(readTrim(d + "/brightness"))
		m, _ := strconv.Atoi(readTrim(d + "/max_brightness"))
		trig := ""
		for _, t := range strings.Fields(readTrim(d + "/trigger")) {
			if strings.HasPrefix(t, "[") {
				trig = strings.Trim(t, "[]")
			}
		}
		leds = append(leds, map[string]any{"name": filepath.Base(d), "brightness": v, "max": m, "trigger": trig})
	}
	nets := []map[string]any{}
	ifs, _ := filepath.Glob("/sys/class/net/*")
	for _, d := range ifs {
		rx, _ := strconv.ParseUint(readTrim(d+"/statistics/rx_packets"), 10, 64)
		tx, _ := strconv.ParseUint(readTrim(d+"/statistics/tx_packets"), 10, 64)
		nets = append(nets, map[string]any{"name": filepath.Base(d), "state": readTrim(d + "/operstate"), "carrier": readTrim(d+"/carrier") == "1",
			"mac": readTrim(d + "/address"), "rx_packets": rx, "tx_packets": tx})
	}
	cfg := s.concentratorFile()
	roles := []map[string]any{}
	for _, x := range [][2]string{{"SPI device", "spi_path"}, {"Concentrator reset GPIO", "reset_gpio"}, {"PA GPIO", "pa_gpio"}, {"RX LED", "led_rx"}, {"TX LED", "led_tx"}} {
		if v := fmt.Sprint(cfg[x[1]]); cfg[x[1]] != nil && v != "" {
			roles = append(roles, map[string]any{"function": x[0], "value": v})
		}
	}
	writeJSON(w, 200, map[string]any{"gpio": s.readGPIO(), "leds": leds, "net": nets, "roles": roles, "board": strings.Trim(readTrim("/proc/device-tree/model"), "\x00")})
}

func (s *Server) setLED(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name  string `json:"name"`
		Value int    `json:"value"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	dir := "/sys/class/leds/" + filepath.Base(req.Name)
	if req.Name == "" || req.Name != filepath.Base(req.Name) || readTrim(dir+"/max_brightness") == "" {
		writeErr(w, 404, "no such LED")
		return
	}
	if err := os.WriteFile(dir+"/brightness", []byte(strconv.Itoa(req.Value)), 0o644); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) setGPIO(w http.ResponseWriter, r *http.Request) {
	var req struct {
		GPIO   int    `json:"gpio"`
		Action string `json:"action"` // set | pulse
		Value  int    `json:"value"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	found := false
	for _, g := range s.readGPIO() {
		if g.Global == req.GPIO && g.CanSet {
			found = true
		}
	}
	if !found {
		writeErr(w, 403, "this GPIO is protected: only lines listed under concentrator.gpio_allowed in lorahub.yml can be driven")
		return
	}
	dir := fmt.Sprintf("/sys/class/gpio/gpio%d", req.GPIO)
	if _, err := os.Stat(dir); err != nil {
		os.WriteFile("/sys/class/gpio/export", []byte(strconv.Itoa(req.GPIO)), 0o200)
	}
	if err := os.WriteFile(dir+"/direction", []byte("out"), 0o644); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	set := func(v int) error { return os.WriteFile(dir+"/value", []byte(strconv.Itoa(v)), 0o644) }
	var err error
	if req.Action == "pulse" {
		if err = set(1); err == nil {
			time.Sleep(100 * time.Millisecond)
			err = set(0)
		}
	} else {
		v := 0
		if req.Value == 1 {
			v = 1
		}
		err = set(v)
	}
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
