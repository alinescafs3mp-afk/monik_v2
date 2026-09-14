package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
)

func TestAudit3IncidentHistoryOverlapAndStablePagination(t *testing.T) {
	s := auditStore(t)
	now := time.Now().UTC().Truncate(time.Second)
	insert := func(id string, start, end time.Time) {
		t.Helper()
		var ended any
		if !end.IsZero() {
			ended = end.Format(dbTimeFormat)
		}
		_, err := s.DB.Exec(`INSERT INTO incidents(id,entity_type,entity_id,metric,severity,status,opened_at,resolved_at,reason) VALUES(?,'agent','a','cpu','warning','resolved',?,?,'test')`, id, start.Format(dbTimeFormat), ended)
		if err != nil {
			t.Fatal(err)
		}
	}
	insert("c", now.Add(-3*time.Hour), now.Add(-time.Hour))
	insert("b", now.Add(-3*time.Hour), now.Add(-time.Hour))
	insert("a", now.Add(-3*time.Hour), now.Add(-time.Hour))
	insert("outside", now.Add(-5*time.Hour), now.Add(-4*time.Hour))
	q := IncidentQuery{From: now.Add(-2 * time.Hour), To: now, State: "resolved", Limit: 2}
	rows, next, err := s.SearchIncidents(context.Background(), q)
	if err != nil || len(rows) != 2 || next == "" {
		t.Fatalf("first page: %+v %q %v", rows, next, err)
	}
	q.Before = next
	more, next, err := s.SearchIncidents(context.Background(), q)
	if err != nil || len(more) != 1 || next != "" {
		t.Fatalf("second page: %+v %q %v", more, next, err)
	}
	ids := map[string]bool{}
	for _, r := range append(rows, more...) {
		ids[r["id"].(string)] = true
	}
	if len(ids) != 3 || ids["outside"] {
		t.Fatal("overlap/tie pagination lost or duplicated incidents")
	}
}
func TestAudit3RawExportUsesRequestedHalfOpenRange(t *testing.T) {
	s := auditStore(t)
	auditAgent(t, s, "a")
	now := time.Now().UTC().Truncate(time.Second)
	for i := 0; i < 3; i++ {
		cpu := float64(i * 10)
		if err := s.InsertHostSample("a", int64(i+1), "s", now.Add(time.Duration(i)*time.Second), &protocol.HostMetrics{CPUPercent: &cpu}); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := s.ExportSamples(context.Background(), "agent", "a", now, now.Add(2*time.Second))
	if err != nil || len(rows) != 2 {
		t.Fatalf("range: %d %v", len(rows), err)
	}
	var h protocol.HostMetrics
	if err = json.Unmarshal(rows[1].Payload, &h); err != nil || h.CPUPercent == nil || *h.CPUPercent != 10 {
		t.Fatal("wrong actual measurement")
	}
	if rows[1].ReceivedAt == "" {
		t.Fatal("receipt provenance missing")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = s.ExportSamples(ctx, "agent", "a", now, now.Add(2*time.Second)); err == nil {
		t.Fatal("query ignored cancellation")
	}
}
func TestAudit3ExportRefusesPartialOverBudgetFile(t *testing.T) {
	s := auditStore(t)
	auditAgent(t, s, "a")
	now := time.Now().UTC()
	tx, err := s.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20001; i++ {
		if _, err = tx.Exec(`INSERT INTO host_samples(agent_id,observed_at,received_at,payload) VALUES('a',?,?,'{}')`, now.Format(dbTimeFormat), now.Format(dbTimeFormat)); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	rows, err := s.ExportSamples(context.Background(), "agent", "a", now.Add(-time.Second), now.Add(time.Second))
	if err == nil || rows != nil {
		t.Fatal(fmt.Sprint("silent truncation", len(rows), err))
	}
}
