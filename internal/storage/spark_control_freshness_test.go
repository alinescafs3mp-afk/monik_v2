package storage

import (
	"testing"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
)

func TestSparkCachedControlFreshnessKeepsContactAndReportFences(t *testing.T) {
	for _, tc := range []struct {
		name                                        string
		controlAge, modelAge, reportAge, contactAge time.Duration
		wantError                                   string
	}{
		{"cached20", 20 * time.Second, 20 * time.Second, 5 * time.Second, 5 * time.Second, ""},
		{"cached31", 31 * time.Second, 31 * time.Second, 5 * time.Second, 5 * time.Second, ""},
		{"cached45", 45 * time.Second, 45 * time.Second, 5 * time.Second, 5 * time.Second, ""},
		{"control46", 46 * time.Second, 31 * time.Second, 5 * time.Second, 5 * time.Second, "stale_or_disabled_control"},
		{"model46", 31 * time.Second, 46 * time.Second, 5 * time.Second, 5 * time.Second, "stale_generation_or_state"},
		{"contact16", 31 * time.Second, 31 * time.Second, 16 * time.Second, 16 * time.Second, "agent_offline_or_session_changed"},
		{"report16", 31 * time.Second, 31 * time.Second, 16 * time.Second, 0, "stale_or_disabled_control"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, c, _, report := sparkStorageFixture(t)
			now := c.Now().Add(tc.contactAge)
			report.ObservedAt = now.Add(-tc.reportAge)
			report.Spark.Inventory.ObservedAt = report.ObservedAt
			report.Spark.ControlState.ObservedAt = now.Add(-tc.controlAge)
			report.Spark.ModelStates[0].ObservedAt = now.Add(-tc.modelAge)
			if _, err := s.AcceptReport(report); err != nil {
				t.Fatal(err)
			}
			c.Advance(tc.contactAge)
			params, err := protocol.DecodeModelActionParams(modelParams(report))
			if err != nil {
				t.Fatal(err)
			}
			_, _, err = s.ValidateSparkControl("spark", params, "model.stop")
			if tc.wantError == "" && err != nil || tc.wantError != "" && (err == nil || err.Error() != tc.wantError) {
				t.Fatalf("cached observation/contact fence: got %v, want %q", err, tc.wantError)
			}
			v, err := s.SparkSnapshot("spark")
			if err != nil || !v.ObservedAt.Equal(report.ObservedAt) || !v.Spark.ControlState.ObservedAt.Equal(report.Spark.ControlState.ObservedAt) || !v.Spark.ModelStates[0].ObservedAt.Equal(report.Spark.ModelStates[0].ObservedAt) {
				t.Fatalf("cached observation timestamps changed: %+v %v", v, err)
			}
		})
	}
}

func TestSparkRestartStartStageRequiresFreshControlAndShutdownProof(t *testing.T) {
	for _, tc := range []struct {
		name            string
		age             time.Duration
		refreshControl  bool
		refreshShutdown bool
		wantError       string
	}{
		{"at15", 15 * time.Second, false, false, ""},
		{"control16", 16 * time.Second, false, true, "stale_or_disabled_control"},
		{"shutdown16", 16 * time.Second, true, false, "restart_stop_unverified"},
		{"both16", 16 * time.Second, false, false, "stale_or_disabled_control"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, c, _, report, job, _ := sparkRestartStartFixture(t, false, "stopped", 2)
			c.Advance(tc.age)
			report.Sequence++
			report.ObservedAt = c.Now()
			if tc.refreshControl {
				report.Spark.ControlState.ObservedAt = c.Now()
			}
			if tc.refreshShutdown {
				report.Spark.ModelStates[0].ObservedAt = c.Now()
			}
			if _, err := s.AcceptReport(report); err != nil {
				t.Fatal(err)
			}
			params, err := protocol.DecodeModelActionParams(modelParams(report))
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err = s.ValidateSparkControl("spark", params, "model.start"); err != nil {
				t.Fatal("snapshot may still propose start", err)
			}
			auth, err := s.AuthorizeSparkStartStage("spark", "restart", job.JobID)
			if tc.wantError == "" {
				if err != nil || auth == nil {
					t.Fatalf("fresh stage proof rejected: %+v %v", auth, err)
				}
				return
			}
			if err == nil || err.Error() != tc.wantError || auth != nil {
				t.Fatalf("old stage proof authorized: %+v %v, want %s", auth, err, tc.wantError)
			}
			var stored string
			if err = s.DB.QueryRow(`SELECT stage_authorization FROM spark_intents WHERE operation_id='restart'`).Scan(&stored); err != nil || stored != "" {
				t.Fatalf("refused proof minted a lease: %q %v", stored, err)
			}
			refreshSparkReport(&report, c)
			if _, err = s.AcceptReport(report); err != nil {
				t.Fatal(err)
			}
			auth, err = s.AuthorizeSparkStartStage("spark", "restart", job.JobID)
			if err != nil || auth == nil || auth.Params.ExpectedGeneration != 2 || auth.Deadline.Sub(auth.IssuedAt) != protocol.SparkDispatchLease {
				t.Fatalf("fresh shutdown/control refresh did not recover exact stage: %+v %v", auth, err)
			}
		})
	}
}
