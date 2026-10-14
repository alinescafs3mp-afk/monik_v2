package protocol

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"
)

func TestSparkImmutableEnvelopeAndLegacyCompatibility(t *testing.T) {
	now := time.Now().UTC()
	zero := 0.0
	r := AgentReport{SchemaVersion: 3, AgentID: "a", SessionID: "s", Sequence: 1, ObservedAt: now, Spark: &SparkExtensionV1{SchemaVersion: 1, MemoryTopology: "unified", Metrics: []SparkMetric{{MetricID: "gpu.0.activity", Observation: SparkObservation{Value: &zero, Unit: "percent", Source: "fixture", Quality: "ok", ObservedAt: now}}}}}
	if e := ValidateSpark(r.Spark, now, now); e != nil {
		t.Fatal(e)
	}
	first, e := ObservationPayload(r)
	if e != nil {
		t.Fatal(e)
	}
	r.IsLive = true
	r.ReportedAt = now
	r.JobReceipts = []JobReceipt{{JobID: "j"}}
	same, _ := ObservationPayload(r)
	if string(first) != string(same) {
		t.Fatal("transport mutation changed immutable observation")
	}
	r.Spark.MemoryTopology = "dedicated"
	different, _ := ObservationPayload(r)
	if string(first) == string(different) {
		t.Fatal("Spark omitted from immutable observation")
	}
	var parsed AgentReport
	if e = json.Unmarshal(first, &parsed); e != nil || parsed.Spark == nil {
		t.Fatalf("roundtrip: %v", e)
	}
	legacy := AgentReport{SchemaVersion: 3}
	b, _ := json.Marshal(legacy)
	if strings.Contains(string(b), `"spark"`) {
		t.Fatal("legacy envelope changed")
	}
}
func TestSparkStrictAndFiniteContracts(t *testing.T) {
	for _, raw := range []string{
		`{"schema_version":1,"memory_topology":"unified"}`,
		`{"schema_version":1,"memory_topology":"unified","metrics":null}`,
		`{"schema_version":1,"memory_topology":"unified","metrics":[],"exec":"shell"}`,
		`{"schema_version":1,"schema_version":1,"memory_topology":"unified","metrics":[]}`,
		`{"schema_version":1,"memory_topology":"unified","metrics":[],"control_state":null}`,
	} {
		var v SparkExtensionV1
		if json.Unmarshal([]byte(raw), &v) == nil {
			t.Fatalf("accepted ambiguous/missing input %s", raw)
		}
	}
	now := time.Now()
	nan := math.NaN()
	v := &SparkExtensionV1{SchemaVersion: 1, MemoryTopology: "unified", Metrics: []SparkMetric{{MetricID: "gpu.activity", Observation: SparkObservation{Value: &nan, Unit: "percent", Source: "fixture", ObservedAt: now, Quality: "ok"}}}}
	if ValidateSpark(v, now, now) == nil {
		t.Fatal("accepted nonfinite observation")
	}
	v.Metrics[0].Observation.Value = nil
	v.Metrics[0].Observation.Quality = "unknown"
	if ValidateSpark(v, now, now) == nil {
		t.Fatal("unknown without reason accepted")
	}
	v.Metrics[0].Observation.Reason = "unsupported"
	if e := ValidateSpark(v, now, now); e != nil {
		t.Fatal(e)
	}
}
func TestSparkTypedModelParams(t *testing.T) {
	m := map[string]any{"profile_id": "p", "profile_revision": "sha256:" + strings.Repeat("1", 64), "expected_generation": float64(0), "control_epoch": "epoch", "expected_agent_session": "s", "confirm_disruption": false}
	if _, e := DecodeModelActionParams(m); e != nil {
		t.Fatal(e)
	}
	m["expected_generation"] = 0.5
	if _, e := DecodeModelActionParams(m); e == nil {
		t.Fatal("fraction accepted")
	}
	m["expected_generation"] = 0
	m["confirm_disruption"] = nil
	if _, e := DecodeModelActionParams(m); e == nil {
		t.Fatal("null boolean accepted")
	}
	m["confirm_disruption"] = true
	m["unit"] = "anything"
	if _, e := DecodeModelActionParams(m); e == nil {
		t.Fatal("browser supervisor field accepted")
	}
}

func TestExistingCoordinatorPublicAdapterIsDistinctAndCompatible(t *testing.T) {
	now := time.Now().UTC()
	for _, supervisor := range []string{"existing-coordinator", "systemd-system", "docker"} {
		extension := &SparkExtensionV1{SchemaVersion: 1, MemoryTopology: "unified", Metrics: []SparkMetric{}, Inventory: &SparkInventory{Revision: "sha256:" + strings.Repeat("a", 64), Complete: true, ObservedAt: now, Profiles: []SparkProfile{{ProfileID: "fixed-profile", DisplayName: "controller", ControlReason: "local_control_disabled", ProfileRevision: "sha256:" + strings.Repeat("b", 64), Engine: "custom", Supervisor: supervisor, Settings: []SparkSetting{}, AllowedActions: []string{}}}}}
		if err := ValidateSpark(extension, now, now); err != nil {
			t.Fatalf("%s: %v", supervisor, err)
		}
	}
}
