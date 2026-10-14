// Package spark is the separately built Linux monitoring worker and narrow local
// control broker. It deliberately does not import the general agent runtime.
package spark

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/alinescafs3mp-afk/monik_v2/internal/jsonutil"
	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
)

// Policy is LOCAL and must never be included in a report. Browser/job data only
// contains profile IDs and anti-stale bindings, never any of these effect paths.
type Policy struct {
	SchemaVersion int       `json:"schema_version"`
	Enabled       bool      `json:"enabled"`
	AgentID       string    `json:"agent_id"`
	ControllerID  string    `json:"controller_id"`
	CollectorUID  uint32    `json:"collector_uid"`
	SocketGID     uint32    `json:"socket_gid"`
	StateDir      string    `json:"state_dir"`
	Profiles      []Profile `json:"profiles"`
}
type Profile struct {
	ID                  string                  `json:"profile_id"`
	DisplayName         string                  `json:"display_name"`
	Engine              string                  `json:"engine"`
	ModelRef            string                  `json:"model_ref"`
	EngineVersion       *string                 `json:"engine_version,omitempty"`
	Settings            []protocol.SparkSetting `json:"settings"`
	AllowedActions      []string                `json:"allowed_actions"`
	ObservationOnly     bool                    `json:"observation_only,omitempty"`
	Target              Target                  `json:"target"`
	HealthURL           string                  `json:"health_url,omitempty"`
	HealthScope         string                  `json:"health_scope,omitempty"`
	ExclusionGroups     []string                `json:"exclusion_groups,omitempty"`
	Weights             []string                `json:"weights,omitempty"`
	RequiredMemoryBytes uint64                  `json:"required_memory_bytes,omitempty"`
	OSHeadroomBytes     uint64                  `json:"os_headroom_bytes,omitempty"`
	BlockUnknownMemory  bool                    `json:"block_unknown_memory,omitempty"`
	Port                uint16                  `json:"port,omitempty"`
	ReadinessSeconds    int                     `json:"readiness_seconds,omitempty"`
	StopSeconds         int                     `json:"stop_seconds,omitempty"`
	LogEnabled          bool                    `json:"log_enabled,omitempty"`
	RedactionFiles      []string                `json:"redaction_files,omitempty"`
	Revision            string                  `json:"profile_revision"`
}
type Target struct {
	Kind           string   `json:"kind"`
	Unit           string   `json:"unit,omitempty"`
	UserUID        uint32   `json:"user_uid,omitempty"`
	DefinitionHash string   `json:"definition_hash"`
	TrustedFiles   []string `json:"trusted_files,omitempty"`
	AllowRootUnit  bool     `json:"allow_root_unit,omitempty"`
	SharedLock     string   `json:"shared_lock,omitempty"`
	// Configured catalog metadata is bound to one fixed, protected LOCAL file.
	// It is never a supervisor selector or an approval to execute effects.
	CatalogSHA256 string `json:"catalog_sha256,omitempty"`
	// Existing-container approval binds a FULL immutable ID, never a name.
	ContainerID  string `json:"container_id,omitempty"`
	DockerSocket string `json:"docker_socket,omitempty"`
	// Frozen recreation is locally approved, typed and digest-pinned. It cannot
	// be supplied by the remotely connected collector or browser.
	Frozen   *DockerDeclaration           `json:"frozen,omitempty"`
	Existing *ExistingCoordinatorApproval `json:"existing_coordinator,omitempty"`
}
type DockerDeclaration struct {
	Name           string                `json:"name"`
	Image          string                `json:"image"`
	User           string                `json:"user"`
	Command        []string              `json:"command"`
	Environment    []string              `json:"environment"`
	WorkingDir     string                `json:"working_dir,omitempty"`
	Mounts         []DockerMount         `json:"mounts"`
	NetworkMode    string                `json:"network_mode"`
	DeviceRequests []DockerDeviceRequest `json:"device_requests,omitempty"`
	IPCMode        string                `json:"ipc_mode,omitempty"`
	ShmSize        int64                 `json:"shm_size,omitempty"`
	ReadonlyRootfs bool                  `json:"readonly_rootfs"`
	AutoRemove     bool                  `json:"auto_remove"`
	StopTimeout    int                   `json:"stop_timeout"`
}
type DockerMount struct {
	Type     string `json:"type"`
	Source   string `json:"source"`
	Target   string `json:"target"`
	ReadOnly bool   `json:"read_only"`
}
type DockerDeviceRequest struct {
	Driver       string     `json:"driver"`
	Count        int        `json:"count"`
	DeviceIDs    []string   `json:"device_ids,omitempty"`
	Capabilities [][]string `json:"capabilities"`
}

