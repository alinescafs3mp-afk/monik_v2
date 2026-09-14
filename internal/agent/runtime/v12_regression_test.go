package runtime

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/agent/configfile"
	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
)

// The controller acknowledges the exact receipt in the request. A different
// final result may already exist locally by the time that response arrives.
func TestV12AcceptanceAckCannotEraseUnsentCompletion(t *testing.T) {
	var agent *Agent
	requests := 0
	a, _ := auditWorker(t, func(w http.ResponseWriter, r *http.Request) {
		var rep protocol.AgentReport
		if err := json.NewDecoder(r.Body).Decode(&rep); err != nil {
			t.Error(err)
			return
		}
		requests++
		if requests == 1 {
			if len(rep.JobReceipts) != 1 || rep.JobReceipts[0].Status != protocol.TargetAccepted {
				t.Error("fixture did not send acceptance")
			}
			agent.mu.Lock()
			rec := agent.jobs["job"]
			rec.Status = protocol.TargetSucceeded
			rec.Stage = "trial_complete"
			rec.Evidence = map[string]any{"result": "completion-not-yet-sent"}
			agent.jobs["job"] = rec
			if err := agent.saveJobsLocked(); err != nil {
				t.Error(err)
			}
			agent.mu.Unlock()
		}
		json.NewEncoder(w).Encode(protocol.ControlResponse{ControllerID: "controller", Ack: &protocol.IngestAck{Committed: true, UpToSequence: rep.Sequence}, ReceiptAcks: []string{"job"}})
	})
	agent = a
	rec := protocol.JobReceipt{JobID: "job", OperationID: "op", Status: protocol.TargetAccepted, Stage: "trial_pending"}
	a.jobs["job"] = rec
	rep := protocol.AgentReport{AgentID: "fixture", SessionID: a.Session, Sequence: 1, ObservedAt: time.Now().UTC(), IsLive: true, JobReceipts: []protocol.JobReceipt{rec}}
	if err := a.send(context.Background(), rep); err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	completed, exists := a.jobs["job"]
	a.mu.Unlock()
	if !exists || completed.Status != protocol.TargetSucceeded {
		t.Fatal("acceptance acknowledgement erased an unsent completion")
	}
	rep.Sequence++
	rep.JobReceipts = []protocol.JobReceipt{completed}
	if err := a.send(context.Background(), rep); err != nil {
		t.Fatal(err)
	}
	if _, exists := a.jobs["job"]; exists {
		t.Fatal("acknowledged exact terminal receipt was not removed")
	}
}

func TestV12ConfigRevisionCannotBeReusedForDifferentContents(t *testing.T) {
	a, _ := auditWorker(t, func(http.ResponseWriter, *http.Request) {})
	cfg := protocol.DefaultAgentConfig()
	hash := configfile.HashConfig(cfg)
	a.applyControl(protocol.ControlResponse{DesiredConfig: &protocol.DesiredConfig{Revision: 2, Hash: hash, Body: cfg}})
	cfg.Paused = true
	a.applyControl(protocol.ControlResponse{DesiredConfig: &protocol.DesiredConfig{Revision: 2, Hash: configfile.HashConfig(cfg), Body: cfg}})
	if a.cfg.Paused || a.cfgHash != hash {
		t.Fatal("same revision changed its accepted content")
	}
	disk, err := configfile.LoadAppliedEnvelope(a.State.File.StateDir)
	if err != nil || disk.Hash != hash {
		t.Fatalf("persisted config changed: %+v %v", disk, err)
	}
}

func TestV12TrustRotationDoesNotMutateInFlightHTTPClient(t *testing.T) {
	a, _ := auditWorker(t, func(http.ResponseWriter, *http.Request) {})
	old, transport := a.client, a.client.Transport
	a.mu.Lock()
	err := a.applyTrustPool()
	a.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if old == a.client || old.Transport != transport {
		t.Fatal("trust rotation mutated a shared client used by in-flight requests")
	}
	if a.client.Timeout != old.Timeout || a.client.CheckRedirect == nil {
		t.Fatal("transport replacement lost client security settings")
	}
}

