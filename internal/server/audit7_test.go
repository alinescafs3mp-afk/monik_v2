package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/clock"
	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
	"github.com/alinescafs3mp-afk/monik_v2/internal/secure"
	"github.com/alinescafs3mp-afk/monik_v2/internal/storage"
)

func audit7Inventory(t *testing.T, a *App) protocol.DiscoveredEndpoint {
	t.Helper()
	cfg := protocol.DefaultAgentConfig()
	raw, _ := json.Marshal(cfg)
	if e := a.Store.InsertAgent(&storage.AgentRow{ID: "h", Hostname: "host", DesiredRevision: 1, DesiredHash: secure.SHA256Bytes(raw), DesiredConfig: string(raw)}, "private-token-hash"); e != nil {
		t.Fatal(e)
	}
	ep := protocol.DiscoveredEndpoint{ServiceID: "svc", DialTarget: "127.0.0.1:8123", URL: "http://127.0.0.1:8123", SpeaksHTTP: true}
	if e := a.Store.UpsertService(ep, "h"); e != nil {
		t.Fatal(e)
	}
	return ep
}
func TestAudit7ServicePinDoesNotStartOrStopMonitoring(t *testing.T) {
	a, h := testApp(t)
	ep := audit7Inventory(t, a)
	a.ensureBaselineCheck("h", ep)
	before, _ := a.Store.Agent("h")
	call := audit6HTTP(t, a, h, time.Minute)
	for n, v := range []bool{true, false, true} {
		w := call("POST", "/api/v1/operations", map[string]any{"action": "service.pin", "client_request_key": strings.Repeat("p", n+1), "params": map[string]any{"service_id": "svc", "pinned": v, "expected_pinned": !v}})
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"completed"`) {
			t.Fatal(w.Code, w.Body.String())
		}
		ag, _ := a.Store.Agent("h")
		if ag.DesiredHash != before.DesiredHash || ag.DesiredRevision != before.DesiredRevision {
			t.Fatal("presentation changed monitoring configuration")
		}
	}
	w := call("GET", "/api/v1/overview", nil)
	if !strings.Contains(w.Body.String(), `"services_selected":1`) {
		t.Fatal(w.Body.String())
	}
	w = call("POST", "/api/v1/operations", map[string]any{"action": "service.pin", "client_request_key": "stale", "params": map[string]any{"service_id": "svc", "pinned": false, "expected_pinned": false}})
	if strings.Contains(w.Body.String(), `"status":"completed"`) {
		t.Fatal("stale toggle accepted")
	}
	if e := a.Store.SetServicePinned("svc", false, nil); e != nil {
		t.Fatal(e)
	}
	w = call("GET", "/api/v1/overview", nil)
	if !strings.Contains(w.Body.String(), `"services_selected":0`) || !strings.Contains(w.Body.String(), `"services_total":1`) {
		t.Fatal(w.Body.String())
	}
}
func TestAudit7ServicePinValidationAndMissingID(t *testing.T) {
	a, h := testApp(t)
	audit7Inventory(t, a)
	call := audit6HTTP(t, a, h, time.Minute)
	for i, params := range []map[string]any{{"service_id": "svc", "pinned": "true"}, {"service_id": "svc", "pinned": true, "extra": true}, {"service_id": "missing", "pinned": true}} {
		w := call("POST", "/api/v1/operations", map[string]any{"action": "service.pin", "client_request_key": string(rune('a' + i)), "params": params})
		if strings.Contains(w.Body.String(), `"status":"completed"`) {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}
func TestAudit7NewChecksStartPausedAndAutoModeDoesNotRewriteExisting(t *testing.T) {
	a, _ := testApp(t)
	ep := audit7Inventory(t, a)
	a.ensureBaselineCheck("h", ep)
	ag, _ := a.Store.Agent("h")
	var cfg protocol.AgentConfig
	_ = json.Unmarshal([]byte(ag.DesiredConfig), &cfg)
	if len(cfg.Checks) != 1 || !cfg.Checks[0].Paused {
		t.Fatal("unselected service started checks")
	}
	cfg.AutoMonitorNew = true
	raw, _ := json.Marshal(cfg)
	if e := a.Store.SetDesired("h", ag.DesiredRevision+1, secure.SHA256Bytes(raw), string(raw)); e != nil {
		t.Fatal(e)
	}
	a.ensureBaselineCheck("h", ep)
	ep.ServiceID = "svc2"
	ep.DialTarget = "127.0.0.1:8124"
	ep.URL = "http://127.0.0.1:8124"
	if e := a.Store.UpsertService(ep, "h"); e != nil {
		t.Fatal(e)
	}
	a.ensureBaselineCheck("h", ep)
	ag, _ = a.Store.Agent("h")
	_ = json.Unmarshal([]byte(ag.DesiredConfig), &cfg)
	if len(cfg.Checks) != 2 || !cfg.Checks[0].Paused || cfg.Checks[1].Paused {
		t.Fatal("auto mode rewrote a previous selection", cfg.Checks)
	}
}
func TestAudit7ServicePolicyDoesNotPauseHost(t *testing.T) {
	cfg := protocol.DefaultAgentConfig()
	cfg.Checks = []protocol.CheckDefinition{{ID: "a", ServiceID: "sa"}, {ID: "b", ServiceID: "sb", Ignored: true}}
	if e := applyConfigPatch(&cfg, protocol.SubmitOperation{Action: "profile.apply", Params: map[string]any{"pause_all_services": true, "auto_monitor_new": false}}); e != nil {
		t.Fatal(e)
	}
	if cfg.Paused || !cfg.Checks[0].Paused || !cfg.Checks[1].Paused || !cfg.Checks[1].Ignored {
		t.Fatal("host/ignore policy changed")
	}
	if e := applyConfigPatch(&cfg, protocol.SubmitOperation{Action: "profile.apply", Params: map[string]any{"pause_all_services": false}}); e != nil {
		t.Fatal(e)
	}
	if cfg.Paused || cfg.Checks[0].Paused || !cfg.Checks[1].Ignored {
		t.Fatal("ignored service accidentally enabled")
	}
}
func TestAudit7RememberedSessionExpiresAndLogoutRevokes(t *testing.T) {
	for _, remember := range []bool{false, true} {
		t.Run(map[bool]string{false: "session", true: "remember"}[remember], func(t *testing.T) {
			a, h := testApp(t)
			clk := clock.NewFake(time.Now().UTC())
			a.Clock = clk
			a.Store.Clock = clk
			raw, _ := json.Marshal(map[string]any{"username": "owner", "password": "supersecret1", "remember": remember})
			req := httptest.NewRequest("POST", "https://localhost/api/v1/login", bytes.NewReader(raw))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			if w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			cookies := w.Result().Cookies()
			if len(cookies) != 1 {
				t.Fatal(cookies)
			}
			c := cookies[0]
			if !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteStrictMode || c.Path != "/" {
				t.Fatal("unsafe cookie", c)
			}
			if remember && (c.MaxAge != 30*24*3600 || c.Expires.IsZero()) {
				t.Fatal("remember duration", c)
			}
			if !remember && (c.MaxAge != 0 || !c.Expires.IsZero()) {
				t.Fatal("nonremember cookie persisted")
			}
			sess, e := a.Store.SessionByToken(c.Value)
			if e != nil {
				t.Fatal(e)
			}
			clk.Advance(11 * time.Hour)
			if _, e = a.Store.SessionByToken(c.Value); e != nil {
				t.Fatal("early expiry")
			}
			if !remember {
				clk.Advance(time.Hour)
				if _, e = a.Store.SessionByToken(c.Value); e == nil {
					t.Fatal("ordinary session never expired")
				}
				return
			}
			if sess.RecentAuthUntil.After(clk.Now()) {
				t.Fatal("long login prolongs recent auth")
			}
			req = httptest.NewRequest("POST", "https://localhost/api/v1/logout", strings.NewReader(`{}`))
			req.AddCookie(c)
			req.Header.Set("X-CSRF-Token", sess.CSRF)
			req.Header.Set("Content-Type", "application/json")
			w = httptest.NewRecorder()
			h.ServeHTTP(w, req)
			if w.Code != 200 || w.Result().Cookies()[0].MaxAge != -1 {
				t.Fatal(w.Code, w.Body.String())
			}
			if _, e = a.Store.SessionByToken(c.Value); e == nil {
				t.Fatal("logout retained bearer")
			}
		})
	}
}
func TestAudit7RememberedSessionAbsoluteLimit(t *testing.T) {
	a, _ := testApp(t)
	clk := clock.NewFake(time.Now().UTC())
	a.Store.Clock = clk
	u, _ := a.Store.UserByName("owner")
	tok, _, e := a.Store.CreateSession(u, 30*24*time.Hour, time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	clk.Advance(30 * 24 * time.Hour)
	if _, e = a.Store.SessionByToken(tok); e == nil {
		t.Fatal("absolute expiry not enforced")
	}
}
func TestAudit7RevokeOtherSessionsNoSecretExposure(t *testing.T) {
	a, h := testApp(t)
	call := audit6HTTP(t, a, h, time.Minute)
	u, _ := a.Store.UserByName("owner")
	tok, _, e := a.Store.CreateSession(u, 30*24*time.Hour, time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	w := call("GET", "/api/v1/sessions", nil)
	if w.Code != 200 || strings.Contains(w.Body.String(), tok) || strings.Contains(w.Body.String(), "csrf") || strings.Contains(w.Body.String(), "token_hash") {
		t.Fatal(w.Code, w.Body.String())
	}
	w = call("POST", "/api/v1/operations", map[string]any{"action": "session.revoke_others", "client_request_key": "revoke-others", "params": map[string]any{}})
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"completed"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	if _, e = a.Store.SessionByToken(tok); e == nil {
		t.Fatal("other session remains valid")
	}
	if w = call("GET", "/api/v1/me", nil); w.Code != 200 {
		t.Fatal("current session revoked", w.Code)
	}
}
func TestAudit7ProfileRejectsUnusedFields(t *testing.T) {
	a, _ := testApp(t)
	for _, p := range []map[string]any{{"auto_monitor_new": "false"}, {"pause_all_services": 1}, {"shell": "echo ignored"}} {
		req := protocol.SubmitOperation{Action: "profile.apply", Params: p}
		if a.validateLifecycleParams(&req) == nil {
			t.Fatal("unsupported profile accepted", p)
		}
	}
}

func TestAudit7SuppressionIncludesOriginalSocketOfCustomCheck(t *testing.T) {
	a, _ := testApp(t)
	audit7Inventory(t, a)
	if _, e := a.Store.DB.Exec(`UPDATE agents SET capabilities='{"selective_monitor_v1":{"status":"supported"}}' WHERE id='h'`); e != nil {
		t.Fatal(e)
	}
	ag, _ := a.Store.Agent("h")
	cfg := protocol.DefaultAgentConfig()
	cfg.Checks = []protocol.CheckDefinition{{ID: "c", ServiceID: "svc", DialTarget: "127.0.0.1:9000", URL: "http://127.0.0.1:9000", Paused: true}}
	if e := a.monitoringExclusions(ag, &cfg); e != nil {
		t.Fatal(e)
	}
	if len(cfg.DiscoveryDisabledTargets) != 1 || cfg.DiscoveryDisabledTargets[0] != "127.0.0.1:8123" {
		t.Fatal(cfg.DiscoveryDisabledTargets)
	}
}
func TestAudit7AutoMonitoringRequiresNewWorkerCapability(t *testing.T) {
	a, _ := testApp(t)
	audit7Inventory(t, a)
	r := protocol.SubmitOperation{Action: "profile.apply", Params: map[string]any{"auto_monitor_new": true}}
	if e := a.validateCheckTargets(r, []string{"h"}); e == nil {
		t.Fatal("old worker accepted new config field")
	}
	if _, e := a.Store.DB.Exec(`UPDATE agents SET capabilities='{"selective_monitor_v1":{"status":"supported"}}' WHERE id='h'`); e != nil {
		t.Fatal(e)
	}
	if e := a.validateCheckTargets(r, []string{"h"}); e != nil {
		t.Fatal(e)
	}
}

func TestAudit7InventoryAndDesiredStateAreNotServiceFailures(t *testing.T) {
	for _, state := range []string{"ok", "responds", "paused", "unmonitored", "pending"} {
		if serviceHasProblem(state) {
			t.Fatalf("%s incorrectly counted as service failure", state)
		}
	}
	for _, state := range []string{"app_fail", "transport_fail", "http_error", "unknown", "stale"} {
		if !serviceHasProblem(state) {
			t.Fatalf("%s lost failure/uncertainty", state)
		}
	}
}
func TestAudit7HiddenFailureStillAppearsInMachineProblemCount(t *testing.T) {
	a, h := testApp(t)
	audit7Inventory(t, a)
	code := 503
	now := a.Clock.Now()
	_, err := a.Store.AcceptReport(protocol.AgentReport{SchemaVersion: 3, AgentID: "h", SessionID: "session", Sequence: 1, ObservedAt: now, IsLive: true, Checks: []protocol.CheckObservation{{ServiceID: "svc", CheckID: "c", ObservedAt: now, IntervalSeconds: 5, Vantage: "agent/local", Transport: "ok", HTTPStatus: &code, AppResult: "fail", Quality: protocol.QualityOK}}})
	if err != nil {
		t.Fatal(err)
	}
	if err = a.Store.UpdateServiceFlags("svc", map[string]any{"hidden": 1, "pinned": 1}); err != nil {
		t.Fatal(err)
	}
	call := audit6HTTP(t, a, h, time.Minute)
	w := call("GET", "/api/v1/overview", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"unselected_service_problems":1`) || !strings.Contains(w.Body.String(), `"services_selected":0`) {
		t.Fatal(w.Code, w.Body.String())
	}
}
