package satpanel

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	cookieName  = "lorahub_sat"
	sessionTTL  = 12 * time.Hour
	maxFails    = 5
	failWindow  = 10 * time.Minute
	lockFor     = 5 * time.Minute
	csrfHeader  = "X-Requested-With"
	csrfValue   = "lorahub-sat"
	chkpwdPath  = "/usr/sbin/unix_chkpwd"
	loginUser   = "root"
	failedDelay = time.Second
)

// CheckRootPassword asks PAM's helper whether pw is root's password, the same
// password used to log in on the console or over SSH. It needs no cgo.
func CheckRootPassword(pw string) bool {
	if pw == "" || strings.ContainsRune(pw, 0) {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, chkpwdPath, loginUser, "nonull")
	cmd.Stdin = strings.NewReader(pw + "\x00")
	return cmd.Run() == nil
}

type failRec struct {
	n     int
	first time.Time
	until time.Time
}

// auth keeps sessions by the SHA-256 of their token, in memory and in a file
// readable only by root, so restarting the page does not sign anyone out.
type auth struct {
	mu       sync.Mutex
	sessions map[string]time.Time
	fails    map[string]*failRec
	check    func(string) bool
	now      func() time.Time
	delay    time.Duration
	file     string
	savedAt  time.Time
}

func newAuth(check func(string) bool, now func() time.Time, delay time.Duration, file string) *auth {
	a := &auth{sessions: map[string]time.Time{}, fails: map[string]*failRec{}, check: check, now: now, delay: delay, file: file}
	if b, err := os.ReadFile(file); err == nil && json.Unmarshal(b, &a.sessions) == nil {
		for k, exp := range a.sessions {
			if now().After(exp) {
				delete(a.sessions, k)
			}
		}
	}
	if a.sessions == nil {
		a.sessions = map[string]time.Time{}
	}
	return a
}

func hashToken(t string) string {
	sum := sha256.Sum256([]byte(t))
	return hex.EncodeToString(sum[:])
}

// saveLocked writes the sessions; a.mu must be held.
func (a *auth) saveLocked() {
	if a.file == "" {
		return
	}
	a.savedAt = a.now()
	b, _ := json.Marshal(a.sessions)
	if os.MkdirAll(filepath.Dir(a.file), 0o700) == nil {
		writeAtomic(a.file, b, 0o600)
	}
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// locked says how long an address must still wait after too many wrong passwords.
func (a *auth) locked(ip string) time.Duration {
	a.mu.Lock()
	defer a.mu.Unlock()
	if f := a.fails[ip]; f != nil && a.now().Before(f.until) {
		return f.until.Sub(a.now())
	}
	return 0
}

func (a *auth) login(ip, pw string) (string, bool) {
	if a.check(pw) {
		tok := make([]byte, 32)
		rand.Read(tok)
		t := hex.EncodeToString(tok)
		a.mu.Lock()
		delete(a.fails, ip)
		now := a.now()
		for k, exp := range a.sessions {
			if now.After(exp) {
				delete(a.sessions, k)
			}
		}
		a.sessions[hashToken(t)] = now.Add(sessionTTL)
		a.saveLocked()
		a.mu.Unlock()
		return t, true
	}
	a.mu.Lock()
	f := a.fails[ip]
	now := a.now()
	if f == nil || now.Sub(f.first) > failWindow {
		f = &failRec{first: now}
		a.fails[ip] = f
	}
	f.n++
	if f.n >= maxFails {
		f.until, f.n, f.first = now.Add(lockFor), 0, now
	}
	a.mu.Unlock()
	time.Sleep(a.delay)
	return "", false
}

func (a *auth) valid(tok string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if tok == "" {
		return false
	}
	key := hashToken(tok)
	exp, ok := a.sessions[key]
	if !ok || a.now().After(exp) {
		if ok {
			delete(a.sessions, key)
			a.saveLocked()
		}
		return false
	}
	a.sessions[key] = a.now().Add(sessionTTL)
	if a.now().Sub(a.savedAt) > 10*time.Minute {
		a.saveLocked()
	}
	return true
}

func (a *auth) logout(tok string) {
	a.mu.Lock()
	delete(a.sessions, hashToken(tok))
	a.saveLocked()
	a.mu.Unlock()
}

func sessionToken(r *http.Request) string {
	if c, err := r.Cookie(cookieName); err == nil {
		return c.Value
	}
	return ""
}
