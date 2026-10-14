//go:build linux

// This command is an explicitly isolated native canary, not a product installer.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/agent/configfile"
	"github.com/alinescafs3mp-afk/monik_v2/internal/install"
	"github.com/alinescafs3mp-afk/monik_v2/internal/servicehost"
)

const (
	base   = "/opt/monik-spark-native-canary-20261014"
	prefix = base + "/prefix"
	state  = "/var/lib/monik-spark-native-canary-20261014"
	config = state + "/agent.json"
	unit   = "/etc/systemd/system/monik-agent.service"
	user   = "monik-canary"
	marker = base + "/CANARY_ONLY"
	full   = base + "/bin/monik-agent"
	host   = base + "/bin/monik-service-host"
	light  = base + "/bin/monik-spark-agent"
)

type result struct {
	Phase          string   `json:"phase"`
	Success        bool     `json:"success"`
	Error          string   `json:"error,omitempty"`
	StartedAt      string   `json:"started_at"`
	DurationMS     int64    `json:"duration_ms"`
	RecoveryBefore string   `json:"recovery_before_sha256,omitempty"`
	RecoveryAfter  string   `json:"recovery_after_sha256,omitempty"`
	CleanupPlan    []string `json:"cleanup_plan,omitempty"`
	EvidenceLimit  string   `json:"evidence_limit"`
}

func main() {
	started := time.Now()
	r := result{StartedAt: started.UTC().Format(time.RFC3339), EvidenceLimit: "API phase result only; identity, native process, boot and model acceptance require separate evidence"}
	var err error
	if len(os.Args) != 2 {
		err = fmt.Errorf("use one phase: install-full, convert-light, replace-light, rollback, cleanup-plan")
	} else {
		r.Phase = os.Args[1]
		err = run(&r)
	}
	r.Success = err == nil
	if err != nil {
		r.Error = err.Error()
	}
	r.DurationMS = time.Since(started).Milliseconds()
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if enc.Encode(r) != nil || err != nil {
		os.Exit(1)
	}
}

