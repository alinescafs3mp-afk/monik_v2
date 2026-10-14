package spark

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/agent/configfile"
	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
)

func workerFixture(t *testing.T, features bool) (*Worker, *[]protocol.AgentReport, *sync.Mutex) {
	t.Helper()
	reports := []protocol.AgentReport{}
	mu := &sync.Mutex{}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/agent/report" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer fixture-credential" || r.Header.Get("X-Monik-Agent-Id") != "fixture-agent" {
			t.Error("auth headers missing")
			http.Error(w, "deny", 401)
			return
		}
		var report protocol.AgentReport
		if json.NewDecoder(r.Body).Decode(&report) != nil {
			http.Error(w, "invalid report", 400)
			return
		}
		if report.SchemaVersion != 3 || report.AgentID != "fixture-agent" {
			t.Error("identity drift")
		}
		if e := protocol.ValidateSpark(report.Spark, report.ObservedAt, time.Now()); e != nil {
			t.Errorf("invalid generated extension: %v", e)
		}
		mu.Lock()
		reports = append(reports, report)
		mu.Unlock()
		response := protocol.ControlResponse{ControllerID: "fixture-controller", ServerTime: time.Now(), Ack: &protocol.IngestAck{Committed: true, UpToSequence: report.Sequence}}
		if features {
			response.Features = protocol.SparkFeatures
		}
		json.NewEncoder(w).Encode(response)
	}))
	t.Cleanup(server.Close)
	certificate, e := x509.ParseCertificate(server.TLS.Certificates[0].Certificate[0])
	if e != nil {
		t.Fatal(e)
	}
	dir := t.TempDir()
	credential := filepath.Join(dir, "credential")
	if e = os.WriteFile(credential, []byte("fixture-credential"), 0600); e != nil {
		t.Fatal(e)
	}
	state := configfile.File{SchemaVersion: 3, AgentID: "fixture-agent", ControllerID: "fixture-controller", ControllerURL: server.URL, CACertPEM: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Raw})), CredentialPath: credential, StateDir: dir, EndpointGeneration: 1}
	cfgpath := filepath.Join(dir, "agent.json")
	b, _ := json.Marshal(state)
	if e = os.WriteFile(cfgpath, b, 0600); e != nil {
		t.Fatal(e)
	}
	worker, e := OpenWorker(cfgpath, "")
	if e != nil {
		t.Fatal(e)
	}
	worker.ctx, worker.cancel = context.WithCancel(context.Background())
	worker.cfg.Paused = true
	worker.hash = configfile.HashConfig(worker.cfg)
	t.Cleanup(func() { worker.cancel(); worker.wg.Wait(); worker.unlock(); worker.client.CloseIdleConnections() })
	return worker, &reports, mu
}
func TestWorkerNegotiationAndOldControllerBasicFallback(t *testing.T) {
	for _, support := range []bool{false, true} {
		t.Run(fmt.Sprint(support), func(t *testing.T) {
			worker, reports, mu := workerFixture(t, support)
			worker.cfg.Paused = false
			worker.cfg.Checks = nil
			worker.discovering = true
			host, caps, extension := worker.collector.Snapshot(time.Now().UTC())
			worker.collection = collectedSnapshot{host: host, caps: caps, spark: extension, at: time.Now()}
			worker.collectRunning = true
			worker.tick()
			worker.tick()
			mu.Lock()
			defer mu.Unlock()
			if len(*reports) != 2 {
				t.Fatalf("reports %d", len(*reports))
			}
			if (*reports)[0].Spark != nil {
				t.Fatal("extension sent before authenticated negotiation")
			}
			if ((*reports)[1].Spark != nil) != support {
				t.Fatal("ACK confused with feature negotiation")
			}
			if (*reports)[1].AgentFlavor != "spark-light" || (*reports)[1].Host == nil {
				t.Fatal("basic host/flavor missing")
			}
			if (*reports)[0].Host.CPUPercent != nil {
				t.Fatal("first CPU sample fabricated")
			}
		})
	}
}
func TestWorkerRejectsDuplicateLiveWorker(t *testing.T) {
	worker, _, _ := workerFixture(t, false)
	if another, e := OpenWorker(filepath.Join(worker.state.File.StateDir, "agent.json"), ""); e == nil {
		another.unlock()
		t.Fatal("second worker acquired same identity")
	}
}

