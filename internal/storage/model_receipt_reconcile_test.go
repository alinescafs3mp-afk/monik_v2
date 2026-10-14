package storage

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
)

func TestReconcileUnverifiedModelReceiptsBoundedAndFair(t *testing.T) {
	s, c, owner, report := sparkStorageFixture(t)
	// A single connection proves that reconciliation closes its SELECT before
	// applying receipts in write transactions.
	s.DB.SetMaxOpenConns(1)
	profile := report.Spark.Inventory.Profiles[0]
	state := report.Spark.ModelStates[0]
	report.Spark.Inventory.Profiles = nil
	report.Spark.ModelStates = nil
	for i := 0; i < 17; i++ {
		p, st := profile, state
		p.ProfileID = fmt.Sprintf("p%02d", i)
		st.ProfileID = p.ProfileID
		report.Spark.Inventory.Profiles = append(report.Spark.Inventory.Profiles, p)
		report.Spark.ModelStates = append(report.Spark.ModelStates, st)
	}
	if _, err := s.AcceptReport(report); err != nil {
		t.Fatal(err)
	}
	for i, profile := range report.Spark.Inventory.Profiles {
		op := sparkSafetyStop(c, owner, report, fmt.Sprintf("stop-%02d", i))
		op.Params["profile_id"] = profile.ProfileID
		if err := s.InsertSparkOperation(op, op.ID, owner); err != nil {
			t.Fatal(err)
		}
	}
	jobs, err := s.ClaimPendingJobs("spark", 32)
	if err != nil || len(jobs) != 17 {
		t.Fatalf("claim independent profile stops: %d %v", len(jobs), err)
	}
	for _, job := range jobs {
		rec := protocol.JobReceipt{JobID: job.JobID, OperationID: job.OperationID, Status: protocol.TargetSucceeded, Stage: "model_stopped", Evidence: map[string]any{"profile_id": job.Params["profile_id"], "profile_revision": profile.ProfileRevision, "control_epoch": "epoch", "agent_session": report.SessionID, "generation": float64(2), "supervisor_state": "failed", "readiness": "not_applicable", "reason": "failed_service_shutdown_verified", "shutdown_verified": true, "instance_identity": "sha256:" + strings.Repeat("a", 64)}}
		if err = s.ApplyReceipt("spark", rec); err != nil {
			t.Fatal(err)
		}
	}
	var candidateJob, candidateProfile string
	if err = s.DB.QueryRow(`SELECT j.job_id,json_extract(j.envelope,'$.params.profile_id') FROM agent_jobs j JOIN operation_targets t ON t.job_id=j.job_id ORDER BY t.updated_at,j.job_id LIMIT 1 OFFSET 16`).Scan(&candidateJob, &candidateProfile); err != nil {
		t.Fatal(err)
	}
	var original, envelope, deadline, delivered string
	if err = s.DB.QueryRow(`SELECT result,envelope,deadline,delivered_at FROM agent_jobs WHERE job_id=?`, candidateJob).Scan(&original, &envelope, &deadline, &delivered); err != nil {
		t.Fatal(err)
	}
	c.Advance(time.Second)
	refreshSparkReport(&report, c)
	for i := range report.Spark.ModelStates {
		st := &report.Spark.ModelStates[i]
		if st.ProfileID == candidateProfile {
			st.Generation, st.SupervisorState, st.Readiness, st.Reason = 2, "failed", "not_applicable", "failed_service_shutdown_verified"
		}
	}
	if _, err = s.AcceptReport(report); err != nil {
		t.Fatal(err)
	}
	if err = s.ReconcileUnverifiedModelReceipts("spark"); err != nil {
		t.Fatal(err)
	}
	var confirmed, attempted int
	if err = s.DB.QueryRow(`SELECT COUNT(*) FROM agent_jobs WHERE status='succeeded'`).Scan(&confirmed); err != nil || confirmed != 0 {
		t.Fatalf("reconciliation exceeded 16 entries: confirmed=%d %v", confirmed, err)
	}
	if err = s.DB.QueryRow(`SELECT COUNT(*) FROM operation_targets WHERE updated_at=?`, c.Now().Format(dbTimeFormat)).Scan(&attempted); err != nil || attempted != 16 {
		t.Fatalf("bounded reconciliation did not inspect exactly 16 candidates: %d %v", attempted, err)
	}
	c.Advance(time.Second)
	if err = s.ReconcileUnverifiedModelReceipts("spark"); err != nil {
		t.Fatal(err)
	}
	var gotOriginal, gotEnvelope, gotDeadline, gotDelivered, status string
	if err = s.DB.QueryRow(`SELECT result,envelope,deadline,delivered_at,status FROM agent_jobs WHERE job_id=?`, candidateJob).Scan(&gotOriginal, &gotEnvelope, &gotDeadline, &gotDelivered, &status); err != nil {
		t.Fatal(err)
	}
	if status != "succeeded" || gotOriginal != original || gotEnvelope != envelope || gotDeadline != deadline || gotDelivered != delivered {
		t.Fatalf("later recoverable receipt starved or execution identity changed: %s", status)
	}
	if countRows(t, s, "agent_jobs") != 17 || countRows(t, s, "operations") != 17 {
		t.Fatal("reconciliation created another effect or operation")
	}
}
