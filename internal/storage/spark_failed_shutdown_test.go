package storage

import (
	"strings"
	"testing"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
)

func TestSparkFailedShutdownReceiptRequiresMatchingPhysicalProof(t *testing.T) {
	for _, cause := range []string{"complete", "missing_witness", "wrong_type", "missing_sample_proof", "wrong_generation", "unbound_instance", "still_starting", "unknown_quality", "missing_receipt_reason", "missing_receipt_readiness", "changed_epoch", "changed_revision", "disabled_control", "nil_control", "nil_inventory"} {
		t.Run(cause, func(t *testing.T) {
			s, c, owner, report := sparkStorageFixture(t)
			if _, err := s.AcceptReport(report); err != nil {
				t.Fatal(err)
			}
			op := sparkSafetyStop(c, owner, report, "failed-shutdown")
			if err := s.InsertSparkOperation(op, "failed-shutdown-hash", owner); err != nil {
				t.Fatal(err)
			}
			jobs, err := s.ClaimPendingJobs("spark", 1)
			if err != nil || len(jobs) != 1 {
				t.Fatalf("claim stop: %v %v", jobs, err)
			}
			c.Advance(time.Second)
			refreshSparkReport(&report, c)
			st := &report.Spark.ModelStates[0]
			st.Generation, st.SupervisorState, st.Readiness, st.Reason = 2, "failed", "not_applicable", "failed_service_shutdown_verified"
			evidence := map[string]any{"profile_id": "p", "profile_revision": op.Params["profile_revision"], "control_epoch": "epoch", "agent_session": "s", "generation": float64(2), "supervisor_state": "failed", "shutdown_verified": true, "reason": "failed_service_shutdown_verified", "readiness": "not_applicable", "instance_identity": "sha256:" + strings.Repeat("a", 64)}
			switch cause {
			case "missing_witness":
				delete(evidence, "shutdown_verified")
			case "wrong_type":
				evidence["shutdown_verified"] = "true"
			case "nil_control":
				report.Spark.ControlState = nil
			case "nil_inventory":
				report.Spark.Inventory = nil
			case "changed_epoch":
				epoch := "new-control-epoch"
				report.Spark.ControlState.Epoch = &epoch
			case "changed_revision":
				report.Spark.Inventory.ObservedAt = c.Now()
				report.Spark.Inventory.Profiles[0].ProfileRevision = "sha256:" + strings.Repeat("c", 64)
			case "disabled_control":
				report.Spark.ControlState.Enabled = false
				report.Spark.ControlState.Epoch = nil
			case "missing_receipt_reason":
				delete(evidence, "reason")
			case "missing_receipt_readiness":
				delete(evidence, "readiness")
			case "missing_sample_proof":
				st.Reason = ""
			case "wrong_generation":
				evidence["generation"] = float64(1)
			case "unbound_instance":
				evidence["instance_identity"] = "stopped"
			case "still_starting":
				st.SupervisorState = "starting"
				st.Readiness = "unknown"
			case "unknown_quality":
				st.Quality = "unknown"
			}
			if _, err = s.AcceptReport(report); err != nil {
				t.Fatal(err)
			}
			err = s.ApplyReceipt("spark", protocol.JobReceipt{JobID: jobs[0].JobID, OperationID: op.ID, Status: protocol.TargetSucceeded, Stage: "model_stopped", Evidence: evidence})
			if cause == "complete" && err != nil {
				t.Fatal(err)
			}
			if cause == "wrong_type" && err == nil {
				t.Fatal("string accepted as a physical shutdown witness")
			}
			actual, err := s.Operation(op.ID)
			if err != nil {
				t.Fatal(err)
			}
			succeeded := actual.Targets[0].Status == protocol.TargetSucceeded
			if succeeded != (cause == "complete") {
				t.Fatalf("unbound failed shutdown accepted or exact proof rejected: %+v", actual.Targets[0])
			}
			if cause == "complete" {
				v, err := s.SparkSnapshot("spark")
				if err != nil || v.Spark.ModelStates[0].SupervisorState != "failed" {
					t.Fatalf("native failure history was hidden: %+v %v", v, err)
				}
				if busy, err := s.HasActiveSparkProfile("spark", "p", "model.start"); err != nil || busy {
					t.Fatalf("confirmed shutdown remained reserved: %v %v", busy, err)
				}
			}
		})
	}
}

func TestSparkRestartStageRetainsFailedStateAndRequiresPhysicalProof(t *testing.T) {
	for _, cause := range []string{"complete", "missing_witness", "missing_sample_proof", "wrong_generation"} {
		t.Run(cause, func(t *testing.T) {
			s, c, _, report, job, _ := sparkRestartStartFixture(t, false, "failed", 3)
			c.Advance(time.Second)
			refreshSparkReport(&report, c)
			st := &report.Spark.ModelStates[0]
			st.Readiness, st.Reason = "not_applicable", "failed_service_shutdown_verified"
			evidence := map[string]any{"profile_id": "p", "profile_revision": job.Params["profile_revision"], "control_epoch": "epoch", "agent_session": "s", "generation": float64(3), "supervisor_state": "failed", "shutdown_verified": true, "reason": "failed_service_shutdown_verified", "readiness": "not_applicable", "instance_identity": "sha256:" + strings.Repeat("b", 64)}
			switch cause {
			case "missing_witness":
				delete(evidence, "shutdown_verified")
			case "nil_control":
				report.Spark.ControlState = nil
			case "nil_inventory":
				report.Spark.Inventory = nil
			case "changed_epoch":
				epoch := "new-control-epoch"
				report.Spark.ControlState.Epoch = &epoch
			case "changed_revision":
				report.Spark.Inventory.ObservedAt = c.Now()
				report.Spark.Inventory.Profiles[0].ProfileRevision = "sha256:" + strings.Repeat("c", 64)
			case "disabled_control":
				report.Spark.ControlState.Enabled = false
				report.Spark.ControlState.Epoch = nil
			case "missing_receipt_reason":
				delete(evidence, "reason")
			case "missing_receipt_readiness":
				delete(evidence, "readiness")
			case "missing_sample_proof":
				st.Reason = ""
			case "wrong_generation":
				evidence["generation"] = float64(2)
			}
			if _, err := s.AcceptReport(report); err != nil {
				t.Fatal(err)
			}
			if err := s.ApplyReceipt("spark", protocol.JobReceipt{JobID: job.JobID, OperationID: job.OperationID, Status: protocol.TargetAwaitingConfirmation, Stage: "restart_stopped", Evidence: evidence}); err != nil {
				t.Fatal(err)
			}
			auth, err := s.AuthorizeSparkStartStage("spark", job.OperationID, job.JobID)
			if cause == "complete" {
				if err != nil || auth == nil || auth.Params.ExpectedGeneration != 3 {
					t.Fatalf("exact failed-unit shutdown cannot receive a fresh distinct stage: %+v %v", auth, err)
				}
			} else if err == nil || auth != nil {
				t.Fatalf("unbound failed shutdown authorized: %+v %v", auth, err)
			}
		})
	}
}
