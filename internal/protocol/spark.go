package protocol

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/alinescafs3mp-afk/monik_v2/internal/jsonutil"
	"math"
	"regexp"
	"strings"
	"time"
)

const SparkDispatchLease = 30 * time.Second
const SparkControlFreshness = 15 * time.Second

var SparkFeatures = map[string]int{"spark_telemetry_v1": 1, "spark_inventory_v1": 1, "spark_control_v1": 1, "spark_logs_v1": 1}

// SparkExtensionV1 is optional in the unchanged schema-3 envelope. These are
// public projections, never local executable manifests or authority grants.
type SparkExtensionV1 struct {
	SchemaVersion  int                `json:"schema_version"`
	MemoryTopology string             `json:"memory_topology"`
	Metrics        []SparkMetric      `json:"metrics"`
	Inventory      *SparkInventory    `json:"inventory,omitempty"`
	ModelStates    []SparkModelState  `json:"model_states,omitempty"`
	ControlState   *SparkControlState `json:"control_state,omitempty"`
}
type SparkExtension = SparkExtensionV1
type SparkObservation struct {
	Value      *float64  `json:"value"`
	Unit       string    `json:"unit"`
	Source     string    `json:"source"`
	ObservedAt time.Time `json:"observed_at"`
	Quality    string    `json:"quality"`
	Reason     string    `json:"reason,omitempty"`
}
type SparkMetric struct {
	MetricID    string           `json:"metric_id"`
	Observation SparkObservation `json:"observation"`
}
type SparkInventory struct {
	Revision          string         `json:"revision"`
	Complete          bool           `json:"complete"`
	ObservedAt        time.Time      `json:"observed_at"`
	Profiles          []SparkProfile `json:"profiles"`
	RemovedProfileIDs []string       `json:"removed_profile_ids,omitempty"`
}
type SparkSetting struct {
	Name       string `json:"name"`
	Value      any    `json:"value"`
	Provenance string `json:"provenance"`
}
type SparkProfile struct {
	LogAllowed         bool           `json:"log_allowed,omitempty"`
	ProfileID          string         `json:"profile_id"`
	DisplayName        string         `json:"display_name"`
	ProfileRevision    string         `json:"profile_revision"`
	Engine             string         `json:"engine"`
	Supervisor         string         `json:"supervisor"`
	ControlAllowed     bool           `json:"control_allowed"`
	ControlReason      string         `json:"control_reason"`
	ConfiguredModelRef string         `json:"configured_model_ref"`
	Settings           []SparkSetting `json:"settings"`
	AllowedActions     []string       `json:"allowed_actions"`
	EngineVersion      *string        `json:"engine_version,omitempty"`
	SupervisorVersion  *string        `json:"supervisor_version,omitempty"`
	EndpointHint       *string        `json:"endpoint_hint,omitempty"`
	ModelAliases       []string       `json:"model_aliases,omitempty"`
}
type SparkModelState struct {
	ProfileID       string    `json:"profile_id"`
	Generation      int64     `json:"generation"`
	ObservedAt      time.Time `json:"observed_at"`
	SupervisorState string    `json:"supervisor_state"`
	Readiness       string    `json:"readiness"`
	Quality         string    `json:"quality"`
	Reason          string    `json:"reason,omitempty"`
}
type SparkControlState struct {
	Enabled    bool      `json:"enabled"`
	Epoch      *string   `json:"epoch"`
	ObservedAt time.Time `json:"observed_at"`
	Reason     string    `json:"reason"`
}
type ModelActionParams struct {
	ProfileID            string `json:"profile_id"`
	ProfileRevision      string `json:"profile_revision"`
	ExpectedGeneration   int64  `json:"expected_generation"`
	ControlEpoch         string `json:"control_epoch"`
	ExpectedAgentSession string `json:"expected_agent_session"`
	ConfirmDisruption    bool   `json:"confirm_disruption"`
}

