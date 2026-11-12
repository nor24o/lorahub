package satpanel

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

// SystemInfo is the board's own telemetry.
type SystemInfo struct {
	Hostname  string             `json:"hostname"`
	OS        string             `json:"os,omitempty"`
	Kernel    string             `json:"kernel,omitempty"`
	UptimeS   float64            `json:"uptime_s"`
	Load      [3]float64         `json:"load"`
	CPUs      int                `json:"cpus"`
	Temps     map[string]float64 `json:"temps_c"`
	MemTotal  uint64             `json:"mem_total"`
	MemAvail  uint64             `json:"mem_available"`
	DiskTotal uint64             `json:"disk_total"`
	DiskFree  uint64             `json:"disk_free"`
	Time      time.Time          `json:"time"`
}

// Iface is a network interface and its addresses.
type Iface struct {
	Name  string   `json:"name"`
	MAC   string   `json:"mac,omitempty"`
	Up    bool     `json:"up"`
	Addrs []string `json:"addrs"`
}

func (s *Server) path(p string) string { return filepath.Join(s.root, filepath.FromSlash(p)) }

func (s *Server) readText(p string) string {
	b, err := os.ReadFile(s.path(p))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func (s *Server) system() SystemInfo {
	si := SystemInfo{CPUs: runtime.NumCPU(), Temps: map[string]float64{}, Time: s.now().UTC()}
	si.Hostname, _ = os.Hostname()
	if h := s.readText("/etc/hostname"); h != "" {
		si.Hostname = h
	}
	si.Kernel = s.readText("/proc/sys/kernel/osrelease")
	for _, l := range strings.Split(s.readText("/etc/os-release"), "\n") {
		if v, ok := strings.CutPrefix(l, "PRETTY_NAME="); ok {
			si.OS = strings.Trim(v, `"'`)
		}
	}
	if f := strings.Fields(s.readText("/proc/uptime")); len(f) > 0 {
		si.UptimeS, _ = strconv.ParseFloat(f[0], 64)
	}
	if f := strings.Fields(s.readText("/proc/loadavg")); len(f) >= 3 {
		for i := 0; i < 3; i++ {
			si.Load[i], _ = strconv.ParseFloat(f[i], 64)
		}
	}
	si.MemTotal, si.MemAvail = parseMeminfo(s.readText("/proc/meminfo"))
	zones, _ := filepath.Glob(s.path("/sys/class/thermal/thermal_zone*"))
	for _, z := range zones {
		name := strings.TrimSpace(readFile(filepath.Join(z, "type")))
		milli, err := strconv.ParseFloat(strings.TrimSpace(readFile(filepath.Join(z, "temp"))), 64)
		if name != "" && err == nil {
			si.Temps[name] = float64(int(milli/100)) / 10
		}
	}
	si.DiskTotal, si.DiskFree = diskSpace(s.path("/"))
	return si
}

func readFile(p string) string {
	b, _ := os.ReadFile(p)
	return string(b)
}

// parseMeminfo returns MemTotal and MemAvailable in bytes.
func parseMeminfo(text string) (total, avail uint64) {
	for _, l := range strings.Split(text, "\n") {
		f := strings.Fields(l)
		if len(f) < 2 {
			continue
		}
		v, _ := strconv.ParseUint(f[1], 10, 64)
		switch f[0] {
		case "MemTotal:":
			total = v * 1024
		case "MemAvailable:":
			avail = v * 1024
		}
	}
	return total, avail
}

func interfaces() []Iface {
	list, _ := net.Interfaces()
	out := []Iface{}
	for _, in := range list {
		if in.Flags&net.FlagLoopback != 0 || strings.HasPrefix(in.Name, "p2p-") {
			continue
		}
		it := Iface{Name: in.Name, MAC: in.HardwareAddr.String(), Up: in.Flags&net.FlagUp != 0, Addrs: []string{}}
		addrs, _ := in.Addrs()
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok && ipn.IP.To4() != nil {
				it.Addrs = append(it.Addrs, ipn.String())
			}
		}
		out = append(out, it)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// loraConfig reads the engine's config.json.
func (s *Server) loraConfig() map[string]any {
	var cfg map[string]any
	b, err := os.ReadFile(s.path(s.opt.LoraConfig))
	if err != nil || json.Unmarshal(b, &cfg) != nil {
		return map[string]any{}
	}
	return cfg
}

// radioKeys are the settings shown read-only on the page; the link key and the
// old bridge's sections stay out.
var radioKeys = []string{"rf0_freq", "rf1_freq", "rf0_tx_enable", "rf1_tx_enable", "tx_default_freq",
	"tx_default_power", "tx_default_sf", "tx_default_bandwidth", "tx_default_coderate", "tx_default_invert",
	"tx_default_preamble", "sync_word", "target_ip", "port_up", "port_down", "status_port",
	"heartbeat_interval_s", "full_duplex", "clksrc"}

func radioSummary(cfg map[string]any) map[string]any {
	out := map[string]any{}
	for _, k := range radioKeys {
		if v, ok := cfg[k]; ok {
			out[k] = v
		}
	}
	_, out["paired"] = cfg["link_key"]
	return out
}

func (s *Server) hubIP() string {
	if ip, _ := s.loraConfig()["target_ip"].(string); ip != "" {
		return ip
	}
	return "10.77.0.1"
}

func (s *Server) statusAddr() string {
	port := 5557
	if p, ok := s.loraConfig()["status_port"].(float64); ok && p > 0 {
		port = int(p)
	}
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
}

// engine reads raw_lora_app's status server, which sends one JSON object and
// closes the connection.
func (s *Server) engine(ctx context.Context) (map[string]any, error) {
	var d net.Dialer
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	c, err := d.DialContext(ctx, "tcp", s.statusAddr())
	if err != nil {
		return nil, err
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(2 * time.Second))
	b, err := io.ReadAll(io.LimitReader(c, 1<<20))
	if err != nil && len(b) == 0 {
		return nil, err
	}
	var st map[string]any
	if err := json.Unmarshal(b, &st); err != nil {
		return nil, err
	}
	return st, nil
}
