package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
	"github.com/alinescafs3mp-afk/monik_v2/internal/secure"
	"github.com/alinescafs3mp-afk/monik_v2/internal/storage"
)

func TestAudit3ConfigPublicationCanBeConfirmed(t *testing.T) {
	a, _ := testApp(t)
	cfg := protocol.DefaultAgentConfig()
	raw, _ := json.Marshal(cfg)
	row := &storage.AgentRow{ID: "revision-agent", DesiredRevision: 1, DesiredConfig: string(raw), DesiredHash: secure.SHA256Bytes(raw)}
	if err := a.Store.InsertAgent(row, "test"); err != nil {
		t.Fatal(err)
	}
	if err := a.Store.SetDesired(row.ID, 1, row.DesiredHash, row.DesiredConfig); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	a.processSubmit(w, &storage.Session{Username: "owner", Role: "owner"}, protocol.SubmitOperation{Action: "profile.apply", ClientRequestKey: "config-test", TargetIDs: []string{row.ID}, Params: map[string]any{"paused": true}})
	if w.Code != 202 {
		t.Fatalf("submit: %d %s", w.Code, w.Body.String())
	}
	current, err := a.Store.Agent(row.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.Store.SetApplied(row.ID, current.DesiredRevision, current.DesiredHash); err != nil {
		t.Fatalf("committed config has no verifiable revision: %v", err)
	}
}

func TestAudit3EnrollmentConflictDoesNotConsumeCode(t *testing.T) {
	a, _ := testApp(t)
	if err := a.Store.InsertAgent(&storage.AgentRow{ID: "already-exists", DesiredRevision: 1}, "test"); err != nil {
		t.Fatal(err)
	}
	code, _, err := a.Store.CreateEnrollmentCode("owner", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	enroll := func(id string) int {
		b, _ := json.Marshal(protocol.EnrollRequest{Code: code, AgentID: id, OS: "linux", Arch: "amd64"})
		w := httptest.NewRecorder()
		a.handleEnroll(w, httptest.NewRequest("POST", "/api/v1/agent/enroll", bytes.NewReader(b)))
		return w.Code
	}
	if n := enroll("already-exists"); n != 409 {
		t.Fatalf("collision: %d", n)
	}
	if n := enroll("new-agent"); n != 200 {
		t.Fatalf("identity collision spent one-use code: %d", n)
	}
}

func TestAudit3EnrollmentRetryUsesSameIdentityAndProof(t *testing.T) {
	a, _ := testApp(t)
	code, _, err := a.Store.CreateEnrollmentCode("owner", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	credential := strings.Repeat("ab", 32)
	b, _ := json.Marshal(map[string]any{"code": code, "agent_id": "retry-agent", "os": "linux", "arch": "amd64", "credential": credential})
	for attempt := 0; attempt < 2; attempt++ {
		w := httptest.NewRecorder()
		a.handleEnroll(w, httptest.NewRequest("POST", "/api/v1/agent/enroll", bytes.NewReader(b)))
		if w.Code != 200 {
			t.Fatalf("same proven retry %d: %d %s", attempt, w.Code, w.Body.String())
		}
		var resp protocol.EnrollResponse
		if err = json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		if resp.Credential != credential {
			t.Fatal("client's persisted proof was replaced; lost response cannot be recovered")
		}
	}
}

func TestAudit3StrictRequestRejectsTrailingJSON(t *testing.T) {
	a, _ := testApp(t)
	w := httptest.NewRecorder()
	a.handleSubmitOp(w, httptest.NewRequest("POST", "/api/v1/operations", strings.NewReader(`{"action":"preference.save","client_request_key":"trailing","params":{"id":"test"}} {"unexpected":true}`)), &storage.Session{Username: "owner", Role: "owner"})
	if w.Code != 400 {
		t.Fatalf("trailing JSON accepted: %d %s", w.Code, w.Body.String())
	}
}

func TestAudit3ExportContainsHistoryAndAuthenticatedDownload(t *testing.T) {
	a, _ := testApp(t)
	now := time.Now().UTC().Truncate(time.Second)
	cpu := 42.0
	if err := a.Store.InsertAgent(&storage.AgentRow{ID: "export-agent", DesiredRevision: 1}, "test"); err != nil {
		t.Fatal(err)
	}
	if err := a.Store.InsertHostSample("export-agent", 1, "s", now, &protocol.HostMetrics{CPUPercent: &cpu}); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	a.processSubmit(w, &storage.Session{Username: "owner", Role: "owner"}, protocol.SubmitOperation{Action: "history.export", ClientRequestKey: "export-test", Params: map[string]any{"agent_id": "export-agent", "from": now.Add(-time.Second).Format(time.RFC3339Nano), "to": now.Add(time.Second).Format(time.RFC3339Nano)}})
	var op protocol.Operation
	if err := json.Unmarshal(w.Body.Bytes(), &op); err != nil {
		t.Fatal(err)
	}
	if op.Status != protocol.OpCompleted || len(op.Targets) != 1 {
		t.Fatalf("export not completed: %d %s", w.Code, w.Body.String())
	}
	r := httptest.NewRequest("GET", "/api/v1/exports/"+op.ID, nil)
	r.SetPathValue("id", op.ID)
	w = httptest.NewRecorder()
	a.handleExportDownload(w, r, &storage.Session{Username: "other", Role: "viewer"})
	if w.Code != 403 {
		t.Fatal("foreign export exposed")
	}
	w = httptest.NewRecorder()
	a.handleExportDownload(w, r, &storage.Session{Username: "owner", Role: "owner"})
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var file struct {
		Samples []struct {
			Payload protocol.HostMetrics `json:"payload"`
		} `json:"samples"`
		Count int `json:"count"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &file); err != nil || file.Count != 1 || file.Samples[0].Payload.CPUPercent == nil || *file.Samples[0].Payload.CPUPercent != 42 {
		t.Fatalf("wrong history export: %s", w.Body.String())
	}
	if strings.Contains(w.Body.String(), "open_incidents") {
		t.Fatal("export substituted current incidents")
	}
}

func TestAudit3ServerOnlyOperationStartsUnconfirmed(t *testing.T) {
	a, _ := testApp(t)
	// A trigger observes the initially inserted target, before server-side effect.
	if _, err := a.Store.DB.Exec(`CREATE TRIGGER no_premature_success BEFORE INSERT ON operation_targets WHEN NEW.agent_id='server' AND NEW.status='succeeded' BEGIN SELECT RAISE(ABORT,'premature success'); END`); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	a.processSubmit(w, &storage.Session{Username: "owner", Role: "owner"}, protocol.SubmitOperation{Action: "preference.save", ClientRequestKey: "initial-stage", Params: map[string]any{"id": "test"}})
	if w.Code != 200 {
		t.Fatalf("server effect skipped actual execution: %d %s", w.Code, w.Body.String())
	}
}
func TestAudit3RateLimitsBoundUntrustedKeyGrowth(t *testing.T) {
	a, _ := testApp(t)
	for i := 0; i < maxRateBuckets; i++ {
		if !a.rateLimit("key-"+fmt.Sprint(i), 1, time.Minute) {
			t.Fatal("unexpected limit before bound")
		}
	}
	if a.rateLimit("overflow", 1, time.Minute) || len(a.limiters) != maxRateBuckets {
		t.Fatal("unbounded limiter cardinality")
	}
	if a.rateLimit("key-0", 1, time.Minute) {
		t.Fatal("full limiter evicted active enforcement")
	}
}
