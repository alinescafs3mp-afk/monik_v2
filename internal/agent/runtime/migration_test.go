package runtime

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"github.com/alinescafs3mp-afk/monik_v2/internal/agent/configfile"
	"github.com/alinescafs3mp-afk/monik_v2/internal/clock"
	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
	"github.com/alinescafs3mp-afk/monik_v2/internal/secure"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func candidateFixture(t *testing.T) (*httptest.Server, *bool) {
	t.Helper()
	commit := true
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/agent/identity" {
			json.NewEncoder(w).Encode(map[string]any{"controller_id": "controller"})
			return
		}
		var rep protocol.AgentReport
		if r.Header.Get("Authorization") != "Bearer fixture-token" || json.NewDecoder(r.Body).Decode(&rep) != nil {
			w.WriteHeader(401)
			return
		}
		json.NewEncoder(w).Encode(protocol.ControlResponse{ControllerID: "controller", Ack: &protocol.IngestAck{UpToSequence: rep.Sequence, Committed: commit}})
	}))
	t.Cleanup(ts.Close)
	return ts, &commit
}
func preparedPlan(t *testing.T, a *Agent, ts *httptest.Server) *protocol.MigrationPlan {
	t.Helper()
	p := &protocol.MigrationPlan{PlanID: "plan", ControllerID: "controller", CurrentURL: a.State.File.ControllerURL, CandidateURL: ts.URL, Generation: 2, ExpiresAt: a.Clock.Now().Add(time.Hour), PrimaryLossSeconds: 30, TrustPEM: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ts.Certificate().Raw}))}
	p.PayloadHash = secure.SHA256Bytes([]byte(p.CandidateURL + "|" + p.ControllerID))
	if err := configfile.SaveMigration(a.State.File.StateDir, p); err != nil {
		t.Fatal(err)
	}
	return p
}
func TestReviewRebindRequiresCommittedCandidateExchanges(t *testing.T) {
	a, old := auditWorker(t, func(http.ResponseWriter, *http.Request) {})
	fc := clock.NewFake(time.Now().UTC())
	a.Clock = fc
	ts, commit := candidateFixture(t)
	preparedPlan(t, a, ts)
	job := auditEnvelope("rebind.activate")
	job.Params = map[string]any{"plan_id": "plan"}
	a.handleJob(job)
	if a.jobs["job"].Status != protocol.TargetAwaitingConfirmation || a.State.File.ControllerURL != ts.URL {
		t.Fatalf("trial: %+v", a.jobs["job"])
	}
	a.recordMigrationContact(old.URL)
	*commit = false
	if a.send(context.Background(), protocol.AgentReport{AgentID: "fixture", Sequence: 1, IsLive: true}) == nil {
		t.Fatal("uncommitted report accepted")
	}
	in, _ := loadIntent(a.State.File.StateDir)
	if in.Acknowledgements != 0 {
		t.Fatal("old URL or uncommitted reply counted")
	}
	*commit = true
	for seq := int64(1); seq <= 3; seq++ {
		fc.Advance(5 * time.Second)
		if err := a.send(context.Background(), protocol.AgentReport{AgentID: "fixture", Sequence: seq, IsLive: true}); err != nil {
			t.Fatal(err)
		}
		if seq < 3 && a.jobs["job"].Status == protocol.TargetSucceeded {
			t.Fatal("premature confirmation")
		}
	}
	if a.jobs["job"].Status != protocol.TargetSucceeded || a.jobs["job"].Stage != "rebind.confirmed" {
		t.Fatalf("%+v", a.jobs["job"])
	}
	reopened, err := Open(a.CfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.State.File.ControllerURL != ts.URL || reopened.jobs["job"].Status != protocol.TargetSucceeded {
		t.Fatal("confirmed state not durable")
	}
}
func TestReviewRebindFailureFallsBackAfterRestart(t *testing.T) {
	a, old := auditWorker(t, func(http.ResponseWriter, *http.Request) {})
	fc := clock.NewFake(time.Now().UTC())
	a.Clock = fc
	ts, _ := candidateFixture(t)
	preparedPlan(t, a, ts)
	job := auditEnvelope("rebind.activate")
	job.Params = map[string]any{"plan_id": "plan"}
	a.handleJob(job)
	reopened, err := Open(a.CfgPath)
	if err != nil {
		t.Fatal(err)
	}
	reopened.Clock = fc
	fc.Advance(61 * time.Second)
	reopened.maintainMigration()
	if reopened.State.File.ControllerURL != old.URL || reopened.jobs["job"].Status != protocol.TargetFailed {
		t.Fatalf("no fallback: %s %+v", reopened.State.File.ControllerURL, reopened.jobs["job"])
	}
}
func TestReviewArmedMigrationExecutesWithoutAnotherControllerCommand(t *testing.T) {
	a, _ := auditWorker(t, func(http.ResponseWriter, *http.Request) {})
	fc := clock.NewFake(time.Now().UTC())
	a.Clock = fc
	ts, _ := candidateFixture(t)
	p := preparedPlan(t, a, ts)
	p.ArmFallback = true
	if err := configfile.SaveMigration(a.State.File.StateDir, p); err != nil {
		t.Fatal(err)
	}
	a.lastContact = fc.Now().Add(-31 * time.Second)
	a.maintainMigration()
	if a.State.File.ControllerURL != ts.URL {
		t.Fatal("persisted primary-loss trigger did not start trial")
	}
}
func TestReviewRetireCannotDeleteAnotherOrUnconfirmedPlan(t *testing.T) {
	for _, id := range []string{"other-plan", "plan"} {
		t.Run(id, func(t *testing.T) {
			a, _ := auditWorker(t, func(http.ResponseWriter, *http.Request) {})
			ts, _ := candidateFixture(t)
			preparedPlan(t, a, ts)
			job := auditEnvelope("rebind.retire")
			job.Params = map[string]any{"plan_id": id}
			a.handleJob(job)
			if a.jobs["job"].Status != protocol.TargetRejected {
				t.Fatal("invalid retirement accepted")
			}
			if _, err := configfile.LoadMigration(a.State.File.StateDir); err != nil {
				t.Fatal("plan deleted")
			}
		})
	}
}
func TestReviewMigrationRejectsExpiredReplayAndWrongController(t *testing.T) {
	a, _ := auditWorker(t, func(http.ResponseWriter, *http.Request) {})
	ts, _ := candidateFixture(t)
	p := preparedPlan(t, a, ts)
	p.ExpiresAt = a.Clock.Now().Add(-time.Second)
	if a.validatePlan(p) == nil {
		t.Fatal("expired plan accepted")
	}
	p.ExpiresAt = a.Clock.Now().Add(time.Hour)
	p.ControllerID = "other"
	if a.validatePlan(p) == nil {
		t.Fatal("foreign controller accepted")
	}
	p.ControllerID = "controller"
	a.State.File.EndpointGeneration = 3
	if a.validatePlan(p) == nil {
		t.Fatal("old generation accepted")
	}
}
