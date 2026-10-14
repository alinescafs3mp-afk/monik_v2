package spark

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
	"github.com/alinescafs3mp-afk/monik_v2/internal/secure"
)

// ApprovePolicy is an explicit LOCAL administration step. Discovery and normal
// updates never call it. The exact effective supervisor definition is frozen
// after protected-file verification; the browser cannot turn a draft into it.
func ApprovePolicy(ctx context.Context, draftPath, outputPath string, enable bool) error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("approval requires local root administration")
	}
	if e := trustedPath(filepath.Dir(outputPath), 0, true); e != nil {
		return e
	}
	b, e := readBoundedFile(draftPath, 256<<10)
	if e != nil {
		return e
	}
	var p Policy
	if e = strictDecode(b, &p); e != nil {
		return e
	}
	p.Enabled = enable
	for i := range p.Profiles {
		profile := &p.Profiles[i]
		if profile.Settings == nil {
			profile.Settings = []protocol.SparkSetting{}
		}
		if profile.AllowedActions == nil {
			profile.AllowedActions = []string{}
		}
		if profile.HealthURL != "" {
			if e = ValidateLoopbackHealthURL(profile.HealthURL); e != nil {
				return e
			}
		}
		probe, end := context.WithTimeout(ctx, 3*time.Second)
		hash, err := (LocalSupervisor{}).Definition(probe, *profile)
		end()
		if err != nil {
			return fmt.Errorf("profile %s binding cannot be approved: %w", profile.ID, err)
		}
		profile.Target.DefinitionHash = hash
		profile.Revision = ProfileRevision(*profile)
	}
	if e = p.Validate(); e != nil {
		return e
	}
	if e = trustedPath(p.StateDir, 0, true); e != nil {
		return e
	}
	b, e = json.MarshalIndent(p, "", "  ")
	if e != nil {
		return e
	}
	return secure.AtomicWrite(outputPath, b, 0600)
}

type DiscoveryCandidate struct {
	PID            int    `json:"pid"`
	Process        string `json:"process"`
	Engine         string `json:"engine"`
	ControlAllowed bool   `json:"control_allowed"`
	Reason         string `json:"reason"`
}

func DiscoverProcesses() []DiscoveryCandidate {
	out := []DiscoveryCandidate{}
	entries, _ := os.ReadDir("/proc")
	visited := 0
	for _, entry := range entries {
		pid, e := strconv.Atoi(entry.Name())
		if e != nil || !entry.IsDir() {
			continue
		}
		visited++
		if visited > 4096 || len(out) >= 64 {
			break
		}
		b, e := readBoundedFile(filepath.Join("/proc", entry.Name(), "comm"), 4096)
		if e != nil {
			continue
		}
		name := strings.TrimSpace(string(b))
		engine := ""
		lower := strings.ToLower(name)
		switch {
		case strings.Contains(lower, "llama-server"):
			engine = "llama.cpp"
		case strings.Contains(lower, "vllm"):
			engine = "vllm"
		case strings.Contains(lower, "sglang"):
			engine = "sglang"
		case strings.Contains(lower, "ollama"):
			engine = "ollama"
		case strings.Contains(lower, "trtllm"):
			engine = "tensorrt-llm"
		}
		if engine != "" {
			out = append(out, DiscoveryCandidate{PID: pid, Process: name, Engine: engine, Reason: "candidate_only_exact_supervisor_adoption_required"})
		}
	}
	return out
}
func LocalAction(ctx context.Context, b BrokerClient, profileID, action, operation string, confirm bool) (protocol.JobReceipt, error) {
	return b.LocalAction(ctx, operation, profileID, action, confirm)
}