func run(r *result) error {
	switch r.Phase {
	case "install-full", "convert-light", "replace-light", "rollback", "cleanup-plan":
	default:
		return fmt.Errorf("unknown phase")
	}
	if runtime.GOARCH != "arm64" || os.Geteuid() != 0 {
		return fmt.Errorf("native canary requires Linux ARM64 and root")
	}
	if err := rootProtected(marker); err != nil {
		return fmt.Errorf("CANARY_ONLY marker is not protected: %w", err)
	}
	f, err := canaryConfig()
	if err != nil {
		return err
	}
	opts := install.Options{Prefix: prefix, StateDir: state, ConfigPath: config, UnitPath: unit, User: user, HostSrc: host, WorkerSrc: full, SkipSystemctl: false, ConvertFlavor: false}
	fullUnit := servicehost.PlanManagedUnit(prefix+"/monik-service-host", state+"/bin/monik-agent", config, state, user)
	lightUnit := install.PlanSparkUnit(state+"/bin/monik-agent", config, state, user)
	if r.Phase == "install-full" {
		if f.Managed {
			return fmt.Errorf("first installation requires unmanaged isolated enrollment")
		}
		for _, p := range []string{unit, prefix + "/monik-agent", prefix + "/monik-service-host", state + "/bin/monik-agent", prefix + "/recovery.json"} {
			if _, err := os.Lstat(p); !os.IsNotExist(err) {
				return fmt.Errorf("first-install path is occupied or inaccessible: %s", p)
			}
		}
	} else {
		if !f.Managed {
			return fmt.Errorf("phase requires this managed canary")
		}
		expected := lightUnit
		if r.Phase == "convert-light" {
			if (f.AgentFlavor != "" && f.AgentFlavor != "general") || f.RuntimeMode != "" {
				return fmt.Errorf("conversion requires the full canary")
			}
			expected = fullUnit
		} else if r.Phase != "cleanup-plan" && r.Phase != "rollback" && (f.AgentFlavor != "spark-light" || f.RuntimeMode != "direct") {
			return fmt.Errorf("phase requires the direct light canary")
		} else if r.Phase == "cleanup-plan" && (f.AgentFlavor == "" || f.AgentFlavor == "general") && f.RuntimeMode == "" {
			expected = fullUnit
		}
		if err := rootProtected(unit); err != nil {
			return err
		}
		b, err := os.ReadFile(unit)
		if err != nil || string(b) != expected && (r.Phase != "rollback" || string(b) != fullUnit) {
			return fmt.Errorf("unit is not the exact isolated canary unit; nothing changed")
		}
	}
	if err := checkResolvedUnit(r.Phase == "install-full"); err != nil {
		return err
	}
	if r.Phase == "cleanup-plan" {
		r.CleanupPlan = []string{"/usr/bin/systemctl disable --now monik-agent.service", "remove only /etc/systemd/system/monik-agent.service after rechecking the exact canary unit", "/usr/bin/systemctl daemon-reload"}
		return nil
	}
	if r.Phase == "install-full" {
		for _, p := range []string{host, full} {
			if err := rootProtected(p); err != nil {
				return err
			}
		}
		return install.InstallLinux(opts)
	}
	if r.Phase == "convert-light" || r.Phase == "replace-light" {
		if err := rootProtected(light); err != nil {
			return err
		}
		opts.HostSrc, opts.WorkerSrc = "", light
		opts.ConvertFlavor = r.Phase == "convert-light"
	}
	if r.Phase == "replace-light" || r.Phase == "rollback" {
		if r.RecoveryBefore, err = fullRecovery(f, r.Phase != "rollback"); err != nil {
			return err
		}
	}
	if r.Phase == "replace-light" {
		// This phase deliberately reuses the exact source, preserving original-full
		// recovery. A distinct upgrade is a separate experiment with a latest-light point.
		candidate, err := os.ReadFile(light)
		if err != nil {
			return err
		}
		for _, p := range []string{prefix + "/monik-agent", state + "/bin/monik-agent"} {
			b, err := os.ReadFile(p)
			if err != nil || digest(b) != digest(candidate) {
				return fmt.Errorf("same-source replacement requires unchanged installed light bytes")
			}
		}
	}
	if r.Phase == "rollback" {
		err = install.RollbackLinux(opts)
	} else {
		err = install.InstallSparkLinux(opts)
	}
	if err != nil {
		return err
	}
	r.RecoveryAfter, err = fullRecovery(f, true)
	if err == nil && r.Phase == "replace-light" && r.RecoveryAfter != r.RecoveryBefore {
		return fmt.Errorf("replacement returned success but original-full recovery changed")
	}
	return err
}

func canaryConfig() (configfile.File, error) {
	var empty configfile.File
	i, err := os.Lstat(config)
	if err != nil || !i.Mode().IsRegular() {
		return empty, fmt.Errorf("canary config must be a regular file")
	}
	s, err := configfile.Load(config)
	if err != nil {
		return empty, fmt.Errorf("cannot load canary config") // Never print config bytes.
	}
	f := s.File
	contained := func(p string) bool {
		return filepath.IsAbs(p) && filepath.Clean(p) == p && filepath.Dir(p) == state
	}
	if f.StateDir != state || !contained(f.CredentialPath) || (f.UpdateRootPath != "" && !contained(f.UpdateRootPath)) || f.AgentID == "" || f.ControllerID == "" || f.ControllerURL == "" || f.CACertPEM == "" || f.PendingRegistration {
		return empty, fmt.Errorf("config is not canonical enrolled canary state")
	}
	return f, nil
}

