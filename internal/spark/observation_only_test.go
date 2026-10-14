package spark

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
)

func observationDefinitionFixture(t *testing.T) (Profile, map[string]string) {
	t.Helper()
	p, m := rootDefinitionFixture(t)
	p.ID = "qwen-observation"
	p.Engine = "custom"
	p.ObservationOnly = true
	p.Target.AllowRootUnit = false
	p.Target.TrustedFiles = nil
	// The script does not need to be protected or even readable by the broker:
	// observing the protected unit metadata never approves this code to run.
	m["ExecStart"] = "{ path=/home/nvidia/models/run-qwen.sh ; argv[]=/home/nvidia/models/run-qwen.sh ; ignore_errors=no ; start_time=[n/a] ; pid=0 }"
	m["Environment"] = "LD_PRELOAD=/home/nvidia/unapproved.so"
	return p, m
}

func TestObservationOnlyDirectLogsRefuseBeforeAdapterDispatch(t *testing.T) {
	for _, adapter := range []string{"systemd-system", "systemd-user", "approved-launcher", "docker", "unsupported-adapter"} {
		t.Run(adapter, func(t *testing.T) {
			// No real Docker socket or system service is referenced. The exact
			// entry refusal must happen before validation, I/O or adapter selection.
			p := Profile{ObservationOnly: true, LogEnabled: true, Target: Target{Kind: adapter, DockerSocket: filepath.Join(t.TempDir(), "never-created.sock")}}
			reader, err := (LocalSupervisor{}).Logs(context.Background(), p)
			if reader != nil {
				reader.Close()
				t.Fatal("observation mode returned log reader")
			}
			if err == nil || err.Error() != "logs_not_locally_approved" {
				t.Fatalf("adapter %s reached log dispatch: %v", adapter, err)
			}
		})
	}
}

func TestObservationOnlyRootScriptMetadataUsesSeparateApprovalDomain(t *testing.T) {
	p, m := observationDefinitionFixture(t)
	observed, err := canonicalSystemdDefinition(p, m)
	if err != nil || !strings.HasPrefix(observed, "sha256:") {
		t.Fatalf("observation unavailable: %s %v", observed, err)
	}
	p, m = observationDefinitionFixture(t)
	m["ExecStart"] = strings.ReplaceAll(m["ExecStart"], "pid=0", "pid=234")
	m["ExecStart"] = strings.ReplaceAll(m["ExecStart"], "start_time=[n/a]", "start_time=[Wed 2026-10-14 10:00:00 MSK]")
	again, err := canonicalSystemdDefinition(p, m)
	if err != nil || again != observed {
		t.Fatalf("runtime altered observation binding: %s %v", again, err)
	}
	p, m = observationDefinitionFixture(t)
	m["ExecStart"] = strings.ReplaceAll(m["ExecStart"], "argv[]=/home/nvidia/models/run-qwen.sh", "argv[]=/home/nvidia/models/run-qwen.sh --different")
	changed, err := canonicalSystemdDefinition(p, m)
	if err != nil || changed == observed {
		t.Fatalf("metadata drift not bound: %s %v", changed, err)
	}
	p, m = observationDefinitionFixture(t)
	p.ObservationOnly = false
	if _, err = canonicalSystemdDefinition(p, m); err == nil {
		t.Fatal("root script became effect-approved by removing observation flag")
	}
	// Even supported code gets a distinct observation hash and effect hash.
	p, m = rootDefinitionFixture(t)
	effect, err := canonicalSystemdDefinition(p, m)
	if err != nil {
		t.Fatal(err)
	}
	p, m = rootDefinitionFixture(t)
	p.ObservationOnly, p.Target.AllowRootUnit, p.Target.TrustedFiles = true, false, nil
	readOnly, err := canonicalSystemdDefinition(p, m)
	if err != nil || readOnly == effect {
		t.Fatalf("observation approved effects by hash reuse: %v", err)
	}
}

func TestObservationOnlyPolicyCannotCombineActionsLogsOrRootApproval(t *testing.T) {
	for _, kind := range []string{"start", "stop", "restart", "logs", "root-approval", "trusted-code", "effect-lock", "user-manager"} {
		t.Run(kind, func(t *testing.T) {
			c, _, p := controlFixture(t)
			p.ObservationOnly = true
			p.AllowedActions = nil
			switch kind {
			case "start", "stop", "restart":
				p.AllowedActions = []string{kind}
			case "logs":
				p.LogEnabled = true
			case "root-approval":
				p.Target.AllowRootUnit = true
			case "trusted-code":
				p.Target.TrustedFiles = []string{"/usr/bin/true"}
			case "effect-lock":
				p.Target.SharedLock = "/run/unsafe-effect.lock"
			case "user-manager":
				p.Target.Kind, p.Target.UserUID = "systemd-user", 1000
			}
			p.Revision = ProfileRevision(p)
			c.Policy.Profiles = []Profile{p}
			if err := c.Policy.Validate(); err == nil {
				t.Fatalf("observation mode accepted %s", kind)
			}
			if _, err := (LocalSupervisor{}).Definition(context.Background(), p); err == nil {
				t.Fatalf("invalid observation mode reached definition commands: %s", kind)
			}
		})
	}
}

func TestObservationOnlyDeniesDirectRemoteLocalEffectsAndLogs(t *testing.T) {
	c, f, p := controlFixture(t)
	p.ObservationOnly = true
	p.AllowedActions = nil
	p.Revision = ProfileRevision(p)
	c.Policy.Profiles = []Profile{p}
	if err := c.Policy.Validate(); err != nil {
		t.Fatal(err)
	}
	snapshot, err := c.Snapshot(context.Background())
	if err != nil || len(snapshot.Inventory.Profiles) != 1 {
		t.Fatalf("observation inventory=%+v %v", snapshot, err)
	}
	public := snapshot.Inventory.Profiles[0]
	if public.ControlAllowed || public.LogAllowed || len(public.AllowedActions) != 0 || public.ControlReason != "observation_only" {
		t.Fatalf("observation advertises effects: %+v", public)
	}
	// Direct calls remain denied even with a malformed in-memory profile that
	// bypasses LoadPolicy. No supervisor operation or log stream can be granted.
	p.AllowedActions, p.LogEnabled = []string{"start", "stop", "restart"}, true
	p.Revision = ProfileRevision(p)
	c.Policy.Profiles = []Profile{p}
	for _, action := range []string{"start", "stop", "restart"} {
		if got := c.Execute(context.Background(), actionRequest(c, p, "remote-"+action, action)); got.Status != protocol.TargetRejected || got.ErrorCode != "local_action_not_approved" {
			t.Fatalf("remote %s=%+v", action, got)
		}
		if got := c.ExecuteLocal(context.Background(), "local-"+action, p.ID, action, true); got.Status != protocol.TargetRejected || got.ErrorCode != "local_action_not_approved" {
			t.Fatalf("local %s=%+v", action, got)
		}
		if _, err := (LocalSupervisor{}).Effect(context.Background(), p, action); err == nil {
			t.Fatalf("direct effect %s admitted", action)
		}
	}
	if reader, err := (LocalSupervisor{}).Logs(context.Background(), p); err == nil || reader != nil {
		if reader != nil {
			reader.Close()
		}
		t.Fatalf("direct observation logs admitted: %v", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.effects) != 0 || f.logCalls != 0 {
		t.Fatalf("effects=%v logs=%d", f.effects, f.logCalls)
	}
}
