package storage

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/clock"
	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
)

func sparkStorageFixture(t *testing.T) (*Store, *clock.Fake, *Session, protocol.AgentReport) {
	t.Helper()
	c := clock.NewFake(time.Now().UTC())
	s, e := Open(filepath.Join(t.TempDir(), "s.db"), c)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	auditAgent(t, s, "spark")
	u, e := s.CreateUser("owner", "hash", "owner")
	if e != nil {
		t.Fatal(e)
	}
	_, owner, e := s.CreateSession(u, time.Hour, 10*time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	r := auditReport("spark", c.Now())
	r.AgentFlavor = "spark-light"
	r.Capabilities = map[string]protocol.Capability{"spark_control_v1": {Status: protocol.CapSupported}, "spark_hardware_v1": {Status: protocol.CapSupported, Reason: "synthetic confirmed GB10 fixture"}}
	epoch := "epoch"
	rev := "sha256:" + strings.Repeat("1", 64)
	r.Spark = &protocol.SparkExtensionV1{SchemaVersion: 1, MemoryTopology: "unified", Metrics: []protocol.SparkMetric{}, Inventory: &protocol.SparkInventory{Revision: rev, Complete: true, ObservedAt: c.Now(), Profiles: []protocol.SparkProfile{{ProfileID: "p", DisplayName: "fixture", ProfileRevision: rev, Engine: "custom", Supervisor: "systemd-user", ControlAllowed: true, LogAllowed: true, Settings: []protocol.SparkSetting{}, AllowedActions: []string{"start", "stop", "restart"}}}}, ModelStates: []protocol.SparkModelState{{ProfileID: "p", Generation: 1, ObservedAt: c.Now(), SupervisorState: "running", Readiness: "unknown", Quality: "ok"}}, ControlState: &protocol.SparkControlState{Enabled: true, Epoch: &epoch, ObservedAt: c.Now()}}
	return s, c, owner, r
}
func copySparkReport(t *testing.T, r protocol.AgentReport) protocol.AgentReport {
	t.Helper()
	b, e := json.Marshal(r)
	if e != nil {
		t.Fatal(e)
	}
	var out protocol.AgentReport
	if e = json.Unmarshal(b, &out); e != nil {
		t.Fatal(e)
	}
	return out
}
func refreshSparkReport(r *protocol.AgentReport, c *clock.Fake) {
	r.Sequence++
	r.ObservedAt = c.Now()
	r.Spark.ControlState.ObservedAt = c.Now()
	for i := range r.Spark.ModelStates {
		r.Spark.ModelStates[i].ObservedAt = c.Now()
	}
}
func modelParams(r protocol.AgentReport) map[string]any {
	return map[string]any{"profile_id": "p", "profile_revision": r.Spark.Inventory.Profiles[0].ProfileRevision, "expected_generation": float64(r.Spark.ModelStates[0].Generation), "control_epoch": *r.Spark.ControlState.Epoch, "expected_agent_session": r.SessionID, "confirm_disruption": true}
}

func TestSparkTransactionalHashBackfillInventoryAndRetention(t *testing.T) {
	s, c, _, r := sparkStorageFixture(t)
	if _, e := s.AcceptReport(r); e != nil {
		t.Fatal(e)
	}
	mut := copySparkReport(t, r)
	mut.Spark.MemoryTopology = "dedicated"
	if _, e := s.AcceptReport(mut); !errors.Is(e, ErrConflict) {
		t.Fatalf("changed extension accepted: %v", e)
	}
	c.Advance(time.Second)
	later := copySparkReport(t, r)
	refreshSparkReport(&later, c)
	later.Spark.ModelStates[0].Generation = 2
	later.Spark.ModelStates[0].SupervisorState = "stopped"
	later.Spark.Inventory.ObservedAt = c.Now()
	later.Spark.Inventory.Complete = false
	later.Spark.Inventory.Profiles = []protocol.SparkProfile{}
	if _, e := s.AcceptReport(later); e != nil {
		t.Fatal(e)
	}
	v, e := s.SparkSnapshot("spark")
	if e != nil || len(v.Spark.Inventory.Profiles) != 1 || v.Spark.ModelStates[0].Generation != 2 {
		t.Fatalf("incomplete removed stopped profile: %+v %v", v, e)
	}
	back := copySparkReport(t, r)
	back.SessionID = "old"
	back.IsLive = false
	back.Spark.ModelStates[0].Readiness = "ready"
	if _, e = s.AcceptReport(back); e != nil {
		t.Fatal(e)
	}
	v, _ = s.SparkSnapshot("spark")
	if v.AgentSession != "s" || v.Spark.ModelStates[0].Generation != 2 || v.Spark.ModelStates[0].Readiness == "ready" {
		t.Fatal("backfill refreshed current authority")
	}
	legacy := copySparkReport(t, later)
	legacy.Sequence++
	legacy.Spark = nil
	legacy.AgentFlavor = ""
	if _, e = s.AcceptReport(legacy); e != nil {
		t.Fatal(e)
	}
	if flavor, _ := s.AgentFlavor("spark"); flavor != "spark-light" {
		t.Fatal("old envelope erased flavor")
	}
	c.Advance(time.Second)
	complete := copySparkReport(t, later)
	complete.Sequence = legacy.Sequence
	refreshSparkReport(&complete, c)
	complete.Spark.Inventory.ObservedAt = c.Now()
	complete.Spark.Inventory.Complete = true
	if _, e = s.AcceptReport(complete); e != nil {
		t.Fatal(e)
	}
	v, _ = s.SparkSnapshot("spark")
	if len(v.Spark.Inventory.Profiles) != 0 || len(v.Spark.ModelStates) != 0 {
		t.Fatal("complete removal not reconciled")
	}
	c.Advance(49 * time.Hour)
	if e = s.RetainRaw(protocol.RawRetention); e != nil {
		t.Fatal(e)
	}
	if n := countRows(t, s, "spark_samples"); n != 0 {
		t.Fatalf("unbounded Spark retention: %d", n)
	}
	if _, e = s.SparkSnapshot("spark"); e != nil {
		t.Fatal("retention erased stable inventory")
	}
}
func TestSparkIngestFailureRollsBackWholeEnvelope(t *testing.T) {
	s, _, _, r := sparkStorageFixture(t)
	if _, e := s.DB.Exec(`CREATE TRIGGER spark_failure BEFORE INSERT ON spark_latest BEGIN SELECT RAISE(ABORT,'fixture failure'); END`); e != nil {
		t.Fatal(e)
	}
	if _, e := s.AcceptReport(r); e == nil {
		t.Fatal("failed latest accepted")
	}
	for _, table := range []string{"host_samples", "spark_samples", "ingest_receipts"} {
		if n := countRows(t, s, table); n != 0 {
			t.Fatalf("partial %s commit: %d", table, n)
		}
	}
}
func TestSparkRestartFreshLaterStageAndCancellation(t *testing.T) {
	for _, cancel := range []bool{false, true} {
		t.Run(map[bool]string{false: "renew", true: "cancel"}[cancel], func(t *testing.T) {
			s, c, owner, r := sparkStorageFixture(t)
			if _, e := s.AcceptReport(r); e != nil {
				t.Fatal(e)
			}
			deadline := c.Now().Add(protocol.SparkDispatchLease)
			op := &protocol.Operation{ID: "op", Action: "model.restart", Actor: owner.Username, ClientRequestKey: "key", Status: protocol.OpQueued, Revision: 1, CreatedAt: c.Now(), Deadline: &deadline, Params: modelParams(r), Targets: []protocol.TargetResult{{AgentID: "spark", Status: protocol.TargetQueued}}}
			if e := s.InsertSparkOperation(op, "hash", owner); e != nil {
				t.Fatal(e)
			}
			jobs, e := s.ClaimPendingJobs("spark", 1)
			if e != nil || len(jobs) != 1 {
				t.Fatalf("jobs %v %v", jobs, e)
			}
			job := jobs[0]
			if e = s.ApplyReceipt("spark", protocol.JobReceipt{JobID: job.JobID, OperationID: "op", Status: protocol.TargetRunning, Stage: "stopping"}); e != nil {
				t.Fatal(e)
			}
			c.Advance(45 * time.Second)
			refreshSparkReport(&r, c)
			r.Spark.ModelStates[0].Generation = 2
			r.Spark.ModelStates[0].SupervisorState = "stopped"
			if _, e = s.AcceptReport(r); e != nil {
				t.Fatal(e)
			}
			evidence := map[string]any{"profile_id": "p", "profile_revision": op.Params["profile_revision"], "control_epoch": "epoch", "agent_session": "s", "generation": float64(2)}
			if e = s.ApplyReceipt("spark", protocol.JobReceipt{JobID: job.JobID, OperationID: "op", Status: protocol.TargetAwaitingConfirmation, Stage: "restart_stopped", Evidence: evidence}); e != nil {
				t.Fatal(e)
			}
			if _, e = s.Operation("op"); e != nil {
				t.Fatal(e)
			}
			if cancel {
				if e = s.CancelPending("op"); e != nil {
					t.Fatal(e)
				}
				if _, e = s.AuthorizeSparkStartStage("spark", "op", job.JobID); e == nil {
					t.Fatal("cancelled later start authorized")
				}
				return
			}
			auth, e := s.AuthorizeSparkStartStage("spark", "op", job.JobID)
			if e != nil {
				t.Fatal(e)
			}
			if !auth.Deadline.After(deadline) || auth.Params.ExpectedGeneration != 2 {
				t.Fatal("original lease reused")
			}
			c.Advance(20 * time.Second)
			same, e := s.AuthorizeSparkStartStage("spark", "op", job.JobID)
			if e != nil || !same.Deadline.Equal(auth.Deadline) {
				t.Fatalf("duplicate stage extended validity: %v", e)
			}
			c.Advance(20 * time.Second)
			if _, e = s.AuthorizeSparkStartStage("spark", "op", job.JobID); e == nil || e.Error() != "stage_authorization_expired" {
				t.Fatalf("expired stage regained dispatch authority: %v", e)
			}
			if e = s.DeleteSession(owner.ID); e != nil {
				t.Fatal(e)
			}
			if _, e = s.AuthorizeSparkStartStage("spark", "op", job.JobID); e == nil {
				t.Fatal("revoked owner stage authorized")
			}
		})
	}
}
func TestSparkHistoricalSuccessNeverRewindsCurrentGeneration(t *testing.T) {
	s, c, owner, r := sparkStorageFixture(t)
	r.Spark.ModelStates[0].SupervisorState = "stopped"
	if _, e := s.AcceptReport(r); e != nil {
		t.Fatal(e)
	}
	deadline := c.Now().Add(protocol.SparkDispatchLease)
	op := &protocol.Operation{ID: "op", Action: "model.start", Actor: owner.Username, ClientRequestKey: "key", Status: protocol.OpQueued, Revision: 1, CreatedAt: c.Now(), Deadline: &deadline, Params: modelParams(r), Targets: []protocol.TargetResult{{AgentID: "spark", Status: protocol.TargetQueued}}}
	if e := s.InsertSparkOperation(op, "hash", owner); e != nil {
		t.Fatal(e)
	}
	jobs, e := s.ClaimPendingJobs("spark", 1)
	if e != nil || len(jobs) != 1 {
		t.Fatal(e)
	}
	c.Advance(time.Second)
	refreshSparkReport(&r, c)
	r.Spark.ModelStates[0].Generation = 2
	r.Spark.ModelStates[0].SupervisorState = "running"
	if _, e = s.AcceptReport(r); e != nil {
		t.Fatal(e)
	}
	c.Advance(time.Second)
	refreshSparkReport(&r, c)
	r.SessionID = "new-session"
	r.Spark.ModelStates[0].Generation = 3
	r.Spark.ModelStates[0].SupervisorState = "stopped"
	if _, e = s.AcceptReport(r); e != nil {
		t.Fatal(e)
	}
	rec := protocol.JobReceipt{JobID: jobs[0].JobID, OperationID: "op", Status: protocol.TargetSucceeded, Stage: "model_started", Evidence: map[string]any{"profile_id": "p", "profile_revision": op.Params["profile_revision"], "control_epoch": "epoch", "agent_session": "s", "generation": float64(2)}}
	if e = s.ApplyReceipt("spark", rec); e != nil {
		t.Fatal(e)
	}
	got, e := s.Operation("op")
	if e != nil || got.Targets[0].Status != protocol.TargetSucceeded {
		t.Fatalf("historical evidence rejected: %+v %v", got, e)
	}
	v, _ := s.SparkSnapshot("spark")
	if v.AgentSession != "new-session" || v.Spark.ModelStates[0].Generation != 3 {
		t.Fatal("historical proof rewound live state")
	}
}

func TestSparkUnconfirmedReceiptReconcilesWithoutNewEffect(t *testing.T) {
	s, c, owner, r := sparkStorageFixture(t)
	r.Spark.ModelStates[0].SupervisorState = "stopped"
	if _, e := s.AcceptReport(r); e != nil {
		t.Fatal(e)
	}
	deadline := c.Now().Add(protocol.SparkDispatchLease)
	op := &protocol.Operation{ID: "op", Action: "model.start", Actor: owner.Username, ClientRequestKey: "key", Status: protocol.OpQueued, Revision: 1, CreatedAt: c.Now(), Deadline: &deadline, Params: modelParams(r), Targets: []protocol.TargetResult{{AgentID: "spark", Status: protocol.TargetQueued}}}
	if e := s.InsertSparkOperation(op, "hash", owner); e != nil {
		t.Fatal(e)
	}
	jobs, e := s.ClaimPendingJobs("spark", 1)
	if e != nil || len(jobs) != 1 {
		t.Fatal(e)
	}
	rec := protocol.JobReceipt{JobID: jobs[0].JobID, OperationID: "op", Status: protocol.TargetSucceeded, Stage: "model_started", Evidence: map[string]any{"profile_id": "p", "profile_revision": op.Params["profile_revision"], "control_epoch": "epoch", "agent_session": "s", "generation": float64(2)}}
	if e = s.ApplyReceipt("spark", rec); e != nil {
		t.Fatal(e)
	}
	got, _ := s.Operation("op")
	if got.Targets[0].Status != protocol.TargetUnknownResult {
		t.Fatal("unverified effect declared success or final rejection")
	}
	c.Advance(time.Second)
	refreshSparkReport(&r, c)
	r.Spark.ModelStates[0].Generation = 2
	r.Spark.ModelStates[0].SupervisorState = "running"
	if _, e = s.AcceptReport(r); e != nil {
		t.Fatal(e)
	}
	if e = s.ApplyReceipt("spark", rec); e != nil {
		t.Fatal(e)
	}
	got, _ = s.Operation("op")
	if got.Targets[0].Status != protocol.TargetSucceeded {
		t.Fatal("same original receipt not reconciled")
	}
	if countRows(t, s, "agent_jobs") != 1 {
		t.Fatal("receipt reconciliation created another effect")
	}
}
func TestSparkOwnerRevocationAndSessionChangePreventUnexecutedDispatch(t *testing.T) {
	for _, change := range []string{"owner", "agent"} {
		t.Run(change, func(t *testing.T) {
			s, c, owner, r := sparkStorageFixture(t)
			if _, e := s.AcceptReport(r); e != nil {
				t.Fatal(e)
			}
			deadline := c.Now().Add(protocol.SparkDispatchLease)
			op := &protocol.Operation{ID: "op", Action: "model.start", Actor: owner.Username, ClientRequestKey: "key", Status: protocol.OpQueued, Revision: 1, CreatedAt: c.Now(), Deadline: &deadline, Params: modelParams(r), Targets: []protocol.TargetResult{{AgentID: "spark", Status: protocol.TargetQueued}}}
			if e := s.InsertSparkOperation(op, "hash", owner); e != nil {
				t.Fatal(e)
			}
			if change == "owner" {
				if e := s.DeleteSession(owner.ID); e != nil {
					t.Fatal(e)
				}
			} else {
				c.Advance(time.Second)
				refreshSparkReport(&r, c)
				r.SessionID = "changed"
				if _, e := s.AcceptReport(r); e != nil {
					t.Fatal(e)
				}
			}
			jobs, e := s.ClaimPendingJobs("spark", 8)
			if e != nil || len(jobs) != 0 {
				t.Fatal("lost authority dispatched", jobs, e)
			}
			c.Advance(31 * time.Second)
			if _, e = s.ClaimPendingJobs("spark", 8); e != nil {
				t.Fatal(e)
			}
			got, e := s.Operation("op")
			if e != nil || got.Targets[0].Status != protocol.TargetExpired {
				t.Fatal("undispatched action retained offline", got, e)
			}
		})
	}
}
