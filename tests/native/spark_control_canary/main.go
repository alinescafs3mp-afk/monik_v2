//go:build linux

// Explicit isolated synthetic control canary; no enrollment or model launch.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
	"github.com/alinescafs3mp-afk/monik_v2/internal/spark"
)

const (
	base         = "/opt/monik-spark-native-control-canary-20261014"
	policyPath   = "/etc/monik-spark-native-control-canary-20261014/policy.json"
	state        = "/var/lib/monik-spark-native-control-canary-20261014"
	config       = "/var/lib/monik-spark-native-canary-20261014/agent.json"
	agentID      = "c7966ecd-e521-4e18-9142-dff2bc9d218e"
	socket       = "/run/monik-spark/control.sock"
	customSecret = "MONIK_SYNTHETIC_LITERAL_REDACTION_20261014"
)

var profileIDs = []string{"synthetic-ready", "synthetic-starting", "synthetic-preflight"}

func main() {
	started := time.Now()
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "use draft | verify | preflight-cancel | stale-bindings | cleanup-plan | emit-synthetic-log | delay-synthetic")
		os.Exit(2)
	}
	phase := os.Args[1]
	// These two bounded non-root commands are exact ExecStartPre entries only.
	if phase == "emit-synthetic-log" || phase == "delay-synthetic" {
		if os.Geteuid() == 0 {
			fmt.Fprintln(os.Stderr, "synthetic helper must run non-root")
			os.Exit(2)
		}
		fmt.Println("MONIK_SYNTHETIC_LOG_CANARY synthetic=true model=none")
		fmt.Println("password=MONIK_SYNTHETIC_PASSWORD_CANARY")
		fmt.Println("Authorization: Bearer MONIK_SYNTHETIC_BEARER_CANARY")
		fmt.Println("literal=" + customSecret)
		fmt.Println("hf_SYNTHETICCANARYTOKEN123456 sk-SYNTHETICCANARYTOKEN123456")
		fmt.Println("\x1b[31mMONIK_SYNTHETIC_ANSI_CANARY\x1b[0m")
		if phase == "delay-synthetic" {
			time.Sleep(20 * time.Second)
		}
		return
	}
	out := map[string]any{"phase": phase, "started_at": started.UTC().Format(time.RFC3339Nano), "evidence_limit": "synthetic canary only; no Qwen/model/GPU/root-launcher acceptance"}
	err := run(phase, out)
	if phase == "draft" && err == nil {
		return
	}
	out["success"] = err == nil
	out["duration_ms"] = time.Since(started).Milliseconds()
	if err != nil {
		out["error"] = err.Error()
	}
	_ = json.NewEncoder(os.Stdout).Encode(out)
	if err != nil {
		os.Exit(1)
	}
}

func protected(path string, directory bool) error {
	st, e := os.Lstat(path)
	if e != nil {
		return fmt.Errorf("protected canary path unavailable: %s", path)
	}
	sys, ok := st.Sys().(*syscall.Stat_t)
	if !ok || sys.Uid != 0 || st.Mode()&os.ModeSymlink != 0 || st.Mode().Perm()&0022 != 0 || directory && !st.IsDir() || !directory && (!st.Mode().IsRegular() || sys.Nlink != 1) {
		return fmt.Errorf("canary path owner/type/mode invalid: %s", path)
	}
	for parent := filepath.Dir(path); parent != "/"; parent = filepath.Dir(parent) {
		st, e := os.Lstat(parent)
		if e != nil {
			return fmt.Errorf("protected ancestor unavailable")
		}
		sys, ok := st.Sys().(*syscall.Stat_t)
		if !ok || sys.Uid != 0 || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 || st.Mode().Perm()&0022 != 0 {
			return fmt.Errorf("protected ancestor invalid")
		}
	}
	return nil
}

