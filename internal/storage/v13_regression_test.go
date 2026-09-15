package storage

import (
	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
	"strings"
	"testing"
	"time"
)

func TestV13HistoricalTelemetryDoesNotDependOnExpiredJobHistory(t *testing.T) {
	s := auditStore(t)
	auditAgent(t, s, "a")
	r := auditReport("a", time.Now().Add(-time.Minute))
	r.IsLive = false
	r.JobReceipts = []protocol.JobReceipt{{JobID: "pruned-job", OperationID: "old-op", Status: protocol.TargetSucceeded}}
	if got, err := s.AcceptReport(r); err != nil || got.Live {
		t.Fatalf("historical telemetry blocked by unrelated command receipt: %+v %v", got, err)
	}
	if n := countRows(t, s, "host_samples"); n != 1 {
		t.Fatal(n)
	}
}

func TestV13ControlOnlyReportPreservesObservedAddresses(t *testing.T) {
	s := auditStore(t)
	auditAgent(t, s, "a")
	if err := s.TouchAgent("a", "session", 1, true, &protocol.HostMetrics{Addresses: []string{"192.0.2.14"}}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.TouchAgent("a", "session", 2, true, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	ag, err := s.Agent("a")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ag.Addresses, "192.0.2.14") {
		t.Fatalf("paused/control-only report erased last known addresses: %q", ag.Addresses)
	}
	if err = s.TouchAgent("a", "session", 3, true, &protocol.HostMetrics{Addresses: []string{}}, nil, nil); err != nil {
		t.Fatal(err)
	}
	ag, err = s.Agent("a")
	if err != nil || ag.Addresses != "[]" {
		t.Fatalf("explicit empty observation did not clear addresses: %+v %v", ag, err)
	}
}
