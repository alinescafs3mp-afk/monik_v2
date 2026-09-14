package runtime

import (
	"encoding/json"
	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestAudit4TrialDoesNotBlockControlAndIsNotRepeated(t *testing.T) {
	var n atomic.Int32
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		started <- struct{}{}
		<-release
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ready":true}`))
	}))
	defer ts.Close()
	a, _ := auditWorker(t, func(http.ResponseWriter, *http.Request) {})
	job := auditEnvelope("check.trial")
	job.Params = map[string]any{"url": ts.URL, "method": "POST", "allow_post": true, "request_version": float64(1), "body": `{"method":"health"}`, "kind": "http_health", "expected_status": []any{float64(200)}, "expect_json_path": "ready", "expect_json_type": "boolean", "expect_json_value": "true"}
	before := time.Now()
	a.handleJob(job)
	if time.Since(before) > 100*time.Millisecond {
		close(release)
		t.Fatal("trial blocked dispatch")
	}
	<-started
	a.handleJob(job)
	a.mu.Lock()
	status := a.jobs[job.JobID].Status
	a.mu.Unlock()
	if status != protocol.TargetAccepted {
		close(release)
		t.Fatal("premature success")
	}
	close(release)
	waitAudit4Trial(t, a, job.JobID)
	a.mu.Lock()
	rec := a.jobs[job.JobID]
	a.mu.Unlock()
	if rec.Status != protocol.TargetSucceeded || rec.Evidence["app_result"] != "pass" || n.Load() != 1 {
		b, _ := json.Marshal(rec)
		t.Fatalf("bad result or duplicate %s count %d", b, n.Load())
	}
}
func TestAudit4RestartedPendingTrialRemainsUnknownNotReplayed(t *testing.T) {
	a, _ := auditWorker(t, func(http.ResponseWriter, *http.Request) {})
	a.mu.Lock()
	a.jobs["pending"] = protocol.JobReceipt{JobID: "pending", Status: protocol.TargetAccepted, Stage: "trial_pending"}
	if e := a.saveJobsLocked(); e != nil {
		t.Fatal(e)
	}
	a.mu.Unlock()
	b, e := Open(a.CfgPath)
	if e != nil {
		t.Fatal(e)
	}
	if b.jobs["pending"].ErrorCode != "outcome_unknown" || b.jobs["pending"].Status != protocol.TargetFailed {
		t.Fatal("pending trial not interrupted")
	}
}
