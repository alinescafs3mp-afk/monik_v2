package server

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"github.com/alinescafs3mp-afk/monik_v2/internal/agent/configfile"
	"github.com/alinescafs3mp-afk/monik_v2/internal/agent/setup"
	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
	"github.com/alinescafs3mp-afk/monik_v2/internal/storage"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAudit5TrustedDiscoveryOwnerApprovalThenReport(t *testing.T) {
	app, h := testApp(t)
	ts := httptest.NewTLSServer(h)
	defer ts.Close()
	u, e := app.Store.UserByName("owner")
	if e != nil {
		t.Fatal(e)
	}
	token, session, e := app.Store.CreateSession(u, time.Hour, time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	call := func(method, path string, body any, authenticated bool) (int, []byte) {
		t.Helper()
		raw, _ := json.Marshal(body)
		req, _ := http.NewRequest(method, ts.URL+path, bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		if authenticated {
			req.AddCookie(&http.Cookie{Name: "monik_session", Value: token})
			req.Header.Set("X-CSRF-Token", session.CSRF)
		}
		resp, e := ts.Client().Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer resp.Body.Close()
		data, e := io.ReadAll(resp.Body)
		if e != nil {
			t.Fatal(e)
		}
		return resp.StatusCode, data
	}
	p := &setup.Profile{AutoDiscover: true, ControllerURL: ts.URL, CACertPEM: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ts.Certificate().Raw})), StateDir: t.TempDir()}
	st, e := setup.Enroll(p)
	if e != nil {
		t.Fatal(e)
	}
	if !st.File.PendingRegistration {
		t.Fatal("discovery config enrolled too early")
	}
	id := st.File.AgentID
	cred, e := configfile.ReadCredential(st.File.CredentialPath)
	if e != nil {
		t.Fatal(e)
	}
	ctx := context.Background()
	if state, e := setup.AnnounceOnce(ctx, st); e != nil || state != "pending" {
		t.Fatal(state, e)
	}
	if _, e = app.Store.Agent(id); e != storage.ErrNotFound {
		t.Fatal("pending agent was enrolled")
	}
	if status, _ := call("GET", "/api/v1/enrollment/pending", nil, false); status != 401 {
		t.Fatal("pending list leaked", status)
	}
	status, raw := call("GET", "/api/v1/enrollment/pending", nil, true)
	if status != 200 {
		t.Fatal(status, string(raw))
	}
	var list struct {
		Candidates []storage.AgentCandidate `json:"candidates"`
	}
	if e = json.Unmarshal(raw, &list); e != nil || len(list.Candidates) != 1 {
		t.Fatal(string(raw), e)
	}
	if strings.Contains(string(raw), cred) {
		t.Fatal("credential in UI")
	}
	sendReport := func(seq int64) int {
		rep := protocol.AgentReport{SchemaVersion: protocol.SchemaVersion, AgentID: id, SessionID: "boot-1", Sequence: seq, ObservedAt: time.Now().UTC(), IsLive: true}
		raw, _ := json.Marshal(rep)
		r, _ := http.NewRequest("POST", ts.URL+"/api/v1/agent/report", bytes.NewReader(raw))
		r.Header.Set("Authorization", "Bearer "+cred)
		r.Header.Set("X-Monik-Agent-Id", id)
		r.Header.Set("Content-Type", "application/json")
		res, e := ts.Client().Do(r)
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		return res.StatusCode
	}
	if status := sendReport(1); status != 401 {
		t.Fatal("unapproved agent allowed telemetry", status)
	}
	op := map[string]any{"action": "enrollment.approve", "client_request_key": "approve-1", "params": map[string]any{"agent_id": id, "fingerprint": list.Candidates[0].Fingerprint}}
	status, raw = call("POST", "/api/v1/operations", op, true)
	if status != 200 {
		t.Fatal(status, string(raw))
	}
	var result protocol.Operation
	_ = json.Unmarshal(raw, &result)
	if result.Status != protocol.OpCompleted {
		t.Fatal(string(raw))
	}
	// Lost approval HTTP response is reconciled through the same operation key.
	status, raw = call("POST", "/api/v1/operations", op, true)
	if status != 200 {
		t.Fatal(status, string(raw))
	}
	ag, e := app.Store.Agent(id)
	if e != nil || ag.LastLiveAt != nil || ag.Pinned {
		t.Fatal("approval fabricated live/pinned status", ag, e)
	}
	// Agent restart still uses the same persisted proof, then publishes enrollment.
	st, e = configfile.Load(filepath.Join(p.StateDir, "agent.json"))
	if e != nil {
		t.Fatal(e)
	}
	if state, e := setup.AnnounceOnce(ctx, st); e != nil || state != "approved" {
		t.Fatal(state, e)
	}
	if st.File.PendingRegistration || st.File.ControllerURL != ts.URL || st.File.AgentID != id || st.File.AppliedRevision != 0 || st.File.CACertPEM != p.CACertPEM {
		t.Fatal("enrollment transition lost configuration", st.File)
	}
	if status := sendReport(1); status != 200 {
		t.Fatal("approved agent report rejected", status)
	}
	ag, e = app.Store.Agent(id)
	if e != nil || ag.LastLiveAt == nil {
		t.Fatal("live report did not update contact", e)
	}
}
func TestAudit5AnnouncementValidationAndApprovalPermissions(t *testing.T) {
	app, h := testApp(t)
	for _, raw := range []string{`{}`, `{"agent_id":"x","credential":"bad","hostname":"x"}`, `{"agent_id":"x","credential":"` + strings.Repeat("aa", 32) + `","hostname":"x","extra":"not allowed"}`, strings.Repeat("x", 4097)} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/api/v1/agent/announce", strings.NewReader(raw))
		h.ServeHTTP(w, r)
		if w.Code != 400 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	u, _ := app.Store.UserByName("owner")
	token, _, _ := app.Store.CreateSession(u, time.Hour, time.Minute)
	raw := `{"action":"enrollment.approve","client_request_key":"no-csrf","params":{"agent_id":"a","fingerprint":"` + strings.Repeat("b", 64) + `"}}`
	r := httptest.NewRequest("POST", "/api/v1/operations", strings.NewReader(raw))
	r.AddCookie(&http.Cookie{Name: "monik_session", Value: token})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("approval without CSRF", w.Code)
	}
}
func TestAudit5OverviewSeparatesUnreadFromOpenAndRenameValidation(t *testing.T) {
	app, h := testApp(t)
	u, _ := app.Store.UserByName("owner")
	token, _, _ := app.Store.CreateSession(u, time.Hour, time.Minute)
	for i := 0; i < 2; i++ {
		if e := app.Store.InsertIncident(map[string]any{"id": fmt.Sprint(i), "entity_type": "agent", "entity_id": "a", "metric": "cpu", "severity": "critical", "status": "confirmed", "reason": "fixture"}); e != nil {
			t.Fatal(e)
		}
	}
	if e := app.Store.AckIncident("0", "owner"); e != nil {
		t.Fatal(e)
	}
	r := httptest.NewRequest("GET", "/api/v1/overview", nil)
	r.AddCookie(&http.Cookie{Name: "monik_session", Value: token})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var v map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &v)
	if w.Code != 200 || v["unread_incidents"] != float64(1) || v["open_incidents"] != float64(2) {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, name := range []any{"", "  ", "a\nb", strings.Repeat("x", 256), true} {
		req := protocol.SubmitOperation{Action: "agent.rename", Params: map[string]any{"agent_id": "id", "display_name": name}}
		if e := app.validateLifecycleParams(&req); e == nil {
			t.Fatalf("invalid name accepted: %q", name)
		}
	}
}
