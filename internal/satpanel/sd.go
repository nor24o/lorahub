package satpanel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The Bobcat's SD card reader: the card is the mmcblk device whose type is SD
// (the eMMC reports MMC). It is mounted at Options.SDMount with nosuid, nodev
// and noexec, read-only unless asked otherwise.

// SDPart is a partition (or the whole card when it has none).
type SDPart struct {
	Path   string `json:"path"`
	Size   uint64 `json:"size"`
	FSType string `json:"fstype,omitempty"`
	Label  string `json:"label,omitempty"`
	Mount  string `json:"mount,omitempty"`
}

// SDCard is the reader's state.
type SDCard struct {
	Present    bool     `json:"present"`
	Device     string   `json:"device,omitempty"`
	Size       uint64   `json:"size,omitempty"`
	Parts      []SDPart `json:"parts"`
	MountPoint string   `json:"mount_point"`
	Mounted    bool     `json:"mounted"`
	From       string   `json:"from,omitempty"` // the partition mounted there
	ReadOnly   bool     `json:"read_only"`
	Total      uint64   `json:"total,omitempty"`
	Free       uint64   `json:"free,omitempty"`
	Backup     bool     `json:"backup"` // this board can copy LoRaHub's database to the card
}

// sdDevice finds the card, e.g. /dev/mmcblk1.
func (s *Server) sdDevice() string {
	types, _ := filepath.Glob(s.path("/sys/block/mmcblk*/device/type"))
	sort.Strings(types)
	for _, t := range types {
		b, _ := os.ReadFile(t)
		if strings.TrimSpace(string(b)) == "SD" {
			return "/dev/" + filepath.Base(filepath.Dir(filepath.Dir(t)))
		}
	}
	return ""
}

// flexUint reads lsblk sizes, which are numbers or strings depending on its version.
type flexUint uint64

func (f *flexUint) UnmarshalJSON(b []byte) error {
	v := strings.Trim(string(b), `"`)
	if v == "null" || v == "" {
		*f = 0
		return nil
	}
	n, err := strconv.ParseUint(v, 10, 64)
	*f = flexUint(n)
	return err
}

type lsblkDev struct {
	Path       string     `json:"path"`
	Size       flexUint   `json:"size"`
	FSType     string     `json:"fstype"`
	Label      string     `json:"label"`
	Mountpoint string     `json:"mountpoint"`
	Children   []lsblkDev `json:"children"`
}

// mountOf reads /proc/mounts for what is mounted at dir.
func (s *Server) mountOf(dir string) (dev string, ro bool, ok bool) {
	for _, l := range strings.Split(s.readText("/proc/mounts"), "\n") {
		f := strings.Fields(l)
		if len(f) >= 4 && f[1] == dir {
			dev, ok = f[0], true
			for _, o := range strings.Split(f[3], ",") {
				if o == "ro" {
					ro = true
				}
			}
		}
	}
	return dev, ro, ok
}

func (s *Server) sdCard(ctx context.Context) SDCard {
	c := SDCard{MountPoint: s.opt.SDMount, Parts: []SDPart{}, Backup: s.opt.Backup != nil}
	c.From, c.ReadOnly, c.Mounted = s.mountOf(s.opt.SDMount)
	if c.Mounted {
		c.Total, c.Free = diskSpace(s.path(s.opt.SDMount))
	}
	dev := s.sdDevice()
	if dev == "" {
		return c
	}
	c.Present, c.Device = true, dev
	out, err := s.run.Run(ctx, "", "lsblk", "-J", "-b", "-o", "PATH,SIZE,FSTYPE,LABEL,MOUNTPOINT", dev)
	if err != nil {
		return c
	}
	var tree struct {
		Devices []lsblkDev `json:"blockdevices"`
	}
	if json.Unmarshal([]byte(out), &tree) != nil || len(tree.Devices) == 0 {
		return c
	}
	d := tree.Devices[0]
	c.Size = uint64(d.Size)
	parts := d.Children
	if len(parts) == 0 {
		parts = []lsblkDev{d}
	}
	for _, p := range parts {
		c.Parts = append(c.Parts, SDPart{Path: p.Path, Size: uint64(p.Size), FSType: p.FSType, Label: p.Label, Mount: p.Mountpoint})
	}
	return c
}

// SDMountRequest mounts one partition of the card.
type SDMountRequest struct {
	Path     string `json:"path"`
	ReadOnly bool   `json:"read_only"`
}

