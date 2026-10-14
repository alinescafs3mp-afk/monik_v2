//go:build linux

package spark

import (
	"strings"
	"testing"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
)

// Exercise real procfs reads across the initial-counter boundary. A current
// successful reading must not still claim its initial failure in the UI.
func TestCollectorRecoveredProcSamplesHaveCurrentReasons(t *testing.T) {
	c := NewCollector()
	_, _, first := c.Snapshot(time.Now().UTC())
	find := func(s *protocol.SparkExtensionV1, id string) protocol.SparkObservation {
		t.Helper()
		for _, m := range s.Metrics {
			if m.MetricID == id {
				return m.Observation
			}
		}
		t.Fatalf("missing actual procfs observation %s", id)
		return protocol.SparkObservation{}
	}
	initial := find(first, "cpu.usage_percent")
	if initial.Value != nil || initial.Quality != "unknown" || initial.Reason != "first_sample_or_counter_reset" {
		t.Fatalf("first counter has no valid baseline: %+v", initial)
	}
	_, _, recovered := c.Snapshot(time.Now().UTC())
	for _, id := range []string{"cpu.usage_percent", "memory.total_bytes", "memory.available_bytes", "memory.swap_total_bytes"} {
		v := find(recovered, id)
		if v.Value == nil || v.Quality != "ok" || v.Reason != "" {
			t.Fatalf("recovered procfs value still carries an unavailable cause: %s %+v", id, v)
		}
	}
	for _, m := range recovered.Metrics {
		if m.Observation.Quality == "ok" && (strings.Contains(m.Observation.Reason, "first_sample_or_counter_reset") || strings.Contains(m.Observation.Reason, "meminfo_field_unavailable")) {
			t.Fatalf("successful observation carries obsolete fallback cause: %+v", m)
		}
	}
	cpu := find(recovered, "agent.cpu_core_percent")
	if cpu.Value == nil || cpu.Quality != "ok" || cpu.Reason != "active_helper_cpu_not_yet_reaped" {
		t.Fatalf("successful CPU sample lost its actual coverage limit: %+v", cpu)
	}
}
