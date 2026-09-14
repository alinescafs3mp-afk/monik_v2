package storage

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/clock"
	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
)

type rolloutFixture struct {
	s               *Store
	c               *clock.Fake
	release, digest string
	plan            []PreparedUpdateTarget
}

func newRolloutFixture(t *testing.T, platforms ...string) *rolloutFixture {
	t.Helper()
	c := clock.NewFake(time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC))
	s, e := Open(filepath.Join(t.TempDir(), "monik.db"), c)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	f := &rolloutFixture{s: s, c: c, release: strings.Repeat("a", 64), digest: strings.Repeat("b", 64)}
	if _, e = s.DB.Exec(`INSERT INTO releases(id,version,digest,notes,metadata,imported_at,trust_ok,platforms) VALUES(?,'1',?,'','{}',?,1,'[]')`, f.release, f.release, c.Now().Format(dbTimeFormat)); e != nil {
		t.Fatal(e)
	}
	if _, e = s.DB.Exec(`INSERT INTO release_publications(release_id,object_digest,file_inventory,expires_at,format_version) VALUES(?,?,'[]',?,1)`, f.release, f.release, c.Now().Add(time.Hour).Format(dbTimeFormat)); e != nil {
		t.Fatal(e)
	}
	end := c.Now().Add(time.Hour)
	op := &protocol.Operation{ID: "roll", Action: "update.rollout", Status: protocol.OpQueued, Revision: 1, Actor: "owner", ClientRequestKey: "roll", CreatedAt: c.Now(), Deadline: &end, Params: map[string]any{"release_id": f.release}}
	for i, platform := range platforms {
		id := string(rune('a' + i))
		pa := strings.Split(platform, "/")
		auditAgent(t, s, id)
		if _, e = s.DB.Exec(`UPDATE agents SET os=?,arch=?,managed_ready=1,capabilities=?,last_live_at=?,session_id=? WHERE id=?`, pa[0], pa[1], `{"immutable_release_v1":{"status":"supported"}}`, c.Now().Format(dbTimeFormat), "old-"+id, id); e != nil {
			t.Fatal(e)
		}
		op.Targets = append(op.Targets, protocol.TargetResult{AgentID: id, Status: protocol.TargetQueued})
		f.plan = append(f.plan, PreparedUpdateTarget{AgentID: id, Status: protocol.TargetQueued, Evidence: map[string]any{"release_id": f.release, "release_digest": f.release, "os": pa[0], "arch": pa[1], "sha256": f.digest, "name": "monik-agent", "length": 10}})
	}
	if e = s.InsertOperation(op, "request"); e != nil {
		t.Fatal(e)
	}
	return f
}
func (f *rolloutFixture) publish(t *testing.T) {
	t.Helper()
	if e := f.s.PublishBatchedUpdatePlan("roll", f.release, f.plan, RolloutOptions{BatchSize: 2, ObserveSeconds: 15}); e != nil {
		t.Fatal(e)
	}
}
func (f *rolloutFixture) get(t *testing.T) *Rollout {
	t.Helper()
	r, e := f.s.Rollout("roll")
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func (f *rolloutFixture) jobs(t *testing.T, id string) []protocol.JobEnvelope {
	t.Helper()
	j, e := f.s.ClaimPendingJobs(id, 8)
	if e != nil {
		t.Fatal(e)
	}
	return j
}
func (f *rolloutFixture) complete(t *testing.T, id string) {
	t.Helper()
	js := f.jobs(t, id)
	if len(js) != 1 {
		t.Fatalf("%s jobs=%d", id, len(js))
	}
	j := js[0]
	if _, e := f.s.DB.Exec(`UPDATE agents SET worker_digest=?,session_id=?,last_live_at=? WHERE id=?`, f.digest, "new-"+id, f.c.Now().Format(dbTimeFormat), id); e != nil {
		t.Fatal(e)
	}
	if e := f.s.ApplyReceipt(id, protocol.JobReceipt{JobID: j.JobID, OperationID: "roll", Status: protocol.TargetSucceeded, Stage: "updated", Evidence: map[string]any{"worker_digest": f.digest, "session_id": "new-" + id}}); e != nil {
		t.Fatal(e)
	}
}
func (f *rolloutFixture) observe(t *testing.T) {
	t.Helper()
	for i := 0; i < 5; i++ {
		if _, e := f.s.DB.Exec(`UPDATE agents SET last_live_at=?`, f.c.Now().Format(dbTimeFormat)); e != nil {
			t.Fatal(e)
		}
		if e := f.s.AdvanceRollouts(); e != nil {
			t.Fatal(e)
		}
		f.c.Advance(5 * time.Second)
	}
}
func TestRolloutSeparatePlatformCanariesAndBatchObservation(t *testing.T) {
	f := newRolloutFixture(t, "linux/amd64", "linux/amd64", "linux/amd64", "windows/amd64", "windows/amd64")
	f.publish(t)
	r := f.get(t)
	if r.CanaryWaves != 2 || r.Members[0].AgentID != "a" || r.Members[1].AgentID != "d" {
		t.Fatalf("bad canaries: %+v", r)
	}
	for _, id := range []string{"b", "c", "d", "e"} {
		if len(f.jobs(t, id)) != 0 {
			t.Fatal("later wave escaped", id)
		}
	}
	f.complete(t, "a")
	if e := f.s.AdvanceRollouts(); e != nil {
		t.Fatal(e)
	}
	if len(f.jobs(t, "d")) != 0 {
		t.Fatal("canary bypassed observation")
	}
	op, _ := f.s.Operation("roll")
	if op.Status == protocol.OpCompleted {
		t.Fatal("premature completion")
	}
	f.observe(t)
	f.complete(t, "d")
	f.observe(t)
	if f.get(t).Wave != 2 {
		t.Fatal("regular wave not released")
	}
	f.complete(t, "b")
	f.complete(t, "c")
	if len(f.jobs(t, "e")) != 0 {
		t.Fatal("exceeded batch size")
	}
	f.observe(t)
	f.complete(t, "e")
	f.observe(t)
	if f.get(t).State != "completed" {
		t.Fatalf("not complete: %+v", f.get(t))
	}
	op, _ = f.s.Operation("roll")
	if op.Status != protocol.OpCompleted {
		t.Fatal(op.Status)
	}
}
func TestRolloutFailedCanaryBlocksEverythingElse(t *testing.T) {
	f := newRolloutFixture(t, "linux/amd64", "linux/amd64")
	f.publish(t)
	j := f.jobs(t, "a")[0]
	if e := f.s.ApplyReceipt("a", protocol.JobReceipt{JobID: j.JobID, OperationID: "roll", Status: protocol.TargetRolledBack, Message: "new worker failed"}); e != nil {
		t.Fatal(e)
	}
	if len(f.jobs(t, "b")) != 0 || f.get(t).State != "blocked" {
		t.Fatal("failed canary did not block")
	}
	if e := f.s.ControlRollout("roll", f.get(t).Revision, false, ""); e == nil {
		t.Fatal("failed canary was waved through")
	}
	if e := f.s.CancelPending("roll"); e != nil {
		t.Fatal(e)
	}
	if e := f.s.AdvanceRollouts(); e != nil {
		t.Fatal(e)
	}
	r := f.get(t)
	if r.State != "cancelled" || r.Members[0].Status != protocol.TargetRolledBack || r.Members[1].Status != protocol.TargetCancelledBeforeExec {
		t.Fatalf("bad cancellation: %+v", r)
	}
}
func TestRolloutPauseResumeKeepsIDsAndCAS(t *testing.T) {
	f := newRolloutFixture(t, "linux/amd64", "linux/amd64")
	f.publish(t)
	r := f.get(t)
	id, _ := f.s.JobIDFor("roll", "a")
	if e := f.s.ControlRollout("roll", r.Revision, true, ""); e != nil {
		t.Fatal(e)
	}
	if len(f.jobs(t, "a")) != 0 {
		t.Fatal("paused job dispatched")
	}
	if e := f.s.ControlRollout("roll", r.Revision, false, ""); !errors.Is(e, ErrConflict) {
		t.Fatal("stale revision accepted", e)
	}
	if e := f.s.ControlRollout("roll", f.get(t).Revision, false, ""); e != nil {
		t.Fatal(e)
	}
	js := f.jobs(t, "a")
	if len(js) != 1 || js[0].JobID != id {
		t.Fatal("resume made another job", js)
	}
	if e := f.s.ControlRollout("roll", f.get(t).Revision, true, ""); e != nil {
		t.Fatal(e)
	}
	// Claimed work is not recallable. A lost response is retried, not silently lost.
	js = f.jobs(t, "a")
	if len(js) != 1 || js[0].JobID != id {
		t.Fatal("paused already-claimed job was lost")
	}
	if e := f.s.CancelPending("roll"); e != nil {
		t.Fatal(e)
	}
	if f.get(t).Members[0].Status == protocol.TargetCancelledBeforeExec {
		t.Fatal("claimed work pretended cancelled")
	}
}
func TestRolloutPublicationFailureIsAtomic(t *testing.T) {
	f := newRolloutFixture(t, "linux/amd64", "linux/amd64")
	if _, e := f.s.DB.Exec(`CREATE TRIGGER bad_wave BEFORE INSERT ON update_rollout_members WHEN NEW.agent_id='b' BEGIN SELECT RAISE(ABORT,'fixture'); END`); e != nil {
		t.Fatal(e)
	}
	if e := f.s.PublishBatchedUpdatePlan("roll", f.release, f.plan, RolloutOptions{BatchSize: 2, ObserveSeconds: 15}); e == nil {
		t.Fatal("partial rollout committed")
	}
	if _, e := f.s.Rollout("roll"); !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}
	if len(f.jobs(t, "a")) != 0 {
		t.Fatal("released on rollback")
	}
}
func TestRolloutReopenRetainsFrozenWorkButReobserves(t *testing.T) {
	f := newRolloutFixture(t, "linux/amd64", "linux/amd64")
	f.publish(t)
	f.complete(t, "a")
	if e := f.s.AdvanceRollouts(); e != nil {
		t.Fatal(e)
	}
	f.c.Advance(10 * time.Second)
	old := f.s.Path()
	f.s.Close()
	s, e := Open(old, f.c)
	if e != nil {
		t.Fatal(e)
	}
	f.s = s
	t.Cleanup(func() { s.Close() })
	if _, e = f.s.DB.Exec(`UPDATE agents SET last_live_at=?`, f.c.Now().Format(dbTimeFormat)); e != nil {
		t.Fatal(e)
	}
	if len(f.jobs(t, "b")) != 0 {
		t.Fatal("restart bypassed observation")
	}
	f.observe(t)
	if len(f.jobs(t, "b")) != 1 {
		t.Fatal("restarted plan did not continue")
	}
}
func TestRolloutDeliveryClaimAndPauseAreSerialized(t *testing.T) {
	f := newRolloutFixture(t, "linux/amd64", "linux/amd64")
	f.publish(t)
	rev := f.get(t).Revision
	var wg sync.WaitGroup
	wg.Add(2)
	var js []protocol.JobEnvelope
	var e1, e2 error
	go func() { defer wg.Done(); js, e1 = f.s.ClaimPendingJobs("a", 8) }()
	go func() { defer wg.Done(); e2 = f.s.ControlRollout("roll", rev, true, "") }()
	wg.Wait()
	if e1 != nil || e2 != nil {
		t.Fatal(e1, e2)
	}
	if e := f.s.CancelPending("roll"); e != nil {
		t.Fatal(e)
	}
	r := f.get(t)
	if len(js) > 0 && r.Members[0].Status == protocol.TargetCancelledBeforeExec {
		t.Fatal("claimed command labelled never dispatched")
	}
	if len(js) == 0 && r.Members[0].Status != protocol.TargetCancelledBeforeExec {
		t.Fatal("unclaimed paused target not cancelled")
	}
}
func TestRolloutHeldReceiptsCannotSkipWave(t *testing.T) {
	f := newRolloutFixture(t, "linux/amd64", "linux/amd64")
	f.publish(t)
	id, _ := f.s.JobIDFor("roll", "b")
	if e := f.s.ApplyReceipt("b", protocol.JobReceipt{JobID: id, OperationID: "roll", Status: protocol.TargetRunning}); e == nil {
		t.Fatal("held job receipt accepted")
	}
}
func TestRolloutFreshnessAndIdentityGate(t *testing.T) {
	for _, kind := range []string{"stale", "digest", "session", "revoked"} {
		t.Run(kind, func(t *testing.T) {
			f := newRolloutFixture(t, "linux/amd64", "linux/amd64")
			f.publish(t)
			f.complete(t, "a")
			switch kind {
			case "stale":
				f.c.Advance(20 * time.Second)
			case "digest":
				f.s.DB.Exec(`UPDATE agents SET worker_digest='wrong' WHERE id='a'`)
			case "session":
				f.s.DB.Exec(`UPDATE agents SET session_id='other' WHERE id='a'`)
			case "revoked":
				f.s.DB.Exec(`UPDATE agents SET revoked=1 WHERE id='a'`)
			}
			if len(f.jobs(t, "b")) != 0 || f.get(t).State != "blocked" {
				t.Fatal("bad canary observation passed")
			}
		})
	}
}
func TestRolloutExpiredMetadataDoesNotReleaseNextWave(t *testing.T) {
	f := newRolloutFixture(t, "linux/amd64", "linux/amd64")
	f.publish(t)
	f.complete(t, "a")
	f.s.DB.Exec(`UPDATE release_publications SET expires_at=?`, f.c.Now().Format(dbTimeFormat))
	f.observe(t)
	if f.get(t).State != "blocked" || len(f.jobs(t, "b")) != 0 {
		t.Fatal("expired release dispatched")
	}
}
func TestRolloutDeliveredTimeoutIsUnknownAndKeepsLifecycleLock(t *testing.T) {
	f := newRolloutFixture(t, "linux/amd64", "linux/amd64")
	f.publish(t)
	f.jobs(t, "a")
	f.c.Advance(11 * time.Minute)
	if len(f.jobs(t, "b")) != 0 {
		t.Fatal("timeout released later wave")
	}
	if f.get(t).Members[0].Status != protocol.TargetUnknownResult {
		t.Fatal("unknown effect represented as no execution")
	}
	if _, busy := f.s.HasActiveLifecycle("a"); !busy {
		t.Fatal("unknown effect unlocked lifecycle")
	}
}
func TestRolloutAPIJSONHasNoJobEnvelope(t *testing.T) {
	f := newRolloutFixture(t, "linux/amd64")
	f.publish(t)
	raw, e := json.Marshal(f.get(t))
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(raw), "previous_session") || strings.Contains(string(raw), "envelope") {
		t.Fatal("internal envelope exposed")
	}
}

