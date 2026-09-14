package storage

import (
	"github.com/alinescafs3mp-afk/monik_v2/internal/clock"
	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
	"path/filepath"
	"testing"
	"time"
)

// These two regressions also compile on the pinned source before the fixes.
func TestAudit5RegressionOwnerNameNotReplacedByReport(t *testing.T) {
	s, e := Open(filepath.Join(t.TempDir(), "db"), nil)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if e = s.InsertAgent(&AgentRow{ID: "host", Hostname: "original", DisplayName: "Owner label", DesiredRevision: 1, DesiredHash: "h", DesiredConfig: "{}"}, "verifier"); e != nil {
		t.Fatal(e)
	}
	if e = s.TouchAgent("host", "session", 1, true, &protocol.HostMetrics{Hostname: "updated", DisplayName: "worker label"}, nil, nil); e != nil {
		t.Fatal(e)
	}
	row, e := s.Agent("host")
	if e != nil {
		t.Fatal(e)
	}
	if row.DisplayName != "Owner label" {
		t.Fatalf("report replaced owner label with %q", row.DisplayName)
	}
}
func TestAudit5RegressionSecondAcknowledgementPreservesFirst(t *testing.T) {
	clk := clock.NewFake(time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC))
	s, e := Open(filepath.Join(t.TempDir(), "db"), clk)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if e = s.InsertIncident(map[string]any{"id": "i", "entity_type": "agent", "entity_id": "a", "metric": "cpu", "severity": "critical", "status": "confirmed", "reason": "fixture"}); e != nil {
		t.Fatal(e)
	}
	if e = s.AckIncident("i", "owner"); e != nil {
		t.Fatal(e)
	}
	rows, _ := s.OpenIncidents()
	first := rows[0]["acked_at"]
	clk.Advance(time.Minute)
	if e = s.AckIncident("i", "other"); e != nil {
		t.Fatal(e)
	}
	rows, _ = s.OpenIncidents()
	if rows[0]["acked_at"] != first {
		t.Fatal("second click rewrote original acknowledgement time")
	}
}
