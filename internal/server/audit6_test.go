package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/clock"
	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
	"github.com/alinescafs3mp-afk/monik_v2/internal/rules"
	"github.com/alinescafs3mp-afk/monik_v2/internal/storage"
)

func audit6HTTP(t *testing.T, a *App, h http.Handler, recent time.Duration) func(string, string, any) *httptest.ResponseRecorder {
	t.Helper()
	user, e := a.Store.UserByName("owner")
	if e != nil {
		t.Fatal(e)
	}
	token, session, e := a.Store.CreateSession(user, time.Hour, recent)
	if e != nil {
		t.Fatal(e)
	}
	return func(method, path string, body any) *httptest.ResponseRecorder {
		t.Helper()
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, "https://localhost"+path, bytes.NewReader(raw))
		r.RemoteAddr = "127.0.0.1:12345"
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(&http.Cookie{Name: "monik_session", Value: token})
		r.Header.Set("X-CSRF-Token", session.CSRF)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
}
func TestAudit6MonitoringAPIsCommitAndValidate(t *testing.T) {
	a, h := testApp(t)
	call := audit6HTTP(t, a, h, time.Minute)
	op := func(action, key string, params any) *httptest.ResponseRecorder {
		return call("POST", "/api/v1/operations", map[string]any{"action": action, "client_request_key": key, "params": params})
	}
	for _, path := range []string{"/api/v1/monitoring", "/api/v1/enrollment/policy", "/api/v1/diagnostics"} {
		w := call("GET", path, nil)
		if w.Code != 200 {
			t.Fatal(path, w.Code, w.Body.String())
		}
	}
	w := op("rule.save", "rules-ok", map[string]any{"base_revision": 0, "rules": rules.DefaultThresholds()})
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"completed"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	w = op("rule.save", "rules-stale", map[string]any{"base_revision": 0, "rules": rules.DefaultThresholds()})
	if !strings.Contains(w.Body.String(), "host rules changed") || strings.Contains(w.Body.String(), `"status":"completed"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	w = op("enrollment.window.set", "admission", map[string]any{"base_revision": 0, "minutes": 30})
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"open":true`) {
		t.Fatal(w.Code, w.Body.String())
	}
	w = op("enrollment.window.set", "admission-bad", map[string]any{"base_revision": 1, "minutes": 30, "unknown": true})
	if !strings.Contains(w.Body.String(), "unknown field") {
		t.Fatal(w.Body.String())
	}
	w = op("maintenance.set", "maintenance", map[string]any{"entity_type": "fleet", "purpose": "Scheduled test", "end_at": time.Now().Add(time.Hour)})
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"completed"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	var result protocol.Operation
	if e := json.Unmarshal(w.Body.Bytes(), &result); e != nil {
		t.Fatal(e)
	}
	active, e := a.Store.InMaintenance("agent", "any", a.Clock.Now())
	if e != nil || !active {
		t.Fatal(active, e)
	}
	w = op("maintenance.cancel", "cancel", map[string]any{"window_id": result.ID})
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"completed"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	active, e = a.Store.InMaintenance("agent", "any", a.Clock.Now())
	if e != nil || active {
		t.Fatal(active, e)
	}
}
func TestAudit6AdmissionRequiresFreshAuthentication(t *testing.T) {
	a, h := testApp(t)
	call := audit6HTTP(t, a, h, -time.Minute)
	w := call("POST", "/api/v1/operations", map[string]any{"action": "enrollment.window.set", "client_request_key": "same-key", "params": map[string]any{"base_revision": 0, "minutes": 60}})
	if w.Code != 401 || !strings.Contains(w.Body.String(), "recent_auth_required") {
		t.Fatal(w.Code, w.Body.String())
	}
	policy, e := a.Store.AdmissionPolicy()
	if e != nil || policy.Open {
		t.Fatal(policy, e)
	}
	var count int
	if e = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM operations WHERE client_request_key='same-key'`).Scan(&count); e != nil || count != 0 {
		t.Fatal("rejected auth created operation", count, e)
	}
	w = call("POST", "/api/v1/reauth", map[string]any{"password": "supersecret1"})
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	w = call("POST", "/api/v1/operations", map[string]any{"action": "enrollment.window.set", "client_request_key": "same-key", "params": map[string]any{"base_revision": 0, "minutes": 60}})
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
}
func TestAudit6PasswordChangeRevokesHTTPAndProtectsAgainstCSRF(t *testing.T) {
	a, h := testApp(t)
	call := audit6HTTP(t, a, h, time.Minute)
	other := audit6HTTP(t, a, h, time.Minute)
	w := call("POST", "/api/v1/account/password", map[string]string{"current_password": "incorrect", "new_password": "new-test-password"})
	if w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w = other("GET", "/api/v1/me", nil); w.Code != 200 {
		t.Fatal("bad password revoked session")
	}
	w = call("POST", "/api/v1/account/password", map[string]string{"current_password": "supersecret1", "new_password": "new-test-password"})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, c := range []func(string, string, any) *httptest.ResponseRecorder{call, other} {
		if w = c("GET", "/api/v1/me", nil); w.Code != 401 {
			t.Fatal("old session valid", w.Code)
		}
	}
	// Password-changing endpoint must use the same CSRF boundary as all UI writes.
	user, e := a.Store.UserByName("owner")
	if e != nil {
		t.Fatal(e)
	}
	token, _, e := a.Store.CreateSession(user, time.Hour, time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	r := httptest.NewRequest("POST", "https://localhost/api/v1/account/password", strings.NewReader(`{"current_password":"new-test-password","new_password":"third-test-password"}`))
	r.AddCookie(&http.Cookie{Name: "monik_session", Value: token})
	r.RemoteAddr = "127.0.0.1:1"
	r.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("CSRF not enforced", w.Code, w.Body.String())
	}
}
func TestAudit6RuleDurationsHysteresisMaintenanceAndHistoricalVersion(t *testing.T) {
	a, _ := testApp(t)
	clk := clock.NewFake(time.Date(2026, 9, 14, 13, 0, 0, 0, time.UTC))
	a.Clock = clk
	a.Store.Clock = clk
	list := rules.DefaultThresholds()
	for i := range list {
		list[i].PersistSeconds = 10
		list[i].RecoverSeconds = 10
	}
	saved, e := a.Store.SaveHostRules(0, list, "owner")
	if e != nil {
		t.Fatal(e)
	}
	if e = a.Store.CreateMaintenance(storage.MaintenanceWindow{ID: "m", EntityType: "fleet", Purpose: "Test", StartAt: clk.Now(), EndAt: clk.Now().Add(time.Hour), CreatedBy: "owner"}); e != nil {
		t.Fatal(e)
	}
	apply := func(value float64) {
		h := &protocol.HostMetrics{CPUPercent: &value}
		a.evalHostIncidents("h", h, clk.Now())
	}
	apply(90)
	clk.Advance(5 * time.Second)
	apply(90)
	rows, e := a.Store.OpenIncidents()
	if e != nil || len(rows) != 1 || rows[0]["status"] != "pending" {
		t.Fatal(rows, e)
	}
	clk.Advance(5 * time.Second)
	apply(90)
	rows, e = a.Store.OpenIncidents()
	if e != nil || rows[0]["status"] != "confirmed" {
		t.Fatal(rows, e)
	}
	var rule int64
	var maintenance int
	if e = a.Store.DB.QueryRow(`SELECT rule_version,maintenance FROM incidents WHERE id=?`, rows[0]["id"]).Scan(&rule, &maintenance); e != nil || rule != saved.Revision || maintenance != 1 {
		t.Fatal(rule, maintenance, e)
	}
	for i := 0; i < 4; i++ {
		clk.Advance(5 * time.Second)
		apply(83)
	}
	rows, e = a.Store.OpenIncidents()
	if e != nil || len(rows) != 1 {
		t.Fatal("hysteresis lost", rows, e)
	}
	for i := 0; i < 3; i++ {
		clk.Advance(5 * time.Second)
		apply(75)
	}
	rows, e = a.Store.OpenIncidents()
	if e != nil || len(rows) != 0 {
		t.Fatal("real recovery missing", rows, e)
	}
	oldAt := clk.Now()
	clk.Advance(time.Second)
	list[0].Warning = 92
	if _, e = a.Store.SaveHostRules(saved.Revision, list, "owner"); e != nil {
		t.Fatal(e)
	}
	x := 99.0
	a.evalHostIncidents("h", &protocol.HostMetrics{CPUPercent: &x}, oldAt)
	a.evalHostIncidents("h", &protocol.HostMetrics{CPUPercent: &x}, clk.Now().Add(time.Hour))
	rows, e = a.Store.OpenIncidents()
	if e != nil || len(rows) != 0 {
		t.Fatal("old/future evidence reopened rule", rows, e)
	}
}
func TestAudit6UnreadLinksAndCorruptObservationHTTP(t *testing.T) {
	a, h := testApp(t)
	call := audit6HTTP(t, a, h, time.Minute)
	if e := a.Store.InsertAgent(&storage.AgentRow{ID: "h", Hostname: "h", DisplayName: "Human name"}, "hash"); e != nil {
		t.Fatal(e)
	}
	if e := a.Store.UpsertService(protocol.DiscoveredEndpoint{ServiceID: "svc", DialTarget: "127.0.0.1:8000", URL: "http://127.0.0.1:8000"}, "h"); e != nil {
		t.Fatal(e)
	}
	if e := a.Store.InsertIncident(map[string]any{"id": "i", "entity_type": "service", "entity_id": "svc", "metric": "http", "severity": "warning", "status": "confirmed", "reason": "fail"}); e != nil {
		t.Fatal(e)
	}
	from := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	to := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	path := fmt.Sprintf("/api/v1/incidents?from=%s&to=%s&acknowledgement=unread", from, to)
	w := call("GET", path, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"agent_id":"h"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	if e := a.Store.AckIncident("i", "owner"); e != nil {
		t.Fatal(e)
	}
	w = call("GET", path, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"incidents":[]`) {
		t.Fatal(w.Body.String())
	}
	w = call("POST", "/api/v1/operations", map[string]any{"action": "incident.unacknowledge", "client_request_key": "unack", "params": map[string]any{"incident_id": "i"}})
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if _, e := a.Store.DB.Exec(`INSERT INTO service_observations(agent_id,service_id,observed_at,received_at,payload) VALUES('h','svc',?,?,'null')`, time.Now().UTC().Format(time.RFC3339Nano), time.Now().UTC().Format(time.RFC3339Nano)); e != nil {
		t.Fatal(e)
	}
	w = call("GET", "/api/v1/services/svc", nil)
	if w.Code != 500 {
		t.Fatal("corrupt observation HTTP success", w.Code)
	}
}

