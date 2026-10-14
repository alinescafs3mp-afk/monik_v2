package spark

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"math"
	"os"
	"strings"
	"syscall"

	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
	"golang.org/x/sys/unix"
)

const configuredCatalogKind = "configured-catalog"
const configuredCatalogPath = "/etc/monik-spark/configured-catalog.json"
const configuredCatalogLimit = 256 << 10

// ConfiguredCatalog is a local, reviewed projection of configuration metadata.
// It contains no selectors, executable paths, credentials or runtime evidence.
// In particular, an absent container or an inactive service template does not
// establish that a configured deployment is stopped.
type ConfiguredCatalog struct {
	SchemaVersion int                      `json:"schema_version"`
	Profiles      []ConfiguredCatalogEntry `json:"profiles"`
}

type ConfiguredCatalogEntry struct {
	ID            string                  `json:"profile_id"`
	DisplayName   string                  `json:"display_name"`
	Engine        string                  `json:"engine"`
	ModelRef      string                  `json:"model_ref"`
	EngineVersion *string                 `json:"engine_version,omitempty"`
	Settings      []protocol.SparkSetting `json:"settings"`
}

func (p Profile) validateConfiguredCatalog() error {
	t := p.Target
	if t.Kind != configuredCatalogKind || !p.ObservationOnly || len(p.AllowedActions) != 0 || p.LogEnabled || p.HealthURL != "" || p.HealthScope != "" || len(p.Weights) != 0 || len(p.RedactionFiles) != 0 || len(p.ExclusionGroups) != 0 || p.RequiredMemoryBytes != 0 || p.OSHeadroomBytes != 0 || p.BlockUnknownMemory || p.Port != 0 || p.ReadinessSeconds != 0 || p.StopSeconds != 0 {
		return fmt.Errorf("configured catalog must remain metadata-only without runtime or effect authority")
	}
	if t.Unit != "" || t.UserUID != 0 || t.AllowRootUnit || len(t.TrustedFiles) != 0 || t.SharedLock != "" || t.ContainerID != "" || t.DockerSocket != "" || t.Frozen != nil || len(t.CatalogSHA256) != 64 || !isHex(t.CatalogSHA256) || strings.ToLower(t.CatalogSHA256) != t.CatalogSHA256 {
		return fmt.Errorf("configured catalog cannot select a supervisor or arbitrary source")
	}
	return validateCatalogEntry(catalogEntry(p))
}

func catalogEntry(p Profile) ConfiguredCatalogEntry {
	return ConfiguredCatalogEntry{ID: p.ID, DisplayName: p.DisplayName, Engine: p.Engine, ModelRef: p.ModelRef, EngineVersion: p.EngineVersion, Settings: p.Settings}
}

func validateCatalogEntry(e ConfiguredCatalogEntry) error {
	if !protocol.SparkValidID(e.ID) || e.DisplayName == "" || e.ModelRef == "" || len(e.DisplayName) > 256 || len(e.ModelRef) > 256 || strings.ContainsAny(e.DisplayName+e.ModelRef, "\r\n\x00") || !member(e.Engine, "llama.cpp", "vllm", "sglang", "tensorrt-llm", "ollama", "custom", "unknown") || e.Settings == nil || len(e.Settings) > 128 {
		return fmt.Errorf("invalid configured catalog metadata")
	}
	if e.EngineVersion != nil && (len(*e.EngineVersion) > 128 || strings.ContainsAny(*e.EngineVersion, "\r\n\x00")) {
		return fmt.Errorf("invalid configured catalog version")
	}
	seen := map[string]bool{}
	for _, s := range e.Settings {
		if seen[s.Name] {
			return fmt.Errorf("duplicate configured catalog setting")
		}
		seen[s.Name] = true
		if err := validateCatalogSetting(s); err != nil {
			return err
		}
	}
	return nil
}

func validateCatalogSetting(s protocol.SparkSetting) error {
	if !publicSettings[s.Name] || !member(s.Provenance, "configured", "unknown") {
		return fmt.Errorf("configured catalog requires allowlisted configuration settings")
	}
	if s.Provenance == "unknown" {
		if s.Value != nil {
			return fmt.Errorf("unknown configured catalog setting must be null")
		}
		return nil
	}
	var numeric *float64
	switch v := s.Value.(type) {
	case bool:
	case string:
		if len(v) > 256 || strings.ContainsAny(v, "\r\n\x00") {
			return fmt.Errorf("unsafe configured catalog setting")
		}
	case float64:
		numeric = &v
	case int:
		f := float64(v)
		numeric = &f
	case int64:
		f := float64(v)
		numeric = &f
	default:
		return fmt.Errorf("configured catalog setting must be a finite scalar")
	}
	if numeric != nil && (math.IsNaN(*numeric) || math.IsInf(*numeric, 0) || math.Abs(*numeric) > 9007199254740991) {
		return fmt.Errorf("configured catalog numeric setting out of bounds")
	}
	switch s.Name {
	case "enable_thinking":
		if _, ok := s.Value.(bool); !ok {
			return fmt.Errorf("thinking setting must be boolean")
		}
	case "reasoning_effort":
		value, ok := s.Value.(string)
		if !ok || !member(value, "none", "low", "medium", "high", "xhigh") {
			return fmt.Errorf("reasoning effort must be explicit native enum")
		}
	case "chunked_prefill_size":
		if numeric == nil || *numeric < 0 || math.Trunc(*numeric) != *numeric {
			return fmt.Errorf("chunked prefill size must be nonnegative integer")
		}
	}
	return nil
}