// Stage authorization is separate from the original restart dispatch. Only the
// authenticated controller issues it after verified stop evidence.
type SparkStageAuthorization struct {
	ParentOperationID string            `json:"parent_operation_id"`
	StageID           string            `json:"stage_id"`
	Stage             string            `json:"stage"`
	Params            ModelActionParams `json:"params"`
	IssuedAt          time.Time         `json:"issued_at"`
	Deadline          time.Time         `json:"deadline"`
}
type SparkLogRequest struct {
	ProfileID            string `json:"profile_id"`
	ProfileRevision      string `json:"profile_revision"`
	ExpectedGeneration   int64  `json:"expected_generation"`
	ControlEpoch         string `json:"control_epoch"`
	ExpectedAgentSession string `json:"expected_agent_session"`
}

func (p *SparkLogRequest) UnmarshalJSON(b []byte) error {
	if e := jsonutil.Validate(b); e != nil {
		return e
	}
	if _, e := requiredSparkObject(b, "profile_id", "profile_revision", "expected_generation", "control_epoch", "expected_agent_session"); e != nil {
		return e
	}
	type plain SparkLogRequest
	var v plain
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if e := d.Decode(&v); e != nil {
		return e
	}
	*p = SparkLogRequest(v)
	return nil
}

type SparkLogLease struct {
	StreamID  string          `json:"stream_id"`
	Request   SparkLogRequest `json:"request"`
	Deadline  time.Time       `json:"deadline"`
	TailLines int             `json:"tail_lines"`
	TailBytes int             `json:"tail_bytes"`
}
type SparkLogRecord struct {
	Sequence int64     `json:"sequence"`
	At       time.Time `json:"at"`
	Text     string    `json:"text"`
	Gap      bool      `json:"gap,omitempty"`
}
type SparkLogBatch struct {
	StreamID     string           `json:"stream_id"`
	AgentSession string           `json:"agent_session"`
	Records      []SparkLogRecord `json:"records"`
	Closed       bool             `json:"closed,omitempty"`
	Reason       string           `json:"reason,omitempty"`
}