func strictDecode(data []byte, target any) error {
	if err := jsonutil.Validate(data); err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		return fmt.Errorf("invalid typed local document")
	}
	return nil
}
func digest(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(h[:])
}
func ProfileRevision(p Profile) string { p.Revision = ""; return digest(p) }
func LoadPolicy(path string, owner uint32) (*Policy, error) {
	if err := trustedPath(path, owner, false); err != nil {
		return nil, err
	}
	b, err := readPrivateFile(path, 256<<10, owner)
	if err != nil {
		return nil, err
	}
	var p Policy
	if err = strictDecode(b, &p); err != nil {
		return nil, err
	}
	if err = p.Validate(); err != nil {
		return nil, err
	}
	return &p, nil
}

var publicSettings = map[string]bool{"context_length": true, "dtype": true, "quantization": true, "tensor_parallel": true, "pipeline_parallel": true, "memory_fraction": true, "memory_limit_bytes": true, "batch_size": true, "max_concurrency": true, "kv_cache": true, "port": true, "speculative": true, "draft_model": true, "reasoning_effort": true, "enable_thinking": true, "chunked_prefill_size": true}

func (p *Policy) Validate() error {
	if p.SchemaVersion != 1 || !protocol.SparkValidID(p.AgentID) || !protocol.SparkValidID(p.ControllerID) || p.CollectorUID == 0 || !filepath.IsAbs(p.StateDir) || len(p.Profiles) > 64 {
		return fmt.Errorf("invalid local policy identity or bounds")
	}
	ids := map[string]bool{}
	for _, q := range p.Profiles {
		if !protocol.SparkValidID(q.ID) || ids[q.ID] || len(q.DisplayName) > 256 || len(q.ModelRef) > 256 || q.Revision != ProfileRevision(q) || len(q.Settings) > 128 || len(q.ExclusionGroups) > 8 || len(q.Weights) > 16 || len(q.AllowedActions) > 3 {
			return fmt.Errorf("invalid local profile")
		}
		ids[q.ID] = true
		if q.Target.Kind == configuredCatalogKind {
			if err := q.validateConfiguredCatalog(); err != nil {
				return err
			}
		} else if q.Target.CatalogSHA256 != "" {
			return fmt.Errorf("catalog binding is only valid for configured catalog")
		} else if q.ObservationOnly {
			if err := q.validateObservationOnly(); err != nil {
				return err
			}
		}
		if !member(q.Engine, "llama.cpp", "vllm", "sglang", "tensorrt-llm", "ollama", "custom", "unknown") || !member(q.Target.Kind, "systemd-system", "systemd-user", "docker", "approved-launcher", configuredCatalogKind, existingCoordinatorKind) {
			return fmt.Errorf("unsupported local adapter")
		}
		seen := map[string]bool{}
		for _, a := range q.AllowedActions {
			if !member(a, "start", "stop", "restart") || seen[a] {
				return fmt.Errorf("invalid profile action")
			}
			seen[a] = true
		}
		for _, s := range q.Settings {
			if !publicSettings[s.Name] || !member(s.Provenance, "configured", "observed-effective", "unknown") {
				return fmt.Errorf("unclassified public setting")
			}
			if s.Provenance == "unknown" && s.Value != nil {
				return fmt.Errorf("unknown setting must be null")
			}
			if member(s.Name, "reasoning_effort", "enable_thinking", "chunked_prefill_size") && s.Provenance != "unknown" {
				// These explicitly exported native options have fixed scalar types.
				configured := s
				configured.Provenance = "configured"
				if err := validateCatalogSetting(configured); err != nil {
					return err
				}
			}
			if t, ok := s.Value.(string); ok && (len(t) > 256 || strings.ContainsAny(t, "\r\n")) {
				return fmt.Errorf("unsafe setting value")
			}
		}
		if q.HealthURL != "" {
			if err := ValidateLoopbackHealthURL(q.HealthURL); err != nil {
				return err
			}
			if q.HealthScope != "deployment" && q.HealthScope != "service" {
				return fmt.Errorf("health scope required")
			}
		}
		if q.ReadinessSeconds < 0 || q.ReadinessSeconds > 86400 || q.StopSeconds < 0 || q.StopSeconds > 3600 || len(q.RedactionFiles) > 32 {
			return fmt.Errorf("invalid observation/log bounds")
		}
		for _, g := range q.ExclusionGroups {
			if !protocol.SparkValidID(g) {
				return fmt.Errorf("invalid exclusion group")
			}
		}
		if q.Target.DefinitionHash == "" || !strings.HasPrefix(q.Target.DefinitionHash, "sha256:") {
			return fmt.Errorf("effective definition approval missing")
		}
		if q.Target.Kind == existingCoordinatorKind {
			if err := q.validateExistingCoordinator(); err != nil {
				return err
			}
			continue
		} else if q.Target.Existing != nil {
			return fmt.Errorf("existing coordinator approval cannot authorize another adapter")
		}
		if q.Target.Kind == configuredCatalogKind {
			// Source data has no ordinary supervisor/effect identity.
			continue
		} else if q.Target.Kind == "docker" {
			if q.Target.DockerSocket != "/var/run/docker.sock" && q.Target.DockerSocket != "/run/docker.sock" {
				return fmt.Errorf("unapproved docker socket")
			}
			if q.Target.Frozen == nil && (len(q.Target.ContainerID) != 64 || !isHex(q.Target.ContainerID)) {
				return fmt.Errorf("full container ID required")
			}
			if q.Target.Frozen != nil {
				if err := q.Target.Frozen.validate(); err != nil {
					return err
				}
			}
		} else {
			if strings.HasPrefix(q.Target.Unit, "monik-") {
				return fmt.Errorf("monitoring service cannot be a model supervisor")
			}
			if !validUnit(q.Target.Unit) {
				return fmt.Errorf("exact service unit required")
			}
			if q.Target.Kind == "systemd-user" && q.Target.UserUID == 0 {
				return fmt.Errorf("non-root user manager required")
			}
			if q.Target.Kind == "approved-launcher" && len(q.Target.TrustedFiles) == 0 {
				return fmt.Errorf("root-owned launcher binding required")
			}
		}
	}
	return nil
}
func (d *DockerDeclaration) validate() error {
	uid := strings.SplitN(d.User, ":", 2)[0]
	numericUID, uidError := strconv.ParseUint(uid, 10, 32)
	if uidError != nil || numericUID == 0 {
		return fmt.Errorf("frozen container must use explicit non-root numeric UID")
	}
	if !protocol.SparkValidID(d.Name) || !strings.Contains(d.Image, "@sha256:") || len(d.Image) > 256 || d.User == "" || d.User == "0" || d.User == "root" || len(d.Command) == 0 || len(d.Command) > 128 || len(d.Environment) > 64 || len(d.Mounts) > 32 || d.StopTimeout < 1 || d.StopTimeout > 3600 || !member(d.NetworkMode, "host", "bridge", "none") {
		return fmt.Errorf("invalid frozen Docker declaration")
	}
	for _, m := range d.Mounts {
		if m.Type != "bind" || !filepath.IsAbs(m.Source) || !filepath.IsAbs(m.Target) || filepath.Clean(m.Source) != m.Source || filepath.Clean(m.Target) != m.Target || strings.ContainsAny(m.Source+m.Target, "\r\n") {
			return fmt.Errorf("invalid frozen mount")
		}
		if e := frozenMountAnchor(m.Source); e != nil {
			return e
		}
		for _, forbidden := range []string{"/", "/proc", "/sys", "/dev", "/run", "/var/run", "/var/lib/docker", "/etc/monik-spark", "/var/lib/monik-spark"} {
			if m.Source == forbidden || forbidden != "/" && strings.HasPrefix(filepath.Clean(m.Source), forbidden+"/") {
				return fmt.Errorf("frozen model mount exposes host authority")
			}
		}
	}
	for _, e := range d.Environment {
		key, _, ok := strings.Cut(e, "=")
		if !ok || key == "LD_PRELOAD" || key == "BASH_ENV" || key == "ENV" || strings.ContainsAny(e, "\r\n") {
			return fmt.Errorf("unsafe frozen environment")
		}
	}
	return nil
}
func frozenMountAnchor(source string) error {
	info, e := os.Lstat(source)
	if e != nil || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("frozen mount source must exist without symlink")
	}
	if info.IsDir() {
		return trustedPath(source, 0, true)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("frozen mount source must be model data file/directory")
	}
	return trustedPath(filepath.Dir(source), 0, true)
}
func validUnit(s string) bool {
	return strings.HasSuffix(s, ".service") && len(s) <= 128 && !strings.ContainsAny(s, "/\\ \t\r\n") && !strings.HasPrefix(s, "-") && !strings.Contains(s, "@")
}
func isHex(s string) bool { _, e := hex.DecodeString(s); return e == nil }
func member(s string, opts ...string) bool {
	for _, o := range opts {
		if s == o {
			return true
		}
	}
	return false
}
func readBoundedFile(path string, max int64) ([]byte, error) {
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil || !st.Mode().IsRegular() || st.Size() > max {
		return nil, fmt.Errorf("invalid local file type/size")
	}
	b, e := io.ReadAll(io.LimitReader(f, max+1))
	if e != nil || int64(len(b)) > max {
		return nil, fmt.Errorf("local file read budget")
	}
	return b, nil
}

