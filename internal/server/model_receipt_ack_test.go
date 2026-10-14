package server

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
)

func sparkAPIStopJob(t *testing.T) (*sparkAPIFixture, protocol.Operation, protocol.JobEnvelope) {
	t.Helper()
	f := newSparkAPI(t, true)
	w := f.request(t, "POST", "/api/v1/operations", map[string]any{"action": "model.stop", "client_request_key": "proof-stop", "target_ids": []string{"spark"}, "params": f.params()}, "owner")
	if w.Code != 202 {
		t.Fatalf("submit stop: %d %s", w.Code, w.Body.String())
	}
	var op protocol.Operation
	if err := json.Unmarshal(w.Body.Bytes(), &op); err != nil {
		t.Fatal(err)
	}
	response := f.restartStopReport(t, "starting", 1)
	if len(response.Jobs) != 1 || response.Jobs[0].OperationID != op.ID {
		t.Fatalf("dispatch stop: %+v", response)
	}
	return f, op, response.Jobs[0]
}

func sparkAPIFailedStopReceipt(op protocol.Operation, job protocol.JobEnvelope) protocol.JobReceipt {
	return protocol.JobReceipt{JobID: job.JobID, OperationID: op.ID, Status: protocol.TargetSucceeded, Stage: "model_stopped", Evidence: map[string]any{"profile_id": "p", "profile_revision": op.Params["profile_revision"], "control_epoch": "epoch", "agent_session": "session", "generation": float64(2), "supervisor_state": "failed", "readiness": "not_applicable", "reason": "failed_service_shutdown_verified", "shutdown_verified": true, "instance_identity": "sha256:" + strings.Repeat("a", 64)}}
}

func TestSparkAPIModelSuccessWaitsForCommittedProof(t *testing.T) {
	for _, resend := range []bool{true, false} {
		t.Run(map[bool]string{true: "same_receipt_disclosure", false: "older_controller_already_acked"}[resend], func(t *testing.T) {
			f, op, job := sparkAPIStopJob(t)
			metadata := protocol.JobReceipt{JobID: job.JobID, Status: protocol.TargetRunning, Stage: "stopping"}
			response := f.restartStopReport(t, "starting", 1, metadata)
			if len(response.ReceiptAcks) != 1 || response.ReceiptAcks[0] != job.JobID {
				t.Fatalf("accepted model metadata not acknowledged: %+v", response)
			}
			receipt := sparkAPIFailedStopReceipt(op, job)
			if !resend {
				receipt.OperationID = "" // Legacy optional operation_id stays supported.
			}
			response = f.restartStopReport(t, "starting", 1, receipt)
			if len(response.ReceiptAcks) != 0 || len(response.Jobs) != 0 {
				t.Fatalf("unproven success drained or redispatched: %+v", response)
			}
			unknown, err := f.app.Store.Operation(op.ID)
			if err != nil || unknown.Targets[0].Status != protocol.TargetUnknownResult || unknown.Targets[0].Stage != "unverified_result" || unknown.Targets[0].ErrorCode != "missing_evidence" {
				t.Fatalf("unknown outcome was hidden: %+v %v", unknown, err)
			}
			var original, envelope, deadline, delivered, oldSample string
			if err = f.app.Store.DB.QueryRow(`SELECT result,envelope,deadline,delivered_at FROM agent_jobs WHERE job_id=?`, job.JobID).Scan(&original, &envelope, &deadline, &delivered); err != nil {
				t.Fatal(err)
			}
			oldSequence := f.report.Sequence
			if err = f.app.Store.DB.QueryRow(`SELECT payload FROM spark_samples WHERE agent_id='spark' AND session_id='session' AND seq=?`, oldSequence).Scan(&oldSample); err != nil {
				t.Fatal(err)
			}
			// Matching proof may arrive after the original execution lease. This
			// confirms that attempt and must not renew or repeat its effect.
			f.clock.Advance(protocol.SparkDispatchLease)
			f.report.Spark.Inventory.ObservedAt = f.clock.Now()
			f.report.Spark.ModelStates[0].Readiness = "not_applicable"
			f.report.Spark.ModelStates[0].Reason = "failed_service_shutdown_verified"
			if resend {
				response = f.restartStopReport(t, "failed", 2, receipt)
			} else {
				response = f.restartStopReport(t, "failed", 2)
			}
			if len(response.Jobs) != 0 || len(response.ReceiptAcks) != map[bool]int{true: 1, false: 0}[resend] {
				t.Fatalf("proof response: %+v", response)
			}
			confirmed, err := f.app.Store.Operation(op.ID)
			if err != nil || confirmed.Status != protocol.OpCompleted || confirmed.Targets[0].Status != protocol.TargetSucceeded || confirmed.Targets[0].Stage != "model_stopped" {
				t.Fatalf("original receipt did not converge: %+v %v", confirmed, err)
			}
			var stored, gotEnvelope, gotDeadline, gotDelivered, gotSample string
			if err = f.app.Store.DB.QueryRow(`SELECT result,envelope,deadline,delivered_at FROM agent_jobs WHERE job_id=?`, job.JobID).Scan(&stored, &gotEnvelope, &gotDeadline, &gotDelivered); err != nil {
				t.Fatal(err)
			}
			if err = f.app.Store.DB.QueryRow(`SELECT payload FROM spark_samples WHERE agent_id='spark' AND session_id='session' AND seq=?`, oldSequence).Scan(&gotSample); err != nil {
				t.Fatal(err)
			}
			if stored != original || gotEnvelope != envelope || gotDeadline != deadline || gotDelivered != delivered || gotSample != oldSample {
				t.Fatal("reconciliation replaced original receipt, execution lease, delivery, or historical sample")
			}
			view, err := f.app.Store.SparkSnapshot("spark")
			if err != nil || view.Spark.ModelStates[0].SupervisorState != "failed" {
				t.Fatalf("physical unit failure lost: %+v %v", view, err)
			}
			// Changed late terminal evidence conflicts; the exact original still
			// drains idempotently and does not publish another command.
			conflict := receipt
			conflict.Message = "changed late terminal evidence"
			response = f.restartStopReport(t, "failed", 2, conflict)
			if len(response.ReceiptAcks) != 0 || len(response.Jobs) != 0 {
				t.Fatalf("conflicting terminal receipt accepted: %+v", response)
			}
			response = f.restartStopReport(t, "failed", 2, receipt)
			if len(response.ReceiptAcks) != 1 || response.ReceiptAcks[0] != job.JobID || len(response.Jobs) != 0 {
				t.Fatalf("exact terminal duplicate not drained: %+v", response)
			}
			for _, table := range []string{"agent_jobs", "operations"} {
				var count int
				if err = f.app.Store.DB.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil || count != 1 {
					t.Fatalf("additional effect/operation created: %s=%d %v", table, count, err)
				}
			}
		})
	}
}

