package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/agent/configfile"
	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
)

func TestV13ControlResponseMustBeCompleteBeforeAnyEffect(t *testing.T) {
	for _, tc := range []struct{ name, tail string }{{"second_value", ` {"unexpected":true}`}, {"junk_tail", ` trailing`}, {"oversized_whitespace", strings.Repeat(" ", 4<<20)}} {
		t.Run(tc.name, func(t *testing.T) {
			tail := tc.tail
			a, _ := auditWorker(t, func(w http.ResponseWriter, r *http.Request) {
				cfg := protocol.DefaultAgentConfig()
				cfg.Paused = true
				b, _ := json.Marshal(protocol.ControlResponse{ControllerID: "controller", Ack: &protocol.IngestAck{Committed: true, UpToSequence: 1}, DesiredConfig: &protocol.DesiredConfig{Revision: 2, Hash: configfile.HashConfig(cfg), Body: cfg}})
				w.Write(append(b, []byte(tail)...))
			})
			err := a.send(context.Background(), protocol.AgentReport{AgentID: "fixture", SessionID: a.Session, Sequence: 1, ObservedAt: time.Now(), IsLive: true})
			if err == nil || a.cfg.Paused || a.cfgRev == 2 {
				t.Fatalf("incomplete/oversized control envelope applied: err=%v rev=%d", err, a.cfgRev)
			}
		})
	}
}

func TestV13ReceiptRemovalRequiresDurableJournal(t *testing.T) {
	a, _ := auditWorker(t, func(http.ResponseWriter, *http.Request) {})
	rec := protocol.JobReceipt{JobID: "job", OperationID: "op", Status: protocol.TargetSucceeded, Stage: "diagnostics"}
	a.jobs["job"] = rec
	if err := a.saveJobsLocked(); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(a.State.File.StateDir, "job-receipts.json")
	if err := os.Rename(p, p+".original"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(p, 0700); err != nil {
		t.Fatal(err)
	}
	a.applyControl(protocol.ControlResponse{ReceiptAcks: []string{"job"}}, []protocol.JobReceipt{rec})
	if _, ok := a.jobs["job"]; !ok {
		t.Fatal("failed durable acknowledgement erased in-memory replay guard")
	}
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(p+".original", p); err != nil {
		t.Fatal(err)
	}
	a.applyControl(protocol.ControlResponse{ReceiptAcks: []string{"job"}}, []protocol.JobReceipt{rec})
	if _, ok := a.jobs["job"]; ok {
		t.Fatal("valid later acknowledgement could not remove result")
	}
}

func TestV13AppliedConfigCannotSilentlyFallBackAfterDataLoss(t *testing.T) {
	for _, mode := range []string{"both_missing", "stale_legacy", "mismatched_mirror"} {
		t.Run(mode, func(t *testing.T) {
			a, _ := auditWorker(t, func(http.ResponseWriter, *http.Request) {})
			cfg := protocol.DefaultAgentConfig()
			cfg.Paused = true
			a.applyControl(protocol.ControlResponse{DesiredConfig: &protocol.DesiredConfig{Revision: 4, Hash: configfile.HashConfig(cfg), Body: cfg}})
			dir := a.State.File.StateDir
			switch mode {
			case "both_missing":
				os.Remove(filepath.Join(dir, "applied-envelope.json"))
				os.Remove(filepath.Join(dir, "applied.json"))
			case "stale_legacy":
				old := protocol.DefaultAgentConfig()
				if err := configfile.SaveAppliedConfig(dir, old, 2, configfile.HashConfig(old)); err != nil {
					t.Fatal(err)
				}
			case "mismatched_mirror":
				a.State.File.AppliedHash = strings.Repeat("a", 64)
				if err := a.State.Save(); err != nil {
					t.Fatal(err)
				}
			}
			if b, err := Open(a.CfgPath); err == nil {
				t.Fatalf("corrupt applied state silently started: rev=%d paused=%v", b.cfgRev, b.cfg.Paused)
			}
		})
	}
}

func TestV13PendingReceiptBatchCannotStarveCompletion(t *testing.T) {
	seen := map[string]bool{}
	a, _ := auditWorker(t, func(w http.ResponseWriter, r *http.Request) {
		var rep protocol.AgentReport
		if err := json.NewDecoder(r.Body).Decode(&rep); err != nil {
			t.Error(err)
			return
		}
		if len(rep.JobReceipts) > 64 {
			t.Error("receipt budget exceeded")
		}
		for _, rec := range rep.JobReceipts {
			seen[rec.JobID] = true
		}
		json.NewEncoder(w).Encode(protocol.ControlResponse{ControllerID: "controller", Ack: &protocol.IngestAck{Committed: true, UpToSequence: rep.Sequence}})
	})
	a.cfg.Paused = true
	for i := 0; i < 80; i++ {
		id := fmt.Sprintf("job-%03d", i)
		a.jobs[id] = protocol.JobReceipt{JobID: id, OperationID: "op", Status: protocol.TargetAccepted, Stage: "awaiting_result"}
	}
	a.jobs["zzz-finished"] = protocol.JobReceipt{JobID: "zzz-finished", OperationID: "done", Status: protocol.TargetSucceeded, Stage: "diagnostics"}
	for i := 0; i < 2; i++ {
		a.tick(context.Background(), false)
	}
	if !seen["zzz-finished"] || len(seen) != 81 {
		t.Fatalf("pending prefix starved receipts: delivered=%d terminal=%v", len(seen), seen["zzz-finished"])
	}
}

func TestV13ControllerRootSlashKeepsReportRouteAndIdentity(t *testing.T) {
	path := ""
	a, _ := auditWorker(t, func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		if path != "/api/v1/agent/report" {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(protocol.ControlResponse{ControllerID: "controller", Ack: &protocol.IngestAck{Committed: true, UpToSequence: 1}})
	})
	a.State.File.ControllerURL += "/"
	original := a.State.File.ControllerURL
	if err := a.send(context.Background(), protocol.AgentReport{AgentID: "fixture", SessionID: a.Session, Sequence: 1, ObservedAt: time.Now(), IsLive: true}); err != nil {
		t.Fatalf("valid root slash broke control report: path=%q err=%v", path, err)
	}
	if a.State.File.ControllerURL != original {
		t.Fatal("request assembly changed persisted controller URL")
	}
}

func TestV13SecretResponseRequiresOneCompleteObject(t *testing.T) {
	for _, body := range []string{`{"header":"Authorization","value":"fixture","version":1} {}`, `{"header":"Authorization","value":"first","value":"second","version":1}`, `{"header":"Authorization","value":"fixture","version":1}` + strings.Repeat(" ", (1<<20)+1)} {
		a, _ := auditWorker(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, body)
		})
		if _, err := fetchRequestSecret(context.Background(), a.client, a.State.File.ControllerURL, a.Cred, "fixture", "s"); err == nil {
			t.Error("periodic secret fetch accepted an ambiguous/truncated envelope")
		}
		if _, _, _, err := a.fetchSecret("s"); err == nil {
			t.Error("secret rotation accepted an ambiguous/truncated envelope")
		}
	}
}