func TestRolloutContinuesWatchingEarlierCanary(t *testing.T) {
	f := newRolloutFixture(t, "linux/amd64", "linux/amd64", "linux/amd64")
	f.publish(t)
	f.complete(t, "a")
	f.observe(t)
	if f.get(t).Wave != 1 {
		t.Fatal("missing second wave")
	}
	if _, e := f.s.DB.Exec(`UPDATE agents SET worker_digest='unexpected' WHERE id='a'`); e != nil {
		t.Fatal(e)
	}
	// The current wave is queued but unclaimed. The earlier canary changes first.
	if len(f.jobs(t, "b")) != 0 || f.get(t).State != "blocked" {
		t.Fatal("earlier canary failure did not gate unclaimed work")
	}
}
func TestRolloutCorruptProgressFailsClosed(t *testing.T) {
	f := newRolloutFixture(t, "linux/amd64", "linux/amd64")
	f.publish(t)
	f.complete(t, "a")
	if _, e := f.s.DB.Exec(`UPDATE agent_jobs SET result='null' WHERE agent_id='a'`); e != nil {
		t.Fatal(e)
	}
	if js, e := f.s.ClaimPendingJobs("b", 8); e == nil && len(js) > 0 {
		t.Fatal("corrupt evidence released next wave")
	}
}
func TestRolloutObservationGapRestartsTimer(t *testing.T) {
	f := newRolloutFixture(t, "linux/amd64", "linux/amd64")
	f.publish(t)
	f.complete(t, "a")
	f.s.AdvanceRollouts()
	f.c.Advance(time.Minute)
	f.s.DB.Exec(`UPDATE agents SET last_live_at=?`, f.c.Now().Format(dbTimeFormat))
	if len(f.jobs(t, "b")) != 0 {
		t.Fatal("controller observation gap counted as healthy time")
	}
	f.observe(t)
	if len(f.jobs(t, "b")) != 1 {
		t.Fatal("new observation window did not complete")
	}
}