func stageWorkerFixture(t *testing.T) (*Worker, *workerJob) {
	t.Helper()
	w, _, _ := workerFixture(t, true)
	params := protocol.ModelActionParams{ProfileID: "synthetic-ready", ProfileRevision: "sha256:684ece91b769a64be0184e02d2be5976d7858083f0d9c44c361a1692fb6f84b2", ExpectedGeneration: 3, ControlEpoch: "0f1f34fb-511a-4dac-9878-c0ce414f02cf", ExpectedAgentSession: w.session, ConfirmDisruption: true}
	j := &workerJob{
		Envelope: protocol.JobEnvelope{JobID: "6f19d442-04c7-4efe-9e03-637b8450207f", OperationID: "0f177618-c5a7-4d86-925c-963fd34f2bc7", Action: "model.restart"},
		Request:  &ActionRequest{Action: "model.restart", Params: params},
		Receipt:  protocol.JobReceipt{JobID: "6f19d442-04c7-4efe-9e03-637b8450207f", OperationID: "0f177618-c5a7-4d86-925c-963fd34f2bc7", Status: protocol.TargetAwaitingConfirmation, Stage: "restart_stopped", Evidence: map[string]any{"profile_id": params.ProfileID, "profile_revision": params.ProfileRevision, "agent_session": w.session, "control_epoch": params.ControlEpoch, "generation": float64(4), "supervisor_state": "stopped", "instance_identity": "stopped"}},
		Acked:    true, StageSnapshotAcked: true,
	}
	w.jobs[j.Envelope.JobID] = j
	return w, j
}

func assertStageNeverAttempted(t *testing.T, j *workerJob, original protocol.JobReceipt) {
	t.Helper()
	if j.StageRequest != nil || j.Receipt.Status != protocol.TargetFailed || j.Receipt.Stage != "restart_start_not_attempted" || j.Acked || !j.RenewalAttempted {
		t.Fatalf("unattempted later start did not close truthfully: %+v", j)
	}
	encoded, ok := j.Receipt.Evidence["restart_stop_receipt"].(string)
	var stopped protocol.JobReceipt
	if !ok || json.Unmarshal([]byte(encoded), &stopped) != nil || j.Receipt.Evidence["start_stage_attempted"] != false || digest(stopped) != digest(original) {
		t.Fatal("start failure erased the original stop receipt or claimed a start effect")
	}
}

func TestWorkerNativeStageLeaseContractAndFailureRecovery(t *testing.T) {
	for _, extra := range []time.Duration{0, 33 * time.Nanosecond} {
		t.Run(extra.String(), func(t *testing.T) {
			w, j := stageWorkerFixture(t)
			original := j.Receipt
			// Exact native receipt timestamps expose the strict consumer boundary.
			issuedAt, err := time.Parse(time.RFC3339Nano, "2026-10-14T12:17:59.445689895Z")
			if err != nil {
				t.Fatal(err)
			}
			if extra == 0 {
				issuedAt = time.Now().UTC()
			}
			params := j.Request.Params
			params.ExpectedGeneration = 4
			auth := protocol.SparkStageAuthorization{ParentOperationID: j.Envelope.OperationID, StageID: "e9c31886-9ffc-4457-8981-34662226480e", Stage: "start", Params: params, IssuedAt: issuedAt, Deadline: issuedAt.Add(protocol.SparkDispatchLease + extra)}
			var requests atomic.Int64
			server := httptest.NewTLSServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.URL.Path != "/api/v1/agent/spark/stage" || r.Header.Get("Authorization") != "Bearer fixture-credential" {
					t.Error("stage request identity drift")
				}
				rw.Header().Set("X-Monik-Server-Time", issuedAt.Format(time.RFC3339Nano))
				json.NewEncoder(rw).Encode(auth)
			}))
			defer server.Close()
			w.state.File.ControllerURL = server.URL
			w.client = server.Client()
			w.reconcilePending()
			w.wg.Wait()
			if requests.Load() != 1 {
				t.Fatalf("stage issuance requests: %d", requests.Load())
			}
			if extra == 0 {
				// No broker is configured: preparation succeeds, then an unknown
				// broker result must not be converted to no-effect failure.
				if j.StageRequest == nil || j.Receipt.Status != protocol.TargetUnknownResult {
					t.Fatalf("valid maximum lease was refused: %+v", j)
				}
				return
			}
			assertStageNeverAttempted(t, j, original)
			if j.Receipt.ErrorCode != "invalid_stage_authorization" {
				t.Fatal("over-budget native lease was not rejected")
			}
			w.reconcilePending()
			if requests.Load() != 1 {
				t.Fatal("invalid stage renewal was repeated")
			}
		})
	}
}