func TestAudit6GlobalPauseRequiresAppliedProof(t *testing.T) {
	a, _ := testApp(t)
	cfg := protocol.DefaultAgentConfig()
	cfg.Paused = true
	raw, _ := json.Marshal(cfg)
	if e := a.Store.InsertAgent(&storage.AgentRow{ID: "h", Hostname: "h", DesiredConfig: string(raw), DesiredHash: "new", DesiredRevision: 2}, "hash"); e != nil {
		t.Fatal(e)
	}
	if e := a.Store.UpsertService(protocol.DiscoveredEndpoint{ServiceID: "svc", URL: "http://127.0.0.1:8000", DialTarget: "127.0.0.1:8000"}, "h"); e != nil {
		t.Fatal(e)
	}
	sv, e := a.serviceSummaries("h", a.Clock.Now())
	if e != nil || len(sv) != 1 || sv[0].State != "pending" {
		t.Fatal(sv, e)
	}
	if _, e = a.Store.DB.Exec(`UPDATE agents SET applied_revision=2,applied_hash='new' WHERE id='h'`); e != nil {
		t.Fatal(e)
	}
	sv, e = a.serviceSummaries("h", a.Clock.Now())
	if e != nil || sv[0].State != "paused" {
		t.Fatal(sv, e)
	}
}
func TestAudit6CommittedRuleResultCannotBeReportedAsNoEffect(t *testing.T) {
	a, h := testApp(t)
	call := audit6HTTP(t, a, h, time.Minute)
	if _, e := a.Store.DB.Exec(`CREATE TRIGGER fail_commit_stage BEFORE UPDATE ON operation_targets WHEN NEW.stage='committed' BEGIN SELECT RAISE(FAIL,'injected result write failure'); END;`); e != nil {
		t.Fatal(e)
	}
	w := call("POST", "/api/v1/operations", map[string]any{"action": "rule.save", "client_request_key": "partial-result", "params": map[string]any{"base_revision": 0, "rules": rules.DefaultThresholds()}})
	var op protocol.Operation
	if e := json.Unmarshal(w.Body.Bytes(), &op); e != nil {
		t.Fatal(e)
	}
	if len(op.Targets) != 1 || op.Targets[0].Status != protocol.TargetUnknownResult {
		t.Fatal(w.Code, w.Body.String())
	}
	rules, e := a.Store.HostRulesAt(a.Clock.Now())
	if e != nil || rules.Revision == 0 {
		t.Fatal(rules, e)
	}
}

