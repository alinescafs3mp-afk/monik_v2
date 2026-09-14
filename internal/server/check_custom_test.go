package server

import (
	"encoding/json"
	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
	"github.com/alinescafs3mp-afk/monik_v2/internal/secure"
	"github.com/alinescafs3mp-afk/monik_v2/internal/storage"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func customAgent(t *testing.T, a *App, id string, supported bool) {
	t.Helper()
	cfg := protocol.DefaultAgentConfig()
	b, _ := json.Marshal(cfg)
	if e := a.Store.InsertAgent(&storage.AgentRow{ID: id, DesiredRevision: 1, DesiredHash: secure.SHA256Bytes(b), DesiredConfig: string(b)}, "test"); e != nil {
		t.Fatal(e)
	}
	caps := "{}"
	if supported {
		caps = `{"http_custom_v1":{"status":"supported"}}`
	}
	if _, e := a.Store.DB.Exec(`UPDATE agents SET capabilities=?,last_live_at=? WHERE id=?`, caps, time.Now().UTC().Format(time.RFC3339Nano), id); e != nil {
		t.Fatal(e)
	}
	if e := a.Store.UpsertService(protocol.DiscoveredEndpoint{ServiceID: id + "-s", URL: "http://127.0.0.1:8080", DialTarget: "127.0.0.1:8080", SpeaksHTTP: true}, id); e != nil {
		t.Fatal(e)
	}
}
func customDefinition(id string) protocol.CheckDefinition {
	return protocol.CheckDefinition{ID: id + "-c", ServiceID: id + "-s", URL: "http://127.0.0.1:8080", RequestVersion: 1, Method: "POST", AllowPOST: true, Kind: "http_health", Body: `{"method":"health"}`, ExpectedStatus: []int{200}, IntervalSeconds: 30, TimeoutSeconds: 10}
}
func asMap(v any) map[string]any {
	b, _ := json.Marshal(v)
	var m map[string]any
	json.Unmarshal(b, &m)
	return m
}
func TestAudit4CustomConfigAndTrialRequireCapAndPreserveRequest(t *testing.T) {
	a, _ := testApp(t)
	customAgent(t, a, "old", false)
	d := customDefinition("old")
	req := protocol.SubmitOperation{Action: "check.apply", ClientRequestKey: "cap", TargetIDs: []string{"old"}, Params: map[string]any{"check": asMap(d), "base_revision": float64(1)}}
	w := httptest.NewRecorder()
	a.processSubmit(w, ownerRecent(t), req)
	if w.Code != 409 {
		t.Fatalf("old worker accepted custom fields %d %s", w.Code, w.Body.String())
	}
	var count int
	a.Store.DB.QueryRow(`SELECT COUNT(*) FROM operations WHERE client_request_key='cap'`).Scan(&count)
	if count != 0 {
		t.Fatal("bad request persisted")
	}
	customAgent(t, a, "new", true)
	d = customDefinition("new")
	req.ClientRequestKey = "new-cap"
	req.TargetIDs = []string{"new"}
	req.Params["check"] = asMap(d)
	w = httptest.NewRecorder()
	a.processSubmit(w, ownerRecent(t), req)
	if w.Code != 202 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	ag, _ := a.Store.Agent("new")
	var cfg protocol.AgentConfig
	json.Unmarshal([]byte(ag.DesiredConfig), &cfg)
	if len(cfg.Checks) != 1 || cfg.Checks[0].Body != d.Body || cfg.Checks[0].IntervalSeconds != 30 {
		t.Fatalf("definition changed: %+v", cfg)
	}
	if e := a.Store.SetApplied("new", ag.DesiredRevision, ag.DesiredHash); e != nil {
		t.Fatal(e)
	}
	req.Action = "check.trial"
	req.ClientRequestKey = "new-trial"
	req.Params = asMap(d)
	w = httptest.NewRecorder()
	a.processSubmit(w, ownerRecent(t), req)
	if w.Code != 202 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	var queued protocol.Operation
	if e := json.Unmarshal(w.Body.Bytes(), &queued); e != nil {
		t.Fatal(e)
	}
	if len(queued.Targets) != 1 {
		t.Fatalf("targets: %+v", queued.Targets)
	}
	ev := queued.Targets[0].Evidence
	if ev["trial"] == true || ev["transport"] != nil || ev["vantage"] != nil || ev["quality"] != nil || ev["http_status"] != nil {
		t.Fatalf("queued trial fabricated a completed result: %+v", ev)
	}
	ag2, _ := a.Store.Agent("new")
	if ag2.DesiredRevision != ag.DesiredRevision {
		t.Fatal("trial persisted config")
	}
}
func TestAudit4CustomSecretOwnershipAndSensitiveParams(t *testing.T) {
	a, _ := testApp(t)
	customAgent(t, a, "a", true)
	customAgent(t, a, "b", true)
	nonce, ct, _ := secure.Seal(a.Master, []byte("secret"))
	a.Store.SaveSecret("foreign", "token", "Authorization", "b", "", 1, nonce, ct)
	d := customDefinition("a")
	d.SecretID = "foreign"
	d.SecretHeader = "Authorization"
	req := protocol.SubmitOperation{Action: "check.trial", ClientRequestKey: "foreign", TargetIDs: []string{"a"}, Params: asMap(d)}
	w := httptest.NewRecorder()
	a.processSubmit(w, ownerRecent(t), req)
	if w.Code != 409 {
		t.Fatal(w.Code, w.Body.String())
	}
	d.SecretID = ""
	d.SecretHeader = ""
	d.Headers = map[string]string{"Authorization": "private-leak"}
	req.ClientRequestKey = "sensitive"
	req.Params = asMap(d)
	w = httptest.NewRecorder()
	a.processSubmit(w, ownerRecent(t), req)
	if w.Code != 400 {
		t.Fatal(w.Code, w.Body.String())
	}
	var n int
	a.Store.DB.QueryRow(`SELECT COUNT(*) FROM operations WHERE params LIKE '%private-leak%'`).Scan(&n)
	if n != 0 {
		t.Fatal("secret journaled")
	}
}
func TestAudit4AdvicePersistenceAndManualDefinitionWins(t *testing.T) {
	a, _ := testApp(t)
	customAgent(t, a, "a", true)
	d := customDefinition("a")
	d.Method = "GET"
	d.AllowPOST = false
	d.Body = ""
	d.Path = "/readyz"
	d.ExpectHealth = true
	d.TimeoutSeconds = 1
	d.IntervalSeconds = 5
	d.Origin = "auto_detected"
	code := 503
	ep := protocol.DiscoveredEndpoint{ServiceID: "a-s", URL: d.URL, DialTarget: "127.0.0.1:8080", SpeaksHTTP: true, Suggestions: []protocol.CheckSuggestion{{Definition: d, Confidence: "high", AutoEligible: true, HTTPStatus: &code, Health: "false", ObservedAt: time.Now()}}}
	ep.Suggestions[0].Definition.DialTarget = ep.DialTarget
	if e := protocol.ValidateSuggestions(ep); e != nil {
		t.Fatal(e)
	}
	if e := a.Store.UpsertService(ep, "a"); e != nil {
		t.Fatal(e)
	}
	a.ensureBaselineCheck("a", ep)
	ag, _ := a.Store.Agent("a")
	var cfg protocol.AgentConfig
	json.Unmarshal([]byte(ag.DesiredConfig), &cfg)
	if len(cfg.Checks) != 1 || cfg.Checks[0].Path != "/readyz" || !cfg.Checks[0].ExpectHealth {
		t.Fatal("healthy liveness was substituted or advice lost")
	}
	cfg.Checks[0].Path = "/my/custom"
	b, _ := json.Marshal(cfg)
	a.Store.SetDesired("a", ag.DesiredRevision+1, secure.SHA256Bytes(b), string(b))
	a.ensureBaselineCheck("a", ep)
	ag, _ = a.Store.Agent("a")
	if !strings.Contains(ag.DesiredConfig, "/my/custom") {
		t.Fatal("manual check overwritten")
	}
	saved, e := a.Store.ServiceDiscovery("a-s")
	if e != nil || saved.Suggestions[0].Definition.ServiceID != "a-s" {
		t.Fatal("advice not persisted with canonical ID")
	}
}
func TestAudit4MissingMasterWithEncryptedDataFailsWithoutReplacement(t *testing.T) {
	dir := t.TempDir()
	a, e := Open(Config{DataDir: dir})
	if e != nil {
		t.Fatal(e)
	}
	n, ct, _ := secure.Seal(a.Master, []byte("hello"))
	a.Store.SaveSecret("s", "secret", "Authorization", "a", "", 1, n, ct)
	a.Close()
	p := filepath.Join(dir, "secret-master.key")
	os.Remove(p)
	if restored, e := Open(Config{DataDir: dir}); e == nil {
		restored.Close()
		t.Fatal("new master created over existing ciphertext")
	}
	if _, e := os.Stat(p); !os.IsNotExist(e) {
		t.Fatal("missing key replaced")
	}
}