func TestWorkerRecoveredStageRenewalDoesNotReplayOrInventEffect(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			w, j := stageWorkerFixture(t)
			original := j.Receipt
			j.RenewalAttempted = true
			if prepared {
				j.StageRequest = &ActionRequest{JobID: "durably-prepared-stage", Action: "model.start"}
			}
			if err := w.saveJobsLocked(); err != nil {
				t.Fatal(err)
			}
			// Recover the actual durable representation, as OpenWorker does.
			data, err := os.ReadFile(filepath.Join(w.state.File.StateDir, "spark-receipts.json"))
			if err != nil || json.Unmarshal(data, &w.jobs) != nil {
				t.Fatal("cannot recover receipt journal", err)
			}
			j = w.jobs[j.Envelope.JobID]
			w.reconcilePending()
			if prepared {
				if j.Receipt.Status != protocol.TargetAwaitingConfirmation || j.Receipt.Stage != "restart_stopped" {
					t.Fatal("possibly attempted stage was falsely classified as no effect")
				}
			} else {
				assertStageNeverAttempted(t, j, original)
			}
		})
	}
}

func TestWorkerActiveStageRenewalCannotBeClosedByReconciliation(t *testing.T) {
	w, j := stageWorkerFixture(t)
	original := j.Receipt
	entered := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewTLSServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		http.Error(rw, "authorization denied", http.StatusConflict)
	}))
	defer server.Close()
	w.state.File.ControllerURL = server.URL
	w.client = server.Client()
	done := make(chan struct{})
	go func() { w.renewStage(j.Envelope.JobID); close(done) }()
	<-entered
	w.reconcilePending()
	w.mu.Lock()
	active := j.stageRenewing && j.Receipt.Status == protocol.TargetAwaitingConfirmation && j.Acked
	w.mu.Unlock()
	close(release)
	<-done
	if !active {
		t.Fatal("in-flight renewal was falsely closed as unattempted")
	}
	assertStageNeverAttempted(t, j, original)
}

