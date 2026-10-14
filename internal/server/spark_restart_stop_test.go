package server

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
)

func (f *sparkAPIFixture) restartStopReport(t *testing.T, state string, generation int64, receipts ...protocol.JobReceipt) protocol.ControlResponse {
	t.Helper()
	f.clock.Advance(time.Second)
	f.report.Sequence++
	f.report.ObservedAt = f.clock.Now()
	f.report.Spark.ControlState.ObservedAt = f.clock.Now()
	f.report.Spark.ModelStates[0].ObservedAt = f.clock.Now()
	f.report.Spark.ModelStates[0].SupervisorState = state
	f.report.Spark.ModelStates[0].Generation = generation
	f.report.JobReceipts = receipts
	w := f.request(t, "POST", "/api/v1/agent/report", f.report, "agent")
	if w.Code != 200 {
		t.Fatalf("agent report: %d %s", w.Code, w.Body.String())
	}
	var response protocol.ControlResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	return response
}

func (f *sparkAPIFixture) restartStopParams() map[string]any {
	p := f.params()
	p["expected_generation"] = f.report.Spark.ModelStates[0].Generation
	return p
}

func sparkAPIAuthorizedRestart(t *testing.T, state string, generation int64) (*sparkAPIFixture, protocol.Operation, protocol.JobEnvelope, protocol.SparkStageAuthorization) {
	t.Helper()
	f := newSparkAPI(t, true)
	f.restartStopReport(t, "running", 1)
	request := map[string]any{"action": "model.restart", "client_request_key": "restart-key", "target_ids": []string{"spark"}, "params": f.restartStopParams()}
	w := f.request(t, "POST", "/api/v1/operations", request, "owner")
	if w.Code != 202 {
		t.Fatalf("restart submit: %d %s", w.Code, w.Body.String())
	}
	var parent protocol.Operation
	if err := json.Unmarshal(w.Body.Bytes(), &parent); err != nil {
		t.Fatal(err)
	}
	response := f.restartStopReport(t, "running", 1)
	if len(response.Jobs) != 1 || response.Jobs[0].OperationID != parent.ID {
		t.Fatalf("restart dispatch: %+v", response)
	}
	job := response.Jobs[0]
	receipt := protocol.JobReceipt{JobID: job.JobID, OperationID: parent.ID, Status: protocol.TargetAwaitingConfirmation, Stage: "restart_stopped", Evidence: map[string]any{"profile_id": "p", "profile_revision": parent.Params["profile_revision"], "control_epoch": "epoch", "agent_session": "session", "generation": float64(2)}}
	response = f.restartStopReport(t, "stopped", 2, receipt)
	if len(response.ReceiptAcks) != 1 || response.ReceiptAcks[0] != job.JobID {
		t.Fatalf("restart stop receipt: %+v", response)
	}
	w = f.request(t, "POST", "/api/v1/agent/spark/stage", map[string]string{"operation_id": parent.ID, "job_id": job.JobID}, "agent")
	if w.Code != 200 {
		t.Fatalf("fresh stage: %d %s", w.Code, w.Body.String())
	}
	var auth protocol.SparkStageAuthorization
	if err := json.Unmarshal(w.Body.Bytes(), &auth); err != nil {
		t.Fatal(err)
	}
	if auth.StageID == job.JobID || auth.Params.ExpectedGeneration != 2 {
		t.Fatal("restart did not acquire a distinct bound stage")
	}
	f.restartStopReport(t, state, generation)
	return f, parent, job, auth
}

