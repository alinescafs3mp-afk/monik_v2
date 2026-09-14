package storage

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
)

func updateFixture10(t *testing.T) *Store {
	t.Helper()
	s, e := Open(filepath.Join(t.TempDir(), "db"), nil)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	now := time.Now()
	end := now.Add(time.Minute)
	op := &protocol.Operation{ID: "plan", Action: "update.rollout", Status: protocol.OpQueued, Revision: 1, Actor: "owner", ClientRequestKey: "key", CreatedAt: now, Deadline: &end, Params: map[string]any{"release_id": "fixture"}, Targets: []protocol.TargetResult{{AgentID: "a", Status: protocol.TargetQueued}, {AgentID: "b", Status: protocol.TargetQueued}}}
	if e = s.InsertOperation(op, "hash"); e != nil {
		t.Fatal(e)
	}
	return s
}
func TestPreparingUpdateCannotBePolled(t *testing.T) {
	s := updateFixture10(t)
	jobs, e := s.PendingJobs("a", 5)
	if e != nil || len(jobs) != 0 {
		t.Fatal(jobs, e)
	}
	if _, ok := s.HasActiveLifecycle("a"); !ok {
		t.Fatal("preparation did not lock lifecycle")
	}
}
func TestPublishingUpdatePlanIsAtomic(t *testing.T) {
	s := updateFixture10(t)
	_, e := s.DB.Exec(`CREATE TRIGGER fail_second BEFORE UPDATE ON operation_targets WHEN NEW.agent_id='b' BEGIN SELECT RAISE(ABORT,'injected second target'); END;`)
	if e != nil {
		t.Fatal(e)
	}
	plan := []PreparedUpdateTarget{{AgentID: "a", Status: protocol.TargetQueued, Stage: "ready", Evidence: map[string]any{"release_digest": "fixed"}}, {AgentID: "b", Status: protocol.TargetQueued, Stage: "ready"}}
	if e = s.PublishUpdatePlan("plan", plan); e == nil {
		t.Fatal("accepted partial plan")
	}
	jobs, e := s.PendingJobs("a", 5)
	if e != nil || len(jobs) != 0 {
		t.Fatal("first target escaped failed transaction", jobs, e)
	}
	_, _ = s.DB.Exec(`DROP TRIGGER fail_second`)
	if e = s.PublishUpdatePlan("plan", plan); e != nil {
		t.Fatal(e)
	}
	jobs, e = s.PendingJobs("a", 5)
	if e != nil || len(jobs) != 1 || jobs[0].Params["release_digest"] != "fixed" {
		t.Fatal(jobs, e)
	}
	if e = s.PublishUpdatePlan("plan", plan); !errors.Is(e, ErrConflict) {
		t.Fatalf("published twice: %v", e)
	}
}
func TestCorruptCommittedReleaseTrustFailsClosed(t *testing.T) {
	s := updateFixture10(t)
	_, e := s.DB.Exec(`INSERT INTO release_trust VALUES(1,1,'{}','null')`)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.ReleaseTrust(); e == nil {
		t.Fatal("corrupt trust accepted")
	}
}
