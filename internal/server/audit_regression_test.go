package server

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
	"github.com/alinescafs3mp-afk/monik_v2/internal/storage"
)

func TestAuditHistoryDoesNotCreateCyclesOrMutateSamples(t *testing.T) {
	points := []map[string]any{{"observed_at": "2026-09-13T10:00:00Z", "cpu_pct": 99.0}, {"observed_at": "2026-09-13T10:00:05Z", "cpu_pct": 2.0}}
	got := downsample(points, time.Minute)
	if _, err := json.Marshal(got); err != nil {
		t.Fatalf("history must be JSON encodable: %v", err)
	}
	if _, ok := points[1]["min"]; ok {
		t.Fatal("query mutated its input sample")
	}
}

func TestAuditDTOsUsePublicJSONContract(t *testing.T) {
	b, err := json.Marshal(&storage.AgentRow{ID: "a", CredentialHash: "PRIVATE_VERIFIER", DisplayName: "Host"})
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	if v["id"] != "a" || v["display_name"] != "Host" {
		t.Fatalf("frontend requires snake_case fields: %s", b)
	}
	for _, value := range v {
		if value == "PRIVATE_VERIFIER" {
			t.Fatal("credential verifier exposed")
		}
	}
	b, _ = json.Marshal(&storage.ServiceRow{ID: "s", AgentID: "a", URL: "http://127.0.0.1:8080"})
	v = nil
	_ = json.Unmarshal(b, &v)
	if v["id"] != "s" || v["agent_id"] != "a" {
		t.Fatalf("service fields mismatch: %s", b)
	}
}

func TestAuditPendingMigrationRespectsTargetScope(t *testing.T) {
	app, _ := testApp(t)
	p := &protocol.MigrationPlan{PlanID: "p", ControllerID: app.ControllerID(), CandidateURL: "https://127.0.0.1:8778", Generation: 2, Mode: "prepare", ExpiresAt: time.Now().Add(time.Hour)}
	if err := app.Store.InsertMigration(p, "op"); err != nil {
		t.Fatal(err)
	}
	if err := app.Store.SetSetting("active_migration_id", "p"); err != nil {
		t.Fatal(err)
	}
	if err := app.Store.SetMigrationTarget("p", "selected", "queued", ""); err != nil {
		t.Fatal(err)
	}
	if got := pendingMigration(app.Store, "not-selected"); got != nil {
		t.Fatal("migration sent to agent outside frozen target set")
	}
}

