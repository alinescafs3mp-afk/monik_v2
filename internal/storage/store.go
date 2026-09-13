package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/clock"
	"github.com/alinescafs3mp-afk/monik_v2/internal/idgen"
	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
	"github.com/alinescafs3mp-afk/monik_v2/internal/secure"

	_ "embed"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaSQL string

var ErrNotFound = errors.New("not found")
var ErrConflict = errors.New("conflict")
var ErrIdempotencyConflict = errors.New("idempotency key reused with different request")

type Store struct {
	DB    *sql.DB
	Clock clock.Clock
	mu    sync.Mutex
	path  string
}

func Open(path string, clk clock.Clock) (*Store, error) {
	if clk == nil {
		clk = clock.Real{}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(4)
	s := &Store{DB: db, Clock: clk, path: path}
	if _, err := db.Exec(schemaSQL); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.DB.Close() }

func (s *Store) Path() string { return s.path }

func (s *Store) now() time.Time { return s.Clock.Now().UTC() }

func (s *Store) Setting(key string) (string, error) {
	var v string
	err := s.DB.QueryRow(`SELECT value FROM settings WHERE key=?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return v, err
}

func (s *Store) MustSetting(key, def string) string {
	v, err := s.Setting(key)
	if err != nil {
		return def
	}
	return v
}

func (s *Store) SetSetting(key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.DB.Exec(`INSERT INTO settings(key,value,revision) VALUES(?,?,1)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value, revision=revision+1`, key, value)
	return err
}

type User struct {
	ID           string
	Username     string
	PasswordHash string
	Role         string
}

func (s *Store) CreateUser(username, passwordHash, role string) (*User, error) {
	u := &User{ID: idgen.New(), Username: username, PasswordHash: passwordHash, Role: role}
	_, err := s.DB.Exec(`INSERT INTO admin_users(id,username,password_hash,role,created_at) VALUES(?,?,?,?,?)`,
		u.ID, u.Username, u.PasswordHash, u.Role, s.now().Format(time.RFC3339Nano))
	return u, err
}

func (s *Store) UserByName(username string) (*User, error) {
	u := &User{}
	err := s.DB.QueryRow(`SELECT id,username,password_hash,role FROM admin_users WHERE username=?`, username).
		Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return u, err
}

func (s *Store) UserCount() (int, error) {
	var n int
	err := s.DB.QueryRow(`SELECT COUNT(*) FROM admin_users`).Scan(&n)
	return n, err
}

type Session struct {
	ID              string
	UserID          string
	TokenHash       string
	CSRF            string
	ExpiresAt       time.Time
	RecentAuthUntil *time.Time
	Role            string
	Username        string
}

func (s *Store) CreateSession(user *User, ttl, recentAuth time.Duration) (rawToken string, sess *Session, err error) {
	rawToken, err = idgen.Secret(32)
	if err != nil {
		return "", nil, err
	}
	csrf, err := idgen.Secret(16)
	if err != nil {
		return "", nil, err
	}
	now := s.now()
	sess = &Session{
		ID: idgen.New(), UserID: user.ID, TokenHash: secure.HashToken(rawToken),
		CSRF: csrf, ExpiresAt: now.Add(ttl), Role: user.Role, Username: user.Username,
	}
	rau := now.Add(recentAuth)
	sess.RecentAuthUntil = &rau
	_, err = s.DB.Exec(`INSERT INTO admin_sessions(id,user_id,token_hash,csrf,expires_at,created_at,last_seen_at,recent_auth_until)
		VALUES(?,?,?,?,?,?,?,?)`, sess.ID, sess.UserID, sess.TokenHash, sess.CSRF,
		sess.ExpiresAt.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), rau.Format(time.RFC3339Nano))
	_, _ = s.DB.Exec(`UPDATE admin_users SET last_login_at=? WHERE id=?`, now.Format(time.RFC3339Nano), user.ID)
	return rawToken, sess, err
}

func (s *Store) SessionByToken(raw string) (*Session, error) {
	h := secure.HashToken(raw)
	sess := &Session{}
	var exp, rau sql.NullString
	err := s.DB.QueryRow(`SELECT s.id,s.user_id,s.token_hash,s.csrf,s.expires_at,s.recent_auth_until,u.role,u.username
		FROM admin_sessions s JOIN admin_users u ON u.id=s.user_id WHERE s.token_hash=?`, h).
		Scan(&sess.ID, &sess.UserID, &sess.TokenHash, &sess.CSRF, &exp, &rau, &sess.Role, &sess.Username)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	sess.ExpiresAt, _ = time.Parse(time.RFC3339Nano, exp.String)
	if s.now().After(sess.ExpiresAt) {
		return nil, ErrNotFound
	}
	if rau.Valid {
		t, _ := time.Parse(time.RFC3339Nano, rau.String)
		sess.RecentAuthUntil = &t
	}
	return sess, nil
}

func (s *Store) DeleteSession(id string) error {
	_, err := s.DB.Exec(`DELETE FROM admin_sessions WHERE id=?`, id)
	return err
}

func (s *Store) CreateEnrollmentCode(actor string, ttl time.Duration) (plain string, expires time.Time, err error) {
	plain, err = idgen.Secret(10)
	if err != nil {
		return "", time.Time{}, err
	}
	plain = strings.ToUpper(plain[:12])
	expires = s.now().Add(ttl)
	_, err = s.DB.Exec(`INSERT INTO enrollment_codes(id,code_hash,created_by,created_at,expires_at) VALUES(?,?,?,?,?)`,
		idgen.New(), secure.HashToken(plain), actor, s.now().Format(time.RFC3339Nano), expires.Format(time.RFC3339Nano))
	return plain, expires, err
}

func (s *Store) ConsumeEnrollmentCode(plain string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	h := secure.HashToken(plain)
	now := s.now().Format(time.RFC3339Nano)
	res, err := s.DB.Exec(`UPDATE enrollment_codes SET consumed_at=? WHERE code_hash=? AND consumed_at IS NULL AND expires_at>?`,
		now, h, now)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return fmt.Errorf("enrollment code invalid or already used")
	}
	return nil
}

type AgentRow struct {
	ID                 string
	DisplayName        string
	Hostname           string
	OS                 string
	Arch               string
	CredentialHash     string
	Revoked            bool
	Archived           bool
	Pinned             bool
	Hidden             bool
	DesiredRevision    int64
	DesiredHash        string
	DesiredConfig      string
	AppliedRevision    int64
	AppliedHash        string
	LastSeenAt         *time.Time
	LastLiveAt         *time.Time
	SessionID          string
	LastSeq            int64
	WorkerVersion      string
	WorkerDigest       string
	ServiceHostVersion string
	ServiceHostDigest  string
	ManagedReady       bool
	Capabilities       string
	Addresses          string
	EndpointGeneration int64
	Conflict           bool
	CreatedAt          time.Time
}

func scanAgent(sc interface{ Scan(...any) error }) (*AgentRow, error) {
	a := &AgentRow{}
	var lastSeen, lastLive, created sql.NullString
	var display, hostname, osn, arch, cred, dhash, dcfg, ahash, session sql.NullString
	var wver, wdig, hver, hdig, caps, addrs sql.NullString
	var revoked, archived, pinned, hidden, ready, conflict int
	err := sc.Scan(&a.ID, &display, &hostname, &osn, &arch, &cred,
		&revoked, &archived, &pinned, &hidden, &a.DesiredRevision, &dhash, &dcfg,
		&a.AppliedRevision, &ahash, &lastSeen, &lastLive, &session, &a.LastSeq,
		&wver, &wdig, &hver, &hdig,
		&ready, &caps, &addrs, &a.EndpointGeneration, &conflict, &created)
	if err != nil {
		return nil, err
	}
	a.DisplayName, a.Hostname, a.OS, a.Arch = display.String, hostname.String, osn.String, arch.String
	a.CredentialHash, a.DesiredHash, a.DesiredConfig, a.AppliedHash = cred.String, dhash.String, dcfg.String, ahash.String
	a.SessionID = session.String
	a.WorkerVersion, a.WorkerDigest, a.ServiceHostVersion, a.ServiceHostDigest = wver.String, wdig.String, hver.String, hdig.String
	a.Capabilities, a.Addresses = caps.String, addrs.String
	a.Revoked, a.Archived, a.Pinned, a.Hidden, a.ManagedReady, a.Conflict = revoked == 1, archived == 1, pinned == 1, hidden == 1, ready == 1, conflict == 1
	if lastSeen.Valid {
		t, _ := time.Parse(time.RFC3339Nano, lastSeen.String)
		a.LastSeenAt = &t
	}
	if lastLive.Valid {
		t, _ := time.Parse(time.RFC3339Nano, lastLive.String)
		a.LastLiveAt = &t
	}
	if created.Valid {
		a.CreatedAt, _ = time.Parse(time.RFC3339Nano, created.String)
	}
	return a, nil
}

const agentCols = `id,display_name,hostname,os,arch,credential_hash,revoked,archived,pinned,hidden,
desired_revision,desired_hash,desired_config,applied_revision,applied_hash,last_seen_at,last_live_at,
session_id,last_seq,worker_version,worker_digest,service_host_version,service_host_digest,managed_ready,
capabilities,addresses,endpoint_generation,conflict,created_at`

func (s *Store) Agent(id string) (*AgentRow, error) {
	row := s.DB.QueryRow(`SELECT `+agentCols+` FROM agents WHERE id=?`, id)
	a, err := scanAgent(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return a, err
}

func (s *Store) Agents() ([]*AgentRow, error) {
	rows, err := s.DB.Query(`SELECT ` + agentCols + ` FROM agents ORDER BY display_name, hostname`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*AgentRow
	for rows.Next() {
		a, err := scanAgent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) InsertAgent(a *AgentRow, credentialHash string) error {
	now := s.now().Format(time.RFC3339Nano)
	_, err := s.DB.Exec(`INSERT INTO agents(id,display_name,hostname,os,arch,credential_hash,desired_revision,desired_hash,desired_config,created_at,endpoint_generation)
		VALUES(?,?,?,?,?,?,?,?,?,?,1)`, a.ID, a.DisplayName, a.Hostname, a.OS, a.Arch, credentialHash,
		a.DesiredRevision, a.DesiredHash, a.DesiredConfig, now)
	return err
}

func (s *Store) TouchAgent(id, session string, seq int64, live bool, host *protocol.HostMetrics, caps any, versions map[string]string) error {
	now := s.now().Format(time.RFC3339Nano)
	capJSON, _ := json.Marshal(caps)
	addrJSON := "[]"
	if host != nil {
		b, _ := json.Marshal(host.Addresses)
		addrJSON = string(b)
	}
	hn, osn, arch, dn := "", "", "", ""
	if host != nil {
		hn, osn, arch, dn = host.Hostname, host.OS, host.Arch, host.DisplayName
	}
	liveSQL := `last_seen_at=?`
	args := []any{now}
	if live {
		liveSQL = `last_seen_at=?, last_live_at=?`
		args = append(args, now)
	}
	args = append(args, session, seq, capJSON, addrJSON, hn, osn, arch, dn,
		versions["worker"], versions["worker_digest"], versions["service_host"], versions["service_host_digest"],
		versions["managed"], id)
	_, err := s.DB.Exec(`UPDATE agents SET `+liveSQL+`, session_id=?, last_seq=?, capabilities=?, addresses=?,
		hostname=COALESCE(NULLIF(?,''),hostname), os=COALESCE(NULLIF(?,''),os), arch=COALESCE(NULLIF(?,''),arch),
		display_name=COALESCE(NULLIF(?,''),display_name),
		worker_version=COALESCE(NULLIF(?,''),worker_version), worker_digest=COALESCE(NULLIF(?,''),worker_digest),
		service_host_version=COALESCE(NULLIF(?,''),service_host_version), service_host_digest=COALESCE(NULLIF(?,''),service_host_digest),
		managed_ready=CASE WHEN ?= '1' THEN 1 ELSE managed_ready END
		WHERE id=?`, args...)
	return err
}

func (s *Store) SetApplied(id string, rev int64, hash string) error {
	_, err := s.DB.Exec(`UPDATE agents SET applied_revision=?, applied_hash=? WHERE id=?`, rev, hash, id)
	return err
}

func (s *Store) SetDesired(id string, rev int64, hash, body string) error {
	_, err := s.DB.Exec(`UPDATE agents SET desired_revision=?, desired_hash=?, desired_config=? WHERE id=?`, rev, hash, body, id)
	if err != nil {
		return err
	}
	_, err = s.DB.Exec(`INSERT INTO config_revisions(agent_id,revision,hash,body,created_at) VALUES(?,?,?,?,?)`,
		id, rev, hash, body, s.now().Format(time.RFC3339Nano))
	return err
}

func (s *Store) MarkConflict(id string) error {
	_, err := s.DB.Exec(`UPDATE agents SET conflict=1 WHERE id=?`, id)
	return err
}

func (s *Store) RevokeAgent(id string) error {
	_, err := s.DB.Exec(`UPDATE agents SET revoked=1, credential_hash='' WHERE id=?`, id)
	return err
}

func (s *Store) UpdateAgentFlags(id string, fields map[string]any) error {
	if len(fields) == 0 {
		return nil
	}
	var sets []string
	var args []any
	for k, v := range fields {
		sets = append(sets, k+"=?")
		args = append(args, v)
	}
	args = append(args, id)
	_, err := s.DB.Exec(`UPDATE agents SET `+strings.Join(sets, ",")+` WHERE id=?`, args...)
	return err
}

func (s *Store) InsertHostSample(agentID string, seq int64, session string, observed time.Time, host *protocol.HostMetrics) error {
	payload, _ := json.Marshal(host)
	var cpu *float64
	var ramUsed, ramAvail, ramTotal *int64
	var pingMean, pingLoss *float64
	if host != nil {
		cpu = host.CPUPercent
		ramUsed, ramAvail, ramTotal = &host.RAMUsed, &host.RAMAvailable, &host.RAMTotal
		if host.Ping != nil {
			pingMean = host.Ping.MeanMS
			pingLoss = host.Ping.LossPct
		}
	}
	_, err := s.DB.Exec(`INSERT INTO host_samples(agent_id,observed_at,received_at,seq,session_id,cpu_pct,ram_used,ram_avail,ram_total,ping_mean_ms,ping_loss,payload)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, agentID, observed.UTC().Format(time.RFC3339Nano), s.now().Format(time.RFC3339Nano),
		seq, session, cpu, ramUsed, ramAvail, ramTotal, pingMean, pingLoss, string(payload))
	return err
}

func (s *Store) InsertCheckObs(o protocol.CheckObservation, agentID string) error {
	payload, _ := json.Marshal(o)
	_, err := s.DB.Exec(`INSERT INTO service_observations(agent_id,service_id,check_id,observed_at,received_at,vantage,transport,http_status,latency_ms,app_result,quality,payload)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, agentID, o.ServiceID, o.CheckID, o.ObservedAt.UTC().Format(time.RFC3339Nano),
		s.now().Format(time.RFC3339Nano), o.Vantage, o.Transport, o.HTTPStatus, o.LatencyMS, o.AppResult, string(o.Quality), string(payload))
	return err
}

func (s *Store) LatestHost(agentID string) (*protocol.HostMetrics, time.Time, error) {
	var payload, obs string
	err := s.DB.QueryRow(`SELECT payload, observed_at FROM host_samples WHERE agent_id=? ORDER BY observed_at DESC LIMIT 1`, agentID).Scan(&payload, &obs)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, time.Time{}, ErrNotFound
	}
	if err != nil {
		return nil, time.Time{}, err
	}
	var h protocol.HostMetrics
	_ = json.Unmarshal([]byte(payload), &h)
	t, _ := time.Parse(time.RFC3339Nano, obs)
	return &h, t, nil
}

func (s *Store) HostAt(agentID string, at time.Time) (*protocol.HostMetrics, time.Time, error) {
	var payload, obs string
	err := s.DB.QueryRow(`SELECT payload, observed_at FROM host_samples WHERE agent_id=? AND observed_at<=? ORDER BY observed_at DESC LIMIT 1`,
		agentID, at.UTC().Format(time.RFC3339Nano)).Scan(&payload, &obs)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, time.Time{}, ErrNotFound
	}
	if err != nil {
		return nil, time.Time{}, err
	}
	var h protocol.HostMetrics
	_ = json.Unmarshal([]byte(payload), &h)
	t, _ := time.Parse(time.RFC3339Nano, obs)
	return &h, t, nil
}

func (s *Store) HostSeries(agentID string, from, to time.Time) ([]map[string]any, error) {
	rows, err := s.DB.Query(`SELECT observed_at,cpu_pct,ram_used,ram_avail,ram_total,ping_mean_ms,ping_loss,payload
		FROM host_samples WHERE agent_id=? AND observed_at>=? AND observed_at<? ORDER BY observed_at`,
		agentID, from.UTC().Format(time.RFC3339Nano), to.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var obs string
		var cpu, pingM, pingL sql.NullFloat64
		var ramU, ramA, ramT sql.NullInt64
		var payload string
		if err := rows.Scan(&obs, &cpu, &ramU, &ramA, &ramT, &pingM, &pingL, &payload); err != nil {
			return nil, err
		}
		m := map[string]any{"observed_at": obs, "payload": json.RawMessage(payload)}
		if cpu.Valid {
			m["cpu_pct"] = cpu.Float64
		}
		if ramU.Valid {
			m["ram_used"] = ramU.Int64
		}
		if ramA.Valid {
			m["ram_avail"] = ramA.Int64
		}
		if ramT.Valid {
			m["ram_total"] = ramT.Int64
		}
		if pingM.Valid {
			m["ping_mean_ms"] = pingM.Float64
		}
		if pingL.Valid {
			m["ping_loss"] = pingL.Float64
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) UpsertService(sv protocol.DiscoveredEndpoint, agentID string) error {
	now := s.now().Format(time.RFC3339Nano)
	_, err := s.DB.Exec(`INSERT INTO services(id,agent_id,display_name,url,dial_target,host_header,tls_server_name,process_name,source,speaks_http,speaks_tls,first_seen_at,last_seen_at,last_discovered_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(agent_id, dial_target, host_header) DO UPDATE SET
			url=excluded.url, process_name=excluded.process_name, speaks_http=excluded.speaks_http, speaks_tls=excluded.speaks_tls,
			last_seen_at=excluded.last_seen_at, last_discovered_at=excluded.last_discovered_at, source=excluded.source`,
		sv.ServiceID, agentID, sv.URL, sv.URL, sv.DialTarget, sv.HostHeader, sv.TLSServerName, sv.ProcessName, sv.Source,
		boolInt(sv.SpeaksHTTP), boolInt(sv.SpeaksTLS), now, now, now)
	return err
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

type ServiceRow struct {
	ID           string
	AgentID      string
	DisplayName  string
	URL          string
	DialTarget   string
	HostHeader   string
	ProcessName  string
	Source       string
	SpeaksHTTP   bool
	Pinned       bool
	Hidden       bool
	Paused       bool
	Ignored      bool
	LastSeenAt   *time.Time
	FirstSeenAt  time.Time
}

func (s *Store) Services(agentID string) ([]*ServiceRow, error) {
	q := `SELECT id,agent_id,display_name,url,dial_target,host_header,process_name,source,speaks_http,pinned,hidden,paused,ignored,last_seen_at,first_seen_at FROM services`
	var args []any
	if agentID != "" {
		q += ` WHERE agent_id=?`
		args = append(args, agentID)
	}
	q += ` ORDER BY display_name`
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*ServiceRow
	for rows.Next() {
		sv := &ServiceRow{}
		var http, pin, hid, pau, ign int
		var last, first sql.NullString
		if err := rows.Scan(&sv.ID, &sv.AgentID, &sv.DisplayName, &sv.URL, &sv.DialTarget, &sv.HostHeader, &sv.ProcessName, &sv.Source, &http, &pin, &hid, &pau, &ign, &last, &first); err != nil {
			return nil, err
		}
		sv.SpeaksHTTP, sv.Pinned, sv.Hidden, sv.Paused, sv.Ignored = http == 1, pin == 1, hid == 1, pau == 1, ign == 1
		if last.Valid {
			t, _ := time.Parse(time.RFC3339Nano, last.String)
			sv.LastSeenAt = &t
		}
		if first.Valid {
			sv.FirstSeenAt, _ = time.Parse(time.RFC3339Nano, first.String)
		}
		out = append(out, sv)
	}
	return out, rows.Err()
}

func (s *Store) Service(id string) (*ServiceRow, error) {
	sv := &ServiceRow{}
	var http, pin, hid, pau, ign int
	var last, first sql.NullString
	err := s.DB.QueryRow(`SELECT id,agent_id,display_name,url,dial_target,host_header,process_name,source,speaks_http,pinned,hidden,paused,ignored,last_seen_at,first_seen_at FROM services WHERE id=?`, id).
		Scan(&sv.ID, &sv.AgentID, &sv.DisplayName, &sv.URL, &sv.DialTarget, &sv.HostHeader, &sv.ProcessName, &sv.Source, &http, &pin, &hid, &pau, &ign, &last, &first)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	sv.SpeaksHTTP, sv.Pinned, sv.Hidden, sv.Paused, sv.Ignored = http == 1, pin == 1, hid == 1, pau == 1, ign == 1
	return sv, nil
}

func (s *Store) UpdateServiceFlags(id string, fields map[string]any) error {
	var sets []string
	var args []any
	for k, v := range fields {
		sets = append(sets, k+"=?")
		args = append(args, v)
	}
	args = append(args, id)
	_, err := s.DB.Exec(`UPDATE services SET `+strings.Join(sets, ",")+` WHERE id=?`, args...)
	return err
}

func (s *Store) AppendEvent(typ, entity, entityID string, revision int64, payload any) error {
	b, _ := json.Marshal(payload)
	_, err := s.DB.Exec(`INSERT INTO event_log(ts,type,entity,entity_id,revision,payload) VALUES(?,?,?,?,?,?)`,
		s.now().Format(time.RFC3339Nano), typ, entity, entityID, revision, string(b))
	return err
}

func (s *Store) EventsAfter(id int64, limit int) ([]Event, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := s.DB.Query(`SELECT id,ts,type,entity,entity_id,revision,payload FROM event_log WHERE id>? ORDER BY id LIMIT ?`, id, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.TS, &e.Type, &e.Entity, &e.EntityID, &e.Revision, &e.Payload); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

type Event struct {
	ID       int64
	TS       string
	Type     string
	Entity   string
	EntityID string
	Revision int64
	Payload  string
}

func (s *Store) MaxEventID() (int64, error) {
	var id sql.NullInt64
	err := s.DB.QueryRow(`SELECT MAX(id) FROM event_log`).Scan(&id)
	return id.Int64, err
}

func (s *Store) Audit(actor, action, entity, detail string) {
	_, _ = s.DB.Exec(`INSERT INTO audit_events(at,actor,action,entity,detail) VALUES(?,?,?,?,?)`,
		s.now().Format(time.RFC3339Nano), actor, action, entity, detail)
}

func (s *Store) RetainRaw(maxAge time.Duration) error {
	cut := s.now().Add(-maxAge).Format(time.RFC3339Nano)
	_, err := s.DB.Exec(`DELETE FROM host_samples WHERE observed_at<?`, cut)
	if err != nil {
		return err
	}
	_, err = s.DB.Exec(`DELETE FROM service_observations WHERE observed_at<?`, cut)
	return err
}

func (s *Store) BackupTo(dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	_, err := s.DB.Exec(`VACUUM INTO ?`, dst)
	return err
}

func (s *Store) InsertBackup(id, path, sha string, size int64) error {
	_, err := s.DB.Exec(`INSERT INTO backups(id,path,sha256,size,created_at,verified) VALUES(?,?,?,?,?,1)`,
		id, path, sha, size, s.now().Format(time.RFC3339Nano))
	return err
}

func (s *Store) Backups() ([]map[string]any, error) {
	rows, err := s.DB.Query(`SELECT id,path,sha256,size,created_at,verified FROM backups ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var id, path, sha, created string
		var size int64
		var verified int
		if err := rows.Scan(&id, &path, &sha, &size, &created, &verified); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"id": id, "path": path, "sha256": sha, "size": size, "created_at": created, "verified": verified == 1})
	}
	return out, rows.Err()
}

