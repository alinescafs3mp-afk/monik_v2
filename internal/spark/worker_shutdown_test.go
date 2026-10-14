package spark

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
)

func stageFailedShutdownReport(w *Worker, j *workerJob) protocol.AgentReport {
	j.Receipt.Evidence["supervisor_state"] = "failed"
	j.Receipt.Evidence["readiness"] = "not_applicable"
	j.Receipt.Evidence["reason"] = "failed_service_shutdown_verified"
	j.Receipt.Evidence["shutdown_verified"] = true
	j.Receipt.Evidence["instance_identity"] = "sha256:" + strings.Repeat("a", 64)
	report := stageStoppedReport(w, j)
	report.Spark.ModelStates[0].SupervisorState = "failed"
	report.Spark.ModelStates[0].Readiness = "not_applicable"
	report.Spark.ModelStates[0].Reason = "failed_service_shutdown_verified"
	return report
}

func TestWorkerFailedShutdownCommitsExactlyOneFreshRestartStage(t *testing.T) {
	w, j := stageWorkerFixture(t)
	j.StageSnapshotAcked, j.Acked = false, false
	var stageRequests atomic.Int64
	server := httptest.NewTLSServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/agent/spark/stage" {
			stageRequests.Add(1)
			http.Error(rw, "synthetic stage unavailable", http.StatusConflict)
			return
		}
		var report protocol.AgentReport
		if err := json.NewDecoder(r.Body).Decode(&report); err != nil {
			t.Error(err)
		}
		response := protocol.ControlResponse{ControllerID: w.state.File.ControllerID, Ack: &protocol.IngestAck{UpToSequence: report.Sequence, Committed: true}, ReceiptAcks: []string{j.Envelope.JobID}}
		json.NewEncoder(rw).Encode(response)
	}))
	defer server.Close()
	w.state.File.ControllerURL, w.client = server.URL, server.Client()
	report := stageFailedShutdownReport(w, j)
	original := j.Receipt
	if err := w.send(w.ctx, report); err != nil {
		t.Fatal(err)
	}
	if !j.StageSnapshotAcked || !j.Acked || report.Spark.ModelStates[0].SupervisorState != "failed" {
		t.Fatal("typed physical closure was not committed or failed state erased")
	}
	w.reconcilePending()
	w.reconcilePending()
	if stageRequests.Load() != 1 {
		t.Fatalf("fresh stage requests=%d", stageRequests.Load())
	}
	assertStageNeverAttempted(t, j, original)
}

func TestWorkerFailedShutdownCannotCommitWithoutFullCurrentBinding(t *testing.T) {
	for _, bad := range []string{"missing_shutdown", "numeric_shutdown", "missing_receipt_reason", "receipt_readiness", "receipt_state", "stopped_identity", "ambiguous_identity", "state_reason", "state_readiness", "stale_state", "wrong_generation", "wrong_profile", "wrong_epoch", "wrong_session", "stale_control", "backfill"} {
		t.Run(bad, func(t *testing.T) {
			w, j := stageWorkerFixture(t)
			report := stageFailedShutdownReport(w, j)
			if !restartStopCommitted(report, j) {
				t.Fatal("complete failed closure fixture rejected")
			}
			switch bad {
			case "missing_shutdown":
				delete(j.Receipt.Evidence, "shutdown_verified")
			case "numeric_shutdown":
				j.Receipt.Evidence["shutdown_verified"] = 1
			case "missing_receipt_reason":
				delete(j.Receipt.Evidence, "reason")
			case "receipt_readiness":
				j.Receipt.Evidence["readiness"] = "unknown"
			case "receipt_state":
				j.Receipt.Evidence["supervisor_state"] = "stopped"
			case "stopped_identity":
				j.Receipt.Evidence["instance_identity"] = "stopped"
			case "ambiguous_identity":
				j.Receipt.Evidence["instance_identity"] = "sha256:" + strings.Repeat("Z", 64)
			case "state_reason":
				report.Spark.ModelStates[0].Reason = "failed"
			case "state_readiness":
				report.Spark.ModelStates[0].Readiness = "unknown"
			case "stale_state":
				report.Spark.ModelStates[0].ObservedAt = report.ObservedAt.Add(-protocol.SparkControlFreshness - time.Second)
			case "wrong_generation":
				report.Spark.ModelStates[0].Generation++
			case "wrong_profile":
				report.Spark.Inventory.Profiles[0].ProfileRevision = "old-revision"
			case "wrong_epoch":
				report.Spark.ControlState.Epoch = nil
			case "wrong_session":
				report.SessionID = "old-session"
			case "stale_control":
				report.Spark.ControlState.ObservedAt = report.ObservedAt.Add(-protocol.SparkControlFreshness - time.Second)
			case "backfill":
				report.IsLive = false
			}
			if restartStopCommitted(report, j) {
				t.Fatalf("%s released new start authority", bad)
			}
		})
	}
}