func TestRolloutNewFailureResurfacesReadPause(t *testing.T) {
	f := newRolloutFixture(t, "linux/amd64", "linux/amd64")
	f.publish(t)
	j := f.jobs(t, "a")[0]
	if e := f.s.ControlRollout("roll", f.get(t).Revision, true, ""); e != nil {
		t.Fatal(e)
	}
	op, e := f.s.Operation("roll")
	if e != nil {
		t.Fatal(e)
	}
	if e = f.s.MarkOperationsRead("owner", true, selection(op)); e != nil {
		t.Fatal(e)
	}
	if e = f.s.ApplyReceipt("a", protocol.JobReceipt{JobID: j.JobID, OperationID: "roll", Status: protocol.TargetFailed, Stage: "activate_failed", Message: "fixture failure after pause"}); e != nil {
		t.Fatal(e)
	}
	if e = f.s.AdvanceRollouts(); e != nil {
		t.Fatal(e)
	}
	got, e := f.s.Operation("roll")
	if e != nil || got.Read || !got.NeedsAttention || f.get(t).State != "blocked" {
		t.Fatal("new failure hidden by prior read", got, e)
	}
	if e = f.s.MarkOperationsRead("owner", true, selection(got)); e != nil {
		t.Fatal(e)
	}
	if len(f.jobs(t, "b")) != 0 || f.get(t).State != "blocked" {
		t.Fatal("reading failure resumed rollout")
	}
}
func TestRolloutReadWithSingleConnection(t *testing.T) {
	f := newRolloutFixture(t, "linux/amd64", "linux/amd64")
	f.publish(t)
	f.s.DB.SetMaxOpenConns(1)
	rows, e := f.s.Rollouts(30)
	if e != nil || len(rows) != 1 || len(rows[0].Members) != 2 {
		t.Fatal(rows, e)
	}
}
func TestRolloutCorruptExpiryRejectsResume(t *testing.T) {
	f := newRolloutFixture(t, "linux/amd64", "linux/amd64")
	f.publish(t)
	if e := f.s.ControlRollout("roll", f.get(t).Revision, true, ""); e != nil {
		t.Fatal(e)
	}
	if _, e := f.s.DB.Exec(`UPDATE release_publications SET expires_at='not-a-date'`); e != nil {
		t.Fatal(e)
	}
	if e := f.s.ControlRollout("roll", f.get(t).Revision, false, ""); e == nil {
		t.Fatal("invalid expiry resumed rollout")
	}
	if f.get(t).State != "paused" {
		t.Fatal("partial resume")
	}
}
