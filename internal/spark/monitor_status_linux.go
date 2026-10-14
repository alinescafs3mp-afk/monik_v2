//go:build linux

package spark

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// MonitorServiceState describes only the installed collector, never a model.
type MonitorServiceState struct {
	Unit   string `json:"unit"`
	State  string `json:"state"`
	Reason string `json:"reason,omitempty"`
	PID    int    `json:"pid,omitempty"`
}

// MonitorServiceStatus uses one bounded read-only query of the fixed monitor
// unit and verifies its effective entrypoint against the selected enrollment.
// A same-name unrelated unit is not reported as this installed worker.
func MonitorServiceStatus(ctx context.Context, configPath, stateDir string) (MonitorServiceState, error) {
	out := MonitorServiceState{Unit: "monik-agent.service", State: "unknown"}
	ctx, end := context.WithTimeout(ctx, 2*time.Second)
	defer end()
	b, err := fixedCommand(ctx, "/usr/bin/systemctl", []string{"show", "--no-pager", "--property=LoadState,ActiveState,SubState,ExecStart,MainPID", "--", out.Unit}, 0, 16<<10)
	if err != nil {
		out.Reason = "systemd_query_unavailable_or_permission_denied"
		return out, fmt.Errorf("monitor service observation unavailable")
	}
	return monitorServiceState(configPath, stateDir, b)
}

func monitorServiceState(configPath, stateDir string, data []byte) (MonitorServiceState, error) {
	out := MonitorServiceState{Unit: "monik-agent.service", State: "unknown"}
	unknown := func(reason string) (MonitorServiceState, error) {
		out.State = "unknown"
		out.PID = 0
		out.Reason = reason
		return out, fmt.Errorf("monitor service observation unverified: %s", reason)
	}
	if !filepath.IsAbs(configPath) || !filepath.IsAbs(stateDir) || filepath.Clean(configPath) != configPath || filepath.Clean(stateDir) != stateDir || filepath.Dir(configPath) != stateDir {
		return unknown("enrollment_path_mismatch")
	}
	m := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return unknown("systemd_response_unverifiable")
		}
		if _, exists := m[key]; exists {
			return unknown("systemd_response_ambiguous")
		}
		m[key] = value
	}
	if m["LoadState"] == "not-found" {
		out.State = "not_installed"
		return out, nil
	}
	if m["LoadState"] != "loaded" {
		return unknown("monitor_unit_not_loaded")
	}
	exec, err := canonicalExec(m["ExecStart"])
	if err != nil {
		return unknown("monitor_entrypoint_unverifiable")
	}
	entries := regexp.MustCompile(`\{ ([^}]*)\}`).FindAllStringSubmatch(exec, -1)
	if len(entries) != 1 {
		return unknown("monitor_entrypoint_ambiguous")
	}
	path := regexp.MustCompile(`(?:^|[ ;])path=([^ ;}]+)`).FindStringSubmatch(entries[0][1])
	argv := regexp.MustCompile(`argv\[\]=([^;}]*)`).FindStringSubmatch(entries[0][1])
	worker := filepath.Join(stateDir, "bin", "monik-agent")
	want := []string{worker, "run", "--config", configPath}
	if len(path) != 2 || len(argv) != 2 || path[1] != worker || digest(strings.Fields(argv[1])) != digest(want) {
		return unknown("monitor_install_identity_mismatch")
	}
	switch m["ActiveState"] {
	case "active":
		out.State = "running"
	case "activating":
		out.State = "starting"
	case "deactivating":
		out.State = "stopping"
	case "inactive":
		out.State = "stopped"
	case "failed":
		out.State = "failed"
	default:
		return unknown("monitor_state_unavailable")
	}
	if out.PID, err = strconv.Atoi(m["MainPID"]); err != nil || out.PID < 0 {
		return unknown("monitor_process_identity_unavailable")
	}
	return out, nil
}
