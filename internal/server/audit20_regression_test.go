package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
	"github.com/alinescafs3mp-afk/monik_v2/internal/secure"
	"github.com/alinescafs3mp-afk/monik_v2/internal/storage"
)

func v20Session(t *testing.T, a *App, role string) (string, *storage.Session) {
	t.Helper()
	u, e := a.Store.UserByName("owner")
	if e != nil {
		t.Fatal(e)
	}
	if role != "owner" {
		u, e = a.Store.CreateUser("test-"+role, u.PasswordHash, role)
		if e != nil {
			t.Fatal(e)
		}
	}
	token, s, e := a.Store.CreateSession(u, time.Hour, time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	return token, s
}

func TestV20CrossSiteLoginRejected(t *testing.T) {
	for _, contentType := range []string{"text/plain", "application/json"} {
		t.Run(contentType, func(t *testing.T) {
			a, h := testApp(t)
			r := httptest.NewRequest("POST", "https://localhost/api/v1/login", strings.NewReader(`{"username":"owner","password":"supersecret1"}`))
			r.RemoteAddr = "127.0.0.1:10"
			r.Header.Set("Origin", "https://attacker.invalid")
			r.Header.Set("Content-Type", contentType)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != 403 {
				t.Fatalf("cross-site login accepted: HTTP %d", w.Code)
			}
			var n int
			if e := a.Store.DB.QueryRow(`SELECT count(*) FROM admin_sessions`).Scan(&n); e != nil || n != 0 {
				t.Fatalf("session created for rejected origin: %d %v", n, e)
			}
		})
	}
}

func TestV20ViewerCannotReadEnrollmentCapability(t *testing.T) {
	a, h := testApp(t)
	owner := audit6HTTP(t, a, h, time.Minute)
	w := owner("POST", "/api/v1/operations", map[string]any{"action": "enrollment.create", "client_request_key": "v20-enroll", "params": map[string]any{}})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var op protocol.Operation
	if e := json.Unmarshal(w.Body.Bytes(), &op); e != nil {
		t.Fatal(e)
	}
	code, _ := op.Targets[0].Evidence["code"].(string)
	if code == "" {
		t.Fatal("owner did not receive code")
	}
	token, _ := v20Session(t, a, "viewer")
	for _, path := range []string{"/api/v1/operations", "/api/v1/operations/" + op.ID} {
		r := httptest.NewRequest("GET", "https://localhost"+path, nil)
		r.AddCookie(&http.Cookie{Name: "monik_session", Value: token})
		w = httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), code) {
			t.Errorf("read-only journal leaked live enrollment capability via %s", path)
		}
	}
	persisted, e := a.Store.Operation(op.ID)
	if e != nil || persisted.Targets[0].Evidence["code"] != code {
		t.Fatal("view redaction mutated stored evidence", e)
	}
	evs, e := a.Store.EventsAfter(0, 100)
	if e != nil {
		t.Fatal(e)
	}
	for _, ev := range evs {
		if strings.Contains(ev.Payload, code) {
			t.Fatal("event stream contains enrollment capability")
		}
	}
}

type v20GateBody struct {
	io.Reader
	entered chan struct{}
	resume  chan struct{}
	first   bool
}

func (b *v20GateBody) Read(p []byte) (int, error) {
	if !b.first {
		b.first = true
		close(b.entered)
		<-b.resume
	}
	return b.Reader.Read(p)
}
func (b *v20GateBody) Close() error { return nil }