func TestV12InvalidReceiptJournalStopsStartupWithoutOverwrite(t *testing.T) {
	for _, text := range []string{"null", "[]", `{"wrong":{"job_id":"different","status":"succeeded"}}`} {
		t.Run(text, func(t *testing.T) {
			a, _ := auditWorker(t, func(http.ResponseWriter, *http.Request) {})
			p := filepath.Join(a.State.File.StateDir, "job-receipts.json")
			if err := os.WriteFile(p, []byte(text), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := Open(a.CfgPath); err == nil {
				t.Fatal("invalid job journal accepted; may panic or discard pending evidence")
			}
			got, _ := os.ReadFile(p)
			if string(got) != text {
				t.Fatal("invalid journal overwritten")
			}
		})
	}
	t.Run("read-error", func(t *testing.T) {
		a, _ := auditWorker(t, func(http.ResponseWriter, *http.Request) {})
		if err := os.Mkdir(filepath.Join(a.State.File.StateDir, "job-receipts.json"), 0700); err != nil {
			t.Fatal(err)
		}
		if _, err := Open(a.CfgPath); err == nil {
			t.Fatal("journal read failure was ignored")
		}
	})
}

func TestV12OnlyOneRunningWorkerOwnsAnIdentity(t *testing.T) {
	seen := make(chan string, 4)
	a, _ := auditWorker(t, func(w http.ResponseWriter, r *http.Request) {
		var rep protocol.AgentReport
		if e := json.NewDecoder(r.Body).Decode(&rep); e != nil {
			t.Error(e)
			return
		}
		seen <- rep.SessionID
		json.NewEncoder(w).Encode(protocol.ControlResponse{ControllerID: "controller", Ack: &protocol.IngestAck{Committed: true, UpToSequence: rep.Sequence}})
	})
	b, e := Open(a.CfgPath)
	if e != nil {
		t.Fatal(e)
	}
	a.cfg.Paused = true
	b.cfg.Paused = true
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first := make(chan error, 1)
	second := make(chan error, 1)
	go func() { first <- a.Run(ctx) }()
	select {
	case <-seen:
	case <-time.After(5 * time.Second):
		t.Fatal("first worker did not report")
	}
	go func() { second <- b.Run(ctx) }()
	select {
	case err := <-second:
		if err == nil {
			t.Error("duplicate worker silently succeeded")
		}
	case <-seen:
		t.Error("two workers reported different sessions for the same enrolled identity")
	case <-time.After(5 * time.Second):
		t.Error("duplicate worker not rejected")
	}
	cancel()
	select {
	case <-first:
	case <-time.After(6 * time.Second):
		t.Error("worker did not shut down")
	}
}

func TestV12StoppingWorkerCancelsAndJoinsOutstandingTrial(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	health := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case started <- struct{}{}:
		default:
		}
		select {
		case <-release:
		case <-r.Context().Done():
		}
		w.Write([]byte("ok"))
	}))
	defer health.Close()
	defer close(release)
	job := auditEnvelope("check.trial")
	job.Params = map[string]any{"url": health.URL, "method": "GET", "timeout_seconds": float64(30), "interval_seconds": float64(60)}
	a, _ := auditWorker(t, func(w http.ResponseWriter, r *http.Request) {
		var rep protocol.AgentReport
		json.NewDecoder(r.Body).Decode(&rep)
		json.NewEncoder(w).Encode(protocol.ControlResponse{ControllerID: "controller", Ack: &protocol.IngestAck{Committed: true, UpToSequence: rep.Sequence}, Jobs: []protocol.JobEnvelope{job}})
	})
	a.cfg.Paused = true
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("trial did not start")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("cancelled worker did not stop")
	}
	if a.trialRunning.Load() != 0 {
		t.Error("Run returned while a trial still sent traffic/wrote the journal")
	}
}

func TestV12RebindDoesNotMutateInFlightHTTPClient(t *testing.T) {
	a, _ := auditWorker(t, func(http.ResponseWriter, *http.Request) {})
	old, transport := a.client, a.client.Transport
	a.mu.Lock()
	err := a.applySwitch(&lifecycleIntent{CandidateURL: a.State.File.ControllerURL, TrustPEM: a.State.File.CACertPEM})
	a.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if old == a.client || old.Transport != transport {
		t.Fatal("rebind mutated the client held by an in-flight request")
	}
}

func TestV12RestartedWorkerDoesNotRepeatUnconfirmedLifecycle(t *testing.T) {
	a, _ := auditWorker(t, func(http.ResponseWriter, *http.Request) {})
	in := &lifecycleIntent{Kind: "update", JobID: "updating", OperationID: "operation", ExpectedDigest: "expected", PreviousSession: "before-update"}
	if err := saveIntent(a.State.File.StateDir, in); err != nil {
		t.Fatal(err)
	}
	b, err := Open(a.CfgPath)
	if err != nil {
		t.Fatal(err)
	}
	b.handleJob(protocol.JobEnvelope{SchemaVersion: protocol.SchemaVersion, ControllerID: b.State.File.ControllerID, JobID: in.JobID, OperationID: in.OperationID, Action: "update.rollout", Deadline: time.Now().Add(time.Minute)})
	r := b.jobs[in.JobID]
	if r.Status != protocol.TargetAwaitingConfirmation || r.Stage != "lifecycle_pending" {
		t.Fatalf("restarted worker retried or prematurely terminated pending activation: %+v", r)
	}
	if _, err := loadIntent(b.State.File.StateDir); err != nil {
		t.Fatal("lost durable intent", err)
	}
}

func TestV12InvalidLifecycleIntentCannotBeSilentlyReplaced(t *testing.T) {
	for _, text := range []string{"null", "{}", `{"kind":"unknown"}`, `{"kind":"update"}`} {
		t.Run(text, func(t *testing.T) {
			a, _ := auditWorker(t, func(http.ResponseWriter, *http.Request) {})
			p := intentPath(a.State.File.StateDir)
			if err := os.WriteFile(p, []byte(text), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := Open(a.CfgPath); err == nil {
				t.Fatal("corrupt pending intent accepted")
			}
			got, _ := os.ReadFile(p)
			if string(got) != text {
				t.Fatal("corrupt intent overwritten")
			}
		})
	}
}