func TestSparkStageResponsePreservesLeaseAndReportsCurrentServerTime(t *testing.T) {
	f, parent, job, auth := sparkAPIAuthorizedRestart(t, "stopped", 2)
	w := f.request(t, "POST", "/api/v1/agent/spark/stage", map[string]string{"operation_id": parent.ID, "job_id": job.JobID}, "agent")
	if w.Code != 200 {
		t.Fatalf("unexpired stage replay: %d %s", w.Code, w.Body.String())
	}
	serverTime, err := time.Parse(time.RFC3339Nano, w.Header().Get("X-Monik-Server-Time"))
	if err != nil || !serverTime.Equal(f.clock.Now()) || !serverTime.After(auth.IssuedAt) {
		t.Fatalf("response did not authenticate its current time: %s %v", w.Header().Get("X-Monik-Server-Time"), err)
	}
	var same protocol.SparkStageAuthorization
	if err = json.Unmarshal(w.Body.Bytes(), &same); err != nil || !same.IssuedAt.Equal(auth.IssuedAt) || !same.Deadline.Equal(auth.Deadline) {
		t.Fatal("stage replay recreated its original lease", err)
	}
	f.clock.Advance(protocol.SparkDispatchLease)
	w = f.request(t, "POST", "/api/v1/agent/spark/stage", map[string]string{"operation_id": parent.ID, "job_id": job.JobID}, "agent")
	if w.Code != 409 || !strings.Contains(w.Body.String(), "stage_authorization_expired") {
		t.Fatalf("expired cached stage regained authority: %d %s", w.Code, w.Body.String())
	}
}