func (s *Store) LatestCheckObs(serviceID string) (*protocol.CheckObservation, error) {
	var payload string
	err := s.DB.QueryRow(`SELECT payload FROM service_observations WHERE service_id=? ORDER BY observed_at DESC LIMIT 1`, serviceID).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var o protocol.CheckObservation
	_ = json.Unmarshal([]byte(payload), &o)
	return &o, nil
}

func (s *Store) CheckSeries(serviceID string, from, to time.Time) ([]protocol.CheckObservation, error) {
	rows, err := s.DB.Query(`SELECT payload FROM service_observations WHERE service_id=? AND observed_at>=? AND observed_at<? ORDER BY observed_at`,
		serviceID, from.UTC().Format(time.RFC3339Nano), to.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []protocol.CheckObservation
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var o protocol.CheckObservation
		_ = json.Unmarshal([]byte(payload), &o)
		out = append(out, o)
	}
	return out, rows.Err()
}

func (s *Store) SetState(entityType, entityID, state, reason string) error {
	now := s.now().Format(time.RFC3339Nano)
	var prev sql.NullString
	_ = s.DB.QueryRow(`SELECT state FROM current_states WHERE entity_type=? AND entity_id=?`, entityType, entityID).Scan(&prev)
	_, err := s.DB.Exec(`INSERT INTO current_states(entity_type,entity_id,state,reason,since,updated_at) VALUES(?,?,?,?,?,?)
		ON CONFLICT(entity_type,entity_id) DO UPDATE SET state=excluded.state, reason=excluded.reason, updated_at=excluded.updated_at,
		since=CASE WHEN current_states.state=excluded.state THEN current_states.since ELSE excluded.since END`,
		entityType, entityID, state, reason, now, now)
	if err != nil {
		return err
	}
	if !prev.Valid || prev.String != state {
		_, _ = s.DB.Exec(`INSERT INTO state_events(entity_type,entity_id,from_state,to_state,reason,at) VALUES(?,?,?,?,?,?)`,
			entityType, entityID, prev.String, state, reason, now)
	}
	return nil
}

func (s *Store) State(entityType, entityID string) (string, string, time.Time, error) {
	var st, reason, since string
	err := s.DB.QueryRow(`SELECT state,reason,since FROM current_states WHERE entity_type=? AND entity_id=?`, entityType, entityID).Scan(&st, &reason, &since)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", time.Time{}, ErrNotFound
	}
	t, _ := time.Parse(time.RFC3339Nano, since)
	return st, reason, t, err
}

func (s *Store) WithTx(fn func(*sql.Tx) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.DB.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}