func TestSparkAPIUnverifiedSuccessRejectsWrongAuthorityAndProof(t *testing.T) {
	for _, cause := range []string{"changed_epoch", "changed_profile_revision", "foreign_session", "nil_control", "nil_inventory", "wrong_generation", "missing_physical_proof", "unknown_quality"} {
		t.Run(cause, func(t *testing.T) {
			f, op, job := sparkAPIStopJob(t)
			receipt := sparkAPIFailedStopReceipt(op, job)
			response := f.restartStopReport(t, "starting", 1, receipt)
			if len(response.ReceiptAcks) != 0 {
				t.Fatal("cached earlier generation acknowledged")
			}
			f.report.Spark.ModelStates[0].Readiness = "not_applicable"
			f.report.Spark.ModelStates[0].Reason = "failed_service_shutdown_verified"
			generation := int64(2)
			switch cause {
			case "changed_epoch":
				epoch := "other-epoch"
				f.report.Spark.ControlState.Epoch = &epoch
			case "changed_profile_revision":
				f.report.Spark.Inventory.ObservedAt = f.clock.Now()
				f.report.Spark.Inventory.Profiles[0].ProfileRevision = "sha256:" + strings.Repeat("b", 64)
			case "foreign_session":
				f.report.SessionID = "other-session"
			case "nil_control":
				// Send the exact raw sample without a control authority. The
				// merged view retains older control but must not qualify it.
				f.report.Spark.ControlState = nil
			case "nil_inventory":
				f.report.Spark.Inventory = nil
			case "wrong_generation":
				generation = 3
			case "missing_physical_proof":
				f.report.Spark.ModelStates[0].Reason = ""
			case "unknown_quality":
				f.report.Spark.ModelStates[0].Quality = "unknown"
			}
			// The regular helper assumes control exists; nil authority uses the
			// same genuine authenticated HTTP report path with explicit times.
			if f.report.Spark.ControlState == nil {
				f.clock.Advance(time.Second)
				f.report.Sequence++
				f.report.ObservedAt = f.clock.Now()
				st := &f.report.Spark.ModelStates[0]
				st.ObservedAt, st.Generation, st.SupervisorState = f.clock.Now(), generation, "failed"
				f.report.JobReceipts = []protocol.JobReceipt{receipt}
				w := f.request(t, "POST", "/api/v1/agent/report", f.report, "agent")
				if w.Code != 200 {
					t.Fatalf("wrong-authority report: %d %s", w.Code, w.Body.String())
				}
				if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
					t.Fatal(err)
				}
			} else {
				response = f.restartStopReport(t, "failed", generation, receipt)
			}
			if len(response.ReceiptAcks) != 0 || len(response.Jobs) != 0 {
				t.Fatalf("wrong authority/proof drained success: %+v", response)
			}
			got, err := f.app.Store.Operation(op.ID)
			if err != nil || got.Targets[0].Status != protocol.TargetUnknownResult || got.Targets[0].ErrorCode != "missing_evidence" {
				t.Fatalf("wrong authority/proof converged: %+v %v", got, err)
			}
		})
	}
}