func TestWorkerStageFailureRequiresDurableReceiptPublication(t *testing.T) {
	w, j := stageWorkerFixture(t)
	original := j.Receipt
	j.RenewalAttempted = true
	dir := w.state.File.StateDir
	notDirectory := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(notDirectory, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	w.state.File.StateDir = notDirectory
	w.reconcilePending()
	if digest(j.Receipt) != digest(original) || !j.Acked {
		t.Fatal("unsaved terminal receipt became eligible for upload")
	}
	w.state.File.StateDir = dir
	w.reconcilePending()
	assertStageNeverAttempted(t, j, original)
	data, err := os.ReadFile(filepath.Join(dir, "spark-receipts.json"))
	var journal map[string]*workerJob
	if err != nil || json.Unmarshal(data, &journal) != nil || digest(journal[j.Envelope.JobID].Receipt) != digest(j.Receipt) {
		t.Fatal("terminal recovery receipt was not durable", err)
	}
}

func stageStoppedReport(w *Worker, j *workerJob) protocol.AgentReport {
	now := time.Now().UTC()
	p := j.Request.Params
	epoch := p.ControlEpoch
	return protocol.AgentReport{SchemaVersion: 3, AgentID: w.state.File.AgentID, SessionID: w.session, Sequence: 1, ObservedAt: now, ReportedAt: now, IsLive: true, JobReceipts: []protocol.JobReceipt{j.Receipt}, Spark: &protocol.SparkExtensionV1{
		SchemaVersion: 1, MemoryTopology: "unified", Metrics: []protocol.SparkMetric{},
		Inventory:    &protocol.SparkInventory{Profiles: []protocol.SparkProfile{{ProfileID: p.ProfileID, ProfileRevision: p.ProfileRevision, Settings: []protocol.SparkSetting{}, AllowedActions: []string{"start", "stop", "restart"}}}},
		ControlState: &protocol.SparkControlState{Enabled: true, Epoch: &epoch, ObservedAt: now},
		ModelStates:  []protocol.SparkModelState{{ProfileID: p.ProfileID, Generation: 4, SupervisorState: "stopped", Quality: "ok", ObservedAt: now}},
	}}
}

func TestWorkerRestartStageWaitsForCommittedStoppedSnapshot(t *testing.T) {
	for _, bad := range []string{"running_snapshot", "wrong_generation", "wrong_revision", "wrong_epoch", "wrong_session", "stale_state", "backfill", "report_uncommitted", "receipt_unacked"} {
		t.Run(bad, func(t *testing.T) {
			w, j := stageWorkerFixture(t)
			j.StageSnapshotAcked = false
			j.Acked = false
			var stageRequests atomic.Int64
			server := httptest.NewTLSServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/v1/agent/spark/stage" {
					stageRequests.Add(1)
					http.Error(rw, "fixture stage denied", http.StatusConflict)
					return
				}
				var report protocol.AgentReport
				if err := json.NewDecoder(r.Body).Decode(&report); err != nil {
					t.Error(err)
				}
				response := protocol.ControlResponse{ControllerID: w.state.File.ControllerID, Ack: &protocol.IngestAck{UpToSequence: report.Sequence, Committed: true}, ReceiptAcks: []string{j.Envelope.JobID}}
				if report.Sequence == 1 {
					if bad == "report_uncommitted" {
						response.Ack.Committed = false
					}
					if bad == "receipt_unacked" {
						response.ReceiptAcks = nil
					}
				}
				json.NewEncoder(rw).Encode(response)
			}))
			defer server.Close()
			w.state.File.ControllerURL = server.URL
			w.client = server.Client()
			report := stageStoppedReport(w, j)
			switch bad {
			case "running_snapshot":
				report.Spark.ModelStates[0].SupervisorState = "running"
			case "wrong_generation":
				report.Spark.ModelStates[0].Generation = 3
			case "wrong_revision":
				report.Spark.Inventory.Profiles[0].ProfileRevision = "other"
			case "wrong_epoch":
				report.Spark.ControlState.Epoch = nil
			case "wrong_session":
				report.SessionID = "other"
			case "stale_state":
				report.Spark.ModelStates[0].ObservedAt = report.ObservedAt.Add(-protocol.SparkControlFreshness - time.Second)
			case "backfill":
				report.IsLive = false
			}
			err := w.send(w.ctx, report)
			if (err != nil) != (bad == "report_uncommitted") {
				t.Fatalf("report response: %v", err)
			}
			w.reconcilePending()
			if j.RenewalAttempted || stageRequests.Load() != 0 {
				t.Fatal("receipt ACK or mismatched/backfill state issued the one-shot stage")
			}
			report = stageStoppedReport(w, j)
			report.Sequence = 2
			if err = w.send(w.ctx, report); err != nil {
				t.Fatal(err)
			}
			w.reconcilePending()
			if !j.RenewalAttempted || stageRequests.Load() != 1 {
				t.Fatal("committed matching stopped state did not release exactly one stage issuance")
			}
		})
	}
}

