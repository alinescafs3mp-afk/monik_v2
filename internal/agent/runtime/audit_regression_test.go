package runtime

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/agent/configfile"
	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
)

func auditWorker(t *testing.T, handler http.HandlerFunc) (*Agent, *httptest.Server) {
	t.Helper()
	ts := httptest.NewTLSServer(handler)
	t.Cleanup(ts.Close)
	dir := t.TempDir()
	cred := filepath.Join(dir, "credential")
	if err := os.WriteFile(cred, []byte("fixture-token"), 0600); err != nil {
		t.Fatal(err)
	}
	f := configfile.File{SchemaVersion: 3, AgentID: "fixture", ControllerURL: ts.URL, ControllerID: "controller", CACertPEM: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ts.Certificate().Raw})), CredentialPath: cred, StateDir: dir, AppliedRevision: 1}
	raw, _ := json.Marshal(f)
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	a, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	a.cfg.Ping.Enabled = false
	return a, ts
}
func auditEnvelope(action string) protocol.JobEnvelope {
	return protocol.JobEnvelope{JobID: "job", OperationID: "op", ControllerID: "controller", SchemaVersion: 3, Action: action, CreatedAt: time.Now().Add(-time.Minute), Deadline: time.Now().Add(time.Minute)}
}
func TestAuditWorkerDoesNotClaimScheduledCollectionSucceeded(t *testing.T) {
	a, _ := auditWorker(t, func(http.ResponseWriter, *http.Request) {})
	a.handleJob(auditEnvelope("agent.collect_now"))
	if a.jobs["job"].Status != protocol.TargetAccepted {
		t.Fatalf("%+v", a.jobs["job"])
	}
	reopened, err := Open(a.CfgPath)
	if err != nil {
		t.Fatal(err)
	}
	reopened.handleJob(auditEnvelope("agent.collect_now"))
	if len(reopened.jobs) != 1 || reopened.jobs["job"].Status != protocol.TargetAccepted {
		t.Fatalf("journal not restored: %+v", reopened.jobs)
	}
}
func TestAuditWorkerRejectsUnsupportedAndExpiredActions(t *testing.T) {
	for _, action := range []string{"agent.restart", "update.rollout", "rebind.activate", "arbitrary_shell"} {
		t.Run(action, func(t *testing.T) {
			a, _ := auditWorker(t, func(http.ResponseWriter, *http.Request) {})
			a.handleJob(auditEnvelope(action))
			if a.jobs["job"].Status != protocol.TargetUnsupported {
				t.Fatalf("false success %+v", a.jobs["job"])
			}
		})
	}
	a, _ := auditWorker(t, func(http.ResponseWriter, *http.Request) {})
	job := auditEnvelope("agent.collect_now")
	job.Deadline = time.Now().Add(-time.Second)
	a.handleJob(job)
	if a.jobs["job"].Status != protocol.TargetExpired {
		t.Fatal("old job executed")
	}
}
func TestAuditConfigValidationAndMigrationCannotOverwriteWorkingState(t *testing.T) {
	a, ts := auditWorker(t, func(http.ResponseWriter, *http.Request) {})
	cfg := protocol.DefaultAgentConfig()
	cfg.Paused = true
	a.applyControl(protocol.ControlResponse{DesiredConfig: &protocol.DesiredConfig{Revision: 2, Hash: "wrong", Body: cfg}, Migration: &protocol.MigrationPlan{CandidateURL: "https://not-authorized.example"}})
	if a.cfg.Paused || a.State.File.ControllerURL != ts.URL {
		t.Fatal("unverified configuration/address applied")
	}
	good := protocol.DesiredConfig{Revision: 2, Body: cfg, Hash: configfile.HashConfig(cfg)}
	a.applyControl(protocol.ControlResponse{DesiredConfig: &good})
	if !a.cfg.Paused || a.cfgRev != 2 {
		t.Fatal("valid persisted config not applied")
	}
	good.Revision = 1
	good.Body.Paused = false
	good.Hash = configfile.HashConfig(good.Body)
	a.applyControl(protocol.ControlResponse{DesiredConfig: &good})
	if !a.cfg.Paused || a.cfgRev != 2 {
		t.Fatal("older config overwrote newer")
	}
	good.Revision = 3
	good.Body.Intervals.CollectSeconds = 17
	good.Hash = configfile.HashConfig(good.Body)
	a.applyControl(protocol.ControlResponse{DesiredConfig: &good})
	if a.cfgRev != 2 {
		t.Fatal("unsupported interval falsely applied")
	}
}
func TestAuditReportRequiresCommittedMatchingAcknowledgement(t *testing.T) {
	for _, kind := range []string{"missing", "wrong-controller", "wrong-sequence", "uncommitted", "valid"} {
		t.Run(kind, func(t *testing.T) {
			a, _ := auditWorker(t, func(w http.ResponseWriter, r *http.Request) {
				cr := protocol.ControlResponse{ControllerID: "controller", Ack: &protocol.IngestAck{UpToSequence: 9, Committed: true}}
				switch kind {
				case "missing":
					cr.Ack = nil
				case "wrong-controller":
					cr.ControllerID = "imposter"
				case "wrong-sequence":
					cr.Ack.UpToSequence = 8
				case "uncommitted":
					cr.Ack.Committed = false
				}
				json.NewEncoder(w).Encode(cr)
			})
			err := a.send(context.Background(), protocol.AgentReport{AgentID: "fixture", Sequence: 9, IsLive: true})
			if (err == nil) != (kind == "valid") {
				t.Fatalf("%s: %v", kind, err)
			}
		})
	}
}
func TestAuditHistoricalAcknowledgementCannotReapplyOldControl(t *testing.T) {
	cfg := protocol.DefaultAgentConfig()
	cfg.Paused = true
	a, _ := auditWorker(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(protocol.ControlResponse{ControllerID: "controller", Ack: &protocol.IngestAck{UpToSequence: 1, Committed: true}, DesiredConfig: &protocol.DesiredConfig{Revision: 10, Hash: configfile.HashConfig(cfg), Body: cfg}})
	})
	if err := a.send(context.Background(), protocol.AgentReport{Sequence: 1, IsLive: false}); err != nil {
		t.Fatal(err)
	}
	if a.cfg.Paused || a.cfgRev == 10 {
		t.Fatal("backlog response altered current control")
	}
}

func TestAuditAcceptanceAckDoesNotDeletePendingWork(t *testing.T) {
	a, _ := auditWorker(t, func(http.ResponseWriter, *http.Request) {})
	a.handleJob(auditEnvelope("agent.collect_now"))
	a.applyControl(protocol.ControlResponse{ReceiptAcks: []string{"job"}})
	if a.jobs["job"].Status != protocol.TargetAccepted {
		t.Fatal("acceptance receipt ACK discarded unfinished collection")
	}
	rec := a.jobs["job"]
	rec.Status = protocol.TargetSucceeded
	a.jobs["job"] = rec
	a.applyControl(protocol.ControlResponse{ReceiptAcks: []string{"job"}})
	if len(a.jobs) != 0 {
		t.Fatal("final receipt acknowledgement not cleared")
	}
}
