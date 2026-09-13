package storage

import (
	"github.com/alinescafs3mp-afk/monik_v2/internal/clock"
	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
	"path/filepath"
	"testing"
	"time"
)

func auditStore(t *testing.T) *Store {
	t.Helper()
	s, e := Open(filepath.Join(t.TempDir(), "test.db"), clock.Real{})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func auditJob(t *testing.T, s *Store) protocol.JobEnvelope {
	t.Helper()
	op := &protocol.Operation{ID: "op", Action: "agent.collect_now", Status: protocol.OpQueued, ClientRequestKey: "key", Actor: "owner", CreatedAt: time.Now().UTC(), Params: map[string]any{}, Targets: []protocol.TargetResult{{AgentID: "owner-agent", Status: protocol.TargetQueued, Stage: "queued"}}}
	if e := s.InsertOperation(op, "h"); e != nil {
		t.Fatal(e)
	}
	jobs, e := s.PendingJobs("owner-agent", 1)
	if e != nil || len(jobs) != 1 {
		t.Fatalf("%v %v", jobs, e)
	}
	return jobs[0]
}
func TestAuditReceiptCannotCompleteAnotherAgentsJob(t *testing.T) {
	s := auditStore(t)
	job := auditJob(t, s)
	e := s.ApplyReceipt("intruder", protocol.JobReceipt{JobID: job.JobID, OperationID: "op", Status: protocol.TargetSucceeded})
	if e == nil {
		t.Fatal("foreign receipt accepted")
	}
	var st string
	s.DB.QueryRow(`SELECT status FROM agent_jobs WHERE job_id=?`, job.JobID).Scan(&st)
	if st != "queued" {
		t.Fatalf("foreign job mutated: %s", st)
	}
}
func TestAuditDeliveredJobIsRetriedWhenResponseLost(t *testing.T) {
	s := auditStore(t)
	job := auditJob(t, s)
	if e := s.MarkJobDelivered(job.JobID); e != nil {
		t.Fatal(e)
	}
	jobs, e := s.PendingJobs("owner-agent", 1)
	if e != nil || len(jobs) != 1 {
		t.Fatalf("unacknowledged delivery was lost: %v %v", jobs, e)
	}
}

func auditAgent(t *testing.T, s *Store, id string) {
	t.Helper()
	if err := s.InsertAgent(&AgentRow{ID: id, Hostname: id, DesiredConfig: "{}", DesiredHash: "h", DesiredRevision: 1}, "test-verifier"); err != nil {
		t.Fatal(err)
	}
}
func auditReport(id string, at time.Time) protocol.AgentReport {
	cpu := 50.0
	return protocol.AgentReport{SchemaVersion: protocol.SchemaVersion, AgentID: id, SessionID: "s", Sequence: 1, ObservedAt: at, IsLive: true, Host: &protocol.HostMetrics{CPUPercent: &cpu, Hostname: id, RAMTotal: 100, RAMUsed: 40, RAMAvailable: 60}}
}
func countRows(t *testing.T, s *Store, table string) int {
	t.Helper()
	var n int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
func TestAuditReportDuplicateIsIdempotentAndConflictsAreRejected(t *testing.T) {
	s := auditStore(t)
	auditAgent(t, s, "a")
	r := auditReport("a", time.Now().UTC())
	accepted, err := s.AcceptReport(r)
	if err != nil || !accepted.Live {
		t.Fatalf("%+v %v", accepted, err)
	}
	r.IsLive = false
	r.ReportedAt = time.Now().Add(time.Minute)
	accepted, err = s.AcceptReport(r)
	if err != nil || !accepted.Duplicate || accepted.Live {
		t.Fatalf("%+v %v", accepted, err)
	}
	r.Host.RAMUsed = 60
	if _, err = s.AcceptReport(r); err == nil {
		t.Fatal("conflicting sequence accepted")
	}
	if n := countRows(t, s, "host_samples"); n != 1 {
		t.Fatalf("duplicate host rows %d", n)
	}
}
func TestAuditHistoricalHostStoredWithoutRefreshingContact(t *testing.T) {
	s := auditStore(t)
	auditAgent(t, s, "a")
	r := auditReport("a", time.Now().Add(-time.Minute))
	r.IsLive = false
	got, err := s.AcceptReport(r)
	if err != nil || got.Live {
		t.Fatalf("%+v %v", got, err)
	}
	a, _ := s.Agent("a")
	if a.LastLiveAt != nil {
		t.Fatal("history made agent live")
	}
	h, at, err := s.LatestHost("a")
	if err != nil || h.RAMUsed != 40 || !at.Equal(r.ObservedAt) {
		t.Fatalf("history missing %+v %v", h, err)
	}
}
func TestAuditForeignServiceRejectsEntireReport(t *testing.T) {
	s := auditStore(t)
	auditAgent(t, s, "a")
	auditAgent(t, s, "b")
	if err := s.UpsertService(protocol.DiscoveredEndpoint{ServiceID: "foreign", URL: "http://127.0.0.1:9999", DialTarget: "127.0.0.1:9999"}, "b"); err != nil {
		t.Fatal(err)
	}
	r := auditReport("a", time.Now())
	r.Checks = []protocol.CheckObservation{{ServiceID: "foreign", CheckID: "c", ObservedAt: r.ObservedAt, Vantage: "agent/local", Quality: protocol.QualityOK, Transport: "ok"}}
	if _, err := s.AcceptReport(r); err == nil {
		t.Fatal("foreign service accepted")
	}
	if countRows(t, s, "host_samples") != 0 || countRows(t, s, "ingest_receipts") != 0 {
		t.Fatal("partially committed rejected report")
	}
}
func TestAuditWriteFailureRollsBackReportAndReceipt(t *testing.T) {
	s := auditStore(t)
	auditAgent(t, s, "a")
	if _, err := s.DB.Exec(`CREATE TRIGGER fail_receipt BEFORE INSERT ON ingest_receipts BEGIN SELECT RAISE(ABORT,'injected disk failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AcceptReport(auditReport("a", time.Now())); err == nil {
		t.Fatal("failed commit acknowledged")
	}
	if countRows(t, s, "host_samples") != 0 {
		t.Fatal("host survived failed transaction")
	}
	a, _ := s.Agent("a")
	if a.LastLiveAt != nil {
		t.Fatal("contact updated by rolled-back report")
	}
}
func TestAuditPointQueryDoesNotUseFractionalFutureSample(t *testing.T) {
	s := auditStore(t)
	auditAgent(t, s, "a")
	at := time.Now().UTC().Truncate(time.Second)
	for i, offset := range []time.Duration{0, 100 * time.Millisecond} {
		r := auditReport("a", at.Add(offset))
		r.Sequence = int64(i + 1)
		if _, err := s.AcceptReport(r); err != nil {
			t.Fatal(err)
		}
	}
	_, got, err := s.HostAt("a", at)
	if err != nil || !got.Equal(at) {
		t.Fatalf("future selected %v %v", got, err)
	}
	rows, err := s.HostSeries("a", at, at.Add(time.Second))
	if err != nil || len(rows) != 2 || rows[0]["observed_at"] != at.Format(dbTimeFormat) {
		t.Fatalf("chronological order %+v %v", rows, err)
	}
}
func TestAuditReceiptIsIdempotentAndCancellationCannotRecallDelivery(t *testing.T) {
	s := auditStore(t)
	job := auditJob(t, s)
	if err := s.MarkJobDelivered(job.JobID); err != nil {
		t.Fatal(err)
	}
	if err := s.CancelPending("op"); err != nil {
		t.Fatal(err)
	}
	var status string
	s.DB.QueryRow(`SELECT status FROM agent_jobs WHERE job_id=?`, job.JobID).Scan(&status)
	if status != "delivered" {
		t.Fatalf("delivered work falsely cancelled: %s", status)
	}
	rec := protocol.JobReceipt{JobID: job.JobID, OperationID: "op", Status: protocol.TargetRunning, Stage: "collect_pending"}
	if err := s.ApplyReceipt("owner-agent", rec); err != nil {
		t.Fatal(err)
	}
	op, _ := s.Operation("op")
	rev := op.Revision
	if err := s.ApplyReceipt("owner-agent", rec); err != nil {
		t.Fatal(err)
	}
	op, _ = s.Operation("op")
	if op.Revision != rev {
		t.Fatal("duplicate receipt changed revision")
	}
}
func TestAuditQueuedCancellationAndExpiry(t *testing.T) {
	t.Run("cancel unsent", func(t *testing.T) {
		s := auditStore(t)
		job := auditJob(t, s)
		if err := s.CancelPending("op"); err != nil {
			t.Fatal(err)
		}
		var status string
		s.DB.QueryRow(`SELECT status FROM agent_jobs WHERE job_id=?`, job.JobID).Scan(&status)
		if status != string(protocol.TargetCancelledBeforeExec) {
			t.Fatal(status)
		}
	})
	t.Run("expiry", func(t *testing.T) {
		s := auditStore(t)
		job := auditJob(t, s)
		s.DB.Exec(`UPDATE agent_jobs SET deadline=? WHERE job_id=?`, time.Now().Add(-time.Minute).UTC().Format(dbTimeFormat), job.JobID)
		jobs, err := s.PendingJobs("owner-agent", 8)
		if err != nil || len(jobs) != 0 {
			t.Fatalf("%v %v", jobs, err)
		}
		op, _ := s.Operation("op")
		if op.Targets[0].Status != protocol.TargetExpired {
			t.Fatalf("%+v", op)
		}
	})
}
func TestAuditAppliedConfigurationRequiresPublishedHash(t *testing.T) {
	s := auditStore(t)
	auditAgent(t, s, "a")
	if err := s.SetDesired("a", 2, "real-hash", "{}"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetApplied("a", 2, "other"); err == nil {
		t.Fatal("unpublished hash accepted")
	}
	if err := s.SetApplied("a", 2, "real-hash"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetApplied("a", 1, "old"); err == nil {
		t.Fatal("applied revision regressed")
	}
}
func TestAuditIncidentPersistenceRecoveryAndUnknownGap(t *testing.T) {
	s := auditStore(t)
	at := time.Now().Add(-2 * time.Minute).UTC()
	p := IncidentPolicy{PersistFor: time.Minute, RecoverFor: 30 * time.Second, MaxGap: 15 * time.Second, Failures: 1, Successes: 1}
	observe := func(offset time.Duration, known, failed bool) {
		t.Helper()
		if err := s.ObserveIncident("agent", "a", "cpu", at.Add(offset), known, failed, "warning", "cpu_high", p); err != nil {
			t.Fatal(err)
		}
	}
	state := func() string {
		var st string
		s.DB.QueryRow(`SELECT status FROM incidents ORDER BY opened_at DESC LIMIT 1`).Scan(&st)
		return st
	}
	observe(0, true, true)
	observe(5*time.Second, true, true)
	if state() != "pending" {
		t.Fatal("confirmed before duration")
	}
	for d := 10 * time.Second; d <= time.Minute; d += 5 * time.Second {
		observe(d, true, true)
	}
	if state() != "confirmed" {
		t.Fatal("duration not confirmed")
	}
	observe(65*time.Second, false, false)
	if state() != "confirmed" {
		t.Fatal("unknown recovered incident")
	}
	for d := 70 * time.Second; d <= 95*time.Second; d += 5 * time.Second {
		observe(d, true, false)
	}
	if state() != "confirmed" {
		t.Fatal("recovered too early")
	}
	observe(100*time.Second, true, false)
	if state() != "resolved" {
		t.Fatal("recovery evidence ignored")
	}
}
func TestAuditServiceRequiresThreeFailuresTwoSuccesses(t *testing.T) {
	s := auditStore(t)
	at := time.Now().UTC()
	p := IncidentPolicy{MaxGap: 15 * time.Second, Failures: 3, Successes: 2}
	steps := []struct {
		failed bool
		want   string
	}{{true, "pending"}, {true, "pending"}, {true, "confirmed"}, {false, "confirmed"}, {false, "resolved"}}
	for i, step := range steps {
		if err := s.ObserveIncident("service", "s", "http", at.Add(time.Duration(i)*5*time.Second), true, step.failed, "warning", "bad", p); err != nil {
			t.Fatal(err)
		}
		var got string
		s.DB.QueryRow(`SELECT status FROM incidents LIMIT 1`).Scan(&got)
		if got != step.want {
			t.Fatalf("step %d: %s", i, got)
		}
	}
}
func TestAuditObservationGapBreaksPendingStreak(t *testing.T) {
	s := auditStore(t)
	at := time.Now().UTC()
	p := IncidentPolicy{MaxGap: 15 * time.Second, Failures: 3, Successes: 2}
	for _, d := range []time.Duration{0, 5 * time.Second, 40 * time.Second} {
		if err := s.ObserveIncident("service", "s", "http", at.Add(d), true, true, "warning", "bad", p); err != nil {
			t.Fatal(err)
		}
	}
	open, err := s.OpenIncidents()
	if err != nil || len(open) != 1 || open[0]["status"] != "pending" {
		t.Fatalf("%+v %v", open, err)
	}
	if countRows(t, s, "incidents") != 2 {
		t.Fatal("interrupted incident lost")
	}
}

func TestAuditOldLiveSessionCannotRewindCurrentAgent(t *testing.T) {
	s := auditStore(t)
	auditAgent(t, s, "a")
	now := time.Now().UTC()
	r := auditReport("a", now)
	r.SessionID = "current"
	r.Sequence = 2
	if _, err := s.AcceptReport(r); err != nil {
		t.Fatal(err)
	}
	r.SessionID = "older"
	r.Sequence = 200
	r.ObservedAt = now.Add(-time.Minute)
	got, err := s.AcceptReport(r)
	if err != nil || got.Live {
		t.Fatalf("%+v %v", got, err)
	}
	ag, _ := s.Agent("a")
	if ag.SessionID != "current" || ag.LastSeq != 2 {
		t.Fatal("old session rewound current state")
	}
}
func TestAuditSuccessNeedsCommittedMeasurementEvidence(t *testing.T) {
	t.Run("claim only", func(t *testing.T) {
		s := auditStore(t)
		job := auditJob(t, s)
		rec := protocol.JobReceipt{JobID: job.JobID, OperationID: "op", Status: protocol.TargetSucceeded, Message: "collection scheduled"}
		if err := s.ApplyReceipt("owner-agent", rec); err != nil {
			t.Fatal(err)
		}
		op, _ := s.Operation("op")
		if op.Targets[0].Status != protocol.TargetRejected {
			t.Fatal("false success accepted")
		}
	})
	t.Run("actual measurement", func(t *testing.T) {
		s := auditStore(t)
		auditAgent(t, s, "owner-agent")
		job := auditJob(t, s)
		accepted := time.Now().UTC()
		r := auditReport("owner-agent", accepted.Add(time.Millisecond))
		if _, err := s.AcceptReport(r); err != nil {
			t.Fatal(err)
		}
		rec := protocol.JobReceipt{JobID: job.JobID, OperationID: "op", Status: protocol.TargetSucceeded, AcceptedAt: &accepted, Evidence: map[string]any{"sequence": r.Sequence, "session_id": r.SessionID}}
		if err := s.ApplyReceipt("owner-agent", rec); err != nil {
			t.Fatal(err)
		}
		op, _ := s.Operation("op")
		if op.Targets[0].Status != protocol.TargetSucceeded {
			t.Fatalf("%+v", op.Targets)
		}
	})
}

func TestAuditRejectedProofCanBeAcknowledgedAgainAfterLostResponse(t *testing.T) {
	s := auditStore(t)
	job := auditJob(t, s)
	rec := protocol.JobReceipt{JobID: job.JobID, OperationID: "op", Status: protocol.TargetSucceeded, Message: "collection scheduled"}
	if err := s.ApplyReceipt("owner-agent", rec); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyReceipt("owner-agent", rec); err != nil {
		t.Fatalf("lost rejection acknowledgement cannot be retried: %v", err)
	}
}