// The only production call uses the fixed path and root owner. Keeping the
// protected file reader separate allows actual filesystem negative controls
// without adding a configurable runtime source or writing /etc in tests.
func readConfiguredCatalogFile(path string, owner uint32) ([]byte, error) {
	if err := trustedPath(path, owner, false); err != nil {
		return nil, fmt.Errorf("configured catalog protected source unavailable")
	}
	before, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("configured catalog protected source unavailable")
	}
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, fmt.Errorf("configured catalog protected source unavailable")
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !catalogFileIdentity(before, opened) {
		return nil, fmt.Errorf("configured catalog source changed while opening")
	}
	native, ok := opened.Sys().(*syscall.Stat_t)
	if !ok || native.Uid != owner || native.Nlink != 1 || !opened.Mode().IsRegular() || opened.Mode().Perm() != 0600 || opened.Size() > configuredCatalogLimit {
		return nil, fmt.Errorf("configured catalog source owner/type/size mismatch")
	}
	b, err := io.ReadAll(io.LimitReader(f, configuredCatalogLimit+1))
	after, statErr := f.Stat()
	pathAfter, pathErr := os.Lstat(path)
	if err != nil || statErr != nil || pathErr != nil || len(b) > configuredCatalogLimit || !catalogFileIdentity(opened, after) || !catalogFileIdentity(opened, pathAfter) || trustedPath(path, owner, false) != nil {
		return nil, fmt.Errorf("configured catalog source changed during bounded read")
	}
	return b, nil
}

func catalogFileIdentity(a, b os.FileInfo) bool {
	x, xok := a.Sys().(*syscall.Stat_t)
	y, yok := b.Sys().(*syscall.Stat_t)
	return xok && yok && x.Dev == y.Dev && x.Ino == y.Ino && x.Size == y.Size && x.Mtim == y.Mtim && x.Ctim == y.Ctim && x.Uid == y.Uid && x.Gid == y.Gid && x.Mode == y.Mode && x.Nlink == y.Nlink
}

func configuredCatalogDefinition(p Profile) (string, error) {
	if err := p.validateConfiguredCatalog(); err != nil {
		return "", err
	}
	b, err := readConfiguredCatalogFile(configuredCatalogPath, 0)
	if err != nil {
		return "", err
	}
	return configuredCatalogDefinitionFromBytes(p, b)
}

func configuredCatalogDefinitionFromBytes(p Profile, b []byte) (string, error) {
	if err := p.validateConfiguredCatalog(); err != nil {
		return "", err
	}
	hash := sha256.Sum256(b)
	if hex.EncodeToString(hash[:]) != p.Target.CatalogSHA256 {
		return "", fmt.Errorf("configured catalog source hash drift")
	}
	var c ConfiguredCatalog
	if len(b) > configuredCatalogLimit || strictDecode(b, &c) != nil || c.SchemaVersion != 1 || len(c.Profiles) == 0 || len(c.Profiles) > 64 {
		return "", fmt.Errorf("invalid configured catalog source")
	}
	seen := map[string]bool{}
	matched := false
	for _, e := range c.Profiles {
		if seen[e.ID] || validateCatalogEntry(e) != nil {
			return "", fmt.Errorf("invalid configured catalog source entry")
		}
		seen[e.ID] = true
		if e.ID == p.ID {
			if digest(e) != digest(catalogEntry(p)) {
				return "", fmt.Errorf("configured catalog profile differs from protected source")
			}
			matched = true
		}
	}
	if !matched {
		return "", fmt.Errorf("configured catalog profile missing from protected source")
	}
	return digest(struct {
		Mode       string
		SourceHash string
		Entry      ConfiguredCatalogEntry
	}{configuredCatalogKind, p.Target.CatalogSHA256, catalogEntry(p)}), nil
}

func configuredCatalogInspect(p Profile) (Instance, error) {
	hash, err := configuredCatalogDefinition(p)
	if err != nil {
		return Instance{}, err
	}
	if hash != p.Target.DefinitionHash {
		return Instance{}, fmt.Errorf("configured catalog approval drift")
	}
	// Metadata can be present and valid while runtime is entirely unobserved.
	// It does not supply a generation, invocation, PID, health, or stopped fact.
	return Instance{State: "unknown", DefinitionHash: hash, Available: false}, nil
}
