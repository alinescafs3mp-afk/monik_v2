package storage

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/clock"
	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
)

func sparkRestartStartFixture(t *testing.T, authorize bool, state string, generation int64) (*Store, *clock.Fake, *Session, protocol.AgentReport, protocol.JobEnvelope, *protocol.SparkStageAuthorization) {
	t.Helper()
	s, c, owner, report := sparkStorageFixture(t)
	if _, err := s.AcceptReport(report); err != nil {
		t.Fatal(err)
	}
	deadline := c.Now().Add(protocol.SparkDispatchLease)
	op := &protocol.Operation{ID: "restart", Action: "model.restart", Actor: owner.Username, ClientRequestKey: "restart", Status: protocol.OpQueued, Revision: 1, CreatedAt: c.Now(), Deadline: &deadline, Params: modelParams(report), Targets: []protocol.TargetResult{{AgentID: "spark", Status: protocol.TargetQueued}}}
	if err := s.InsertSparkOperation(op, "restart-hash", owner); err != nil {
		t.Fatal(err)
	}
	jobs, err := s.ClaimPendingJobs("spark", 1)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("claim restart: %v %v", jobs, err)
	}
	c.Advance(time.Second)
	refreshSparkReport(&report, c)
	report.Spark.ModelStates[0].Generation = 2
	report.Spark.ModelStates[0].SupervisorState = "stopped"
	if _, err = s.AcceptReport(report); err != nil {
		t.Fatal(err)
	}
	receipt := protocol.JobReceipt{JobID: jobs[0].JobID, OperationID: op.ID, Status: protocol.TargetAwaitingConfirmation, Stage: "restart_stopped", Evidence: map[string]any{"profile_id": "p", "profile_revision": op.Params["profile_revision"], "control_epoch": "epoch", "agent_session": "s", "generation": float64(2)}}
	if err = s.ApplyReceipt("spark", receipt); err != nil {
		t.Fatal(err)
	}
	var auth *protocol.SparkStageAuthorization
	if authorize {
		auth, err = s.AuthorizeSparkStartStage("spark", op.ID, jobs[0].JobID)
		if err != nil {
			t.Fatal(err)
		}
		if auth.StageID == jobs[0].JobID || auth.ParentOperationID != op.ID || auth.Params.ExpectedGeneration != 2 {
			t.Fatalf("not a distinct bound start stage: %+v", auth)
		}
	}
	c.Advance(time.Second)
	refreshSparkReport(&report, c)
	report.Spark.ModelStates[0].Generation = generation
	report.Spark.ModelStates[0].SupervisorState = state
	if _, err = s.AcceptReport(report); err != nil {
		t.Fatal(err)
	}
	return s, c, owner, report, jobs[0], auth
}

func sparkSafetyStop(c *clock.Fake, owner *Session, report protocol.AgentReport, id string) *protocol.Operation {
	deadline := c.Now().Add(protocol.SparkDispatchLease)
	return &protocol.Operation{ID: id, Action: "model.stop", Actor: owner.Username, ClientRequestKey: id, Status: protocol.OpQueued, Revision: 1, CreatedAt: c.Now(), Deadline: &deadline, Params: modelParams(report), Targets: []protocol.TargetResult{{AgentID: "spark", Status: protocol.TargetQueued}}}
}

type advancingSparkStageClock struct {
	*clock.Fake
}

func (c advancingSparkStageClock) Now() time.Time {
	at := c.Fake.Now()
	c.Advance(33 * time.Nanosecond)
	return at
}