func TestSparkAPIReceiptAckPreservesWorkerUnknownAndGenericOutcomes(t *testing.T) {
	f, _, job := sparkAPIStopJob(t)
	receipt := protocol.JobReceipt{JobID: job.JobID, Status: protocol.TargetUnknownResult, Stage: "awaiting_shutdown_observation"}
	response := f.restartStopReport(t, "starting", 1, receipt)
	if len(response.ReceiptAcks) != 1 || response.ReceiptAcks[0] != job.JobID {
		t.Fatalf("worker unknown metadata not acknowledged: %+v", response)
	}
	dl := f.clock.Now().Add(time.Minute)
	op := &protocol.Operation{ID: "generic-op", Action: "agent.diagnostics", ClientRequestKey: "generic-key", Actor: "owner", Status: protocol.OpQueued, Revision: 1, CreatedAt: f.clock.Now(), Deadline: &dl, Targets: []protocol.TargetResult{{AgentID: "spark", Status: protocol.TargetQueued}}}
	if err := f.app.Store.InsertOperation(op, "generic-hash"); err != nil {
		t.Fatal(err)
	}
	response = f.restartStopReport(t, "starting", 1)
	if len(response.Jobs) != 1 || response.Jobs[0].OperationID != op.ID {
		t.Fatalf("generic job not dispatched: %+v", response)
	}
	receipt = protocol.JobReceipt{JobID: response.Jobs[0].JobID, Status: protocol.TargetSucceeded, Stage: "unproven_diagnostics"}
	response = f.restartStopReport(t, "starting", 1, receipt)
	if len(response.ReceiptAcks) != 1 || response.ReceiptAcks[0] != receipt.JobID {
		t.Fatalf("generic unverified receipt ACK changed: %+v", response)
	}
	got, err := f.app.Store.Operation(op.ID)
	if err != nil || got.Targets[0].Status != protocol.TargetRejected {
		t.Fatalf("generic proof validation changed: %+v %v", got, err)
	}
}

func TestSparkAPIReceiptReconciliationRequiresLiveTriggerAndKeepsHistoricalAuthority(t *testing.T) {
	f, op, job := sparkAPIStopJob(t)
	receipt := sparkAPIFailedStopReceipt(op, job)
	f.restartStopReport(t, "starting", 1, receipt)
	f.clock.Advance(time.Second)
	f.report.Sequence++
	f.report.ObservedAt = f.clock.Now()
	f.report.Spark.ControlState.ObservedAt = f.clock.Now()
	f.report.Spark.Inventory.ObservedAt = f.clock.Now()
	f.report.Spark.ModelStates[0].ObservedAt = f.clock.Now()
	f.report.Spark.ModelStates[0].Generation = 2
	f.report.Spark.ModelStates[0].SupervisorState = "failed"
	f.report.Spark.ModelStates[0].Readiness = "not_applicable"
	f.report.Spark.ModelStates[0].Reason = "failed_service_shutdown_verified"
	f.report.IsLive = false
	f.report.JobReceipts = nil
	w := f.request(t, "POST", "/api/v1/agent/report", f.report, "agent")
	if w.Code != 200 {
		t.Fatalf("historical exact proof: %d %s", w.Code, w.Body.String())
	}
	got, err := f.app.Store.Operation(op.ID)
	if err != nil || got.Targets[0].Status != protocol.TargetUnknownResult {
		t.Fatalf("historical report triggered reconciliation: %+v %v", got, err)
	}
	f.report.IsLive = true
	epoch := "new-current-epoch"
	f.report.Spark.ControlState.Epoch = &epoch
	f.report.Spark.ModelStates[0].Reason = ""
	f.report.Spark.ModelStates[0].Readiness = "unknown"
	response := f.restartStopReport(t, "starting", 3)
	if len(response.Jobs) != 0 || len(response.ReceiptAcks) != 0 {
		t.Fatalf("live reconciliation dispatched/drained absent receipt: %+v", response)
	}
	got, err = f.app.Store.Operation(op.ID)
	if err != nil || got.Targets[0].Status != protocol.TargetSucceeded {
		t.Fatalf("original exact historical proof not used: %+v %v", got, err)
	}
	view, err := f.app.Store.SparkSnapshot("spark")
	if err != nil || view.Spark.ControlState.Epoch == nil || *view.Spark.ControlState.Epoch != epoch || view.Spark.ModelStates[0].Generation != 3 {
		t.Fatalf("historical proof refreshed current authority: %+v %v", view, err)
	}
	w = f.request(t, "POST", "/api/v1/operations", map[string]any{"action": "model.start", "client_request_key": "old-authority-start", "target_ids": []string{"spark"}, "params": op.Params}, "owner")
	if w.Code != 409 {
		t.Fatalf("old outcome proof granted current start authority: %d %s", w.Code, w.Body.String())
	}
}
