package storage

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
)

func TestAudit11RejectReceiptForUnpublishedUpdate(t *testing.T) {
	s := updateFixture10(t)
	id, err := s.JobIDFor("plan", "a")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ApplyReceipt("a", protocol.JobReceipt{JobID: id, OperationID: "plan", Status: protocol.TargetRunning, Stage: "download"}); err == nil {
		t.Fatal("receipt accepted before update plan was published")
	}
}
func TestAudit11LifecycleLookupFailsClosed(t *testing.T) {
	s := auditStore(t)
	if _, err := s.DB.Exec("DROP TABLE agent_jobs"); err != nil {
		t.Fatal(err)
	}
	if _, busy := s.HasActiveLifecycle("a"); !busy {
		t.Fatal("database failure presented as no lifecycle conflict")
	}
}
func TestAudit11NotBeforeRespected(t *testing.T) {
	s := auditStore(t)
	j := auditJob(t, s)
	j.NotBefore = time.Now().UTC().Add(time.Hour)
	j.Deadline = j.NotBefore.Add(time.Hour)
	raw, _ := json.Marshal(j)
	if _, err := s.DB.Exec("UPDATE agent_jobs SET envelope=?,deadline=? WHERE job_id=?", string(raw), j.Deadline.Format(dbTimeFormat), j.JobID); err != nil {
		t.Fatal(err)
	}
	got, err := s.PendingJobs("owner-agent", 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatal("future scheduled job is immediately deliverable")
	}
}
