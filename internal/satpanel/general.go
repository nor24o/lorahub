package satpanel

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// General is the board's name and clock.
type General struct {
	Hostname string    `json:"hostname"`
	Timezone string    `json:"timezone"`
	NTP      bool      `json:"ntp"`    // time is set from the internet
	Synced   bool      `json:"synced"` // and it is in step
	Time     time.Time `json:"time"`
	Local    string    `json:"local_time"`
}

func (s *Server) general(ctx context.Context) General {
	g := General{Hostname: s.hostname(), Time: s.now().UTC()}
	out, _ := s.run.Run(ctx, "", "timedatectl", "show")
	for _, l := range lines(out) {
		k, v, _ := strings.Cut(strings.TrimSpace(l), "=")
		switch k {
		case "Timezone":
			g.Timezone = v
		case "NTP":
			g.NTP = v == "yes"
		case "NTPSynchronized":
			g.Synced = v == "yes"
		}
	}
	loc := time.Local
	if l, err := time.LoadLocation(g.Timezone); err == nil && g.Timezone != "" {
		loc = l
	}
	g.Local = s.now().In(loc).Format("2006-01-02 15:04:05 MST")
	return g
}

func (s *Server) timezones(ctx context.Context) []string {
	out, _ := s.run.Run(ctx, "", "timedatectl", "list-timezones")
	list := []string{}
	for _, l := range lines(out) {
		list = append(list, strings.TrimSpace(l))
	}
	sort.Strings(list)
	return list
}

// GeneralRequest changes the host name, the time zone and network time.
type GeneralRequest struct {
	Hostname string `json:"hostname"`
	Timezone string `json:"timezone"`
	NTP      *bool  `json:"ntp"`
}

var hostLabel = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?$`)

func (s *Server) setGeneral(ctx context.Context, req GeneralRequest) error {
	cur := s.general(ctx)
	if h := strings.TrimSpace(req.Hostname); h != "" && h != cur.Hostname {
		if !hostLabel.MatchString(h) {
			return badRequest(errors.New("a host name has letters, digits and dashes, up to 63, and does not start or end with a dash"))
		}
		if _, err := s.run.Run(ctx, "", "hostnamectl", "set-hostname", h); err != nil {
			return err
		}
		s.setHostsName(h)
		s.run.Run(ctx, "", "systemctl", "try-restart", "avahi-daemon")
		s.log.Info("host name changed", "from", cur.Hostname, "to", h)
	}
	if tz := strings.TrimSpace(req.Timezone); tz != "" && tz != cur.Timezone {
		known := false
		for _, z := range s.timezones(ctx) {
			if z == tz {
				known = true
				break
			}
		}
		if !known {
			return badRequest(fmt.Errorf("unknown time zone %q", tz))
		}
		if _, err := s.run.Run(ctx, "", "timedatectl", "set-timezone", tz); err != nil {
			return err
		}
	}
	if req.NTP != nil && *req.NTP != cur.NTP {
		if _, err := s.run.Run(ctx, "", "timedatectl", "set-ntp", yesNo(*req.NTP)); err != nil {
			return err
		}
	}
	return nil
}

// setHostsName keeps 127.0.1.1 in /etc/hosts pointing at the new name.
func (s *Server) setHostsName(name string) {
	p := s.path("/etc/hosts")
	b, err := os.ReadFile(p)
	if err != nil {
		return
	}
	var out []string
	found := false
	for _, l := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
		if strings.HasPrefix(l, "127.0.1.1") {
			l, found = "127.0.1.1\t"+name, true
		}
		out = append(out, l)
	}
	if !found {
		out = append(out, "127.0.1.1\t"+name)
	}
	writeAtomic(p, []byte(strings.Join(out, "\n")+"\n"), 0o644)
}

func (s *Server) getGeneral(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	writeJSON(w, 200, s.general(ctx))
}

func (s *Server) getTimezones(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	writeJSON(w, 200, s.timezones(ctx))
}

func (s *Server) postGeneral(w http.ResponseWriter, r *http.Request) {
	var req GeneralRequest
	if err := readJSON(r, &req); err != nil {
		fail(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if err := s.setGeneral(ctx, req); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, s.general(ctx))
}

// localZone is the board's time zone now; Go reads /etc/localtime only once,
// so a zone changed on the page is picked up here.
func (s *Server) localZone() *time.Location {
	if target, err := os.Readlink(s.path("/etc/localtime")); err == nil {
		if i := strings.Index(target, "zoneinfo/"); i >= 0 {
			if l, err := time.LoadLocation(filepath.ToSlash(target[i+len("zoneinfo/"):])); err == nil {
				return l
			}
		}
	}
	return time.Local
}