func nativeIdentity() (string, uint32, uint32, error) {
	if runtime.GOARCH != "arm64" || os.Geteuid() != 0 {
		return "", 0, 0, fmt.Errorf("native canary requires Linux ARM64 and root")
	}
	for _, path := range []string{base + "/CANARY_ONLY", base + "/spark-control-canary", base + "/fake-engine", base + "/monik-spark-agent"} {
		if e := protected(path, false); e != nil {
			return "", 0, 0, e
		}
	}
	u, e := user.Lookup("monik-canary")
	if e != nil {
		return "", 0, 0, fmt.Errorf("monik-canary account unavailable")
	}
	uid, e := strconv.ParseUint(u.Uid, 10, 32)
	if e != nil || uid == 0 {
		return "", 0, 0, fmt.Errorf("nonroot collector account required")
	}
	gid, e := strconv.ParseUint(u.Gid, 10, 32)
	if e != nil || gid == 0 {
		return "", 0, 0, fmt.Errorf("nonroot collector group required")
	}
	// Installer intentionally gives this private enrollment to the collector.
	for _, path := range []string{config, filepath.Dir(config)} {
		st, e := os.Lstat(path)
		if e != nil {
			return "", 0, 0, fmt.Errorf("private enrollment unavailable")
		}
		sys, ok := st.Sys().(*syscall.Stat_t)
		if !ok || sys.Uid != uint32(uid) || st.Mode()&os.ModeSymlink != 0 || st.Mode().Perm()&0077 != 0 || path == config && (!st.Mode().IsRegular() || sys.Nlink != 1) || path != config && !st.IsDir() {
			return "", 0, 0, fmt.Errorf("private enrollment owner/type/mode invalid")
		}
	}
	if e := protected(filepath.Dir(filepath.Dir(config)), true); e != nil {
		return "", 0, 0, e
	}
	fd, e := syscall.Open(config, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if e != nil {
		return "", 0, 0, fmt.Errorf("private enrollment unavailable")
	}
	f := os.NewFile(uintptr(fd), config)
	defer f.Close()
	st, e := f.Stat()
	if e != nil || st.Size() > 256<<10 {
		return "", 0, 0, fmt.Errorf("bounded isolated enrollment unavailable")
	}
	b, e := io.ReadAll(io.LimitReader(f, 256<<10+1))
	if e != nil || len(b) > 256<<10 {
		return "", 0, 0, fmt.Errorf("private enrollment read failed")
	}
	var c struct {
		AgentID      string `json:"agent_id"`
		ControllerID string `json:"controller_id"`
		StateDir     string `json:"state_dir"`
		AgentFlavor  string `json:"agent_flavor"`
	}
	if json.Unmarshal(b, &c) != nil || c.AgentID != agentID || c.StateDir != filepath.Dir(config) || !protocol.SparkValidID(c.ControllerID) || c.AgentFlavor != "spark-light" {
		return "", 0, 0, fmt.Errorf("exact isolated light identity required")
	}
	return c.ControllerID, uint32(uid), uint32(gid), nil
}

func draft(controller string, uid, gid uint32) spark.Policy {
	p := spark.Policy{SchemaVersion: 1, Enabled: false, AgentID: agentID, ControllerID: controller, CollectorUID: uid, SocketGID: gid, StateDir: state, Profiles: []spark.Profile{}}
	for n, id := range profileIDs {
		name := strings.TrimPrefix(id, "synthetic-")
		q := spark.Profile{ID: id, DisplayName: "SYNTHETIC CPU canary (no model): " + name, Engine: "custom", ModelRef: "synthetic-test-only-not-Qwen-no-model", Settings: []protocol.SparkSetting{}, AllowedActions: []string{"start", "stop", "restart"}, Target: spark.Target{Kind: "systemd-system", Unit: "spark-native-control-" + name + "-canary.service", TrustedFiles: []string{base + "/fake-engine", base + "/spark-control-canary"}}, HealthURL: fmt.Sprintf("http://127.0.0.1:%d/health", 18971+n), HealthScope: "deployment", Port: uint16(18971 + n), ReadinessSeconds: 30, StopSeconds: 5, LogEnabled: true, RedactionFiles: []string{base + "/redaction.txt"}}
		if name == "preflight" {
			q.Weights = []string{base + "/synthetic-metadata"}
		}
		p.Profiles = append(p.Profiles, q)
	}
	return p
}

func approved() (*spark.Policy, error) {
	controller, uid, gid, e := nativeIdentity()
	if e != nil {
		return nil, e
	}
	p, e := spark.LoadPolicy(policyPath, 0)
	if e != nil {
		return nil, e
	}
	want := draft(controller, uid, gid)
	if !p.Enabled || p.AgentID != agentID || p.ControllerID != controller || p.CollectorUID != uid || p.SocketGID != gid || p.StateDir != state || len(p.Profiles) != len(want.Profiles) {
		return nil, fmt.Errorf("exact isolated approved policy required")
	}
	for i, q := range p.Profiles {
		w := want.Profiles[i]
		w.Target.DefinitionHash = q.Target.DefinitionHash
		w.Revision = q.Revision
		b, _ := json.Marshal(q)
		wb, _ := json.Marshal(w)
		if string(b) != string(wb) {
			return nil, fmt.Errorf("canary profile scope changed")
		}
	}
	return p, nil
}

func snapshot(ctx context.Context, b spark.BrokerClient) (spark.BrokerSnapshot, error) {
	s, e := b.Snapshot(ctx)
	if e != nil {
		return s, e
	}
	if !s.Control.Enabled || s.Control.Epoch == nil || !protocol.SparkValidID(s.AgentSession) {
		return s, fmt.Errorf("collector has not bound enabled canary broker")
	}
	return s, nil
}

func params(s spark.BrokerSnapshot, id string) (protocol.ModelActionParams, error) {
	p := protocol.ModelActionParams{ProfileID: id, ControlEpoch: *s.Control.Epoch, ExpectedAgentSession: s.AgentSession, ConfirmDisruption: true}
	for _, q := range s.Inventory.Profiles {
		if q.ProfileID == id {
			p.ProfileRevision = q.ProfileRevision
		}
	}
	found := false
	for _, q := range s.States {
		if q.ProfileID == id && q.Quality == "ok" {
			p.ExpectedGeneration = q.Generation
			found = true
		}
	}
	if p.ProfileRevision == "" || !found {
		return p, fmt.Errorf("fresh canary profile/state missing")
	}
	return p, nil
}

func request(s spark.BrokerSnapshot, id, action string) (spark.ActionRequest, error) {
	p, e := params(s, id)
	now := time.Now().UTC()
	ident := fmt.Sprintf("native-canary-%d", now.UnixNano())
	return spark.ActionRequest{JobID: ident, OperationID: ident, Action: "model." + action, Params: p, IssuedAt: now, Deadline: now.Add(30 * time.Second)}, e
}

func run(phase string, out map[string]any) (err error) {
	if phase == "draft" {
		controller, uid, gid, e := nativeIdentity()
		if e != nil {
			return e
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(draft(controller, uid, gid))
	}
	if phase != "verify" && phase != "stale-bindings" && phase != "preflight-cancel" && phase != "cleanup-plan" {
		return fmt.Errorf("unknown phase")
	}
	p, e := approved()
	if e != nil {
		return e
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	b := spark.BrokerClient{Socket: socket}
	s, e := snapshot(ctx, b)
	if e != nil {
		return e
	}
	out["snapshot_before"] = s
	if phase == "verify" {
		out["agent_id"] = p.AgentID
		return nil
	}
	if phase == "cleanup-plan" {
		out["executed"] = false
		out["plan"] = []string{"Stop only spark-native-control-{ready,starting,preflight}-canary.service, then spark-native-control-broker-canary.service", "Verify each fragment is the exact staged canary file and has no drop-ins before deletion", "Remove only those four exact staged canary fragments; daemon-reload; retain policy, journal, marker, enrolled identity, account and source binaries", "Never stop/delete monik-agent.service or any real model/container/launcher as part of control cleanup"}
		return nil
	}
	if phase == "stale-bindings" {
		r, e := request(s, "synthetic-ready", "start")
		if e != nil {
			return e
		}
		checks := []map[string]any{}
		expectedCodes := map[string]string{"generation": "generation_drift", "revision": "authority_or_profile_drift", "epoch": "authority_or_profile_drift", "session": "authority_or_profile_drift", "lease": "dispatch_expired"}
		for _, field := range []string{"generation", "revision", "epoch", "session", "lease"} {
			n := r
			n.JobID += "-" + field
			n.OperationID = n.JobID
			switch field {
			case "generation":
				n.Params.ExpectedGeneration++
			case "revision":
				n.Params.ProfileRevision = "sha256:" + strings.Repeat("0", 64)
			case "epoch":
				n.Params.ControlEpoch = "stale-canary-epoch"
			case "session":
				n.Params.ExpectedAgentSession = "stale-canary-session"
			case "lease":
				n.IssuedAt = time.Now().Add(-time.Minute)
				n.Deadline = time.Now().Add(-time.Second)
			}
			rec, e := b.Execute(ctx, n)
			if e != nil {
				return e
			}
			checks = append(checks, map[string]any{"field": field, "receipt": rec})
			if rec.Status != protocol.TargetRejected || rec.ErrorCode != expectedCodes[field] {
				out["checks"] = checks
				return fmt.Errorf("stale binding was not rejected")
			}
		}
		out["checks"] = checks
		after, e := snapshot(ctx, b)
		if e != nil {
			return e
		}
		out["snapshot_after"] = after
		beforeParams, _ := params(s, "synthetic-ready")
		afterParams, _ := params(after, "synthetic-ready")
		if beforeParams.ExpectedGeneration != afterParams.ExpectedGeneration {
			return fmt.Errorf("generation changed across denied attempts")
		}
		return nil
	}
	// One observed-window attempt. Never retry a missed preflight window.
	pre := "synthetic-preflight"
	defer func() {
		// Independent cleanup budget survives the original attempt's timeout.
		cleanCtx, end := context.WithTimeout(context.Background(), 15*time.Second)
		defer end()
		rec, e := b.LocalAction(cleanCtx, fmt.Sprintf("native-canary-clean-after-%d", time.Now().UnixNano()), pre, "stop", true)
		out["cleanup_stop_receipt"] = rec
		if e != nil || rec.Status != protocol.TargetSucceeded {
			out["cleanup_confirmed"] = false
			if err == nil {
				err = fmt.Errorf("owned cleanup stop unconfirmed")
			}
		} else {
			out["cleanup_confirmed"] = true
		}
	}()
	stop, e := b.LocalAction(ctx, fmt.Sprintf("native-canary-clean-before-%d", time.Now().UnixNano()), pre, "stop", true)
	if e != nil {
		return e
	}
	if stop.Status != protocol.TargetSucceeded {
		return fmt.Errorf("initial canary stop not confirmed")
	}
	s, e = snapshot(ctx, b)
	if e != nil {
		return e
	}
	r, e := request(s, pre, "start")
	if e != nil {
		return e
	}
	type answer struct {
		Receipt protocol.JobReceipt
		Err     error
	}
	done := make(chan answer, 1)
	go func() { rec, err := b.Execute(ctx, r); done <- answer{rec, err} }()
	observed := false
	cut := time.Now().Add(3 * time.Second)
	for time.Now().Before(cut) {
		data, e := os.ReadFile(state + "/control-journal.json")
		if e == nil && len(data) <= 4<<20 {
			var journal struct {
				Attempts map[string]struct {
					Stage string `json:"stage"`
				} `json:"attempts"`
			}
			if json.Unmarshal(data, &journal) == nil && journal.Attempts[r.JobID].Stage == "preflight_pending" {
				observed = true
				break
			}
		}
		select {
		case a := <-done:
			out["start_receipt"] = a.Receipt
			if a.Err != nil {
				return a.Err
			}
			done <- a
			cut = time.Now()
		default:
			time.Sleep(500 * time.Microsecond)
		}
	}
	out["preflight_pending_observed"] = observed
	var cancelled bool
	if observed {
		st := r
		st.JobID += "-stop"
		st.OperationID = st.JobID
		st.Action = "model.stop"
		rec, e := b.Execute(ctx, st)
		out["stop_receipt"] = rec
		if e != nil {
			return e
		}
		cancelled = rec.Status == protocol.TargetSucceeded
	}
	select {
	case a := <-done:
		out["start_receipt"] = a.Receipt
		if a.Err != nil {
			return a.Err
		}
		cancelled = cancelled && a.Receipt.Status == protocol.TargetRejected && a.Receipt.ErrorCode == "start_cancelled_by_stop"
	case <-ctx.Done():
		return fmt.Errorf("preflight start receipt timed out")
	}
	if cancelled {
		out["test_status"] = "PASS"
	} else {
		out["test_status"] = "NOT_OBSERVED"
		out["reason"] = "real preflight cancellation window did not yield both rejection and stop proof; no retry or fabricated coverage"
	}
	return nil
}
