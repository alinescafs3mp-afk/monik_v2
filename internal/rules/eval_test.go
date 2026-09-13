package rules

import (
	"testing"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
)

func TestEvaluateHost(t *testing.T) {
	cpu := 96.0
	h := &protocol.HostMetrics{CPUPercent: &cpu, RAMUsed: 1, RAMTotal: 10, Disks: []protocol.Disk{{UsedPct: 50}}}
	b := EvaluateHost(h, DefaultRules())
	if len(b) == 0 || b[0].Metric != "cpu" || b[0].Severity != "critical" {
		t.Fatalf("%+v", b)
	}
}

func TestFreshness(t *testing.T) {
	now := time.Now()
	if st, _ := AgentFreshness(nil, now); st != "unknown" {
		t.Fatal(st)
	}
	old := now.Add(-40 * time.Second)
	if st, _ := AgentFreshness(&old, now); st != "unreachable" {
		t.Fatal(st)
	}
}