func TestWorkerStageAuthorizationAgeCannotRenewOriginalLease(t *testing.T) {
	for _, bad := range []string{"server_expired", "local_expired", "header_missing", "header_before_issue", "malformed_json", "transport_failure", "delayed_response"} {
		t.Run(bad, func(t *testing.T) {
			w, j := stageWorkerFixture(t)
			original := j.Receipt
			issuedAt := time.Now().UTC()
			params := j.Request.Params
			params.ExpectedGeneration = 4
			auth := protocol.SparkStageAuthorization{ParentOperationID: j.Envelope.OperationID, StageID: "e9c31886-9ffc-4457-8981-34662226480e", Stage: "start", Params: params, IssuedAt: issuedAt, Deadline: issuedAt.Add(protocol.SparkDispatchLease)}
			serverTime := issuedAt
			switch bad {
			case "server_expired":
				serverTime = auth.Deadline.Add(time.Nanosecond)
			case "local_expired":
				auth.IssuedAt = issuedAt.Add(-time.Minute)
				auth.Deadline = auth.IssuedAt.Add(protocol.SparkDispatchLease)
				serverTime = auth.IssuedAt
			case "header_before_issue":
				serverTime = issuedAt.Add(-time.Nanosecond)
			case "delayed_response":
				auth.Deadline = issuedAt.Add(40 * time.Millisecond)
			}
			server := httptest.NewTLSServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
				if bad != "header_missing" {
					rw.Header().Set("X-Monik-Server-Time", serverTime.Format(time.RFC3339Nano))
				}
				if bad == "delayed_response" {
					time.Sleep(100 * time.Millisecond)
				}
				if bad == "malformed_json" {
					rw.Write([]byte("invalid json"))
					return
				}
				json.NewEncoder(rw).Encode(auth)
			}))
			defer server.Close()
			w.state.File.ControllerURL = server.URL
			w.client = server.Client()
			if bad == "transport_failure" {
				server.Close()
			}
			w.reconcilePending()
			assertStageNeverAttempted(t, j, original)
		})
	}
}

func TestWorkerRecoveredStagePreservesMaximumOrdinaryStopEvidence(t *testing.T) {
	w, j := stageWorkerFixture(t)
	j.RenewalAttempted = true
	for _, key := range []string{"stage_id", "observed_at", "readiness", "no_op", "effect_attempted", "reason", "stop_observed", "start_observed"} {
		j.Receipt.Evidence[key] = strings.Repeat("x", 1024)
	}
	original := j.Receipt
	w.reconcilePending()
	assertStageNeverAttempted(t, j, original)
	if len(j.Receipt.Evidence) != 2 {
		t.Fatal("recovery unnecessarily duplicates bounded stop metadata")
	}
}
func TestMemAvailableMissingIsExplicitUnknownNotZero(t *testing.T) {
	m, e := ParseMeminfo(strings.NewReader("MemTotal: 100 kB\nMemFree: 90 kB\nSwapTotal: 0 kB\nSwapFree: 0 kB\n"))
	if e != nil {
		t.Fatal(e)
	}
	if m.AvailableBytes != nil || m.UsedPressureBytes != nil {
		t.Fatal("free guessed as available")
	}
	if m.SwapTotalBytes == nil || *m.SwapTotalBytes != 0 {
		t.Fatal("observed zero swap lost")
	}
}
func TestPolicyTypedBoundaryRejectsDuplicatesCommandsAndUnknownKeys(t *testing.T) {
	for _, input := range []string{`{"enabled":true,"enabled":false}`, `{"shell":"rm -rf /"}`, `{} {}`} {
		var p Policy
		if strictDecode([]byte(input), &p) == nil {
			t.Fatal("ambiguous/free-form document accepted")
		}
	}
}