var sparkID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$`)
var sparkRevision = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

func SparkValidID(s string) bool { return sparkID.MatchString(s) }
func sparkMember(v string, options ...string) bool {
	for _, x := range options {
		if x == v {
			return true
		}
	}
	return false
}
func sparkTime(at, report, now time.Time) bool {
	return !at.IsZero() && !at.Before(now.Add(-RawRetention)) && !at.After(now.Add(5*time.Minute)) && !at.After(report.Add(5*time.Second))
}

func (s *SparkExtensionV1) UnmarshalJSON(b []byte) error {
	if err := jsonutil.Validate(b); err != nil {
		return err
	}
	type plain SparkExtensionV1
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	var v plain
	if err := d.Decode(&v); err != nil {
		return err
	}
	if err := validateSparkRequiredJSON(b); err != nil {
		return err
	}
	*s = SparkExtensionV1(v)
	return nil
}
func requiredSparkObject(raw json.RawMessage, keys ...string) (map[string]json.RawMessage, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil || m == nil {
		return nil, fmt.Errorf("Spark object required")
	}
	for _, key := range keys {
		if b, ok := m[key]; !ok || bytes.Equal(bytes.TrimSpace(b), []byte("null")) {
			return nil, fmt.Errorf("Spark field %s required", key)
		}
	}
	return m, nil
}
func validateSparkRequiredJSON(raw []byte) error {
	m, e := requiredSparkObject(raw, "schema_version", "memory_topology", "metrics")
	if e != nil {
		return e
	}
	var metrics []json.RawMessage
	if e = json.Unmarshal(m["metrics"], &metrics); e != nil {
		return e
	}
	for _, metric := range metrics {
		v, e := requiredSparkObject(metric, "metric_id", "observation")
		if e != nil {
			return e
		}
		obs, e := requiredSparkObject(v["observation"], "unit", "source", "observed_at", "quality")
		if e != nil {
			return e
		}
		if _, ok := obs["value"]; !ok {
			return fmt.Errorf("Spark observation value required")
		}
	}
	if raw, ok := m["inventory"]; ok {
		v, e := requiredSparkObject(raw, "revision", "complete", "observed_at", "profiles")
		if e != nil {
			return e
		}
		var profiles []json.RawMessage
		if e = json.Unmarshal(v["profiles"], &profiles); e != nil {
			return e
		}
		for _, p := range profiles {
			if _, e = requiredSparkObject(p, "profile_id", "display_name", "profile_revision", "engine", "supervisor", "control_allowed", "control_reason", "configured_model_ref", "settings", "allowed_actions"); e != nil {
				return e
			}
		}
	}
	if raw, ok := m["model_states"]; ok {
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return fmt.Errorf("Spark model_states cannot be null")
		}
		var states []json.RawMessage
		if e = json.Unmarshal(raw, &states); e != nil {
			return e
		}
		for _, st := range states {
			if _, e = requiredSparkObject(st, "profile_id", "generation", "observed_at", "supervisor_state", "readiness", "quality"); e != nil {
				return e
			}
		}
	}
	if raw, ok := m["control_state"]; ok {
		v, e := requiredSparkObject(raw, "enabled", "observed_at", "reason")
		if e != nil {
			return e
		}
		if _, ok = v["epoch"]; !ok {
			return fmt.Errorf("Spark control epoch required")
		}
	}
	return nil
}
func ValidateSpark(s *SparkExtensionV1, report, now time.Time) error {
	if s == nil {
		return nil
	}
	if s.SchemaVersion != 1 || !sparkMember(s.MemoryTopology, "unified", "dedicated", "unknown") || s.Metrics == nil || len(s.Metrics) > 512 || len(s.ModelStates) > 64 {
		return fmt.Errorf("unsupported Spark version/topology or item budget")
	}
	seen := map[string]bool{}
	for _, m := range s.Metrics {
		o := m.Observation
		if !SparkValidID(m.MetricID) || seen[m.MetricID] || len(o.Unit) < 1 || len(o.Unit) > 32 || len(o.Source) < 1 || len(o.Source) > 128 || len(o.Reason) > 256 || !sparkTime(o.ObservedAt, report, now) || !sparkMember(o.Quality, "ok", "unsupported", "permission_denied", "unknown", "stale", "error") {
			return fmt.Errorf("invalid Spark metric")
		}
		seen[m.MetricID] = true
		if o.Value != nil && (math.IsNaN(*o.Value) || math.IsInf(*o.Value, 0)) {
			return fmt.Errorf("nonfinite Spark observation")
		}
		if o.Quality == "ok" && o.Value == nil || o.Quality != "ok" && o.Quality != "stale" && (o.Value != nil || o.Reason == "") || o.Quality == "stale" && o.Reason == "" {
			return fmt.Errorf("invalid Spark observation quality/value")
		}
	}
	if i := s.Inventory; i != nil {
		if !sparkRevision.MatchString(i.Revision) || !sparkTime(i.ObservedAt, report, now) || i.Profiles == nil || len(i.Profiles) > 64 || len(i.RemovedProfileIDs) > 64 {
			return fmt.Errorf("invalid Spark inventory")
		}
		seen = map[string]bool{}
		for _, p := range i.Profiles {
			if !SparkValidID(p.ProfileID) || seen[p.ProfileID] || !sparkRevision.MatchString(p.ProfileRevision) || len(p.DisplayName) > 256 || len(p.ConfiguredModelRef) > 256 || len(p.ControlReason) > 256 || p.Settings == nil || p.AllowedActions == nil || len(p.Settings) > 128 || len(p.AllowedActions) > 3 || len(p.ModelAliases) > 32 || !sparkMember(p.Engine, "llama.cpp", "vllm", "sglang", "tensorrt-llm", "ollama", "custom", "unknown") || !sparkMember(p.Supervisor, "systemd-system", "systemd-user", "docker", "approved-launcher", "existing-coordinator", "unknown") {
				return fmt.Errorf("invalid Spark profile")
			}
			seen[p.ProfileID] = true
			if !p.ControlAllowed && (len(p.AllowedActions) != 0 || p.ControlReason == "") || p.ControlAllowed && len(p.AllowedActions) == 0 {
				return fmt.Errorf("invalid Spark profile control projection")
			}
			acts := map[string]bool{}
			for _, act := range p.AllowedActions {
				if !sparkMember(act, "start", "stop", "restart") || acts[act] {
					return fmt.Errorf("invalid Spark action")
				}
				acts[act] = true
			}
			for _, st := range p.Settings {
				if len(st.Name) > 256 || !sparkMember(st.Provenance, "configured", "observed-effective", "unknown") {
					return fmt.Errorf("invalid Spark setting")
				}
				switch v := st.Value.(type) {
				case nil, bool:
				case string:
					if len(v) > 1024 {
						return fmt.Errorf("oversized Spark setting")
					}
				case float64:
					if math.IsNaN(v) || math.IsInf(v, 0) {
						return fmt.Errorf("nonfinite Spark setting")
					}
				case int, int64, json.Number:
				default:
					return fmt.Errorf("non-scalar Spark setting")
				}
			}
			for _, v := range []*string{p.EngineVersion, p.SupervisorVersion} {
				if v != nil && len(*v) > 128 {
					return fmt.Errorf("oversized Spark version")
				}
			}
			if p.EndpointHint != nil && (len(*p.EndpointHint) > 512 || strings.ContainsAny(*p.EndpointHint, "\r\n")) {
				return fmt.Errorf("invalid Spark endpoint hint")
			}
			aliases := map[string]bool{}
			for _, alias := range p.ModelAliases {
				if len(alias) > 256 || aliases[alias] {
					return fmt.Errorf("invalid Spark alias")
				}
				aliases[alias] = true
			}
		}
		for _, id := range i.RemovedProfileIDs {
			if !SparkValidID(id) || seen[id] {
				return fmt.Errorf("invalid Spark inventory removal")
			}
			seen[id] = true
		}
	}
	seen = map[string]bool{}
	for _, st := range s.ModelStates {
		if !SparkValidID(st.ProfileID) || seen[st.ProfileID] || st.Generation < 0 || st.Generation > 9007199254740991 || !sparkTime(st.ObservedAt, report, now) || !sparkMember(st.SupervisorState, "stopped", "starting", "running", "stopping", "failed", "unknown") || !sparkMember(st.Readiness, "ready", "unready", "unknown", "not_applicable") || !sparkMember(st.Quality, "ok", "stale", "error", "unknown") || len(st.Reason) > 256 {
			return fmt.Errorf("invalid Spark model state")
		}
		seen[st.ProfileID] = true
		if st.Readiness == "ready" && st.SupervisorState != "running" {
			return fmt.Errorf("ready Spark state lacks running fresh evidence")
		}
	}
	if c := s.ControlState; c != nil {
		if !sparkTime(c.ObservedAt, report, now) || len(c.Reason) > 256 || c.Enabled && (c.Epoch == nil || !SparkValidID(*c.Epoch)) || !c.Enabled && c.Epoch != nil {
			return fmt.Errorf("invalid Spark control state")
		}
	}
	return nil
}
func DecodeModelActionParams(m map[string]any) (ModelActionParams, error) {
	var p ModelActionParams
	for _, key := range []string{"profile_id", "profile_revision", "expected_generation", "control_epoch", "expected_agent_session", "confirm_disruption"} {
		if value, ok := m[key]; !ok || value == nil {
			return p, fmt.Errorf("missing %s", key)
		}
	}
	if _, ok := m["confirm_disruption"].(bool); !ok {
		return p, fmt.Errorf("confirm_disruption must be boolean")
	}
	b, e := json.Marshal(m)
	if e != nil {
		return p, e
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if e = d.Decode(&p); e != nil {
		return p, e
	}
	if !SparkValidID(p.ProfileID) || !sparkRevision.MatchString(p.ProfileRevision) || p.ExpectedGeneration < 0 || p.ExpectedGeneration > 9007199254740991 || !SparkValidID(p.ControlEpoch) || !SparkValidID(p.ExpectedAgentSession) {
		return p, fmt.Errorf("invalid model action binding")
	}
	return p, nil
}
