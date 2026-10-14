package storage

import (
	"errors"
	"testing"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
)

func TestSparkDirectStartCancelledByDistinctVerifiedStopReleasesReservation(t *testing.T) {
	s, c, owner, report := sparkStorageFixture(t)
	report.Spark.ModelStates[0].SupervisorState = "stopped"
	if _, err := s.AcceptReport(report); err != nil {
		t.Fatal(err)
	}
	start := sparkSafetyStop(c, owner, report, "direct-start")
	start.Action = "model.start"
	if err := s.InsertSparkOperation(start, "start-hash", owner); err != nil {
		t.Fatal(err)
	}
	jobs, err := s.ClaimPendingJobs("spark", 1)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("claim start: %+v %v", jobs, err)
	}
	startJob := jobs[0]
	c.Advance(time.Second)
	refreshSparkReport(&report, c)
	report.Spark.ModelStates[0].Generation = 2
	report.Spark.ModelStates[0].SupervisorState = "starting"
	if _, err = s.AcceptReport(report); err != nil {
		t.Fatal(err)
	}
	if err = s.ApplyReceipt("spark", protocol.JobReceipt{JobID: startJob.JobID, OperationID: start.ID, Status: protocol.TargetRunning, Stage: "model_starting"}); err != nil {
		t.Fatal(err)
	}
	stop := sparkSafetyStop(c, owner, report, "distinct-stop")
	if err = s.InsertSparkOperation(stop, "stop-hash", owner); err != nil {
		t.Fatal(err)
	}
	jobs, err = s.ClaimPendingJobs("spark", 8)
	if err != nil || len(jobs) != 1 || jobs[0].OperationID != stop.ID {
		t.Fatalf("distinct stop claim: %+v %v", jobs, err)
	}
	c.Advance(time.Second)
	refreshSparkReport(&report, c)
	report.Spark.ModelStates[0].Generation = 3
	report.Spark.ModelStates[0].SupervisorState = "stopped"
	if _, err = s.AcceptReport(report); err != nil {
		t.Fatal(err)
	}
	evidence := map[string]any{"profile_id": "p", "profile_revision": stop.Params["profile_revision"], "control_epoch": "epoch", "agent_session": "s", "generation": float64(3), "supervisor_state": "stopped"}
	if err = s.ApplyReceipt("spark", protocol.JobReceipt{JobID: jobs[0].JobID, OperationID: stop.ID, Status: protocol.TargetSucceeded, Stage: "model_stopped", Evidence: evidence}); err != nil {
		t.Fatal(err)
	}
	if err = s.ApplyReceipt("spark", protocol.JobReceipt{JobID: startJob.JobID, OperationID: start.ID, Status: protocol.TargetUnknownResult, Stage: "attempt_unresolved"}); err != nil {
		t.Fatal(err)
	}
	if busy, err := s.HasActiveSparkProfile("spark", "p", "model.start"); err != nil || !busy {
		t.Fatalf("unknown start lost reservation: %v %v", busy, err)
	}
	// The broker reports cancellation only after observing the exact bound stop.
	// This is failure of the start intent; it never claims that startup succeeded.
	if err = s.ApplyReceipt("spark", protocol.JobReceipt{JobID: startJob.JobID, OperationID: start.ID, Status: protocol.TargetFailed, Stage: "start_cancelled_after_stop", ErrorCode: "superseded_by_stop", Evidence: evidence}); err != nil {
		t.Fatal(err)
	}
	if busy, err := s.HasActiveSparkProfile("spark", "p", "model.start"); err != nil || busy {
		t.Fatalf("cancelled start stuck reserved: %v %v", busy, err)
	}
	fresh := sparkSafetyStop(c, owner, report, "fresh-start")
	fresh.Action = "model.start"
	if err = s.InsertSparkOperation(fresh, "fresh-start-hash", owner); err != nil {
		t.Fatal(err)
	}
	if err = s.ApplyReceipt("spark", protocol.JobReceipt{JobID: startJob.JobID, OperationID: start.ID, Status: protocol.TargetSucceeded, Stage: "model_started", Evidence: evidence}); !errors.Is(err, ErrConflict) {
		t.Fatalf("late start receipt resurrected cancelled intent: %v", err)
	}
	old, err := s.Operation(start.ID)
	if err != nil || old.Targets[0].Status != protocol.TargetFailed || old.Targets[0].Stage != "start_cancelled_after_stop" {
		t.Fatalf("original outcome changed: %+v %v", old, err)
	}
	jobs, err = s.ClaimPendingJobs("spark", 8)
	if err != nil || len(jobs) != 1 || jobs[0].OperationID != fresh.ID || countRows(t, s, "agent_jobs") != 3 {
		t.Fatalf("replay or fresh start lost: %+v %v", jobs, err)
	}
}
