package storage

import (
	"context"
	"testing"
	"time"
)

func TestAudit6CriticalEscalationRequiresNewAcknowledgement(t *testing.T) {
	s, clk := audit5Store(t)
	policy := IncidentPolicy{MaxGap: time.Minute, Failures: 1, Successes: 1}
	if err := s.ObserveIncident("agent", "host", "cpu", clk.Now(), true, true, "warning", "high CPU", policy); err != nil {
		t.Fatal(err)
	}
	id, err := s.FindOpenIncident("host", "cpu")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.AckIncident(id, "owner"); err != nil {
		t.Fatal(err)
	}
	clk.Advance(5 * time.Second)
	if err = s.ObserveIncident("agent", "host", "cpu", clk.Now(), true, true, "critical", "critical CPU", policy); err != nil {
		t.Fatal(err)
	}
	rows, err := s.OpenIncidents()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0]["acked_at"] != "" {
		t.Fatalf("critical escalation stayed read: %v", rows)
	}
}
func TestAudit6CorruptServiceObservationsAreErrors(t *testing.T) {
	s, clk := audit5Store(t)
	_, err := s.DB.Exec(`INSERT INTO service_observations(agent_id,service_id,observed_at,received_at,payload) VALUES('host','service',?,?,?)`, clk.Now().Format(dbTimeFormat), clk.Now().Format(dbTimeFormat), `{"truncated":`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.LatestCheckObs("service"); err == nil {
		t.Error("corrupt latest observation became valid zero-value data")
	}
	if _, err = s.CheckSeries("service", clk.Now().Add(-time.Minute), clk.Now().Add(time.Minute)); err == nil {
		t.Error("corrupt series became valid zero-value data")
	}
	// Existing export correctly rejects the same corruption and must keep doing so.
	if _, err = s.ExportSamples(context.Background(), "service", "service", clk.Now().Add(-time.Minute), clk.Now().Add(time.Minute)); err == nil {
		t.Error("corrupt export accepted")
	}
}
