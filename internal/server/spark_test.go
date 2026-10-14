package server

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
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

type sparkAPIFixture struct {
	app                     *App
	handler                 http.Handler
	owner, viewer           *storage.Session
	ownerToken, viewerToken string
	report                  protocol.AgentReport
	clock                   *clock.Fake
}

func newSparkAPI(t *testing.T, fake bool) *sparkAPIFixture {
	t.Helper()
	a, h := testApp(t)
	f := &sparkAPIFixture{app: a, handler: h}
	if fake {
		f.clock = clock.NewFake(a.Clock.Now())
		a.Clock = f.clock
		a.Store.Clock = f.clock
	}
	u, e := a.Store.UserByName("owner")
	if e != nil {
		t.Fatal(e)
	}
	f.ownerToken, f.owner, e = a.Store.CreateSession(u, time.Hour, 10*time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	v, e := a.Store.CreateUser("viewer", "hash", "viewer")
	if e != nil {
		t.Fatal(e)
	}
	f.viewerToken, f.viewer, e = a.Store.CreateSession(v, time.Hour, 10*time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	if e = a.Store.InsertAgent(&storage.AgentRow{ID: "spark", Hostname: "fixture", DesiredConfig: "{}", DesiredRevision: 1, DesiredHash: "h"}, secure.HashToken("credential")); e != nil {
		t.Fatal(e)
	}
	now := a.Clock.Now()
	rev := "sha256:" + strings.Repeat("1", 64)
	epoch := "epoch"
	f.report = protocol.AgentReport{AgentID: "spark", AgentFlavor: "spark-light", SchemaVersion: 3, SessionID: "session", Sequence: 1, ObservedAt: now, IsLive: true, Capabilities: map[string]protocol.Capability{"spark_control_v1": {Status: protocol.CapSupported}, "spark_logs_v1": {Status: protocol.CapSupported}, "spark_hardware_v1": {Status: protocol.CapSupported, Reason: "synthetic GB10 fixture"}}, Host: &protocol.HostMetrics{Hostname: "fixture", OS: "linux", Arch: "arm64", RAMTotal: 100, RAMUsed: 99, RAMAvailable: 1}, Spark: &protocol.SparkExtensionV1{SchemaVersion: 1, MemoryTopology: "unified", Metrics: []protocol.SparkMetric{}, Inventory: &protocol.SparkInventory{Revision: rev, Complete: true, ObservedAt: now, Profiles: []protocol.SparkProfile{{ProfileID: "p", DisplayName: "fixture", ProfileRevision: rev, Engine: "custom", Supervisor: "systemd-user", ControlAllowed: true, LogAllowed: true, ConfiguredModelRef: "private model detail", Settings: []protocol.SparkSetting{}, AllowedActions: []string{"start", "stop", "restart"}}}}, ModelStates: []protocol.SparkModelState{{ProfileID: "p", Generation: 1, ObservedAt: now, SupervisorState: "starting", Readiness: "unknown", Quality: "ok"}}, ControlState: &protocol.SparkControlState{Enabled: true, Epoch: &epoch, ObservedAt: now}}}
	if _, e = a.Store.AcceptReport(f.report); e != nil {
		t.Fatal(e)
	}
	return f
}
func (f *sparkAPIFixture) request(t *testing.T, method, path string, body any, role string) *httptest.ResponseRecorder {
	t.Helper()
	var b []byte
	switch v := body.(type) {
	case string:
		b = []byte(v)
	case nil:
	default:
		var e error
		b, e = json.Marshal(v)
		if e != nil {
			t.Fatal(e)
		}
	}
	r := httptest.NewRequest(method, path, bytes.NewReader(b))
	r.Header.Set("Content-Type", "application/json")
	switch role {
	case "owner":
		r.AddCookie(&http.Cookie{Name: "monik_session", Value: f.ownerToken})
		r.Header.Set("X-CSRF-Token", f.owner.CSRF)
	case "viewer":
		r.AddCookie(&http.Cookie{Name: "monik_session", Value: f.viewerToken})
		r.Header.Set("X-CSRF-Token", f.viewer.CSRF)
	case "agent":
		r.Header.Set("Authorization", "Bearer credential")
		r.Header.Set("X-Monik-Agent-Id", "spark")
	}
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	return w
}
func (f *sparkAPIFixture) params() map[string]any {
	return map[string]any{"profile_id": "p", "profile_revision": f.report.Spark.Inventory.Profiles[0].ProfileRevision, "expected_generation": 1, "control_epoch": "epoch", "expected_agent_session": "session", "confirm_disruption": true}
}
func (f *sparkAPIFixture) logParams() map[string]any {
	p := f.params()
	delete(p, "confirm_disruption")
	return p
}

func TestSparkOwnerOnlineTypedDuplicateAndStopDuringStart(t *testing.T) {
	f := newSparkAPI(t, true)
	req := map[string]any{"action": "model.stop", "client_request_key": "key", "target_ids": []string{"spark"}, "params": f.params()}
	w := f.request(t, "POST", "/api/v1/operations", req, "owner")
	if w.Code != 202 {
		t.Fatalf("stop under low memory while starting %d %s", w.Code, w.Body.String())
	}
	var op protocol.Operation
	if e := json.Unmarshal(w.Body.Bytes(), &op); e != nil {
		t.Fatal(e)
	}
	if op.Deadline == nil || op.Deadline.Sub(op.CreatedAt) != 30*time.Second {
		t.Fatal("generic offline lease used")
	}
	jobs, e := f.app.Store.PendingJobs("spark", 8)
	if e != nil || len(jobs) != 1 || jobs[0].Action != "model.stop" {
		t.Fatal("model job not published", e)
	}
	f.clock.Advance(16 * time.Second)
	w = f.request(t, "POST", "/api/v1/operations", req, "owner")
	if w.Code != 202 || !strings.Contains(w.Body.String(), op.ID) {
		t.Fatalf("lost response duplicate blocked by stale state: %d %s", w.Code, w.Body.String())
	}
	req["client_request_key"] = "new-key"
	w = f.request(t, "POST", "/api/v1/operations", req, "owner")
	if w.Code != 409 {
		t.Fatal("offline dispatch queued", w.Code)
	}
	req["client_request_key"] = "key"
	req["action"] = "model.start"
	if w = f.request(t, "POST", "/api/v1/operations", req, "owner"); w.Code != 409 || !strings.Contains(w.Body.String(), "idempotency_conflict") {
		t.Fatal("changed request key did not conflict", w.Code)
	}
	if w = f.request(t, "POST", "/api/v1/operations", req, "viewer"); w.Code != 403 {
		t.Fatal("viewer model mutation allowed", w.Code)
	}
}
func TestSparkStrictModelRESTAndReadOnlyProjection(t *testing.T) {
	f := newSparkAPI(t, false)
	for _, raw := range []string{
		`{"action":"model.start","action":"model.stop","client_request_key":"k","target_ids":["spark"],"params":{}}`,
		`{"action":"model.start","client_request_key":"k","target_ids":["spark"],"params":{},"exec":"anything"}`,
	} {
		if w := f.request(t, "POST", "/api/v1/operations", raw, "owner"); w.Code != 400 {
			t.Fatalf("ambiguous body accepted %d %s", w.Code, w.Body.String())
		}
	}
	req := map[string]any{"action": "model.start", "client_request_key": "k", "target_mode": "all", "target_ids": []string{"spark"}, "params": f.params()}
	if w := f.request(t, "POST", "/api/v1/operations", req, "owner"); w.Code != 400 {
		t.Fatal("bulk action accepted")
	}
	p := f.params()
	p["unit"] = "browser-unit"
	req["target_mode"] = "selected"
	req["params"] = p
	if w := f.request(t, "POST", "/api/v1/operations", req, "owner"); w.Code != 400 {
		t.Fatal("browser effect selector accepted")
	}
	w := f.request(t, "GET", "/api/v1/sparks", nil, "viewer")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"agent_id":"spark"`) {
		t.Fatal("viewer summary missing", w.Code)
	}
	for _, private := range []string{"profile_revision", "control_epoch", "private model detail", "control_state"} {
		if strings.Contains(w.Body.String(), private) {
			t.Fatal("sensitive summary projection leaked", private)
		}
	}
	if w = f.request(t, "GET", "/api/v1/sparks/spark", nil, "viewer"); w.Code == 200 {
		t.Fatal("viewer detail allowed")
	}
	w = f.request(t, "GET", "/api/v1/sparks/spark", nil, "owner")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"operations":[]`) {
		t.Fatal("owner coherent detail missing", w.Code, w.Body.String())
	}
	w = f.request(t, "POST", "/api/v1/agent/report", f.report, "agent")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"spark_telemetry_v1":1`) {
		t.Fatal("authenticated feature negotiation absent", w.Code, w.Body.String())
	}
	if w = f.request(t, "GET", "/api/v1/agents", nil, "viewer"); !strings.Contains(w.Body.String(), `"agent_flavor":"spark-light"`) {
		t.Fatal("same global identity absent")
	}
}
func TestSparkLogSharingBoundsAndNoPersistence(t *testing.T) {
	f := newSparkAPI(t, false)
	w := f.request(t, "POST", "/api/v1/sparks/spark/logs", f.logParams(), "owner")
	if w.Code != 201 {
		t.Fatalf("log create %d %s", w.Code, w.Body.String())
	}
	var created struct {
		StreamID string `json:"stream_id"`
	}
	json.Unmarshal(w.Body.Bytes(), &created)
	same := f.request(t, "POST", "/api/v1/sparks/spark/logs", f.logParams(), "owner")
	if same.Code != 200 || !strings.Contains(same.Body.String(), created.StreamID) {
		t.Fatal("shared source multiplied")
	}
	w = f.request(t, "GET", "/api/v1/agent/spark/logs", nil, "agent")
	var leases struct {
		Leases []protocol.SparkLogLease `json:"leases"`
	}
	if e := json.Unmarshal(w.Body.Bytes(), &leases); e != nil || len(leases.Leases) != 1 {
		t.Fatalf("leases %s %v", w.Body.String(), e)
	}
	batch := protocol.SparkLogBatch{StreamID: created.StreamID, AgentSession: "session", Records: []protocol.SparkLogRecord{{Sequence: 1, At: time.Now(), Text: ""}, {Sequence: 2, At: time.Now(), Text: "[filtered fixture]"}}}
	if w = f.request(t, "POST", "/api/v1/agent/spark/logs", batch, "agent"); w.Code != 200 {
		t.Fatal("empty complete record rejected", w.Code, w.Body.String())
	}
	batch.Records = []protocol.SparkLogRecord{{Sequence: 3, At: time.Now(), Text: strings.Repeat("x", (16<<10)+1)}}
	if w = f.request(t, "POST", "/api/v1/agent/spark/logs", batch, "agent"); w.Code != 400 {
		t.Fatal("oversized line accepted")
	}
	batch.Records[0].Text = "secret\x1b[1m"
	if w = f.request(t, "POST", "/api/v1/agent/spark/logs", batch, "agent"); w.Code != 400 {
		t.Fatal("unfiltered control bytes accepted")
	}
	var count int
	if e := f.app.Store.DB.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name LIKE '%log%' AND type='table' AND sql LIKE '%spark%'`).Scan(&count); e != nil {
		t.Fatal(e)
	}
	if count != 0 {
		t.Fatal("raw log persistence introduced")
	}
	if w = f.request(t, "POST", "/api/v1/sparks/spark/logs", f.logParams(), "viewer"); w.Code != 403 {
		t.Fatal("viewer log access allowed")
	}
	if e := f.app.Store.DeleteSession(f.owner.ID); e != nil {
		t.Fatal(e)
	}
	w = f.request(t, "GET", "/api/v1/agent/spark/logs", nil, "agent")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"leases":[]`) {
		t.Fatal("revoked upstream lease kept alive", w.Code, w.Body.String())
	}
}
func TestSparkLogLiveSessionRevocation(t *testing.T) {
	f := newSparkAPI(t, false)
	w := f.request(t, "POST", "/api/v1/sparks/spark/logs", f.logParams(), "owner")
	var created struct {
		StreamID string `json:"stream_id"`
	}
	json.Unmarshal(w.Body.Bytes(), &created)
	f.request(t, "GET", "/api/v1/agent/spark/logs", nil, "agent")
	f.request(t, "POST", "/api/v1/agent/spark/logs", protocol.SparkLogBatch{StreamID: created.StreamID, AgentSession: "session", Records: []protocol.SparkLogRecord{{Sequence: 1, At: time.Now(), Text: "safe fixture"}}}, "agent")
	ts := httptest.NewServer(f.handler)
	defer ts.Close()
	r, _ := http.NewRequest("GET", ts.URL+"/api/v1/sparks/spark/logs/"+created.StreamID+"/events", nil)
	r.AddCookie(&http.Cookie{Name: "monik_session", Value: f.ownerToken})
	client := &http.Client{Timeout: 5 * time.Second}
	res, e := client.Do(r)
	if e != nil {
		t.Fatal(e)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		b, _ := io.ReadAll(res.Body)
		t.Fatal(res.StatusCode, string(b))
	}
	reader := bufio.NewReader(res.Body)
	first, e := reader.ReadString('\n')
	if e != nil || first != "event: log\n" {
		t.Fatal("initial filtered tail missing", first, e)
	}
	if e = f.app.Store.DeleteSession(f.owner.ID); e != nil {
		t.Fatal(e)
	}
	remaining, e := io.ReadAll(reader)
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(string(remaining), "authority_revoked") {
		t.Fatal("revocation did not terminate SSE", string(remaining))
	}
}
func TestSparkFlavorArtifactSelection(t *testing.T) {
	arts := []map[string]any{{"os": "linux", "arch": "arm64", "name": "spark-light/linux-arm64/monik-agent", "agent_flavor": "spark-light"}, {"os": "linux", "arch": "arm64", "name": "linux-arm64/monik-agent", "agent_flavor": "general"}, {"os": "linux", "arch": "amd64", "name": "spark-light/linux-amd64/monik-agent", "agent_flavor": "unsupported"}}
	if got := matchArtifact(arts, "linux", "arm64"); got == nil || got["agent_flavor"] != "general" {
		t.Fatal("general selected Spark artifact")
	}
	if got := matchArtifactFlavor(arts, "linux", "arm64", "spark-light"); got == nil || got["agent_flavor"] != "spark-light" {
		t.Fatal("Spark selected general artifact")
	}
	if matchArtifactFlavor(arts, "linux", "amd64", "spark-light") != nil {
		t.Fatal("wrong architecture selected")
	}
}

func TestSparkViewerOperationCannotReadControlBindings(t *testing.T) {
	f := newSparkAPI(t, false)
	op := &protocol.Operation{Action: "model.stop", Params: f.params(), Targets: []protocol.TargetResult{{AgentID: "spark", Message: "private supervisor detail", Evidence: map[string]any{"control_epoch": "epoch"}}}}
	v := operationView(op, f.viewer)
	b, _ := json.Marshal(v)
	if strings.Contains(string(b), "epoch") || strings.Contains(string(b), "private supervisor detail") {
		t.Fatal("viewer control projection leaked", string(b))
	}
	if op.Params["control_epoch"] != "epoch" || op.Targets[0].Message != "private supervisor detail" {
		t.Fatal("redaction mutated durable result")
	}
}

func TestSparkHistoricalOrOldSequenceCannotPollJobs(t *testing.T) {
	f := newSparkAPI(t, true)
	b, _ := json.Marshal(f.report)
	var old protocol.AgentReport
	if e := json.Unmarshal(b, &old); e != nil {
		t.Fatal(e)
	}
	f.clock.Advance(time.Second)
	f.report.Sequence++
	f.report.ObservedAt = f.clock.Now()
	f.report.Spark.ControlState.ObservedAt = f.clock.Now()
	f.report.Spark.ModelStates[0].ObservedAt = f.clock.Now()
	if _, e := f.app.Store.AcceptReport(f.report); e != nil {
		t.Fatal(e)
	}
	op := &protocol.Operation{ID: "poll-job", Action: "agent.collect_now", ClientRequestKey: "poll", Actor: "owner", Revision: 1, Status: protocol.OpQueued, CreatedAt: f.clock.Now(), Targets: []protocol.TargetResult{{AgentID: "spark", Status: protocol.TargetQueued}}}
	if e := f.app.Store.InsertOperation(op, "h"); e != nil {
		t.Fatal(e)
	}
	w := f.request(t, "POST", "/api/v1/agent/report", old, "agent")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var control protocol.ControlResponse
	if e := json.Unmarshal(w.Body.Bytes(), &control); e != nil || len(control.Jobs) != 0 || control.DesiredConfig != nil {
		t.Fatal("old live sequence delivered control", w.Body.String(), e)
	}
	w = f.request(t, "POST", "/api/v1/agent/report", f.report, "agent")
	if e := json.Unmarshal(w.Body.Bytes(), &control); e != nil || len(control.Jobs) != 1 {
		t.Fatal("current duplicate lost control response", w.Body.String(), e)
	}
}

func TestSparkClassificationNeedsEvidenceAndNeverGrantsControl(t *testing.T) {
	f := newSparkAPI(t, false)
	if _, e := f.app.Store.DB.Exec(`DELETE FROM spark_hardware_evidence`); e != nil {
		t.Fatal(e)
	}
	w := f.request(t, "GET", "/api/v1/sparks", nil, "viewer")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"sparks":[]`) {
		t.Fatal("ARM64/flavor alone classified hardware", w.Body.String())
	}
	if e := f.app.Store.InsertAgent(&storage.AgentRow{ID: "ordinary", Hostname: "ordinary", Arch: "arm64", DesiredConfig: "{}", DesiredRevision: 1, DesiredHash: "h"}, "hash"); e != nil {
		t.Fatal(e)
	}
	if _, e := f.app.Store.AcceptReport(protocol.AgentReport{AgentID: "ordinary", SessionID: "basic", Sequence: 1, SchemaVersion: 3, ObservedAt: f.app.Clock.Now(), IsLive: true, Host: &protocol.HostMetrics{Hostname: "ordinary", OS: "linux", Arch: "arm64"}, Capabilities: map[string]protocol.Capability{}}); e != nil {
		t.Fatal(e)
	}
	w = f.request(t, "POST", "/api/v1/sparks/ordinary/classification", map[string]any{"classification": "spark"}, "owner")
	if w.Code != 200 {
		t.Fatal("owner correction failed", w.Code, w.Body.String())
	}
	w = f.request(t, "GET", "/api/v1/sparks", nil, "viewer")
	if !strings.Contains(w.Body.String(), `"agent_id":"ordinary"`) {
		t.Fatal("owner corrected old agent not visible", w.Body.String())
	}
	req := map[string]any{"action": "model.start", "client_request_key": "label-only", "target_ids": []string{"ordinary"}, "params": f.params()}
	if w = f.request(t, "POST", "/api/v1/operations", req, "owner"); w.Code != 409 {
		t.Fatal("label granted model authority", w.Code, w.Body.String())
	}
}

func TestSparkIdentityFeatureCompatibilityIsNotAuthority(t *testing.T) {
	f := newSparkAPI(t, false)
	w := f.request(t, "GET", "/api/v1/agent/identity", nil, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"spark_telemetry_v1":1`) {
		t.Fatal("pre-enrollment feature metadata missing", w.Code, w.Body.String())
	}
	if w = f.request(t, "GET", "/api/v1/agent/spark/logs", nil, ""); w.Code != 401 {
		t.Fatal("public metadata granted agent authority", w.Code)
	}
	if w = f.request(t, "POST", "/api/v1/operations", map[string]any{"action": "model.start", "client_request_key": "identity", "target_ids": []string{"spark"}, "params": f.params()}, ""); w.Code != 401 {
		t.Fatal("public metadata granted browser authority", w.Code)
	}
}