func TestAuditUnavailableActionsDoNotPersistSecretOrFakeOperation(t *testing.T) {
	app, _ := testApp(t)
	session := &storage.Session{Username: "owner", Role: "owner"}
	for _, action := range []string{"credential.rotate", "trust.stage", "trust.retire"} {
		w := httptest.NewRecorder()
		app.processSubmit(w, session, protocol.SubmitOperation{Action: action, ClientRequestKey: action, Params: map[string]any{"value": "DO_NOT_PERSIST_TEST_SECRET", "trust_pem": "not-a-cert", "fingerprint": "x"}, TargetIDs: []string{"a"}})
		if w.Code != 401 {
			t.Fatalf("%s without recent auth: %d %s", action, w.Code, w.Body.String())
		}
	}
	w := httptest.NewRecorder()
	app.processSubmit(w, session, protocol.SubmitOperation{Action: "check.trial", ClientRequestKey: "check.trial", Params: map[string]any{"value": "DO_NOT_PERSIST_TEST_SECRET", "url": "http://127.0.0.1/"}, TargetIDs: []string{"a"}})
	if w.Code != 400 {
		t.Fatalf("trial with secret plaintext: %d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	app.processSubmit(w, session, protocol.SubmitOperation{Action: "secret.replace", ClientRequestKey: "secret.replace", Params: map[string]any{"value": "DO_NOT_PERSIST_TEST_SECRET", "name": "n", "header": "X-Token"}, TargetIDs: []string{"a"}})
	if w.Code != 401 {
		t.Fatalf("secret.replace without recent auth: %d %s", w.Code, w.Body.String())
	}
	var n int
	if err := app.Store.DB.QueryRow(`SELECT COUNT(*) FROM operations`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("blocked action journalled: %d %v", n, err)
	}
	var secrets int
	if err := app.Store.DB.QueryRow(`SELECT COUNT(*) FROM check_secrets`).Scan(&secrets); err != nil || secrets != 0 {
		t.Fatalf("secret rows: %d %v", secrets, err)
	}
}
func TestAuditOverviewContainsMetricsAndRealServiceOutcome(t *testing.T) {
	app, _ := testApp(t)
	now := time.Now().UTC()
	cpu, ping, loss := 37.0, 24.0, 0.0
	code := 401
	if err := app.Store.InsertAgent(&storage.AgentRow{ID: "a", Hostname: "fixture", DisplayName: "Fixture", DesiredConfig: "{}"}, "PRIVATE_VERIFIER"); err != nil {
		t.Fatal(err)
	}
	if err := app.Store.UpsertService(protocol.DiscoveredEndpoint{ServiceID: "s", URL: "http://127.0.0.1:8080", DialTarget: "127.0.0.1:8080", SpeaksHTTP: true}, "a"); err != nil {
		t.Fatal(err)
	}
	// Overview now shows explicitly pinned services only.
	if err := app.Store.SetServicePinned("s", true, nil); err != nil {
		t.Fatal(err)
	}
	report := protocol.AgentReport{SchemaVersion: 3, AgentID: "a", SessionID: "s1", Sequence: 1, ObservedAt: now, IsLive: true, Host: &protocol.HostMetrics{Hostname: "fixture", CPUPercent: &cpu, RAMTotal: 100, RAMUsed: 40, RAMAvailable: 60, Disks: []protocol.Disk{{Mount: "/", Total: 200, Used: 100, Available: 100, UsedPct: 50}}, Ping: &protocol.PingSummary{Target: "8.8.8.8", MeanMS: &ping, LossPct: &loss}}, Checks: []protocol.CheckObservation{{ServiceID: "s", CheckID: "c", ObservedAt: now, Vantage: "agent/local", Transport: "ok", HTTPStatus: &code, AppResult: "not_configured", Quality: protocol.QualityOK}}}
	if _, err := app.Store.AcceptReport(report); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	app.handleOverview(w, httptest.NewRequest("GET", "/api/v1/overview", nil), &storage.Session{})
	var got struct {
		Cards []map[string]any `json:"cards"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Cards) != 1 || got.Cards[0]["cpu"] != 37.0 {
		t.Fatalf("card missing: %s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "HTTP 401") || !strings.Contains(w.Body.String(), "ram_total") || !strings.Contains(w.Body.String(), "disks") || strings.Contains(w.Body.String(), "PRIVATE_VERIFIER") {
		t.Fatalf("unsafe/incomplete card: %s", w.Body.String())
	}
	summaries, err := app.serviceSummaries("a", now)
	if err != nil || len(summaries) != 1 || summaries[0].State != "http_error" {
		t.Fatalf("401 retains its HTTP response but fails the default success policy: %+v %v", summaries, err)
	}
	summaries, err = app.serviceSummaries("a", now.Add(time.Minute))
	if err != nil || summaries[0].State != "stale" {
		t.Fatalf("old service painted live: %+v %v", summaries, err)
	}
}
func TestAuditAdminNetworkPolicyDoesNotTrustSpoofedForwardingHeaders(t *testing.T) {
	app, h := testApp(t)
	app.Cfg.AdminAllowlist = []string{"192.0.2.0/24"}
	req := httptest.NewRequest("GET", "/api/v1/setup/status", nil)
	req.RemoteAddr = "198.51.100.2:1234"
	req.Header.Set("X-Forwarded-For", "192.0.2.1")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 403 {
		t.Fatalf("spoofed admin allowed %d", w.Code)
	}
	req = httptest.NewRequest("GET", "/api/v1/agent/identity", nil)
	req.RemoteAddr = "198.51.100.2:1234"
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code == 403 {
		t.Fatal("admin allowlist blocked agent control")
	}
	req = httptest.NewRequest("GET", "/api/v1/setup/status", nil)
	req.RemoteAddr = "192.0.2.7:1234"
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
}
func TestAuditSerializationFailureIs500NotBlank200(t *testing.T) {
	app, _ := testApp(t)
	w := httptest.NewRecorder()
	bad := map[string]any{}
	bad["cycle"] = bad
	app.writeJSON(w, 200, bad)
	if w.Code != 500 || !json.Valid(w.Body.Bytes()) {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
}
func TestAuditServicePauseDoesNotPauseHostCollection(t *testing.T) {
	cfg := protocol.DefaultAgentConfig()
	cfg.Checks = []protocol.CheckDefinition{{ID: "c", ServiceID: "s"}}
	applyConfigPatch(&cfg, protocol.SubmitOperation{Action: "service.pause", Params: map[string]any{"service_id": "s", "paused": true}})
	if cfg.Paused || !cfg.Checks[0].Paused {
		t.Fatalf("wrong pause scope: %+v", cfg)
	}
}
func TestAuditHistoryBucketsPreserveShortPeaks(t *testing.T) {
	pts := []map[string]any{{"observed_at": "2026-09-13T10:00:00Z", "cpu_pct": 99.0, "ram_used": int64(20)}, {"observed_at": "2026-09-13T10:00:05Z", "cpu_pct": 2.0, "ram_used": int64(10)}}
	got := downsample(pts, time.Minute)
	if len(got) != 1 {
		t.Fatal(got)
	}
	max := got[0]["max"].(map[string]any)
	if max["cpu_pct"] != 99.0 {
		t.Fatalf("peak erased: %+v", got)
	}
}
