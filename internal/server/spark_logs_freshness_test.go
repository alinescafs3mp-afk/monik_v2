package server

import (
	"testing"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
)

func TestSparkLogCachedObservationsKeepOnlineContactFences(t *testing.T) {
	for _, tc := range []struct {
		name                                        string
		controlAge, modelAge, reportAge, contactAge time.Duration
		wantError                                   string
		online                                      bool
	}{
		{"cached20", 20 * time.Second, 20 * time.Second, 5 * time.Second, 5 * time.Second, "", true},
		{"cached31", 31 * time.Second, 31 * time.Second, 5 * time.Second, 5 * time.Second, "", true},
		{"cached45", 45 * time.Second, 45 * time.Second, 5 * time.Second, 5 * time.Second, "", true},
		{"control46", 46 * time.Second, 31 * time.Second, 5 * time.Second, 5 * time.Second, "stale_or_disabled_control", true},
		{"model46", 31 * time.Second, 46 * time.Second, 5 * time.Second, 5 * time.Second, "stale_generation_or_state", true},
		{"contact16", 31 * time.Second, 31 * time.Second, 16 * time.Second, 16 * time.Second, "agent_offline_or_session_changed", false},
		{"report16", 31 * time.Second, 31 * time.Second, 16 * time.Second, 0, "agent_offline_or_session_changed", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newSparkAPI(t, true)
			f.clock.Advance(time.Minute)
			now := f.clock.Now().Add(tc.contactAge)
			f.report.Sequence++
			f.report.ObservedAt = now.Add(-tc.reportAge)
			f.report.Spark.Inventory.ObservedAt = f.report.ObservedAt
			f.report.Spark.ControlState.ObservedAt = now.Add(-tc.controlAge)
			f.report.Spark.ModelStates[0].ObservedAt = now.Add(-tc.modelAge)
			if _, err := f.app.Store.AcceptReport(f.report); err != nil {
				t.Fatal(err)
			}
			f.clock.Advance(tc.contactAge)
			p := protocol.SparkLogRequest{ProfileID: "p", ProfileRevision: f.report.Spark.Inventory.Profiles[0].ProfileRevision, ExpectedGeneration: 1, ControlEpoch: "epoch", ExpectedAgentSession: "session"}
			err := f.app.logRequestValid("spark", p)
			if tc.wantError == "" && err != nil || tc.wantError != "" && (err == nil || err.Error() != tc.wantError) {
				t.Fatalf("log observation/contact fence: got %v, want %q", err, tc.wantError)
			}
			ag, err := f.app.Store.Agent("spark")
			if err != nil {
				t.Fatal(err)
			}
			v, err := f.app.Store.SparkSnapshot("spark")
			if err != nil {
				t.Fatal(err)
			}
			if online := f.app.sparkOnline(ag, v); online != tc.online {
				t.Fatalf("online freshness changed with cached models: got %v, want %v", online, tc.online)
			}
		})
	}
}
