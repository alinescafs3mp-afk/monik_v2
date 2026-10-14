package spark

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
	"github.com/alinescafs3mp-afk/monik_v2/internal/secure"
	"github.com/alinescafs3mp-afk/monik_v2/internal/server"
	"github.com/alinescafs3mp-afk/monik_v2/internal/storage"
)

type existingCouplingFixture struct {
	coordinator *Coordinator
	backend     *existingFake
	profile     Profile
	app         *server.App
	handler     http.Handler
	owner       *storage.Session
	ownerToken  string
	sequence    int64
}

func newExistingCouplingFixture(t *testing.T) *existingCouplingFixture {
	t.Helper()
	c, backend, p := existingFixture(t)
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	// App exposes its production handler through HTTP after Run initializes it.
	// Reserve its address so Run stops at bind, before spawning any workers. This
	// avoids a second server implementation, readiness polling and background work.
	reserved, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer reserved.Close()
	listen := reserved.Addr().String()
	app, err := server.Open(server.Config{DataDir: dir, Listen: listen, AdvertisedURL: "https://localhost:8777", Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := app.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := app.CompleteSetup(server.SetupRequest{Username: "owner", Password: "coupling-fixture-password", AdvertisedURL: "https://localhost:8777", Listen: listen}); err != nil {
		t.Fatal(err)
	}
	initializeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := app.Run(initializeCtx); !errors.Is(err, syscall.EADDRINUSE) {
		t.Fatalf("expected reserved-address refusal before workers: %v", err)
	}
	if app.HTTP == nil || app.HTTP.Handler == nil {
		t.Fatal("production HTTP handler not initialized")
	}
	if err := app.Store.SetSetting("controller_id", c.Policy.ControllerID); err != nil {
		t.Fatal(err)
	}
	owner, err := app.Store.UserByName("owner")
	if err != nil {
		t.Fatal(err)
	}
	token, session, err := app.Store.CreateSession(owner, time.Hour, 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := app.Store.InsertAgent(&storage.AgentRow{ID: c.Policy.AgentID, Hostname: "coupling-fixture", OS: "linux", Arch: "arm64", DesiredConfig: "{}", DesiredRevision: 1, DesiredHash: "fixture"}, secure.HashToken("coupling-credential")); err != nil {
		t.Fatal(err)
	}
	return &existingCouplingFixture{coordinator: c, backend: backend, profile: p, app: app, handler: app.HTTP.Handler, owner: session, ownerToken: token}
}

func (f *existingCouplingFixture) request(t *testing.T, path string, body any, owner bool) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
	r.Header.Set("Content-Type", "application/json")
	if owner {
		r.AddCookie(&http.Cookie{Name: "monik_session", Value: f.ownerToken})
		r.Header.Set("X-CSRF-Token", f.owner.CSRF)
	} else {
		r.Header.Set("Authorization", "Bearer coupling-credential")
		r.Header.Set("X-Monik-Agent-Id", f.coordinator.Policy.AgentID)
	}
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	return w
}

func (f *existingCouplingFixture) snapshot(t *testing.T) BrokerSnapshot {
	t.Helper()
	snapshot, err := f.coordinator.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func (f *existingCouplingFixture) report(t *testing.T, snapshot BrokerSnapshot, receipts ...protocol.JobReceipt) protocol.ControlResponse {
	t.Helper()
	f.sequence++
	// Inventory, states and control are the actual producer output. The report
	// envelope supplies only transport identity and the synthetic hardware fixture.
	report := protocol.AgentReport{AgentID: f.coordinator.Policy.AgentID, AgentFlavor: "spark-light", SchemaVersion: protocol.SchemaVersion, SessionID: snapshot.AgentSession, Sequence: f.sequence, ObservedAt: time.Now().UTC(), IsLive: true, Capabilities: map[string]protocol.Capability{"spark_control_v1": {Status: protocol.CapSupported}, "spark_hardware_v1": {Status: protocol.CapSupported, Reason: "synthetic GB10 coupling fixture"}}, Spark: &protocol.SparkExtensionV1{SchemaVersion: 1, MemoryTopology: "unified", Metrics: []protocol.SparkMetric{}, Inventory: &snapshot.Inventory, ModelStates: snapshot.States, ControlState: &snapshot.Control}, JobReceipts: receipts}
	w := f.request(t, "/api/v1/agent/report", report, false)
	if w.Code != http.StatusOK {
		t.Fatalf("producer report rejected: %d %s", w.Code, w.Body.String())
	}
	var response protocol.ControlResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Ack == nil || !response.Ack.Committed || response.Ack.UpToSequence != f.sequence {
		t.Fatalf("producer report not committed: %+v", response)
	}
	return response
}

func (f *existingCouplingFixture) start(t *testing.T) (ActionRequest, protocol.JobReceipt, BrokerSnapshot) {
	t.Helper()
	f.report(t, f.snapshot(t))
	request := actionRequest(f.coordinator, f.profile, "fixture-owner-key", "start")
	ownerParams := map[string]any{"profile_id": request.Params.ProfileID, "profile_revision": request.Params.ProfileRevision, "expected_generation": request.Params.ExpectedGeneration, "control_epoch": request.Params.ControlEpoch, "expected_agent_session": request.Params.ExpectedAgentSession, "confirm_disruption": request.Params.ConfirmDisruption}
	w := f.request(t, "/api/v1/operations", protocol.SubmitOperation{Action: request.Action, ClientRequestKey: "coupling-start", TargetIDs: []string{f.coordinator.Policy.AgentID}, Params: ownerParams}, true)
	if w.Code != http.StatusAccepted {
		t.Fatalf("owner start rejected: %d %s", w.Code, w.Body.String())
	}
	var operation protocol.Operation
	if err := json.Unmarshal(w.Body.Bytes(), &operation); err != nil {
		t.Fatal(err)
	}
	response := f.report(t, f.snapshot(t))
	if len(response.Jobs) != 1 || response.Jobs[0].OperationID != operation.ID {
		t.Fatalf("owner operation not dispatched exactly once: %+v", response)
	}
	job := response.Jobs[0]
	params, err := protocol.DecodeModelActionParams(job.Params)
	if err != nil {
		t.Fatal(err)
	}
	request = ActionRequest{JobID: job.JobID, OperationID: job.OperationID, Action: job.Action, Params: params, IssuedAt: job.CreatedAt, Deadline: job.Deadline}
	progress := f.coordinator.Execute(context.Background(), request)
	if progress.Status != protocol.TargetRunning || progress.Stage != "model_starting" {
		t.Fatalf("actual Execute progress: %+v", progress)
	}
	starting := f.snapshot(t)
	if len(starting.States) != 1 || starting.States[0].SupervisorState != "starting" {
		t.Fatalf("actual starting snapshot: %+v", starting)
	}
	response = f.report(t, starting, progress)
	f.assertReceiptAck(t, response, job.JobID, true)
	f.assertTarget(t, operation.ID, protocol.TargetRunning, "")
	return request, progress, starting
}

func (f *existingCouplingFixture) completedStart(t *testing.T, request ActionRequest) protocol.JobReceipt {
	t.Helper()
	f.backend.mu.Lock()
	f.backend.snapshot.State = "READY"
	f.backend.snapshot.ProcessActive = true
	f.backend.snapshot.APIReady = true
	backendReceipt := f.backend.receipts[request.JobID]
	backendReceipt.Phase = "completed"
	result := f.backend.snapshot.existingResult
	backendReceipt.Result = &result
	f.backend.receipts[request.JobID] = backendReceipt
	f.backend.mu.Unlock()
	completed := f.coordinator.Execute(context.Background(), request)
	if completed.Status != protocol.TargetSucceeded || completed.Stage != "model_started" {
		t.Fatalf("actual Execute completed receipt: %+v", completed)
	}
	return completed
}

func (f *existingCouplingFixture) assertTarget(t *testing.T, operationID string, status protocol.TargetStatus, errorCode string) {
	t.Helper()
	operation, err := f.app.Store.Operation(operationID)
	if err != nil {
		t.Fatal(err)
	}
	if len(operation.Targets) != 1 || operation.Targets[0].Status != status || operation.Targets[0].ErrorCode != errorCode {
		t.Fatalf("consumer target: %+v; expected %s/%s", operation, status, errorCode)
	}
}

func (f *existingCouplingFixture) assertReceiptAck(t *testing.T, response protocol.ControlResponse, jobID string, expected bool) {
	t.Helper()
	if len(response.Jobs) != 0 {
		t.Fatalf("receipt handling reissued a job: %+v", response.Jobs)
	}
	if expected {
		if len(response.ReceiptAcks) != 1 || response.ReceiptAcks[0] != jobID {
			t.Fatalf("accepted producer receipt not acknowledged: %+v", response)
		}
	} else if len(response.ReceiptAcks) != 0 {
		t.Fatalf("unproven receipt acknowledged: %+v", response)
	}
}

func TestExistingCoordinatorHTTPReceiptProofCoupling(t *testing.T) {
	f := newExistingCouplingFixture(t)
	request, _, starting := f.start(t)
	completed := f.completedStart(t, request)
	// A valid producer success reaches the real consumer before a running sample.
	// It must pass receipt parsing, retain its exact bytes, and await proof.
	response := f.report(t, starting, completed)
	f.assertReceiptAck(t, response, request.JobID, false)
	f.assertTarget(t, request.OperationID, protocol.TargetUnknownResult, "missing_evidence")
	var stored string
	if err := f.app.Store.DB.QueryRow(`SELECT result FROM agent_jobs WHERE job_id=?`, request.JobID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	want, err := json.Marshal(completed)
	if err != nil {
		t.Fatal(err)
	}
	if stored != string(want) {
		t.Fatalf("consumer did not retain exact producer JSON: %s; want %s", stored, want)
	}
	// The next actual Coordinator snapshot proves the stored original even when
	// the success receipt is absent, then its idempotent disclosure receives ACK.
	f.report(t, f.snapshot(t))
	f.assertTarget(t, request.OperationID, protocol.TargetSucceeded, "")
	response = f.report(t, f.snapshot(t), completed)
	f.assertReceiptAck(t, response, request.JobID, true)
	f.backend.mu.Lock()
	defer f.backend.mu.Unlock()
	if len(f.backend.submitted) != 1 {
		t.Fatalf("completion resubmitted backend effect: %+v", f.backend.submitted)
	}
}

func TestExistingCoordinatorHTTPRejectsForeignReceiptProof(t *testing.T) {
	for _, field := range []string{"generation", "profile_id", "agent_session"} {
		t.Run(field, func(t *testing.T) {
			f := newExistingCouplingFixture(t)
			request, _, _ := f.start(t)
			completed := f.completedStart(t, request)
			raw, err := json.Marshal(completed)
			if err != nil {
				t.Fatal(err)
			}
			var foreign protocol.JobReceipt
			if err := json.Unmarshal(raw, &foreign); err != nil {
				t.Fatal(err)
			}
			switch field {
			case "generation":
				foreign.Evidence[field] = foreign.Evidence[field].(float64) + 1
			case "profile_id":
				foreign.Evidence[field] = existingIDs[1]
			case "agent_session":
				foreign.Evidence[field] = "foreign-session"
			}
			response := f.report(t, f.snapshot(t), foreign)
			f.assertReceiptAck(t, response, request.JobID, false)
			f.assertTarget(t, request.OperationID, protocol.TargetUnknownResult, "missing_evidence")
			f.report(t, f.snapshot(t))
			f.assertTarget(t, request.OperationID, protocol.TargetUnknownResult, "missing_evidence")
			response = f.report(t, f.snapshot(t), completed)
			f.assertReceiptAck(t, response, request.JobID, true)
			f.assertTarget(t, request.OperationID, protocol.TargetSucceeded, "")
		})
	}
}

func TestExistingCoordinatorHTTPRestartKeepsOriginalParentAndFreshChild(t *testing.T) {
	f := newExistingCouplingFixture(t)
	f.backend.mu.Lock()
	run := strings.Repeat("8", 32)
	generation := int64(8)
	f.backend.snapshot.Generation = &generation
	f.backend.snapshot.RunID = &run
	f.backend.snapshot.Instance = &existingPhysicalInstance{RunID: &run}
	f.backend.snapshot.State = "READY"
	f.backend.snapshot.ProcessActive = true
	f.backend.snapshot.APIReady = true
	f.backend.mu.Unlock()
	f.report(t, f.snapshot(t))
	original := actionRequest(f.coordinator, f.profile, "restart-key", "restart")
	params := map[string]any{"profile_id": original.Params.ProfileID, "profile_revision": original.Params.ProfileRevision, "expected_generation": original.Params.ExpectedGeneration, "control_epoch": original.Params.ControlEpoch, "expected_agent_session": original.Params.ExpectedAgentSession, "confirm_disruption": true}
	response := f.request(t, "/api/v1/operations", protocol.SubmitOperation{Action: "model.restart", ClientRequestKey: "coupling-restart", TargetIDs: []string{f.coordinator.Policy.AgentID}, Params: params}, true)
	if response.Code != http.StatusAccepted {
		t.Fatalf("restart request: %d %s", response.Code, response.Body.String())
	}
	var operation protocol.Operation
	if err := json.Unmarshal(response.Body.Bytes(), &operation); err != nil {
		t.Fatal(err)
	}
	dispatched := f.report(t, f.snapshot(t))
	if len(dispatched.Jobs) != 1 {
		t.Fatal(dispatched)
	}
	job := dispatched.Jobs[0]
	originalParams, err := protocol.DecodeModelActionParams(job.Params)
	if err != nil {
		t.Fatal(err)
	}
	parent := ActionRequest{JobID: job.JobID, OperationID: job.OperationID, Action: job.Action, Params: originalParams, IssuedAt: job.CreatedAt, Deadline: job.Deadline}
	stopped := f.coordinator.Execute(context.Background(), parent)
	if stopped.Stage != "restart_stopped" {
		t.Fatal(stopped)
	}
	stoppedReport := f.report(t, f.snapshot(t), stopped)
	f.assertReceiptAck(t, stoppedReport, parent.JobID, true)
	stageResponse := f.request(t, "/api/v1/agent/spark/stage", map[string]string{"operation_id": operation.ID, "job_id": parent.JobID}, false)
	if stageResponse.Code != http.StatusOK {
		t.Fatalf("fresh stage: %d %s", stageResponse.Code, stageResponse.Body.String())
	}
	var stage protocol.SparkStageAuthorization
	if err = json.Unmarshal(stageResponse.Body.Bytes(), &stage); err != nil {
		t.Fatal(err)
	}
	if stage.StageID == parent.JobID || stage.ParentOperationID != parent.OperationID {
		t.Fatal("stage not distinct/linked")
	}
	child := ActionRequest{JobID: stage.StageID, OperationID: parent.OperationID, Action: "model.start", Params: stage.Params, IssuedAt: stage.IssuedAt, Deadline: stage.Deadline, ParentJobID: parent.JobID}
	if started := f.coordinator.Execute(context.Background(), child); started.Status != protocol.TargetRunning {
		t.Fatal(started)
	}
	progress := f.coordinator.Execute(context.Background(), parent)
	if progress.Status != protocol.TargetRunning || progress.Stage != "restart_start_attempted" {
		t.Fatal(progress)
	}
	f.assertReceiptAck(t, f.report(t, f.snapshot(t), progress), parent.JobID, true)
	_ = f.completedStart(t, child)
	completed := f.coordinator.Execute(context.Background(), parent)
	if completed.Status != protocol.TargetSucceeded || completed.Stage != "model_restarted" || completed.Evidence["stage_id"] != child.JobID {
		t.Fatal(completed)
	}
	f.assertReceiptAck(t, f.report(t, f.snapshot(t), completed), parent.JobID, true)
	f.assertTarget(t, parent.OperationID, protocol.TargetSucceeded, "")
	if len(f.backend.submitted) != 2 || f.backend.submitted[0].OperationID == f.backend.submitted[1].OperationID {
		t.Fatal("restart stages replayed or collapsed")
	}
}