func TestSparkAPINoStartFailurePreservesStopAndAllowsSeparateCleanup(t *testing.T) {
	f, parent, job, _ := sparkAPIAuthorizedRestart(t, "stopped", 2)
	var raw string
	if err := f.app.Store.DB.QueryRow(`SELECT result FROM agent_jobs WHERE job_id=?`, job.JobID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var failure protocol.JobReceipt
	if err := json.Unmarshal([]byte(raw), &failure); err != nil {
		t.Fatal(err)
	}
	failure.Status = protocol.TargetFailed
	failure.Stage = "restart_start_not_attempted"
	failure.ErrorCode = "invalid_stage_authorization"
	failure.Message = "This restart's start stage was not attempted"
	failure.Evidence = map[string]any{"restart_stop_receipt": raw, "start_stage_attempted": false}
	response := f.restartStopReport(t, "stopped", 2, failure)
	if len(response.ReceiptAcks) != 1 || response.ReceiptAcks[0] != job.JobID {
		t.Fatal("genuine agent report did not acknowledge the no-start result")
	}
	got, err := f.app.Store.Operation(parent.ID)
	if err != nil || got.Targets[0].Status != protocol.TargetFailed || got.Targets[0].Evidence["restart_stop_receipt"] != raw {
		t.Fatalf("old stop evidence was lost: %+v %v", got, err)
	}
	request := map[string]any{"action": "model.stop", "client_request_key": "after-no-start-failure", "target_ids": []string{"spark"}, "params": f.restartStopParams()}
	w := f.request(t, "POST", "/api/v1/operations", request, "owner")
	if w.Code != 202 {
		t.Fatalf("truthfully failed restart still blocks a separately validated cleanup: %d %s", w.Code, w.Body.String())
	}
}

func TestSparkAPISafetyStopInterruptsAuthorizedRestartStartup(t *testing.T) {
	for _, state := range []string{"starting", "running"} {
		t.Run(state, func(t *testing.T) {
			generation := int64(3)
			f, parent, parentJob, auth := sparkAPIAuthorizedRestart(t, state, generation)
			request := map[string]any{"action": "model.stop", "client_request_key": "stop-key", "target_ids": []string{"spark"}, "params": f.restartStopParams()}
			w := f.request(t, "POST", "/api/v1/operations", request, "viewer")
			if w.Code != 403 {
				t.Fatal("viewer admitted disruptive startup stop", w.Code)
			}
			w = f.request(t, "POST", "/api/v1/operations", request, "owner")
			if w.Code != 202 {
				t.Fatalf("safe stop blocked during restart %s: %d %s", state, w.Code, w.Body.String())
			}
			var stop protocol.Operation
			if err := json.Unmarshal(w.Body.Bytes(), &stop); err != nil {
				t.Fatal(err)
			}
			got, err := f.app.Store.Operation(parent.ID)
			if err != nil || got.Targets[0].Stage != "restart_start_superseded" || got.Targets[0].Status != protocol.TargetFailed {
				t.Fatalf("restart claimed stop outcome: %+v %v", got, err)
			}
			w = f.request(t, "POST", "/api/v1/operations", request, "owner")
			if w.Code != 202 || !strings.Contains(w.Body.String(), stop.ID) {
				t.Fatal("lost stop response duplicated operation", w.Code, w.Body.String())
			}
			w = f.request(t, "POST", "/api/v1/agent/spark/stage", map[string]string{"operation_id": parent.ID, "job_id": parentJob.JobID}, "agent")
			if w.Code != 409 {
				t.Fatal("superseded start authority replayed", w.Code, w.Body.String())
			}
			late := protocol.JobReceipt{JobID: parentJob.JobID, OperationID: parent.ID, Status: protocol.TargetSucceeded, Stage: "model_restarted", Evidence: map[string]any{"profile_id": "p", "profile_revision": parent.Params["profile_revision"], "control_epoch": "epoch", "agent_session": "session", "generation": float64(generation), "stage_id": auth.StageID}}
			response := f.restartStopReport(t, state, generation, late)
			if len(response.ReceiptAcks) != 1 || response.ReceiptAcks[0] != parentJob.JobID || len(response.Jobs) != 1 || response.Jobs[0].OperationID != stop.ID || response.Jobs[0].Action != "model.stop" {
				t.Fatalf("late parent revived / separate stop missing: %+v", response)
			}
			stopJob := response.Jobs[0]
			final := protocol.JobReceipt{JobID: stopJob.JobID, OperationID: stop.ID, Status: protocol.TargetSucceeded, Stage: "model_stopped", Evidence: map[string]any{"profile_id": "p", "profile_revision": stop.Params["profile_revision"], "control_epoch": "epoch", "agent_session": "session", "generation": float64(generation + 1)}}
			response = f.restartStopReport(t, "stopped", generation+1, final)
			if len(response.ReceiptAcks) != 1 || response.ReceiptAcks[0] != stopJob.JobID || len(response.Jobs) != 0 {
				t.Fatalf("stop receipt not proven separately: %+v", response)
			}
			got, _ = f.app.Store.Operation(parent.ID)
			stopResult, err := f.app.Store.Operation(stop.ID)
			if err != nil || stopResult.Status != protocol.OpCompleted || got.Targets[0].Stage != "restart_start_superseded" {
				t.Fatalf("parent/stop outcomes conflated: parent=%+v stop=%+v err=%v", got, stopResult, err)
			}
			f.clock.Advance(16 * time.Second)
			w = f.request(t, "POST", "/api/v1/operations", request, "owner")
			if w.Code != 200 || !strings.Contains(w.Body.String(), stop.ID) {
				t.Fatal("completed stale duplicate lost original stop", w.Code, w.Body.String())
			}
			var count int
			if err = f.app.Store.DB.QueryRow(`SELECT COUNT(*) FROM agent_jobs`).Scan(&count); err != nil || count != 2 {
				t.Fatal("stop/restart created duplicate effect", count, err)
			}
		})
	}
}

func TestSparkAPIRestartSafetyStopRejectsForeignTargetAndGeneration(t *testing.T) {
	for _, kind := range []string{"wrong_profile", "stale_generation", "foreign_generation", "stopped_pending_generation", "unconfirmed_disruption"} {
		t.Run(kind, func(t *testing.T) {
			f, parent, _, _ := sparkAPIAuthorizedRestart(t, "starting", 3)
			params := f.restartStopParams()
			switch kind {
			case "wrong_profile":
				params["profile_id"] = "foreign-profile"
			case "stale_generation":
				params["expected_generation"] = 2
			case "foreign_generation":
				f.restartStopReport(t, "starting", 4)
				params = f.restartStopParams()
			case "stopped_pending_generation":
				f.restartStopReport(t, "stopped", 2)
				params = f.restartStopParams()
			case "unconfirmed_disruption":
				params["confirm_disruption"] = false
			}
			request := map[string]any{"action": "model.stop", "client_request_key": "stop-key", "target_ids": []string{"spark"}, "params": params}
			w := f.request(t, "POST", "/api/v1/operations", request, "owner")
			if w.Code != 409 {
				t.Fatalf("foreign stop admitted: %d %s", w.Code, w.Body.String())
			}
			got, err := f.app.Store.Operation(parent.ID)
			if err != nil || got.Targets[0].Status != protocol.TargetAwaitingConfirmation {
				t.Fatalf("rejected stop canceled original restart: %+v %v", got, err)
			}
		})
	}
}