func TestAudit6ExplicitCheckContractAndBaselineRemainDifferent(t *testing.T) {
	for _, v := range []struct {
		app   string
		code  int
		state string
	}{{"pass", 503, "ok"}, {"not_configured", 503, "http_error"}, {"fail", 200, "app_fail"}} {
		t.Run(v.app, func(t *testing.T) {
			a, _ := testApp(t)
			now := a.Clock.Now()
			if e := a.Store.InsertAgent(&storage.AgentRow{ID: "h", Hostname: "h", DesiredConfig: "{}"}, "hash"); e != nil {
				t.Fatal(e)
			}
			if e := a.Store.UpsertService(protocol.DiscoveredEndpoint{ServiceID: "svc", URL: "http://127.0.0.1:8000", DialTarget: "127.0.0.1:8000"}, "h"); e != nil {
				t.Fatal(e)
			}
			if _, e := a.Store.DB.Exec(`UPDATE agents SET last_live_at=?,desired_revision=1,applied_revision=1 WHERE id='h'`, now.UTC().Format(time.RFC3339Nano)); e != nil {
				t.Fatal(e)
			}
			obs := protocol.CheckObservation{ServiceID: "svc", ObservedAt: now, Transport: "ok", HTTPStatus: &v.code, Quality: protocol.QualityOK, AppResult: v.app, ConfigRev: 1, IntervalSeconds: 5}
			raw, _ := json.Marshal(obs)
			if _, e := a.Store.DB.Exec(`INSERT INTO service_observations(agent_id,service_id,observed_at,received_at,payload) VALUES('h','svc',?,?,?)`, now.UTC().Format(time.RFC3339Nano), now.UTC().Format(time.RFC3339Nano), string(raw)); e != nil {
				t.Fatal(e)
			}
			a.evalCheck("h", obs)
			state, _, _, e := a.Store.State("service", "svc")
			if e != nil || state != v.state {
				t.Fatal(state, e)
			}
			rows, e := a.serviceSummaries("h", now)
			if e != nil || len(rows) != 1 || rows[0].State != v.state {
				t.Fatal(rows, e)
			}
		})
	}
}
