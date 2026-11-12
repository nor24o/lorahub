// Package store persists LoRaHub state in SQLite and keeps an in-memory
// cache of gateways, applications and devices for fast packet matching.
package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/nor24o/lorahub/internal/model"
	_ "modernc.org/sqlite"
)

// ErrNotFound is returned for missing entities.
var ErrNotFound = errors.New("not found")

// Store is the persistence layer.
type Store struct {
	db *sql.DB
	mu sync.RWMutex

	gateways map[string]*model.Gateway // by ID
	apps     map[string]*model.Application
	devices  map[string]*model.Device // key app/dev
}

const schema = `
CREATE TABLE IF NOT EXISTS users (username TEXT PRIMARY KEY, password_hash TEXT NOT NULL, created_at INTEGER);
CREATE TABLE IF NOT EXISTS sessions (token TEXT PRIMARY KEY, username TEXT, expires_at INTEGER);
CREATE TABLE IF NOT EXISTS api_keys (id TEXT PRIMARY KEY, name TEXT, key_hash TEXT UNIQUE, created_at INTEGER);
CREATE TABLE IF NOT EXISTS settings (key TEXT PRIMARY KEY, value TEXT);
CREATE TABLE IF NOT EXISTS gateways (id TEXT PRIMARY KEY, eui TEXT UNIQUE, data TEXT);
CREATE TABLE IF NOT EXISTS applications (id TEXT PRIMARY KEY, data TEXT);
CREATE TABLE IF NOT EXISTS devices (app_id TEXT, id TEXT, data TEXT, PRIMARY KEY(app_id, id));
CREATE TABLE IF NOT EXISTS events (
  id INTEGER PRIMARY KEY AUTOINCREMENT, time INTEGER, name TEXT, app_id TEXT, dev_id TEXT,
  gateways TEXT, summary TEXT, data TEXT);
CREATE INDEX IF NOT EXISTS ev_time ON events(time);
CREATE INDEX IF NOT EXISTS ev_dev ON events(app_id, dev_id, id);
`

// Open opens (and creates) the database in dataDir.
func Open(dataDir string) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, err
	}
	dsn := "file:" + filepath.Join(dataDir, "lorahub.db") + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		return nil, fmt.Errorf("schema: %w", err)
	}
	s := &Store{db: db, gateways: map[string]*model.Gateway{}, apps: map[string]*model.Application{}, devices: map[string]*model.Device{}}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// Backup writes a consistent copy of the database to path, which must not
// exist yet (SQLite's VACUUM INTO); LoRaHub keeps running meanwhile.
func (s *Store) Backup(ctx context.Context, path string) error {
	_, err := s.db.ExecContext(ctx, "VACUUM INTO ?", path)
	return err
}

func (s *Store) load() error {
	rows, err := s.db.Query(`SELECT data FROM gateways`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var d string
		rows.Scan(&d)
		g := &model.Gateway{}
		if json.Unmarshal([]byte(d), g) == nil {
			s.gateways[g.ID] = g
		}
	}
	rows.Close()
	rows, err = s.db.Query(`SELECT data FROM applications`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var d string
		rows.Scan(&d)
		a := &model.Application{}
		if json.Unmarshal([]byte(d), a) == nil {
			s.apps[a.ID] = a
		}
	}
	rows.Close()
	rows, err = s.db.Query(`SELECT data FROM devices`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var d string
		rows.Scan(&d)
		dv := &model.Device{}
		if json.Unmarshal([]byte(d), dv) == nil {
			s.devices[dv.AppID+"/"+dv.ID] = dv
		}
	}
	rows.Close()
	return nil
}

func clone[T any](v *T) *T {
	b, _ := json.Marshal(v)
	o := new(T)
	json.Unmarshal(b, o)
	return o
}

// ---------- Gateways ----------

