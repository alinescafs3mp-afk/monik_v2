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

const dbTimeFormat = "2006-01-02T15:04:05.000000000Z07:00"

var ErrNotFound = errors.New("not found")
var ErrConflict = errors.New("conflict")
var ErrIdempotencyConflict = errors.New("idempotency key reused with different request")

type Store struct {
	DB       *sql.DB
	executor sqlExecutor
	Clock    clock.Clock
	mu       sync.Mutex
	path     string
}

func Open(path string, clk clock.Clock) (*Store, error) {
	if clk == nil {
		clk = clock.Real{}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=synchronous(FULL)", path)
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
	if err := s.normalizeTimes(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.DB.Close() }

func (s *Store) Path() string { return s.path }

func (s *Store) now() time.Time { return s.Clock.Now().UTC() }

func (s *Store) Setting(key string) (string, error) {
	var v string
	err := s.db().QueryRow(`SELECT value FROM settings WHERE key=?`, key).Scan(&v)
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
	_, err := s.db().Exec(`INSERT INTO settings(key,value,revision) VALUES(?,?,1)
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
	_, err := s.db().Exec(`INSERT INTO admin_users(id,username,password_hash,role,created_at) VALUES(?,?,?,?,?)`,
		u.ID, u.Username, u.PasswordHash, u.Role, s.now().UTC().Format(dbTimeFormat))
	return u, err
}

func (s *Store) UserByName(username string) (*User, error) {
	u := &User{}
	err := s.db().QueryRow(`SELECT id,username,password_hash,role FROM admin_users WHERE username=?`, username).
		Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return u, err
}

func (s *Store) UserCount() (int, error) {
	var n int
	err := s.db().QueryRow(`SELECT COUNT(*) FROM admin_users`).Scan(&n)
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
	err = s.WithTx(func(tx *sql.Tx) error {
		// Password verification precedes this transaction. Recheck the exact hash
		// to prevent an in-flight old login from surviving a password change.
		var current string
		if e := tx.QueryRow(`SELECT password_hash FROM admin_users WHERE id=?`, user.ID).Scan(&current); e != nil {
			return e
		}
		if current != user.PasswordHash {
			return ErrConflict
		}
		if _, e := tx.Exec(`INSERT INTO admin_sessions(id,user_id,token_hash,csrf,expires_at,created_at,last_seen_at,recent_auth_until)
		VALUES(?,?,?,?,?,?,?,?)`, sess.ID, sess.UserID, sess.TokenHash, sess.CSRF, sess.ExpiresAt.UTC().Format(dbTimeFormat), now.UTC().Format(dbTimeFormat), now.UTC().Format(dbTimeFormat), rau.UTC().Format(dbTimeFormat)); e != nil {
			return e
		}
		_, e := tx.Exec(`UPDATE admin_users SET last_login_at=? WHERE id=?`, now.UTC().Format(dbTimeFormat), user.ID)
		return e
	})
	return rawToken, sess, err
}

func (s *Store) SessionByToken(raw string) (*Session, error) {
	h := secure.HashToken(raw)
	sess := &Session{}
	var exp, rau sql.NullString
	err := s.db().QueryRow(`SELECT s.id,s.user_id,s.token_hash,s.csrf,s.expires_at,s.recent_auth_until,u.role,u.username
		FROM admin_sessions s JOIN admin_users u ON u.id=s.user_id WHERE s.token_hash=?`, h).
		Scan(&sess.ID, &sess.UserID, &sess.TokenHash, &sess.CSRF, &exp, &rau, &sess.Role, &sess.Username)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	sess.ExpiresAt, _ = time.Parse(time.RFC3339Nano, exp.String)
	if !s.now().Before(sess.ExpiresAt) {
		return nil, ErrNotFound
	}
	if rau.Valid {
		t, _ := time.Parse(time.RFC3339Nano, rau.String)
		sess.RecentAuthUntil = &t
	}
	return sess, nil
}

func (s *Store) DeleteSession(id string) error {
	_, err := s.db().Exec(`DELETE FROM admin_sessions WHERE id=?`, id)
	return err
}

func (s *Store) CreateEnrollmentCode(actor string, ttl time.Duration) (plain string, expires time.Time, err error) {
	plain, err = idgen.Secret(10)
	if err != nil {
		return "", time.Time{}, err
	}
	plain = strings.ToUpper(plain[:12])
	expires = s.now().Add(ttl)
	_, err = s.db().Exec(`INSERT INTO enrollment_codes(id,code_hash,created_by,created_at,expires_at) VALUES(?,?,?,?,?)`,
		idgen.New(), secure.HashToken(plain), actor, s.now().UTC().Format(dbTimeFormat), expires.UTC().Format(dbTimeFormat))
	return plain, expires, err
}

func (s *Store) ConsumeEnrollmentCode(plain string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	h := secure.HashToken(plain)
	now := s.now().UTC().Format(dbTimeFormat)
	res, err := s.db().Exec(`UPDATE enrollment_codes SET consumed_at=? WHERE code_hash=? AND consumed_at IS NULL AND expires_at>?`,
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
	ID                 string     `json:"id"`
	DisplayName        string     `json:"display_name"`
	Hostname           string     `json:"hostname"`
	OS                 string     `json:"os"`
	Arch               string     `json:"arch"`
	CredentialHash     string     `json:"-"`
	Revoked            bool       `json:"revoked"`
	Archived           bool       `json:"archived"`
	Pinned             bool       `json:"pinned"`
	Hidden             bool       `json:"hidden"`
	DesiredRevision    int64      `json:"desired_revision"`
	DesiredHash        string     `json:"desired_hash"`
	DesiredConfig      string     `json:"-"`
	AppliedRevision    int64      `json:"applied_revision"`
	AppliedHash        string     `json:"applied_hash"`
	LastSeenAt         *time.Time `json:"last_seen_at"`
	LastLiveAt         *time.Time `json:"last_live_at"`
	SessionID          string     `json:"session_id"`
	LastSeq            int64      `json:"last_seq"`
	WorkerVersion      string     `json:"worker_version"`
	WorkerDigest       string     `json:"worker_digest"`
	ServiceHostVersion string     `json:"service_host_version"`
	ServiceHostDigest  string     `json:"service_host_digest"`
	ManagedReady       bool       `json:"managed_ready"`
	Capabilities       string     `json:"capabilities"`
	Addresses          string     `json:"addresses"`
	EndpointGeneration int64      `json:"endpoint_generation"`
	Conflict           bool       `json:"conflict"`
	CreatedAt          time.Time  `json:"created_at"`
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
	row := s.db().QueryRow(`SELECT `+agentCols+` FROM agents WHERE id=?`, id)
	a, err := scanAgent(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return a, err
}

func (s *Store) Agents() ([]*AgentRow, error) {
	rows, err := s.db().Query(`SELECT ` + agentCols + ` FROM agents ORDER BY display_name, hostname`)
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
	now := s.now().UTC().Format(dbTimeFormat)
	_, err := s.db().Exec(`INSERT INTO agents(id,display_name,hostname,os,arch,credential_hash,desired_revision,desired_hash,desired_config,created_at,endpoint_generation)
		VALUES(?,?,?,?,?,?,?,?,?,?,1)`, a.ID, a.DisplayName, a.Hostname, a.OS, a.Arch, credentialHash,
		a.DesiredRevision, a.DesiredHash, a.DesiredConfig, now)
	return err
}

func (s *Store) TouchAgent(id, session string, seq int64, live bool, host *protocol.HostMetrics, caps any, versions map[string]string) error {
	now := s.now().UTC().Format(dbTimeFormat)
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
	_, err := s.db().Exec(`UPDATE agents SET `+liveSQL+`, session_id=?, last_seq=?, capabilities=?, addresses=?,
		hostname=COALESCE(NULLIF(?,''),hostname), os=COALESCE(NULLIF(?,''),os), arch=COALESCE(NULLIF(?,''),arch),
		display_name=COALESCE(NULLIF(display_name,''),NULLIF(?,''),''),
		worker_version=COALESCE(NULLIF(?,''),worker_version), worker_digest=COALESCE(NULLIF(?,''),worker_digest),
		service_host_version=COALESCE(NULLIF(?,''),service_host_version), service_host_digest=COALESCE(NULLIF(?,''),service_host_digest),
		managed_ready=CASE WHEN ?= '1' THEN 1 ELSE 0 END
		WHERE id=?`, args...)
	return err
}

func (s *Store) SetEndpointGeneration(id string, gen int64) error {
	_, err := s.db().Exec(`UPDATE agents SET endpoint_generation=? WHERE id=? AND endpoint_generation<=?`, gen, id, gen)
	return err
}

func (s *Store) SetApplied(id string, rev int64, hash string) error {
	res, err := s.db().Exec(`UPDATE agents SET applied_revision=?, applied_hash=? WHERE id=? AND applied_revision<=?
 AND EXISTS(SELECT 1 FROM config_revisions WHERE agent_id=? AND revision=? AND hash=?)`, rev, hash, id, rev, id, rev, hash)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return ErrConflict
	}
	return nil
}

func (s *Store) SetDesired(id string, rev int64, hash, body string) error {
	return s.WithTx(func(tx *sql.Tx) error {
		res, err := tx.Exec(`UPDATE agents SET desired_revision=?,desired_hash=?,desired_config=? WHERE id=? AND desired_revision<=?`, rev, hash, body, id, rev)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		if n != 1 {
			return ErrConflict
		}
		_, err = tx.Exec(`INSERT INTO config_revisions(agent_id,revision,hash,body,created_at) VALUES(?,?,?,?,?)`, id, rev, hash, body, s.now().UTC().Format(dbTimeFormat))
		return err
	})
}

func (s *Store) MarkConflict(id string) error {
	_, err := s.db().Exec(`UPDATE agents SET conflict=1 WHERE id=?`, id)
	return err
}

func (s *Store) RevokeAgent(id string) error {
	_, err := s.db().Exec(`UPDATE agents SET revoked=1, credential_hash='' WHERE id=?`, id)
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
	res, err := s.db().Exec(`UPDATE agents SET `+strings.Join(sets, ",")+` WHERE id=?`, args...)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return ErrNotFound
	}
	return nil
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
	_, err := s.db().Exec(`INSERT INTO host_samples(agent_id,observed_at,received_at,seq,session_id,cpu_pct,ram_used,ram_avail,ram_total,ping_mean_ms,ping_loss,payload)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, agentID, observed.UTC().Format(dbTimeFormat), s.now().UTC().Format(dbTimeFormat),
		seq, session, cpu, ramUsed, ramAvail, ramTotal, pingMean, pingLoss, string(payload))
	return err
}

func (s *Store) InsertCheckObs(o protocol.CheckObservation, agentID string) error {
	payload, _ := json.Marshal(o)
	_, err := s.db().Exec(`INSERT INTO service_observations(agent_id,service_id,check_id,observed_at,received_at,vantage,transport,http_status,latency_ms,app_result,quality,payload)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, agentID, o.ServiceID, o.CheckID, o.ObservedAt.UTC().Format(dbTimeFormat),
		s.now().UTC().Format(dbTimeFormat), o.Vantage, o.Transport, o.HTTPStatus, o.LatencyMS, o.AppResult, string(o.Quality), string(payload))
	return err
}

func (s *Store) LatestHost(agentID string) (*protocol.HostMetrics, time.Time, error) {
	var payload, obs string
	err := s.db().QueryRow(`SELECT payload, observed_at FROM host_samples WHERE agent_id=? ORDER BY observed_at DESC LIMIT 1`, agentID).Scan(&payload, &obs)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, time.Time{}, ErrNotFound
	}
	if err != nil {
		return nil, time.Time{}, err
	}
	var h *protocol.HostMetrics
	if e := json.Unmarshal([]byte(payload), &h); e != nil || h == nil {
		return nil, time.Time{}, fmt.Errorf("stored host measurement is invalid")
	}
	stamp, e := time.Parse(time.RFC3339Nano, obs)
	if e != nil {
		return nil, time.Time{}, fmt.Errorf("stored measurement timestamp is invalid")
	}
	return h, stamp, nil
}

func (s *Store) HostAt(agentID string, at time.Time) (*protocol.HostMetrics, time.Time, error) {
	var payload, obs string
	err := s.db().QueryRow(`SELECT payload, observed_at FROM host_samples WHERE agent_id=? AND observed_at<=? ORDER BY observed_at DESC LIMIT 1`,
		agentID, at.UTC().Format(dbTimeFormat)).Scan(&payload, &obs)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, time.Time{}, ErrNotFound
	}
	if err != nil {
		return nil, time.Time{}, err
	}
	var h *protocol.HostMetrics
	if e := json.Unmarshal([]byte(payload), &h); e != nil || h == nil {
		return nil, time.Time{}, fmt.Errorf("stored host measurement is invalid")
	}
	stamp, e := time.Parse(time.RFC3339Nano, obs)
	if e != nil {
		return nil, time.Time{}, fmt.Errorf("stored measurement timestamp is invalid")
	}
	return h, stamp, nil
}

func (s *Store) HostSeries(agentID string, from, to time.Time) ([]map[string]any, error) {
	rows, err := s.db().Query(`SELECT observed_at,cpu_pct,ram_used,ram_avail,ram_total,ping_mean_ms,ping_loss,payload
		FROM host_samples WHERE agent_id=? AND observed_at>=? AND observed_at<? ORDER BY observed_at,id LIMIT 20001`,
		agentID, from.UTC().Format(dbTimeFormat), to.UTC().Format(dbTimeFormat))
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
		var host protocol.HostMetrics
		if json.Unmarshal([]byte(payload), &host) == nil {
			for _, d := range host.Disks {
				m["disk:"+d.Mount] = d.UsedPct
			}
			for _, v := range host.Temperatures {
				if v.Celsius != nil {
					m["temperature:"+v.Source+": "+v.Label] = *v.Celsius
				}
			}
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) UpsertService(sv protocol.DiscoveredEndpoint, agentID string) error {
	now := s.now().UTC().Format(dbTimeFormat)
	_, err := s.db().Exec(`INSERT INTO services(id,agent_id,display_name,url,dial_target,host_header,tls_server_name,process_name,source,speaks_http,speaks_tls,first_seen_at,last_seen_at,last_discovered_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(agent_id, dial_target, host_header) DO UPDATE SET
			url=excluded.url, process_name=excluded.process_name, speaks_http=excluded.speaks_http, speaks_tls=excluded.speaks_tls,
			last_seen_at=excluded.last_seen_at, last_discovered_at=excluded.last_discovered_at, source=excluded.source`,
		sv.ServiceID, agentID, sv.URL, sv.URL, sv.DialTarget, sv.HostHeader, sv.TLSServerName, sv.ProcessName, sv.Source,
		boolInt(sv.SpeaksHTTP), boolInt(sv.SpeaksTLS), now, now, now)
	if err != nil {
		return err
	}
	// Upsert can retain an existing canonical ID; query it instead of trusting a worker ID.
	var id string
	if err := s.db().QueryRow(`SELECT id FROM services WHERE agent_id=? AND dial_target=? AND host_header=?`, agentID, sv.DialTarget, sv.HostHeader).Scan(&id); err != nil {
		return err
	}
	sv.ServiceID = id
	for i := range sv.Suggestions {
		sv.Suggestions[i].Definition.ServiceID = id
	}
	payload, err := json.Marshal(sv)
	if err != nil {
		return err
	}
	_, err = s.db().Exec(`INSERT INTO service_discovery(service_id,payload,updated_at) VALUES(?,?,?) ON CONFLICT(service_id) DO UPDATE SET payload=excluded.payload,updated_at=excluded.updated_at`, id, string(payload), now)
	return err
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

type ServiceRow struct {
	ID          string     `json:"id"`
	AgentID     string     `json:"agent_id"`
	DisplayName string     `json:"display_name"`
	URL         string     `json:"url"`
	DialTarget  string     `json:"dial_target"`
	HostHeader  string     `json:"host_header"`
	ProcessName string     `json:"process_name"`
	Source      string     `json:"source"`
	SpeaksHTTP  bool       `json:"speaks_http"`
	Pinned      bool       `json:"pinned"`
	Hidden      bool       `json:"hidden"`
	Paused      bool       `json:"paused"`
	Ignored     bool       `json:"ignored"`
	LastSeenAt  *time.Time `json:"last_seen_at"`
	FirstSeenAt time.Time  `json:"first_seen_at"`
}

func (s *Store) Services(agentID string) ([]*ServiceRow, error) {
	q := `SELECT id,agent_id,display_name,url,dial_target,host_header,process_name,source,speaks_http,pinned,hidden,paused,ignored,last_seen_at,first_seen_at FROM services`
	var args []any
	if agentID != "" {
		q += ` WHERE agent_id=?`
		args = append(args, agentID)
	}
	q += ` ORDER BY display_name`
	rows, err := s.db().Query(q, args...)
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
	err := s.db().QueryRow(`SELECT id,agent_id,display_name,url,dial_target,host_header,process_name,source,speaks_http,pinned,hidden,paused,ignored,last_seen_at,first_seen_at FROM services WHERE id=?`, id).
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
	res, err := s.db().Exec(`UPDATE services SET `+strings.Join(sets, ",")+` WHERE id=?`, args...)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) AppendEvent(typ, entity, entityID string, revision int64, payload any) error {
	b, _ := json.Marshal(payload)
	_, err := s.db().Exec(`INSERT INTO event_log(ts,type,entity,entity_id,revision,payload) VALUES(?,?,?,?,?,?)`,
		s.now().UTC().Format(dbTimeFormat), typ, entity, entityID, revision, string(b))
	return err
}

func (s *Store) EventsAfter(id int64, limit int) ([]Event, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := s.db().Query(`SELECT id,ts,type,entity,entity_id,revision,payload FROM event_log WHERE id>? ORDER BY id LIMIT ?`, id, limit)
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
	err := s.db().QueryRow(`SELECT MAX(id) FROM event_log`).Scan(&id)
	return id.Int64, err
}

func (s *Store) Audit(actor, action, entity, detail string) {
	_, _ = s.db().Exec(`INSERT INTO audit_events(at,actor,action,entity,detail) VALUES(?,?,?,?,?)`,
		s.now().UTC().Format(dbTimeFormat), actor, action, entity, detail)
}

// RetainRaw does bounded work. Catch-up can span many passes; retained bounds
// and cleanup lag are diagnostic facts, not a promise that the target is met.
func (s *Store) RetainRaw(maxAge time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := s.retainRaw(ctx, maxAge)
	// Our own time budget is a cooperative yield, not a failed cleanup. Other
	// database failures still surface; diagnostics expose remaining retention lag.
	if errors.Is(err, context.DeadlineExceeded) && ctx.Err() != nil {
		return nil
	}
	return err
}

func (s *Store) retainRaw(ctx context.Context, maxAge time.Duration) error {
	if maxAge < time.Hour {
		return fmt.Errorf("raw retention must be at least one hour")
	}
	queries := []struct {
		table, column string
		cutoff        time.Time
	}{
		{"host_samples", "observed_at", s.now().Add(-maxAge)},
		{"service_observations", "observed_at", s.now().Add(-maxAge)},
		{"ingest_receipts", "received_at", s.now().Add(-2 * maxAge)},
		{"event_log", "ts", s.now().Add(-24 * time.Hour)},
		{"admin_sessions", "expires_at", s.now()},
	}
	done := make([]bool, len(queries))
	// Interleave tables so a large host backlog cannot monopolize all ten batches.
	for batch := 0; batch < 10; batch++ {
		for i, q := range queries {
			if done[i] {
				continue
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			result, err := s.DB.ExecContext(ctx, `DELETE FROM `+q.table+` WHERE rowid IN (SELECT rowid FROM `+q.table+` WHERE `+q.column+`<? ORDER BY `+q.column+` LIMIT 1000)`, q.cutoff.UTC().Format(dbTimeFormat))
			if err != nil {
				return fmt.Errorf("bounded %s cleanup: %w", q.table, err)
			}
			n, err := result.RowsAffected()
			if err != nil {
				return err
			}
			done[i] = n < 1000
		}
	}
	return nil
}

func (s *Store) BackupTo(dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	_, err := s.db().Exec(`VACUUM INTO ?`, dst)
	return err
}

func (s *Store) InsertBackup(id, path, sha string, size int64) error {
	_, err := s.db().Exec(`INSERT INTO backups(id,path,sha256,size,created_at,verified) VALUES(?,?,?,?,?,0)`,
		id, path, sha, size, s.now().UTC().Format(dbTimeFormat))
	return err
}

func (s *Store) Backups() ([]map[string]any, error) {
	rows, err := s.db().Query(`SELECT id,path,sha256,size,created_at,verified FROM backups ORDER BY created_at DESC`)
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
	err := s.db().QueryRow(`SELECT payload FROM service_observations WHERE service_id=? ORDER BY observed_at DESC,id DESC LIMIT 1`, serviceID).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var o protocol.CheckObservation
	if !strings.HasPrefix(strings.TrimSpace(payload), "{") {
		return nil, fmt.Errorf("corrupt service observation: object required")
	}
	if err := json.Unmarshal([]byte(payload), &o); err != nil {
		return nil, fmt.Errorf("corrupt service observation: %w", err)
	}
	return &o, nil
}

func (s *Store) CheckSeries(serviceID string, from, to time.Time) ([]protocol.CheckObservation, error) {
	rows, err := s.db().Query(`SELECT payload FROM service_observations WHERE service_id=? AND observed_at>=? AND observed_at<? ORDER BY observed_at,id LIMIT 20001`,
		serviceID, from.UTC().Format(dbTimeFormat), to.UTC().Format(dbTimeFormat))
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
		if !strings.HasPrefix(strings.TrimSpace(payload), "{") {
			return nil, fmt.Errorf("corrupt service observation: object required")
		}
		if err := json.Unmarshal([]byte(payload), &o); err != nil {
			return nil, fmt.Errorf("corrupt service observation: %w", err)
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func (s *Store) SetState(entityType, entityID, state, reason string) error {
	now := s.now().UTC().Format(dbTimeFormat)
	var prev sql.NullString
	_ = s.db().QueryRow(`SELECT state FROM current_states WHERE entity_type=? AND entity_id=?`, entityType, entityID).Scan(&prev)
	_, err := s.db().Exec(`INSERT INTO current_states(entity_type,entity_id,state,reason,since,updated_at) VALUES(?,?,?,?,?,?)
		ON CONFLICT(entity_type,entity_id) DO UPDATE SET state=excluded.state, reason=excluded.reason, updated_at=excluded.updated_at,
		since=CASE WHEN current_states.state=excluded.state THEN current_states.since ELSE excluded.since END`,
		entityType, entityID, state, reason, now, now)
	if err != nil {
		return err
	}
	if !prev.Valid || prev.String != state {
		_, _ = s.db().Exec(`INSERT INTO state_events(entity_type,entity_id,from_state,to_state,reason,at) VALUES(?,?,?,?,?,?)`,
			entityType, entityID, prev.String, state, reason, now)
	}
	return nil
}

func (s *Store) State(entityType, entityID string) (string, string, time.Time, error) {
	var st, reason, since string
	err := s.db().QueryRow(`SELECT state,reason,since FROM current_states WHERE entity_type=? AND entity_id=?`, entityType, entityID).Scan(&st, &reason, &since)
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

func (s *Store) ServiceDiscovery(id string) (*protocol.DiscoveredEndpoint, error) {
	var raw string
	if err := s.db().QueryRow(`SELECT payload FROM service_discovery WHERE service_id=?`, id).Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	var ep protocol.DiscoveredEndpoint
	if err := json.Unmarshal([]byte(raw), &ep); err != nil {
		return nil, err
	}
	return &ep, nil
}

func (s *Store) DiscoveryForAgent(id string) (*protocol.DiscoveryDelta, error) {
	var raw string
	if err := s.db().QueryRow(`SELECT payload FROM agent_discovery WHERE agent_id=?`, id).Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	var d protocol.DiscoveryDelta
	if err := json.Unmarshal([]byte(raw), &d); err != nil {
		return nil, err
	}
	return &d, nil
}

// RenameAgent compares the displayed value, not hostname identity. Telemetry must
// never overwrite this owner label. Empty legacy labels fall back to hostname/id.
func (s *Store) RenameAgent(id, name string, expected *string) error {
	query := `UPDATE agents SET display_name=? WHERE id=?`
	args := []any{name, id}
	if expected != nil {
		query += ` AND COALESCE(NULLIF(display_name,''),NULLIF(hostname,''),id)=?`
		args = append(args, *expected)
	}
	result, err := s.db().Exec(query, args...)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("machine not found or name changed; refresh before saving: %w", ErrConflict)
	}
	return nil
}
