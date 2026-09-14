package runtime

import (
	"encoding/json"
	"github.com/alinescafs3mp-afk/monik_v2/internal/agent/configfile"
	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
	"github.com/alinescafs3mp-afk/monik_v2/internal/update"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestReviewUpdateNeedsSupervisorProbation(t *testing.T) {
	a, _ := auditWorker(t, func(http.ResponseWriter, *http.Request) {})
	in := &lifecycleIntent{Kind: "update", JobID: "update", OperationID: "operation", ExpectedDigest: a.digest, PreviousSession: "previous-session"}
	if e := saveIntent(a.State.File.StateDir, in); e != nil {
		t.Fatal(e)
	}
	j := &update.Journal{TxID: "transaction", Stage: update.StageProbation, Current: a.selfBin, NewSHA: a.digest, OldSHA: "old"}
	if e := j.Save(a.State.File.StateDir); e != nil {
		t.Fatal(e)
	}
	a.reconcileIntents()
	if _, ok := a.jobs[in.JobID]; ok {
		t.Fatal("premature update success")
	}
	j.Stage = update.StageConfirmed
	if e := j.Save(a.State.File.StateDir); e != nil {
		t.Fatal(e)
	}
	a.reconcileIntents()
	if a.jobs[in.JobID].Status != protocol.TargetSucceeded {
		t.Fatal("confirmed update missing")
	}
	reopened, e := Open(a.CfgPath)
	if e != nil {
		t.Fatal(e)
	}
	if reopened.jobs[in.JobID].Status != protocol.TargetSucceeded {
		t.Fatal("receipt was not durable")
	}
}
func TestReviewRollbackIsNotUpdateSuccess(t *testing.T) {
	a, _ := auditWorker(t, func(http.ResponseWriter, *http.Request) {})
	in := &lifecycleIntent{Kind: "update", JobID: "update", OperationID: "operation", ExpectedDigest: "bad-candidate", PreviousSession: "previous-session"}
	if e := saveIntent(a.State.File.StateDir, in); e != nil {
		t.Fatal(e)
	}
	j := &update.Journal{TxID: "tx", Stage: update.StageRollback, Current: a.selfBin, NewSHA: "bad-candidate", OldSHA: a.digest}
	if e := j.Save(a.State.File.StateDir); e != nil {
		t.Fatal(e)
	}
	a.reconcileIntents()
	if a.jobs[in.JobID].Status != protocol.TargetRolledBack {
		t.Fatalf("%+v", a.jobs)
	}
}
func TestReviewRestartCannotCompleteInOriginalSession(t *testing.T) {
	a, _ := auditWorker(t, func(http.ResponseWriter, *http.Request) {})
	in := &lifecycleIntent{Kind: "restart", JobID: "restart", PreviousSession: a.Session}
	if e := saveIntent(a.State.File.StateDir, in); e != nil {
		t.Fatal(e)
	}
	a.reconcileIntents()
	if _, ok := a.jobs[in.JobID]; ok {
		t.Fatal("restart not executed")
	}
	a.Session = "next"
	a.reconcileIntents()
	if a.jobs[in.JobID].Status != protocol.TargetSucceeded {
		t.Fatal("restart receipt missing")
	}
}

func TestReviewConfigRevisionSurvivesIdentityMirrorLag(t *testing.T) {
	a, _ := auditWorker(t, func(http.ResponseWriter, *http.Request) {})
	cfg := protocol.DefaultAgentConfig()
	cfg.Paused = true
	hash := configfile.HashConfig(cfg)
	if e := configfile.SaveAppliedConfig(a.State.File.StateDir, cfg, 42, hash); e != nil {
		t.Fatal(e)
	}
	b, e := Open(a.CfgPath)
	if e != nil {
		t.Fatal(e)
	}
	if !b.cfg.Paused || b.cfgRev != 42 || b.cfgHash != hash {
		t.Fatal("torn config revision")
	}
	if e := os.WriteFile(filepath.Join(a.State.File.StateDir, "applied-envelope.json"), []byte(`{"body":{}}`), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := Open(a.CfgPath); e == nil {
		t.Fatal("corrupt config silently reset")
	}
}

func TestReviewAppliedConfigKeepsRollbackReaderCompatible(t *testing.T) {
	a, _ := auditWorker(t, func(http.ResponseWriter, *http.Request) {})
	cfg := protocol.DefaultAgentConfig()
	cfg.Paused = true
	if e := configfile.SaveAppliedConfig(a.State.File.StateDir, cfg, 2, configfile.HashConfig(cfg)); e != nil {
		t.Fatal(e)
	}
	raw, e := os.ReadFile(filepath.Join(a.State.File.StateDir, "applied.json"))
	if e != nil {
		t.Fatal(e)
	}
	var oldReader protocol.AgentConfig
	if e := json.Unmarshal(raw, &oldReader); e != nil || !oldReader.Paused || oldReader.Intervals.ReportSeconds != 5 {
		t.Fatal("old worker cannot read configuration after rollback")
	}
	cfg.Paused = false
	raw, _ = json.Marshal(cfg)
	_ = os.WriteFile(filepath.Join(a.State.File.StateDir, "applied.json"), raw, 0600)
	a.State.File.AppliedRevision = 3
	a.State.File.AppliedHash = configfile.HashConfig(cfg)
	if e := a.State.Save(); e != nil {
		t.Fatal(e)
	}
	b, e := Open(a.CfgPath)
	if e != nil {
		t.Fatal(e)
	}
	if b.cfgRev != 3 || b.cfg.Paused {
		t.Fatal("lost configuration applied by previous worker")
	}
}