func (p Profile) Public(enabled bool) protocol.SparkProfile {
	settings := p.Settings
	if settings == nil {
		settings = []protocol.SparkSetting{}
	}
	acts := []string{}
	reason := "local_control_disabled"
	if enabled && !p.ObservationOnly && p.Target.Kind != configuredCatalogKind && len(p.AllowedActions) > 0 {
		acts = append(acts, p.AllowedActions...)
		reason = ""
	}
	if p.ObservationOnly {
		reason = "observation_only"
	}
	supervisor := p.Target.Kind
	if p.Target.Kind == configuredCatalogKind {
		supervisor = "unknown"
		reason = "configured_catalog_runtime_unobserved"
	}
	var endpoint *string
	if p.HealthURL != "" && p.Target.Kind != configuredCatalogKind {
		s := strings.TrimSuffix(strings.TrimSuffix(p.HealthURL, "/v1/health"), "/health")
		endpoint = &s
	}
	return protocol.SparkProfile{ProfileID: p.ID, DisplayName: p.DisplayName, ProfileRevision: p.Revision, Engine: p.Engine, Supervisor: supervisor, ControlAllowed: len(acts) > 0, ControlReason: reason, ConfiguredModelRef: p.ModelRef, Settings: settings, AllowedActions: acts, EngineVersion: p.EngineVersion, EndpointHint: endpoint, LogAllowed: enabled && !p.ObservationOnly && p.Target.Kind != configuredCatalogKind && p.LogEnabled}
}

func (p Profile) validateObservationOnly() error {
	if p.Target.Kind == configuredCatalogKind {
		return p.validateConfiguredCatalog()
	}
	if p.Target.Kind != "systemd-system" || !validUnit(p.Target.Unit) || strings.HasPrefix(p.Target.Unit, "monik-") || len(p.AllowedActions) != 0 || p.LogEnabled || p.Target.AllowRootUnit || len(p.Target.TrustedFiles) != 0 || p.Target.SharedLock != "" {
		return fmt.Errorf("observation-only profile must be exact system unit without actions, logs or effect approval")
	}
	return nil
}
