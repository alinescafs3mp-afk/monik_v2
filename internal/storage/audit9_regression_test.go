package storage

import (
	"errors"
	"testing"

	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
)

func TestAudit9MissingTargetCannotSucceed(t *testing.T) {
	s := auditStore(t)
	auditJob(t, s)
	if e := s.UpdateTarget("op", "absent", protocol.TargetSucceeded, "done", "done", "", false, nil); !errors.Is(e, ErrNotFound) {
		t.Fatalf("missing target must fail, got %v", e)
	}
}
func TestAudit9CorruptOperationCannotLookValid(t *testing.T) {
	s := auditStore(t)
	auditJob(t, s)
	if _, e := s.DB.Exec(`UPDATE operations SET params='{"broken"' WHERE id='op'`); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Operation("op"); e == nil {
		t.Fatal("corrupted operation silently returned as valid")
	}
}
func TestAudit9LateReceiptCannotRegressProgress(t *testing.T) {
	s := auditStore(t)
	j := auditJob(t, s)
	if e := s.ApplyReceipt("owner-agent", protocol.JobReceipt{JobID: j.JobID, OperationID: "op", Status: protocol.TargetRunning, Stage: "running"}); e != nil {
		t.Fatal(e)
	}
	_ = s.ApplyReceipt("owner-agent", protocol.JobReceipt{JobID: j.JobID, OperationID: "op", Status: protocol.TargetAccepted, Stage: "accepted"})
	targets, e := s.Targets("op")
	if e != nil {
		t.Fatal(e)
	}
	if targets[0].Status != protocol.TargetRunning {
		t.Fatalf("late accepted receipt rewound running state: %s", targets[0].Status)
	}
}
func TestAudit9TargetAndAggregateRollbackTogether(t *testing.T) {
	s := auditStore(t)
	auditJob(t, s)
	if _, e := s.DB.Exec(`CREATE TRIGGER fail_op_update BEFORE UPDATE ON operations BEGIN SELECT RAISE(ABORT,'injected write failure'); END`); e != nil {
		t.Fatal(e)
	}
	if e := s.UpdateTarget("op", "owner-agent", protocol.TargetFailed, "failed", "bad", "test", false, nil); e == nil {
		t.Fatal("injected failure was ignored")
	}
	targets, e := s.Targets("op")
	if e != nil {
		t.Fatal(e)
	}
	if targets[0].Status != protocol.TargetQueued {
		t.Fatalf("partial target write survived aggregate failure: %s", targets[0].Status)
	}
}