func TestV20RotatedCredentialCannotFinishDelayedReport(t *testing.T) {
	a, h := testApp(t)
	old := strings.Repeat("a", 64)
	next := strings.Repeat("b", 64)
	cfg := protocol.DefaultAgentConfig()
	raw, _ := json.Marshal(cfg)
	if e := a.Store.InsertAgent(&storage.AgentRow{ID: "v20-agent", Hostname: "fixture", DesiredRevision: 1, DesiredHash: secure.SHA256Bytes(raw), DesiredConfig: string(raw)}, secure.HashToken(old)); e != nil {
		t.Fatal(e)
	}
	rep := protocol.AgentReport{SchemaVersion: protocol.SchemaVersion, AgentID: "v20-agent", SessionID: "s1", Sequence: 1, IsLive: true, ObservedAt: time.Now().UTC()}
	payload, _ := json.Marshal(rep)
	gate := &v20GateBody{Reader: bytes.NewReader(payload), entered: make(chan struct{}), resume: make(chan struct{})}
	r := httptest.NewRequest("POST", "https://localhost/api/v1/agent/report", nil)
	r.Body = gate
	r.Header.Set("Authorization", "Bearer "+old)
	r.Header.Set("X-Monik-Agent-Id", "v20-agent")
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { h.ServeHTTP(w, r); close(done) }()
	select {
	case <-gate.entered:
	case <-time.After(5 * time.Second):
		close(gate.resume)
		t.Fatal("body not reached")
	}
	_, err := a.Store.DB.Exec(`UPDATE agents SET credential_hash=? WHERE id=?`, secure.HashToken(next), "v20-agent")
	close(gate.resume)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("request did not finish")
	}
	if w.Code != 401 {
		t.Fatalf("retired credential received control response: HTTP %d", w.Code)
	}
	var n int
	if e := a.Store.DB.QueryRow(`SELECT count(*) FROM ingest_receipts WHERE agent_id='v20-agent'`).Scan(&n); e != nil || n != 0 {
		t.Fatalf("retired credential committed report: %d %v", n, e)
	}
}