func (s *Server) mountSD(ctx context.Context, req SDMountRequest) error {
	c := s.sdCard(ctx)
	if !c.Present {
		return badRequest(errors.New("no card in the reader"))
	}
	if c.Mounted {
		return badRequest(fmt.Errorf("%s is already mounted at %s; unmount it first", c.From, c.MountPoint))
	}
	var part *SDPart
	for i := range c.Parts {
		if c.Parts[i].Path == req.Path {
			part = &c.Parts[i]
		}
	}
	if part == nil {
		return badRequest(errors.New("that is not a partition of the card"))
	}
	if part.FSType == "" {
		return badRequest(errors.New("that partition has no file system this board knows"))
	}
	if part.Mount != "" {
		return badRequest(fmt.Errorf("that partition is already mounted at %s", part.Mount))
	}
	if err := os.MkdirAll(s.path(s.opt.SDMount), 0o755); err != nil {
		return err
	}
	opts := "nosuid,nodev,noexec," + map[bool]string{true: "ro", false: "rw"}[req.ReadOnly]
	if _, err := s.run.Run(ctx, "", "mount", "-o", opts, part.Path, s.opt.SDMount); err != nil {
		return err
	}
	s.log.Info("SD card mounted", "partition", part.Path, "at", s.opt.SDMount, "options", opts)
	return nil
}

func (s *Server) unmountSD(ctx context.Context) error {
	if _, _, ok := s.mountOf(s.opt.SDMount); !ok {
		return badRequest(errors.New("the card is not mounted"))
	}
	s.run.Run(ctx, "", "sync")
	if _, err := s.run.Run(ctx, "", "umount", s.opt.SDMount); err != nil {
		return err
	}
	s.log.Info("SD card unmounted")
	return nil
}

// sdPath turns a path relative to the card into a file name on the board,
// refusing anything (a .. or a symbolic link) that leads off the card.
func (s *Server) sdPath(rel string) (string, error) {
	if _, _, ok := s.mountOf(s.opt.SDMount); !ok {
		return "", badRequest(errors.New("the card is not mounted"))
	}
	root, err := filepath.EvalSymlinks(s.path(s.opt.SDMount))
	if err != nil {
		return "", badRequest(errors.New("the card is not mounted"))
	}
	full, err := filepath.EvalSymlinks(filepath.Join(root, filepath.FromSlash(path.Clean("/"+rel))))
	if err != nil {
		return "", badRequest(errors.New("no such file on the card"))
	}
	if full != root && !strings.HasPrefix(full, root+string(filepath.Separator)) {
		return "", badRequest(errors.New("that path leads off the card"))
	}
	return full, nil
}

// SDEntry is one file or folder on the card.
type SDEntry struct {
	Name     string    `json:"name"`
	Dir      bool      `json:"dir"`
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
}

func (s *Server) listSD(rel string) ([]SDEntry, error) {
	dir, err := s.sdPath(rel)
	if err != nil {
		return nil, err
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, badRequest(err)
	}
	out := []SDEntry{}
	for i, e := range ents {
		if i >= 2000 {
			break
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, SDEntry{Name: e.Name(), Dir: e.IsDir(), Size: info.Size(), Modified: info.ModTime().UTC()})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Dir != out[j].Dir {
			return out[i].Dir
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}

func (s *Server) backupToSD(ctx context.Context) (string, int64, error) {
	if s.opt.Backup == nil {
		return "", 0, badRequest(errors.New("this board has no LoRaHub database to back up"))
	}
	_, ro, ok := s.mountOf(s.opt.SDMount)
	if !ok {
		return "", 0, badRequest(errors.New("mount the card first"))
	}
	if ro {
		return "", 0, badRequest(errors.New("the card is mounted read-only; mount it for writing"))
	}
	name := "lorahub-backup-" + s.now().In(s.localZone()).Format("20060102-150405") + ".db"
	dst := filepath.Join(s.path(s.opt.SDMount), name)
	if err := s.opt.Backup(ctx, dst); err != nil {
		return "", 0, err
	}
	s.run.Run(ctx, "", "sync")
	st, err := os.Stat(dst)
	if err != nil {
		return "", 0, err
	}
	s.log.Info("LoRaHub database copied to the SD card", "file", name, "bytes", st.Size())
	return name, st.Size(), nil
}

func (s *Server) getSD(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	writeJSON(w, 200, s.sdCard(ctx))
}

func (s *Server) postSDMount(w http.ResponseWriter, r *http.Request) {
	var req SDMountRequest
	if err := readJSON(r, &req); err != nil {
		fail(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if err := s.mountSD(ctx, req); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, s.sdCard(ctx))
}

func (s *Server) postSDUnmount(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	if err := s.unmountSD(ctx); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, s.sdCard(ctx))
}

func (s *Server) getSDFiles(w http.ResponseWriter, r *http.Request) {
	list, err := s.listSD(r.URL.Query().Get("path"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, list)
}

func (s *Server) getSDDownload(w http.ResponseWriter, r *http.Request) {
	p, err := s.sdPath(r.URL.Query().Get("path"))
	if err != nil {
		fail(w, err)
		return
	}
	f, err := os.Open(p)
	if err != nil {
		fail(w, badRequest(err))
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		fail(w, badRequest(errors.New("only files can be downloaded")))
		return
	}
	name := strings.Map(func(r rune) rune {
		if r < 32 || r == '"' || r == '\\' {
			return '_'
		}
		return r
	}, st.Name())
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("Content-Type", "application/octet-stream")
	http.ServeContent(w, r, name, st.ModTime(), f)
}

func (s *Server) postSDBackup(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	name, size, err := s.backupToSD(ctx)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"file": name, "size": size})
}
