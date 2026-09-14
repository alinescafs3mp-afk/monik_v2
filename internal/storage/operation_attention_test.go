package storage

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
)

func attentionOp(t *testing.T, s *Store, id string, states ...protocol.TargetStatus) *protocol.Operation {
	t.Helper()
	op := &protocol.Operation{ID: id, Action: "preference.save", Status: protocol.OpRunning, Revision: 1, ClientRequestKey: id, Actor: "owner", CreatedAt: s.now(), Params: map[string]any{"safe": true}}
	for i, st := range states {
		op.Targets = append(op.Targets, protocol.TargetResult{AgentID: fmt.Sprintf("target-%d", i), Status: st, Stage: string(st), Message: string(st), Evidence: map[string]any{"test": true}})
	}
	if e := s.InsertOperation(op, "hash"); e != nil {
		t.Fatal(e)
	}
	got, e := s.Operation(id)
	if e != nil {
		t.Fatal(e)
	}
	return got
}
func selection(op *protocol.Operation) []OperationReadTarget {
	return []OperationReadTarget{{op.ID, op.AttentionRevision}}
}
func TestAudit9ReadPersistsWithoutRewritingExecution(t *testing.T) {
	s, _ := audit5Store(t)
	op := attentionOp(t, s, "read", protocol.TargetFailed)
	if !op.NeedsAttention || op.Read {
		t.Fatal(op)
	}
	for i := 0; i < 2; i++ {
		if e := s.MarkOperationsRead("owner", true, selection(op)); e != nil {
			t.Fatal(e)
		}
	}
	got, e := s.Operation(op.ID)
	if e != nil {
		t.Fatal(e)
	}
	if !got.Read || got.NeedsAttention || !got.AttentionRequired || got.ReadBy != "owner" || got.ReadAt == nil || got.Status != op.Status || got.Targets[0].Status != protocol.TargetFailed || got.Revision != op.Revision {
		t.Fatalf("read rewrote execution: %+v", got)
	}
	var count int
	if e = s.DB.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE action='operation.read'`).Scan(&count); e != nil || count != 1 {
		t.Fatal(count, e)
	}
	path := s.Path()
	s.Close()
	restored, e := Open(path, s.Clock)
	if e != nil {
		t.Fatal(e)
	}
	defer restored.Close()
	again, e := restored.Operation(op.ID)
	if e != nil || !again.Read || !again.ReadAt.Equal(*got.ReadAt) {
		t.Fatal(again, e)
	}
	if e = restored.MarkOperationsRead("owner", false, selection(again)); e != nil {
		t.Fatal(e)
	}
	again, e = restored.Operation(op.ID)
	if e != nil || again.Read || !again.NeedsAttention {
		t.Fatal(again, e)
	}
}
func TestAudit9OnlyNewAttentionReopensReadOperation(t *testing.T) {
	s, _ := audit5Store(t)
	op := attentionOp(t, s, "mixed", protocol.TargetFailed, protocol.TargetRunning)
	if !op.NeedsAttention || op.Status != protocol.OpRunning {
		t.Fatal("mixed-running failure must need attention")
	}
	if e := s.MarkOperationsRead("owner", true, selection(op)); e != nil {
		t.Fatal(e)
	}
	if e := s.UpdateTarget(op.ID, "target-1", protocol.TargetRunning, "download", "75 percent", "", false, nil); e != nil {
		t.Fatal(e)
	}
	got, e := s.Operation(op.ID)
	if e != nil || !got.Read || got.AttentionRevision != op.AttentionRevision {
		t.Fatal("progress re-opened old error", got, e)
	}
	if e = s.UpdateTarget(op.ID, "target-1", protocol.TargetFailed, "failed", "timeout", "timeout", true, nil); e != nil {
		t.Fatal(e)
	}
	got, e = s.Operation(op.ID)
	if e != nil || got.Read || !got.NeedsAttention || got.AttentionRevision <= op.AttentionRevision {
		t.Fatal("new error stayed read", got, e)
	}
	if e = s.MarkOperationsRead("owner", true, selection(op)); !errors.Is(e, ErrConflict) {
		t.Fatal("stale screen acknowledged new error", e)
	}
	if e = s.MarkOperationsRead("owner", true, selection(got)); e != nil {
		t.Fatal(e)
	}
	if e = s.UpdateTarget(op.ID, "target-1", protocol.TargetFailed, "failed", "timeout", "timeout", true, map[string]any{"received_again": true}); e != nil {
		t.Fatal(e)
	}
	same, e := s.Operation(op.ID)
	if e != nil || !same.Read {
		t.Fatal("duplicate evidence opened new notice", same, e)
	}
}
func TestAudit9BulkReadIsVersionedAndAtomic(t *testing.T) {
	s, _ := audit5Store(t)
	a := attentionOp(t, s, "a", protocol.TargetFailed)
	b := attentionOp(t, s, "b", protocol.TargetFailed)
	if e := s.UpdateTarget(b.ID, "target-0", protocol.TargetFailed, "different", "different", "different", false, nil); e != nil {
		t.Fatal(e)
	}
	targets := append(selection(a), selection(b)...)
	if e := s.MarkOperationsRead("owner", true, targets); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	got, _ := s.Operation(a.ID)
	if got.Read {
		t.Fatal("bulk partially committed")
	}
	b, _ = s.Operation(b.ID)
	targets = append(selection(a), selection(b)...)
	if e := s.MarkOperationsRead("owner", true, targets); e != nil {
		t.Fatal(e)
	}
	for _, id := range []string{a.ID, b.ID} {
		got, _ = s.Operation(id)
		if !got.Read {
			t.Fatal("missing read", id)
		}
	}
	if e := s.MarkOperationsRead("owner", false, append(selection(a), selection(a)...)); !errors.Is(e, ErrInvalidOperationQuery) {
		t.Fatal(e)
	}
}
func TestAudit9ReadAuditFailureRollsBack(t *testing.T) {
	s, _ := audit5Store(t)
	op := attentionOp(t, s, "audit-fault", protocol.TargetFailed)
	if _, e := s.DB.Exec(`CREATE TRIGGER read_audit_fault BEFORE INSERT ON audit_events BEGIN SELECT RAISE(ABORT,'injected failure'); END`); e != nil {
		t.Fatal(e)
	}
	if e := s.MarkOperationsRead("owner", true, selection(op)); e == nil {
		t.Fatal("audit failure ignored")
	}
	got, e := s.Operation(op.ID)
	if e != nil || got.Read || !got.NeedsAttention {
		t.Fatal("partial read commit", got, e)
	}
}
func TestAudit9CountsIncludeOldErrorsAndOneConnectionReads(t *testing.T) {
	s, _ := audit5Store(t)
	old := attentionOp(t, s, "000-old", protocol.TargetFailed)
	for i := 0; i < 205; i++ {
		attentionOp(t, s, fmt.Sprintf("new-%03d", i), protocol.TargetSucceeded)
	}
	s.DB.SetMaxOpenConns(1)
	s.DB.SetMaxIdleConns(1)
	page, e := s.QueryOperations(OperationQuery{Limit: 10})
	if e != nil {
		t.Fatal(e)
	}
	if page.Counts.Total != 206 || page.Counts.UnreadAttention != 1 || len(page.Operations) != 10 || page.Next == "" {
		t.Fatalf("wrong global counts: %+v", page.Counts)
	}
	page, e = s.QueryOperations(OperationQuery{Filter: "attention", Limit: 10})
	if e != nil || len(page.Operations) != 1 || page.Operations[0].ID != old.ID {
		t.Fatal(page, e)
	}
	if e = s.MarkOperationsRead("owner", true, selection(old)); e != nil {
		t.Fatal(e)
	}
	c, e := s.OperationCounts()
	if e != nil || c.UnreadAttention != 0 || c.Attention != 1 || c.Read != 1 {
		t.Fatal(c, e)
	}
}
func TestAudit9PaginationEqualTimesAndNewInsert(t *testing.T) {
	s, _ := audit5Store(t)
	for i := 0; i < 7; i++ {
		attentionOp(t, s, fmt.Sprint(i), protocol.TargetFailed)
	}
	first, e := s.QueryOperations(OperationQuery{Limit: 2})
	if e != nil {
		t.Fatal(e)
	}
	attentionOp(t, s, "later", protocol.TargetFailed)
	ids := map[string]bool{}
	page := first
	for {
		for _, op := range page.Operations {
			if ids[op.ID] || op.ID == "later" {
				t.Fatal("unstable page", op.ID)
			}
			ids[op.ID] = true
		}
		if page.Next == "" {
			break
		}
		page, e = s.QueryOperations(OperationQuery{Limit: 2, Before: page.Next})
		if e != nil {
			t.Fatal(e)
		}
	}
	if len(ids) != 7 {
		t.Fatal(ids)
	}
	for _, q := range []OperationQuery{{Before: first.Next, Filter: "running"}, {Before: "bad"}, {Limit: 201}, {Read: "bogus"}, {Filter: "bogus"}} {
		if _, e = s.QueryOperations(q); !errors.Is(e, ErrInvalidOperationQuery) {
			t.Fatal(q, e)
		}
	}
}
func TestAudit9AttentionMigrationSeedsButDoesNotForgetRead(t *testing.T) {
	s, _ := audit5Store(t)
	op := attentionOp(t, s, "legacy", protocol.TargetFailed)
	if _, e := s.DB.Exec(`DELETE FROM operation_attention`); e != nil {
		t.Fatal(e)
	}
	if e := s.initializeOperationAttention(); e != nil {
		t.Fatal(e)
	}
	got, e := s.Operation(op.ID)
	if e != nil || !got.NeedsAttention {
		t.Fatal(got, e)
	}
	if e = s.MarkOperationsRead("owner", true, selection(got)); e != nil {
		t.Fatal(e)
	}
	if e = s.initializeOperationAttention(); e != nil {
		t.Fatal(e)
	}
	got, e = s.Operation(op.ID)
	if e != nil || !got.Read {
		t.Fatal(got, e)
	}
}
func TestAudit9CorruptTargetsAndTimeReturnErrors(t *testing.T) {
	s, _ := audit5Store(t)
	op := attentionOp(t, s, "corrupt", protocol.TargetFailed)
	if _, e := s.DB.Exec(`UPDATE operation_targets SET evidence='not-json' WHERE operation_id=?`, op.ID); e != nil {
		t.Fatal(e)
	}
	if _, e := s.QueryOperations(OperationQuery{}); e == nil {
		t.Fatal("corrupt targets ignored")
	}
	if _, e := s.DB.Exec(`UPDATE operation_targets SET evidence='{}'; UPDATE operations SET created_at='not-time'`); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Operation(op.ID); e == nil {
		t.Fatal("corrupt time ignored")
	}
}
func TestAudit9DeadlineReopensPreviouslyReadOperation(t *testing.T) {
	s, clk := audit5Store(t)
	deadline := clk.Now().Add(time.Minute)
	op := &protocol.Operation{ID: "expiry", Action: "agent.collect_now", Status: protocol.OpQueued, Revision: 1, ClientRequestKey: "expiry", Actor: "owner", Params: map[string]any{}, CreatedAt: clk.Now(), Deadline: &deadline, Targets: []protocol.TargetResult{{AgentID: "a", Status: protocol.TargetQueued, Stage: "queued"}}}
	if e := s.InsertOperation(op, "expiry"); e != nil {
		t.Fatal(e)
	}
	got, e := s.Operation(op.ID)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.MarkOperationsRead("owner", true, selection(got)); e != nil {
		t.Fatal(e)
	}
	clk.Advance(2 * time.Minute)
	counts, e := s.OperationCounts()
	if e != nil || counts.UnreadAttention != 1 {
		t.Fatal(counts, e)
	}
	got, e = s.Operation(op.ID)
	if e != nil || got.Read || got.Targets[0].Status != protocol.TargetExpired {
		t.Fatal(got, e)
	}
}

func TestAudit9RecoveryDoesNotResurfaceKnownProblems(t *testing.T) {
	s, _ := audit5Store(t)
	op := attentionOp(t, s, "recover-notice", protocol.TargetFailed, protocol.TargetWaitingOffline)
	if e := s.MarkOperationsRead("owner", true, selection(op)); e != nil {
		t.Fatal(e)
	}
	if e := s.UpdateTarget(op.ID, "target-1", protocol.TargetRunning, "running", "agent returned", "", false, nil); e != nil {
		t.Fatal(e)
	}
	got, e := s.Operation(op.ID)
	if e != nil || !got.Read || got.NeedsAttention || got.AttentionRevision != op.AttentionRevision {
		t.Fatal("recovery redisplayed old failure", got, e)
	}
	if e = s.UpdateTarget(op.ID, "target-1", protocol.TargetFailed, "failure", "a new failure", "new", false, nil); e != nil {
		t.Fatal(e)
	}
	got, e = s.Operation(op.ID)
	if e != nil || got.Read || !got.NeedsAttention {
		t.Fatal(got, e)
	}
}
func TestAudit9TargetAndJobRollbackTogether(t *testing.T) {
	s := auditStore(t)
	j := auditJob(t, s)
	if _, e := s.DB.Exec(`CREATE TRIGGER fail_job_update BEFORE UPDATE ON agent_jobs BEGIN SELECT RAISE(ABORT,'injected job failure'); END`); e != nil {
		t.Fatal(e)
	}
	if e := s.MarkTargetAndJob("op", "owner-agent", protocol.TargetFailed, "failed", "test", false, nil); e == nil {
		t.Fatal("ignored job update failure")
	}
	targets, e := s.Targets("op")
	if e != nil || targets[0].Status != protocol.TargetQueued {
		t.Fatal("partial target commit", targets, e)
	}
	var state string
	if e = s.DB.QueryRow(`SELECT status FROM agent_jobs WHERE job_id=?`, j.JobID).Scan(&state); e != nil || state != "queued" {
		t.Fatal(state, e)
	}
}
func TestAudit9PendingJobsRejectCorruptOrMismatchedEnvelope(t *testing.T) {
	for _, bad := range []string{"null", "broken", `{"job_id":"other"}`} {
		t.Run(bad, func(t *testing.T) {
			s := auditStore(t)
			auditJob(t, s)
			if _, e := s.DB.Exec(`UPDATE agent_jobs SET envelope=?`, bad); e != nil {
				t.Fatal(e)
			}
			if jobs, e := s.PendingJobs("owner-agent", 8); e == nil {
				t.Fatal("invalid job reached agent", jobs)
			}
		})
	}
}
func TestAudit9ReopenReconcilesOlderControllerWrites(t *testing.T) {
	s, _ := audit5Store(t)
	op := attentionOp(t, s, "old-controller", protocol.TargetFailed)
	if e := s.MarkOperationsRead("owner", true, selection(op)); e != nil {
		t.Fatal(e)
	}
	// Simulate an older binary that can write targets but predates read metadata.
	if _, e := s.DB.Exec(`UPDATE operation_targets SET message='different error after downgrade' WHERE operation_id=?`, op.ID); e != nil {
		t.Fatal(e)
	}
	path := s.Path()
	s.Close()
	next, e := Open(path, s.Clock)
	if e != nil {
		t.Fatal(e)
	}
	defer next.Close()
	got, e := next.Operation(op.ID)
	if e != nil || got.Read || !got.NeedsAttention || got.AttentionRevision <= op.AttentionRevision {
		t.Fatal(got, e)
	}
}