func TestSparkStartStageLeaseWithAdvancingClock(t *testing.T) {
	s, c, _, _, job, _ := sparkRestartStartFixture(t, false, "stopped", 2)
	// The native failure issued a 30s+33ns lease. A stationary fake clock
	// conceals the producer/consumer disagreement, so advance every read.
	s.Clock = advancingSparkStageClock{c}
	auth, err := s.AuthorizeSparkStartStage("spark", "restart", job.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if lease := auth.Deadline.Sub(auth.IssuedAt); lease != protocol.SparkDispatchLease {
		t.Fatalf("stage lease rejected by worker: got %s, maximum %s", lease, protocol.SparkDispatchLease)
	}
	same, err := s.AuthorizeSparkStartStage("spark", "restart", job.JobID)
	if err != nil || !same.IssuedAt.Equal(auth.IssuedAt) || !same.Deadline.Equal(auth.Deadline) {
		t.Fatalf("stored stage replay changed its original lease: %+v %v", same, err)
	}
}

func TestSparkUnattemptedRestartStageFailurePreservesStopEvidence(t *testing.T) {
	s, _, _, _, job, _ := sparkRestartStartFixture(t, true, "stopped", 2)
	var raw string
	if err := s.DB.QueryRow(`SELECT result FROM agent_jobs WHERE job_id=?`, job.JobID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var stopped protocol.JobReceipt
	if err := json.Unmarshal([]byte(raw), &stopped); err != nil {
		t.Fatal(err)
	}
	evidence := map[string]any{}
	for key, value := range stopped.Evidence {
		evidence[key] = value
	}
	evidence["restart_stop_receipt"] = raw
	evidence["start_stage_attempted"] = false
	failure := stopped
	failure.Status = protocol.TargetFailed
	failure.Stage = "restart_start_not_attempted"
	failure.ErrorCode = "invalid_stage_authorization"
	failure.Message = "This restart's start stage was not attempted"
	failure.Evidence = evidence
	for _, bad := range []string{"changed_stop", "nested_receipt", "oversized_receipt", "unknown_stop_field", "attempted_start"} {
		t.Run(bad, func(t *testing.T) {
			mutated := failure
			mutated.Evidence = map[string]any{}
			for key, value := range evidence {
				mutated.Evidence[key] = value
			}
			switch bad {
			case "changed_stop":
				changed := stopped
				changed.Message = "different stop receipt"
				b, _ := json.Marshal(changed)
				mutated.Evidence["restart_stop_receipt"] = string(b)
			case "nested_receipt":
				mutated.Evidence["restart_stop_receipt"] = stopped
			case "oversized_receipt":
				mutated.Evidence["restart_stop_receipt"] = strings.Repeat("x", (32<<10)+1)
			case "unknown_stop_field":
				var changed protocol.JobReceipt
				json.Unmarshal([]byte(raw), &changed)
				changed.Evidence["free_form_command"] = "disallowed"
				b, _ := json.Marshal(changed)
				mutated.Evidence["restart_stop_receipt"] = string(b)
			case "attempted_start":
				mutated.Evidence["start_stage_attempted"] = true
			}
			if err := s.ApplyReceipt("spark", mutated); err == nil {
				t.Fatal("unsafe or altered no-start evidence accepted")
			}
		})
	}
	for range 2 {
		if err := s.ApplyReceipt("spark", failure); err != nil {
			t.Fatal("truthful no-start failure or identical replay rejected", err)
		}
	}
	got, err := s.Operation("restart")
	if err != nil || got.Targets[0].Status != protocol.TargetFailed || got.Targets[0].Evidence["restart_stop_receipt"] != raw {
		t.Fatalf("original stop proof lost: %+v %v", got, err)
	}
	if busy, err := s.HasActiveSparkProfile("spark", "p", "model.stop"); err != nil || busy {
		t.Fatalf("known unattempted stage still blocks a separately validated stop: %v %v", busy, err)
	}
}

func TestSparkStopSupersedesOnlyExactRestartStartStage(t *testing.T) {
	for _, state := range []string{"starting", "running"} {
		t.Run(state, func(t *testing.T) {
			generation := int64(3)
			s, c, owner, report, parentJob, auth := sparkRestartStartFixture(t, true, state, generation)
			params, err := protocol.DecodeModelActionParams(modelParams(report))
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err = s.ValidateSparkControl("spark", params, "model.stop"); err != nil {
				t.Fatal(err)
			}
			if busy, err := s.HasActiveSparkProfile("spark", "p", "model.stop"); err != nil || busy {
				t.Fatalf("exact startup safety stop blocked: %v %v", busy, err)
			}
			for _, action := range []string{"model.start", "model.restart"} {
				if busy, err := s.HasActiveSparkProfile("spark", "p", action); err != nil || !busy {
					t.Fatalf("restart reservation loosened for %s: %v %v", action, busy, err)
				}
			}
			stop := sparkSafetyStop(c, owner, report, "safety-stop")
			if err = s.InsertSparkOperation(stop, "stop-hash", owner); err != nil {
				t.Fatal(err)
			}
			parent, err := s.Operation("restart")
			if err != nil || parent.Status != protocol.OpCompletedWithErrs || parent.Targets[0].Status != protocol.TargetFailed || parent.Targets[0].Stage != "restart_start_superseded" || parent.Targets[0].ErrorCode != "superseded_by_stop" {
				t.Fatalf("parent not honestly superseded: %+v %v", parent, err)
			}
			if _, err = s.AuthorizeSparkStartStage("spark", "restart", parentJob.JobID); err == nil {
				t.Fatal("superseded parent returned its saved start authorization")
			}
			for _, late := range []protocol.JobReceipt{
				{JobID: parentJob.JobID, OperationID: "restart", Status: protocol.TargetRunning, Stage: "restart_start_attempted"},
				{JobID: parentJob.JobID, OperationID: "restart", Status: protocol.TargetSucceeded, Stage: "model_restarted", Evidence: map[string]any{"profile_id": "p", "profile_revision": params.ProfileRevision, "control_epoch": "epoch", "agent_session": "s", "generation": float64(3), "stage_id": auth.StageID}},
			} {
				if err = s.ApplyReceipt("spark", late); err != nil {
					t.Fatalf("bounded delayed receipt could not drain: %v", err)
				}
			}
			parent, _ = s.Operation("restart")
			if parent.Targets[0].Stage != "restart_start_superseded" || parent.Targets[0].Status != protocol.TargetFailed {
				t.Fatal("ACK of delayed parent receipt resurrected restart")
			}
			if err = s.ApplyReceipt("spark", protocol.JobReceipt{JobID: parentJob.JobID, OperationID: "another-parent", Status: protocol.TargetFailed}); !errors.Is(err, ErrConflict) {
				t.Fatal("foreign delayed parent identity acknowledged", err)
			}
			if err = s.ApplyReceipt("spark", protocol.JobReceipt{JobID: parentJob.JobID, OperationID: "restart", Status: protocol.TargetFailed, Evidence: map[string]any{"raw_log": "unapproved"}}); err == nil {
				t.Fatal("superseded receipt metadata validation bypassed")
			}
			jobs, err := s.ClaimPendingJobs("spark", 8)
			if err != nil || len(jobs) != 1 || jobs[0].OperationID != stop.ID || jobs[0].Action != "model.stop" {
				t.Fatalf("stop not distinct / restart replayed: %+v %v", jobs, err)
			}
			if err = s.InsertSparkOperation(sparkSafetyStop(c, owner, report, "second-stop"), "second-hash", owner); !errors.Is(err, ErrConflict) {
				t.Fatalf("active safety stop conflict bypassed: %v", err)
			}
			c.Advance(time.Second)
			refreshSparkReport(&report, c)
			report.Spark.ModelStates[0].Generation = generation + 1
			report.Spark.ModelStates[0].SupervisorState = "stopped"
			if _, err = s.AcceptReport(report); err != nil {
				t.Fatal(err)
			}
			receipt := protocol.JobReceipt{JobID: jobs[0].JobID, OperationID: stop.ID, Status: protocol.TargetSucceeded, Stage: "model_stopped", Evidence: map[string]any{"profile_id": "p", "profile_revision": params.ProfileRevision, "control_epoch": "epoch", "agent_session": "s", "generation": float64(generation + 1)}}
			if err = s.ApplyReceipt("spark", receipt); err != nil {
				t.Fatal(err)
			}
			result, err := s.Operation(stop.ID)
			if err != nil || result.Status != protocol.OpCompleted || result.Targets[0].Stage != "model_stopped" {
				t.Fatalf("distinct stop outcome lost: %+v %v", result, err)
			}
			parent, _ = s.Operation("restart")
			if parent.Targets[0].Stage != "restart_start_superseded" || countRows(t, s, "agent_jobs") != 2 {
				t.Fatal("stop success resurrected parent or duplicated effect")
			}
		})
	}
}

func TestSparkRestartSafetyStopFencesAndUnissuedConflicts(t *testing.T) {
	for _, kind := range []string{"unissued", "foreign_generation", "stopped_pending_generation", "stopped_later_generation", "stopping", "revision", "epoch", "session", "wrong_stage_profile"} {
		t.Run(kind, func(t *testing.T) {
			s, c, owner, report, _, _ := sparkRestartStartFixture(t, kind != "unissued", "starting", 3)
			c.Advance(time.Second)
			refreshSparkReport(&report, c)
			switch kind {
			case "foreign_generation":
				report.Spark.ModelStates[0].Generation = 4
			case "stopped_pending_generation":
				report.Spark.ModelStates[0].SupervisorState = "stopped"
				report.Spark.ModelStates[0].Generation = 2
			case "stopped_later_generation":
				report.Spark.ModelStates[0].SupervisorState = "stopped"
			case "stopping":
				report.Spark.ModelStates[0].SupervisorState = "stopping"
			case "revision":
				report.Spark.Inventory.ObservedAt = c.Now()
				report.Spark.Inventory.Profiles[0].ProfileRevision = "sha256:2222222222222222222222222222222222222222222222222222222222222222"
			case "epoch":
				epoch := "different-epoch"
				report.Spark.ControlState.Epoch = &epoch
			case "session":
				report.SessionID = "other-session"
			case "wrong_stage_profile":
				if _, err := s.DB.Exec(`UPDATE spark_intents SET stage_authorization=json_set(stage_authorization,'$.params.profile_id','q') WHERE operation_id='restart'`); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.AcceptReport(report); err != nil {
				t.Fatal(err)
			}
			params, err := protocol.DecodeModelActionParams(modelParams(report))
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err = s.ValidateSparkControl("spark", params, "model.stop"); err != nil {
				t.Fatalf("fixture fresh stop invalid: %v", err)
			}
			if busy, err := s.HasActiveSparkProfile("spark", "p", "model.stop"); err != nil || !busy {
				t.Fatalf("foreign/unissued restart bypassed: %v %v", busy, err)
			}
			if err = s.InsertSparkOperation(sparkSafetyStop(c, owner, report, "safety-stop"), "stop-hash", owner); !errors.Is(err, ErrConflict) {
				t.Fatalf("atomic reservation check missing: %v", err)
			}
			parent, _ := s.Operation("restart")
			if parent.Targets[0].Status != protocol.TargetAwaitingConfirmation || countRows(t, s, "agent_jobs") != 1 {
				t.Fatal("rejected stop changed original workflow")
			}
		})
	}
}

func TestSparkStopSupersessionPublicationRollsBack(t *testing.T) {
	s, c, owner, report, parentJob, auth := sparkRestartStartFixture(t, true, "starting", 3)
	if _, err := s.DB.Exec(`CREATE TRIGGER supersession_failure BEFORE UPDATE ON operations WHEN OLD.id='restart' AND NEW.status='completed_with_errors' BEGIN SELECT RAISE(ABORT,'fixture failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertSparkOperation(sparkSafetyStop(c, owner, report, "safety-stop"), "stop-hash", owner); err == nil {
		t.Fatal("failed parent publication committed")
	}
	parent, err := s.Operation("restart")
	if err != nil || parent.Targets[0].Status != protocol.TargetAwaitingConfirmation || countRows(t, s, "agent_jobs") != 1 || countRows(t, s, "spark_intents") != 1 {
		t.Fatalf("partial stop/parent publication: %+v %v", parent, err)
	}
	same, err := s.AuthorizeSparkStartStage("spark", "restart", parentJob.JobID)
	if err != nil || same.StageID != auth.StageID || !same.Deadline.Equal(auth.Deadline) {
		t.Fatalf("rollback lost immutable stage: %+v %v", same, err)
	}
}

func TestSparkConcurrentSafetyStopsPublishOneEffect(t *testing.T) {
	s, c, owner, report, _, _ := sparkRestartStartFixture(t, true, "starting", 3)
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, id := range []string{"safety-stop-a", "safety-stop-b"} {
		op := sparkSafetyStop(c, owner, report, id)
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results <- s.InsertSparkOperation(op, op.ID, owner)
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		}
	}
	if success != 1 || countRows(t, s, "agent_jobs") != 2 {
		t.Fatalf("concurrent stop duplicate: accepted=%d jobs=%d", success, countRows(t, s, "agent_jobs"))
	}
}