func TestV20StorageFailureIsNotEmptySuccess(t *testing.T) {
	for _, kind := range []string{"lookup", "secrets", "backups"} {
		t.Run(kind, func(t *testing.T) {
			a, _ := testApp(t)
			_, s := v20Session(t, a, "owner")
			if e := a.Store.Close(); e != nil {
				t.Fatal(e)
			}
			w := httptest.NewRecorder()
			r := httptest.NewRequest("POST", "https://localhost/", strings.NewReader(`{"client_request_key":"unknown","action":"agent.restart","target_mode":"selected","target_ids":["h"],"params":{}}`))
			switch kind {
			case "lookup":
				a.handleLookupOp(w, r, s)
			case "secrets":
				a.handleSecrets(w, r, s)
			case "backups":
				a.handleBackups(w, r, s)
			}
			if w.Code < 500 {
				t.Fatalf("storage failure presented as success: HTTP %d %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestV20SetupDoesNotCommitOwnerWithoutSettings(t *testing.T) {
	a, e := Open(Config{DataDir: t.TempDir(), Listen: "127.0.0.1:0", AdvertisedURL: "https://localhost:8777"})
	if e != nil {
		t.Fatal(e)
	}
	defer a.Close()
	if _, e = a.Store.DB.Exec(`CREATE TRIGGER v20_fail_setup BEFORE INSERT ON settings WHEN NEW.key='advertised_url' BEGIN SELECT RAISE(ABORT,'injected setting failure'); END`); e != nil {
		t.Fatal(e)
	}
	e = a.CompleteSetup(SetupRequest{Username: "owner", Password: "a-long-test-password", AdvertisedURL: "https://localhost:8777", Listen: "127.0.0.1:0"})
	if e == nil {
		t.Error("failed configuration publication reported setup success")
	}
	n, countErr := a.Store.UserCount()
	if countErr != nil || n != 0 {
		t.Errorf("partial setup left owner: %d %v", n, countErr)
	}
}

func TestV20RoleChangeWhileReadingCannotDispatch(t *testing.T) {
	a, h := testApp(t)
	token, s := v20Session(t, a, "owner")
	gate := &v20GateBody{Reader: strings.NewReader(`{"action":"enrollment.create","client_request_key":"stale-owner","params":{}}`), entered: make(chan struct{}), resume: make(chan struct{})}
	r := httptest.NewRequest("POST", "https://localhost/api/v1/operations", nil)
	r.Body = gate
	r.AddCookie(&http.Cookie{Name: "monik_session", Value: token})
	r.Header.Set("X-CSRF-Token", s.CSRF)
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { h.ServeHTTP(w, r); close(done) }()
	select {
	case <-gate.entered:
	case <-time.After(5 * time.Second):
		close(gate.resume)
		t.Fatal("body not reached")
	}
	_, err := a.Store.DB.Exec(`UPDATE admin_users SET role='viewer' WHERE id=?`, s.UserID)
	close(gate.resume)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("request did not finish")
	}
	if w.Code != 401 && w.Code != 403 {
		t.Fatalf("stale owner privilege used after role change: HTTP %d", w.Code)
	}
	var n int
	if e := a.Store.DB.QueryRow(`SELECT count(*) FROM operations WHERE client_request_key='stale-owner'`).Scan(&n); e != nil || n != 0 {
		t.Fatalf("old role dispatched: %d %v", n, e)
	}
}

func TestV20ConsoleConfigRevocationDuringBody(t *testing.T) {
	a, h := testApp(t)
	audit7Inventory(t, a)
	token, s := v20Session(t, a, "owner")
	_, revision, e := a.readConsoleConfiguration()
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(map[string]any{"base_revision": revision, "enabled": true, "fingerprint_verified": true, "target": map[string]any{"host": "127.0.0.1", "port": 22, "username": "test-user", "host_key_type": "ssh-ed25519", "host_key_sha256": "SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}})
	gate := &v20GateBody{Reader: bytes.NewReader(raw), entered: make(chan struct{}), resume: make(chan struct{})}
	r := httptest.NewRequest("POST", "https://localhost/api/v1/agents/h/console-config", nil)
	r.Body = gate
	r.AddCookie(&http.Cookie{Name: "monik_session", Value: token})
	r.Header.Set("X-CSRF-Token", s.CSRF)
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { h.ServeHTTP(w, r); close(done) }()
	select {
	case <-gate.entered:
	case <-time.After(5 * time.Second):
		close(gate.resume)
		t.Fatal("body not reached")
	}
	e = a.Store.DeleteSession(s.ID)
	close(gate.resume)
	if e != nil {
		t.Fatal(e)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("request did not finish")
	}
	if w.Code != 401 && w.Code != 403 {
		t.Fatalf("revoked session configured SSH: HTTP %d %s", w.Code, w.Body.String())
	}
	cfg, _, e := a.readConsoleConfiguration()
	if e != nil || len(cfg.Targets) != 0 {
		t.Fatal("revoked session published SSH authority", e)
	}
}

func TestV20EnrollmentRequiresRecentAuthentication(t *testing.T) {
	a, h := testApp(t)
	call := audit6HTTP(t, a, h, -time.Minute)
	w := call("POST", "/api/v1/operations", map[string]any{"action": "enrollment.create", "client_request_key": "not-recent", "params": map[string]any{}})
	if w.Code != 401 || !strings.Contains(w.Body.String(), "recent_auth_required") {
		t.Fatalf("old browser login issued enrollment code: %d", w.Code)
	}
}

func TestV20CredentialRevokeCannotClaimSuccessOnWriteFailure(t *testing.T) {
	a, h := testApp(t)
	cfg := protocol.DefaultAgentConfig()
	raw, _ := json.Marshal(cfg)
	secret := strings.Repeat("e", 64)
	if e := a.Store.InsertAgent(&storage.AgentRow{ID: "revoke-fixture", Hostname: "fixture", DesiredRevision: 1, DesiredHash: secure.SHA256Bytes(raw), DesiredConfig: string(raw)}, secure.HashToken(secret)); e != nil {
		t.Fatal(e)
	}
	if _, e := a.Store.DB.Exec(`CREATE TRIGGER v20_revoke_failure BEFORE UPDATE OF revoked ON agents BEGIN SELECT RAISE(FAIL,'injected revocation failure'); END`); e != nil {
		t.Fatal(e)
	}
	owner := audit6HTTP(t, a, h, time.Minute)
	w := owner("POST", "/api/v1/operations", map[string]any{"action": "credential.revoke", "client_request_key": "revoke-test", "target_ids": []string{"revoke-fixture"}, "params": map[string]any{}})
	var op protocol.Operation
	if e := json.Unmarshal(w.Body.Bytes(), &op); e != nil {
		t.Fatal(w.Code, w.Body.String(), e)
	}
	ag, e := a.Store.Agent("revoke-fixture")
	if e != nil {
		t.Fatal(e)
	}
	if ag.Revoked || ag.CredentialHash != secure.HashToken(secret) {
		t.Fatal("failed write changed credential")
	}
	if op.Status == protocol.OpCompleted {
		t.Fatal("revocation announced complete while credential is still valid")
	}
	for _, target := range op.Targets {
		if target.Status == protocol.TargetSucceeded {
			t.Fatal("failed revocation target was marked successful")
		}
	}
}