// Gateways returns copies of all gateways sorted by ID.
func (s *Store) Gateways() []*model.Gateway {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*model.Gateway, 0, len(s.gateways))
	for _, g := range s.gateways {
		out = append(out, clone(g))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Gateway returns a gateway copy by ID.
func (s *Store) Gateway(id string) (*model.Gateway, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	g, ok := s.gateways[id]
	if !ok {
		return nil, ErrNotFound
	}
	return clone(g), nil
}

// GatewayByEUI finds a gateway by EUI.
func (s *Store) GatewayByEUI(eui string) (*model.Gateway, error) {
	eui = strings.ToUpper(eui)
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, g := range s.gateways {
		if g.EUI == eui {
			return clone(g), nil
		}
	}
	return nil, ErrNotFound
}

// SaveGateway inserts or replaces a gateway.
func (s *Store) SaveGateway(g *model.Gateway) error {
	g.EUI = strings.ToUpper(g.EUI)
	b, _ := json.Marshal(g)
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, o := range s.gateways {
		if o.EUI == g.EUI && id != g.ID {
			return fmt.Errorf("EUI %s already used by gateway %s", g.EUI, id)
		}
	}
	if _, err := s.db.Exec(`INSERT OR REPLACE INTO gateways(id, eui, data) VALUES(?,?,?)`, g.ID, g.EUI, string(b)); err != nil {
		return err
	}
	s.gateways[g.ID] = clone(g)
	return nil
}

// UpdateGateway applies fn to the cached gateway and persists it.
// persist=false only updates the cache (used for hot counters).
func (s *Store) UpdateGateway(id string, persist bool, fn func(*model.Gateway)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.gateways[id]
	if !ok {
		return ErrNotFound
	}
	fn(g)
	if !persist {
		return nil
	}
	b, _ := json.Marshal(g)
	_, err := s.db.Exec(`UPDATE gateways SET data=? WHERE id=?`, string(b), id)
	return err
}

// RenameGateway moves a gateway to a new ID, together with its event history
// and the nodes that were last heard through it. oldEUI is rewritten to the
// gateway's current EUI in stored events.
func (s *Store) RenameGateway(oldID, newID, oldEUI string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.gateways[oldID]
	if !ok {
		return ErrNotFound
	}
	if _, taken := s.gateways[newID]; taken {
		return fmt.Errorf("gateway %s already exists", newID)
	}
	moved := clone(g)
	moved.ID = newID
	data, _ := json.Marshal(moved)
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM gateways WHERE id=?`, oldID); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO gateways(id, eui, data) VALUES(?,?,?)`, newID, moved.EUI, string(data)); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE events SET gateways=replace(gateways,?1,?2), summary=replace(summary,?1,?2),
		data=replace(replace(data,?1,?2),?3,?4) WHERE gateways LIKE ?5`,
		oldID, newID, oldEUI, moved.EUI, "%,"+oldID+",%"); err != nil {
		return err
	}
	var touched []*model.Device
	for _, d := range s.devices {
		if d.LastGateway != oldID {
			continue
		}
		c := clone(d)
		c.LastGateway = newID
		b, _ := json.Marshal(c)
		if _, err := tx.Exec(`UPDATE devices SET data=? WHERE app_id=? AND id=?`, string(b), c.AppID, c.ID); err != nil {
			return err
		}
		touched = append(touched, c)
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	delete(s.gateways, oldID)
	s.gateways[newID] = moved
	for _, d := range touched {
		s.devices[d.AppID+"/"+d.ID] = d
	}
	return nil
}

// DeleteGateway removes a gateway.
func (s *Store) DeleteGateway(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.gateways[id]; !ok {
		return ErrNotFound
	}
	delete(s.gateways, id)
	_, err := s.db.Exec(`DELETE FROM gateways WHERE id=?`, id)
	return err
}

// FlushGateways persists runtime counters of all gateways.
func (s *Store) FlushGateways() {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, g := range s.gateways {
		b, _ := json.Marshal(g)
		s.db.Exec(`UPDATE gateways SET data=? WHERE id=?`, string(b), g.ID)
	}
}

// ---------- Applications ----------

// Applications lists applications.
func (s *Store) Applications() []*model.Application {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*model.Application, 0, len(s.apps))
	for _, a := range s.apps {
		out = append(out, clone(a))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Application returns an application.
func (s *Store) Application(id string) (*model.Application, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	a, ok := s.apps[id]
	if !ok {
		return nil, ErrNotFound
	}
	return clone(a), nil
}

// SaveApplication inserts or replaces an application.
func (s *Store) SaveApplication(a *model.Application) error {
	b, _ := json.Marshal(a)
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.db.Exec(`INSERT OR REPLACE INTO applications(id, data) VALUES(?,?)`, a.ID, string(b)); err != nil {
		return err
	}
	s.apps[a.ID] = clone(a)
	return nil
}

// DeleteApplication deletes an application and its devices.
func (s *Store) DeleteApplication(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.apps[id]; !ok {
		return ErrNotFound
	}
	delete(s.apps, id)
	for k, d := range s.devices {
		if d.AppID == id {
			delete(s.devices, k)
		}
	}
	s.db.Exec(`DELETE FROM devices WHERE app_id=?`, id)
	_, err := s.db.Exec(`DELETE FROM applications WHERE id=?`, id)
	return err
}

// ---------- Devices ----------

// Devices lists devices, optionally filtered by application.
func (s *Store) Devices(appID string) []*model.Device {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []*model.Device{}
	for _, d := range s.devices {
		if appID == "" || d.AppID == appID {
			out = append(out, clone(d))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].AppID != out[j].AppID {
			return out[i].AppID < out[j].AppID
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// Device returns a device.
func (s *Store) Device(appID, id string) (*model.Device, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	d, ok := s.devices[appID+"/"+id]
	if !ok {
		return nil, ErrNotFound
	}
	return clone(d), nil
}

// FindDevices returns copies of devices for which match returns true.
func (s *Store) FindDevices(match func(*model.Device) bool) []*model.Device {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []*model.Device
	for _, d := range s.devices {
		if match(d) {
			out = append(out, clone(d))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].AppID+"/"+out[i].ID < out[j].AppID+"/"+out[j].ID })
	return out
}

// SaveDevice inserts or replaces a device.
func (s *Store) SaveDevice(d *model.Device) error {
	b, _ := json.Marshal(d)
	s.mu.Lock()
	defer s.mu.Unlock()
	if d.DevEUI != "" {
		for k, o := range s.devices {
			if k != d.AppID+"/"+d.ID && strings.EqualFold(o.DevEUI, d.DevEUI) && o.Mode != "raw" && d.Mode != "raw" {
				return fmt.Errorf("DevEUI %s already used by %s", d.DevEUI, k)
			}
		}
	}
	if _, err := s.db.Exec(`INSERT OR REPLACE INTO devices(app_id, id, data) VALUES(?,?,?)`, d.AppID, d.ID, string(b)); err != nil {
		return err
	}
	s.devices[d.AppID+"/"+d.ID] = clone(d)
	return nil
}

// UpdateDevice applies fn atomically to the cached device and persists it.
func (s *Store) UpdateDevice(appID, id string, fn func(*model.Device) error) (*model.Device, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.devices[appID+"/"+id]
	if !ok {
		return nil, ErrNotFound
	}
	work := clone(d)
	if err := fn(work); err != nil {
		return nil, err
	}
	b, _ := json.Marshal(work)
	if _, err := s.db.Exec(`UPDATE devices SET data=? WHERE app_id=? AND id=?`, string(b), appID, id); err != nil {
		return nil, err
	}
	s.devices[appID+"/"+id] = work
	return clone(work), nil
}

// DeleteDevice deletes a device.
func (s *Store) DeleteDevice(appID, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.devices[appID+"/"+id]; !ok {
		return ErrNotFound
	}
	delete(s.devices, appID+"/"+id)
	_, err := s.db.Exec(`DELETE FROM devices WHERE app_id=? AND id=?`, appID, id)
	return err
}

// ---------- Events ----------

// AddEvent stores an event and sets its ID.
func (s *Store) AddEvent(e *model.Event) error {
	b, _ := json.Marshal(e.Data)
	gws := ""
	if len(e.Gateways) > 0 {
		gws = "," + strings.Join(e.Gateways, ",") + ","
	}
	res, err := s.db.Exec(`INSERT INTO events(time, name, app_id, dev_id, gateways, summary, data) VALUES(?,?,?,?,?,?,?)`,
		e.Time.UnixMilli(), e.Name, e.AppID, e.DevID, gws, e.Summary, string(b))
	if err != nil {
		return err
	}
	e.ID, _ = res.LastInsertId()
	return nil
}

// EventFilter filters event queries.
type EventFilter struct {
	AppID, DevID, GatewayID string
	Names                   []string
	Prefix                  string
	BeforeID                int64
	Since                   time.Time
	Limit                   int
}

// Events queries stored events, newest first.
func (s *Store) Events(f EventFilter) ([]model.Event, error) {
	q := `SELECT id, time, name, app_id, dev_id, gateways, summary, data FROM events WHERE 1=1`
	var args []any
	if f.AppID != "" {
		q += ` AND app_id=?`
		args = append(args, f.AppID)
	}
	if f.DevID != "" {
		q += ` AND dev_id=?`
		args = append(args, f.DevID)
	}
	if f.GatewayID != "" {
		q += ` AND gateways LIKE ?`
		args = append(args, "%,"+f.GatewayID+",%")
	}
	if f.Prefix != "" {
		q += ` AND name LIKE ?`
		args = append(args, f.Prefix+"%")
	}
	if len(f.Names) > 0 {
		q += ` AND name IN (` + strings.TrimSuffix(strings.Repeat("?,", len(f.Names)), ",") + `)`
		for _, n := range f.Names {
			args = append(args, n)
		}
	}
	if f.BeforeID > 0 {
		q += ` AND id<?`
		args = append(args, f.BeforeID)
	}
	if !f.Since.IsZero() {
		q += ` AND time>=?`
		args = append(args, f.Since.UnixMilli())
	}
	if f.Limit <= 0 || f.Limit > 5000 {
		f.Limit = 200
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, f.Limit)
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Event{}
	for rows.Next() {
		var e model.Event
		var t int64
		var gws, data string
		if err := rows.Scan(&e.ID, &t, &e.Name, &e.AppID, &e.DevID, &gws, &e.Summary, &data); err != nil {
			return nil, err
		}
		e.Time = time.UnixMilli(t).UTC()
		if gws != "" {
			e.Gateways = strings.Split(strings.Trim(gws, ","), ",")
		}
		e.Data = json.RawMessage(data)
		out = append(out, e)
	}
	return out, nil
}

// EventHistogram returns per-bucket counts of events matching prefix since t.
func (s *Store) EventHistogram(prefix string, since time.Time, bucket time.Duration) (map[int64]int, error) {
	rows, err := s.db.Query(`SELECT (time/?)*? AS b, COUNT(*) FROM events WHERE time>=? AND name LIKE ? GROUP BY b`,
		bucket.Milliseconds(), bucket.Milliseconds(), since.UnixMilli(), prefix+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]int{}
	for rows.Next() {
		var b int64
		var c int
		rows.Scan(&b, &c)
		out[b] = c
	}
	return out, nil
}

// CountEvents counts events with a name prefix since t.
func (s *Store) CountEvents(prefix string, since time.Time) int {
	var c int
	s.db.QueryRow(`SELECT COUNT(*) FROM events WHERE time>=? AND name LIKE ?`, since.UnixMilli(), prefix+"%").Scan(&c)
	return c
}

// PruneEvents deletes events older than maxAge and keeps at most maxRows.
func (s *Store) PruneEvents(maxAge time.Duration, maxRows int) {
	if maxAge > 0 {
		s.db.Exec(`DELETE FROM events WHERE time<?`, time.Now().Add(-maxAge).UnixMilli())
	}
	if maxRows > 0 {
		s.db.Exec(`DELETE FROM events WHERE id <= (SELECT id FROM events ORDER BY id DESC LIMIT 1 OFFSET ?)`, maxRows)
	}
}

// ClearEvents deletes all events matching the filter's app/dev/gateway.
func (s *Store) ClearEvents(appID, devID, gwID string) {
	q := `DELETE FROM events WHERE 1=1`
	var args []any
	if appID != "" {
		q += ` AND app_id=?`
		args = append(args, appID)
	}
	if devID != "" {
		q += ` AND dev_id=?`
		args = append(args, devID)
	}
	if gwID != "" {
		q += ` AND gateways LIKE ?`
		args = append(args, "%,"+gwID+",%")
	}
	s.db.Exec(q, args...)
}

// ---------- Settings ----------

// GetSetting decodes the setting key into v. Returns false if absent.
func (s *Store) GetSetting(key string, v any) bool {
	var d string
	if err := s.db.QueryRow(`SELECT value FROM settings WHERE key=?`, key).Scan(&d); err != nil {
		return false
	}
	return json.Unmarshal([]byte(d), v) == nil
}

// SetSetting stores a setting.
func (s *Store) SetSetting(key string, v any) error {
	b, _ := json.Marshal(v)
	_, err := s.db.Exec(`INSERT OR REPLACE INTO settings(key, value) VALUES(?,?)`, key, string(b))
	return err
}

// ---------- Users / sessions / API keys ----------

// UserCount returns the number of users.
func (s *Store) UserCount() int {
	var c int
	s.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&c)
	return c
}

// SetUser creates or updates a user's password hash.
func (s *Store) SetUser(username, hash string) error {
	_, err := s.db.Exec(`INSERT INTO users(username, password_hash, created_at) VALUES(?,?,?)
		ON CONFLICT(username) DO UPDATE SET password_hash=excluded.password_hash`, username, hash, time.Now().Unix())
	return err
}

// UserHash returns a user's password hash.
func (s *Store) UserHash(username string) (string, error) {
	var h string
	err := s.db.QueryRow(`SELECT password_hash FROM users WHERE username=?`, username).Scan(&h)
	if err != nil {
		return "", ErrNotFound
	}
	return h, nil
}

// NewToken returns a random hex token.
func NewToken(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// CreateSession creates a login session.
func (s *Store) CreateSession(username string, ttl time.Duration) (string, error) {
	t := NewToken(24)
	_, err := s.db.Exec(`INSERT INTO sessions(token, username, expires_at) VALUES(?,?,?)`, t, username, time.Now().Add(ttl).Unix())
	s.db.Exec(`DELETE FROM sessions WHERE expires_at<?`, time.Now().Unix())
	return t, err
}

// SessionUser resolves a session token.
func (s *Store) SessionUser(token string) (string, bool) {
	var u string
	var exp int64
	if err := s.db.QueryRow(`SELECT username, expires_at FROM sessions WHERE token=?`, token).Scan(&u, &exp); err != nil {
		return "", false
	}
	return u, exp > time.Now().Unix()
}

// DeleteSession logs out.
func (s *Store) DeleteSession(token string) { s.db.Exec(`DELETE FROM sessions WHERE token=?`, token) }

// APIKey is an API key (the secret is only returned at creation).
type APIKey struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Key       string    `json:"key,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

func hashKey(k string) string {
	h := sha256.Sum256([]byte(k))
	return hex.EncodeToString(h[:])
}

// CreateAPIKey creates a new API key.
func (s *Store) CreateAPIKey(name string) (*APIKey, error) {
	k := &APIKey{ID: NewToken(6), Name: name, Key: "lhk_" + NewToken(24), CreatedAt: time.Now().UTC()}
	_, err := s.db.Exec(`INSERT INTO api_keys(id, name, key_hash, created_at) VALUES(?,?,?,?)`, k.ID, name, hashKey(k.Key), k.CreatedAt.Unix())
	return k, err
}

// APIKeys lists API keys.
func (s *Store) APIKeys() []APIKey {
	rows, err := s.db.Query(`SELECT id, name, created_at FROM api_keys ORDER BY created_at`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := []APIKey{}
	for rows.Next() {
		var k APIKey
		var t int64
		rows.Scan(&k.ID, &k.Name, &t)
		k.CreatedAt = time.Unix(t, 0).UTC()
		out = append(out, k)
	}
	return out
}

// DeleteAPIKey deletes an API key.
func (s *Store) DeleteAPIKey(id string) { s.db.Exec(`DELETE FROM api_keys WHERE id=?`, id) }

// CheckAPIKey validates an API key.
func (s *Store) CheckAPIKey(key string) bool {
	var id string
	return s.db.QueryRow(`SELECT id FROM api_keys WHERE key_hash=?`, hashKey(key)).Scan(&id) == nil
}