func fullRecovery(f configfile.File, requireSettled bool) (string, error) {
	p := prefix + "/recovery.json"
	if err := rootProtected(p); err != nil {
		return "", err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return "", err
	}
	var point struct {
		AgentID      string `json:"agent_id"`
		ControllerID string `json:"controller_id"`
		StateDir     string `json:"state_dir"`
		Flavor       string `json:"flavor"`
		Managed      bool   `json:"managed"`
		Settled      bool   `json:"settled"`
	}
	if json.Unmarshal(b, &point) != nil || point.AgentID != f.AgentID || point.ControllerID != f.ControllerID || point.StateDir != state || point.Flavor != "general" || !point.Managed || requireSettled && !point.Settled {
		return "", fmt.Errorf("original-full recovery is absent, unfinished or not this canary")
	}
	return digest(b), nil
}

func rootProtected(path string) error {
	for p := path; ; p = filepath.Dir(p) {
		i, err := os.Lstat(p)
		if err != nil {
			return fmt.Errorf("protected path unavailable: %s", p)
		}
		st, ok := i.Sys().(*syscall.Stat_t)
		if !ok || st.Uid != 0 || i.Mode().Perm()&0022 != 0 || (p == path && (!i.Mode().IsRegular() || st.Nlink != 1)) || (p != path && !i.IsDir()) {
			return fmt.Errorf("path must be root-owned, unlinked and not writable by others: %s", p)
		}
		if p == "/" {
			return nil
		}
	}
}

func digest(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// Checking only the /etc file would miss vendor/runtime/transient services and
// drop-ins: the public API deliberately controls the resolved supported unit.
func checkResolvedUnit(absent bool) error {
	const ctl = "/usr/bin/systemctl"
	if err := rootProtected(ctl); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, ctl, "show", "monik-agent.service", "--no-pager", "--property=LoadState", "--property=ActiveState", "--property=FragmentPath", "--property=DropInPaths", "--property=Transient")
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C"}
	b, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("cannot verify resolved native service")
	}
	values := map[string]string{}
	for line := range strings.SplitSeq(string(b), "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			values[k] = v
		}
	}
	if values["DropInPaths"] != "" || values["Transient"] != "no" {
		return fmt.Errorf("resolved service has drop-ins, is transient or cannot be verified")
	}
	if absent {
		if values["LoadState"] != "not-found" || values["ActiveState"] != "inactive" || values["FragmentPath"] != "" {
			return fmt.Errorf("monik-agent.service already exists in systemd; nothing changed")
		}
	} else if values["LoadState"] != "loaded" || values["FragmentPath"] != unit {
		return fmt.Errorf("resolved service is not the exact canary unit; nothing changed")
	}
	// Missing fragments do not load their orphaned drop-ins. Inspect the actual
	// manager search path before the installer publishes a fragment and reloads it.
	cmd = exec.CommandContext(ctx, ctl, "show", "--no-pager", "--property=UnitPath", "--value")
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C"}
	b, err = cmd.Output()
	if err != nil || len(strings.Fields(string(b))) == 0 {
		return fmt.Errorf("cannot verify native unit search paths")
	}
	for _, dir := range strings.Fields(string(b)) {
		if !filepath.IsAbs(dir) || filepath.Clean(dir) != dir || strings.ContainsAny(dir, "\\\n\r") {
			return fmt.Errorf("cannot inspect canonical native unit search paths")
		}
		for _, name := range []string{"monik-agent.service.d", "monik-.service.d", "service.d"} {
			p := filepath.Join(dir, name)
			i, err := os.Lstat(p)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil || !i.IsDir() {
				return fmt.Errorf("cannot exclude prospective native service drop-ins")
			}
			entries, err := os.ReadDir(p)
			if err != nil {
				return fmt.Errorf("cannot inspect prospective native service drop-ins")
			}
			for _, entry := range entries {
				if strings.HasSuffix(entry.Name(), ".conf") {
					return fmt.Errorf("prospective native service drop-in exists; nothing changed")
				}
			}
		}
	}
	return nil
}
