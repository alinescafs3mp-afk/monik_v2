package server

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
	"github.com/alinescafs3mp-afk/monik_v2/internal/storage"
)

func serverRolloutFixture(t *testing.T) (*App, string) {
	t.Helper()
	a, _ := testApp(t)
	op := importRelease10(t, a, releaseBundle10(t, t.TempDir(), t.TempDir(), "worker", "rollout"), "bundle")
	if op.Status != protocol.OpCompleted {
		t.Fatal(op)
	}
	rels, e := a.Store.Releases()
	if e != nil {
		t.Fatal(e)
	}
	release := rels[0]["id"].(string)
	for _, id := range []string{"a", "b", "c"} {
		if e = a.Store.InsertAgent(&storage.AgentRow{ID: id, Hostname: id, OS: "linux", Arch: "amd64", DesiredConfig: "{}"}, "verifier"); e != nil {
			t.Fatal(e)
		}
		if _, e = a.Store.DB.Exec(`UPDATE agents SET managed_ready=1,capabilities=?,last_live_at=?,session_id='original' WHERE id=?`, `{"immutable_release_v1":{"status":"supported"}}`, time.Now().UTC().Format(time.RFC3339Nano), id); e != nil {
			t.Fatal(e)
		}
	}
	w := httptest.NewRecorder()
	a.processSubmit(w, ownerRecent(t), protocol.SubmitOperation{Action: "update.rollout", ClientRequestKey: "rollout", TargetIDs: []string{"c", "a", "b"}, Params: map[string]any{"release_id": release, "batch_size": float64(2), "observe_seconds": float64(15)}})
	var roll protocol.Operation
	if w.Code >= 400 || json.Unmarshal(w.Body.Bytes(), &roll) != nil {
		t.Fatal(w.Code, w.Body.String())
	}
	if roll.Status != protocol.OpRunning {
		t.Fatal(roll)
	}
	return a, roll.ID
}
func TestRolloutServerFrozenCanaryAndControls(t *testing.T) {
	a, id := serverRolloutFixture(t)
	r, e := a.Store.Rollout(id)
	if e != nil || r.Members[0].AgentID != "a" || len(r.Members) != 3 {
		t.Fatal(r, e)
	}
	w := httptest.NewRecorder()
	req := protocol.SubmitOperation{Action: "update.pause", ClientRequestKey: "pause", Params: map[string]any{"operation_id": id, "rollout_revision": float64(r.Revision)}}
	a.processSubmit(w, ownerRecent(t), req)
	var control protocol.Operation
	json.Unmarshal(w.Body.Bytes(), &control)
	if control.Status != protocol.OpCompleted {
		t.Fatal(w.Code, w.Body.String())
	}
	r, _ = a.Store.Rollout(id)
	if r.State != "paused" {
		t.Fatal(r)
	}
	j, e := a.Store.ClaimPendingJobs("a", 8)
	if e != nil || len(j) != 0 {
		t.Fatal(j, e)
	}
	// Repeat same key is a read of the original control operation, not a second pause.
	repeat := httptest.NewRecorder()
	a.processSubmit(repeat, ownerRecent(t), req)
	var same protocol.Operation
	json.Unmarshal(repeat.Body.Bytes(), &same)
	if same.ID != control.ID {
		t.Fatal("idempotency broken")
	}
	w = httptest.NewRecorder()
	a.processSubmit(w, ownerRecent(t), protocol.SubmitOperation{Action: "update.resume", ClientRequestKey: "resume", Params: map[string]any{"operation_id": id, "rollout_revision": float64(r.Revision)}})
	if json.Unmarshal(w.Body.Bytes(), &control) != nil || control.Status != protocol.OpCompleted {
		t.Fatal(w.Code, w.Body.String())
	}
	j, e = a.Store.ClaimPendingJobs("a", 8)
	if e != nil || len(j) != 1 {
		t.Fatal(j, e)
	}
	if j[0].Params["release_id"] != r.ReleaseID {
		t.Fatal("release changed")
	}
	j, e = a.Store.ClaimPendingJobs("b", 8)
	if e != nil || len(j) != 0 {
		t.Fatal("later wave escaped")
	}
}
func TestRolloutResumeRequiresRecentAuth(t *testing.T) {
	a, id := serverRolloutFixture(t)
	w := httptest.NewRecorder()
	s := &storage.Session{Username: "owner", Role: "owner"}
	a.processSubmit(w, s, protocol.SubmitOperation{Action: "update.resume", ClientRequestKey: "old-session", Params: map[string]any{"operation_id": id, "rollout_revision": float64(1)}})
	if w.Code != 403 && w.Code != 401 {
		t.Fatal(w.Code, w.Body.String())
	}
}
func TestRolloutGETNeedsAuthentication(t *testing.T) {
	_, h := testApp(t)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/rollouts", nil))
	if w.Code != 401 {
		t.Fatal(w.Code)
	}
}
func TestRolloutTypedOptions(t *testing.T) {
	a, _ := testApp(t)
	for _, opts := range []map[string]any{{"release_id": "x", "batch_size": 0.0}, {"release_id": "x", "batch_size": 11.0}, {"release_id": "x", "observe_seconds": 14.0}, {"release_id": "x", "observe_seconds": 301.0}, {"release_id": "x", "observe_seconds": true}, {"release_id": "x", "batch_size": 1.5}, {"release_id": "x", "url": "https://untrusted.invalid"}} {
		req := protocol.SubmitOperation{Action: "update.rollout", Params: opts}
		if a.validateLifecycleParams(&req) == nil {
			t.Fatalf("accepted %+v", opts)
		}
	}
}
func TestRolloutControlOutcomeIsAtomic(t *testing.T) {
	a, id := serverRolloutFixture(t)
	r, _ := a.Store.Rollout(id)
	_, e := a.Store.DB.Exec(`CREATE TRIGGER fail_control BEFORE UPDATE ON operation_targets WHEN NEW.stage='rollout.control_committed' BEGIN SELECT RAISE(ABORT,'test control result'); END`)
	if e != nil {
		t.Fatal(e)
	}
	w := httptest.NewRecorder()
	a.processSubmit(w, ownerRecent(t), protocol.SubmitOperation{Action: "update.pause", ClientRequestKey: "atomic", Params: map[string]any{"operation_id": id, "rollout_revision": float64(r.Revision)}})
	got, _ := a.Store.Rollout(id)
	if got.State != r.State || got.Revision != r.Revision {
		t.Fatal("partial pause committed", got)
	}
}

func TestRolloutResumeEmptyRequestDoesNotCreateOperation(t *testing.T) {
	a, _ := testApp(t)
	w := httptest.NewRecorder()
	a.processSubmit(w, ownerRecent(t), protocol.SubmitOperation{Action: "update.resume", ClientRequestKey: "bad-resume", Params: map[string]any{}})
	if w.Code != 400 {
		t.Fatal(w.Code, w.Body.String())
	}
	var n int
	if e := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM operations WHERE client_request_key='bad-resume'`).Scan(&n); e != nil || n != 0 {
		t.Fatal(n, e)
	}
}
