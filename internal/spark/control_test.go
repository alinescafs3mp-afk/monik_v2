package spark

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
)

type fakeSupervisor struct {
	mu        sync.Mutex
	instances map[string]Instance
	effects   map[string]int
	starting  bool
	failAfter bool
	stopGate  chan struct{}
	logCalls  int
	logPipe   *io.PipeWriter
}

func (f *fakeSupervisor) Inspect(_ context.Context, p Profile) (Instance, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	i, ok := f.instances[p.ID]
	if !ok {
		i = Instance{State: "stopped", Identity: "stopped", Available: true, ShutdownVerified: true}
	}
	return i, nil
}
func (f *fakeSupervisor) Definition(_ context.Context, p Profile) (string, error) {
	return p.Target.DefinitionHash, nil
}
func (f *fakeSupervisor) Effect(ctx context.Context, p Profile, action string) (Instance, error) {
	f.mu.Lock()
	f.effects[p.ID+":"+action]++
	state := "running"
	identity := fmt.Sprintf("instance-%d", f.effects[p.ID+":"+action])
	if action == "stop" {
		state = "stopped"
		identity = "stopped"
	}
	if action == "start" && f.starting {
		state = "starting"
	}
	i := Instance{State: state, Identity: identity, Available: true, ShutdownVerified: state == "stopped"}
	f.instances[p.ID] = i
	fail := f.failAfter
	gate := f.stopGate
	f.mu.Unlock()
	if action == "stop" && gate != nil {
		select {
		case <-ctx.Done():
			return i, ctx.Err()
		case <-gate:
		}
	}
	if fail {
		return i, fmt.Errorf("synthetic lost response")
	}
	return i, nil
}
func (f *fakeSupervisor) Logs(ctx context.Context, _ Profile) (LogReader, error) {
	f.mu.Lock()
	f.logCalls++
	r, w := io.Pipe()
	f.logPipe = w
	f.mu.Unlock()
	go func() { <-ctx.Done(); r.Close(); w.Close() }()
	return r, nil
}
func controlFixture(t *testing.T) (*Coordinator, *fakeSupervisor, Profile) {
	t.Helper()
	p := Profile{ID: "model-a", DisplayName: "fixture", Engine: "custom", Target: Target{Kind: "systemd-system", Unit: "fixture.service", DefinitionHash: "sha256:" + strings.Repeat("a", 64)}, Settings: []protocol.SparkSetting{}, AllowedActions: []string{"start", "stop", "restart"}, ExclusionGroups: []string{"gpu"}}
	p.Revision = ProfileRevision(p)
	policy := &Policy{SchemaVersion: 1, Enabled: true, AgentID: "fixture-agent", ControllerID: "fixture-controller", CollectorUID: 1000, StateDir: t.TempDir(), Profiles: []Profile{p}}
	f := &fakeSupervisor{instances: map[string]Instance{}, effects: map[string]int{}}
	c, e := OpenCoordinator(policy, f)
	if e != nil {
		t.Fatal(e)
	}
	if e = c.Bind(policy.AgentID, policy.ControllerID, "session-a"); e != nil {
		t.Fatal(e)
	}
	_, e = c.Snapshot(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	return c, f, p
}
func actionRequest(c *Coordinator, p Profile, id, action string) ActionRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	return ActionRequest{JobID: id, OperationID: id, Action: "model." + action, Params: protocol.ModelActionParams{ProfileID: p.ID, ProfileRevision: p.Revision, ExpectedGeneration: c.journal.Profiles[p.ID].Generation, ControlEpoch: c.journal.Epoch, ExpectedAgentSession: c.session, ConfirmDisruption: true}, IssuedAt: now, Deadline: now.Add(30 * time.Second)}
}
func TestDurableDuplicateReconcilesLostReplyBeforeNewEffect(t *testing.T) {
	c, f, p := controlFixture(t)
	f.failAfter = true
	r := actionRequest(c, p, "op-start", "start")
	first := c.Execute(context.Background(), r)
	if first.Status != protocol.TargetUnknownResult {
		t.Fatalf("first=%+v", first)
	}
	reopened, e := OpenCoordinator(c.Policy, f)
	if e != nil {
		t.Fatal(e)
	}
	_ = reopened.Bind(c.Policy.AgentID, c.Policy.ControllerID, "session-new")
	got := reopened.Execute(context.Background(), r)
	if got.Status != protocol.TargetSucceeded || got.Stage != "model_started" {
		t.Fatalf("reconciled=%+v", got)
	}
	if f.effects[p.ID+":start"] != 1 {
		t.Fatalf("effect repeated: %v", f.effects)
	}
	changed := r
	changed.Action = "model.stop"
	if got = reopened.Execute(context.Background(), changed); got.ErrorCode != "idempotency_conflict" {
		t.Fatalf("changed duplicate=%+v", got)
	}
}
func TestStopIgnoresLaunchFailuresAndRestartStoppedRejected(t *testing.T) {
	c, f, p := controlFixture(t)
	f.instances[p.ID] = Instance{State: "running", Identity: "original", Available: true}
	p.Weights = []string{"/missing/fixture-weight"}
	p.RequiredMemoryBytes = ^uint64(0) - 1024
	p.Revision = ProfileRevision(p)
	c.Policy.Profiles[0] = p
	_, _ = c.Snapshot(context.Background())
	r := actionRequest(c, p, "stop-low-memory", "stop")
	got := c.Execute(context.Background(), r)
	if got.Status != protocol.TargetSucceeded || got.Stage != "model_stopped" {
		t.Fatalf("stop=%+v", got)
	}
	restart := actionRequest(c, p, "restart-stopped", "restart")
	if got = c.Execute(context.Background(), restart); got.ErrorCode != "not_running_use_start" {
		t.Fatalf("restart=%+v", got)
	}
	start := actionRequest(c, p, "missing-weights", "start")
	if got = c.Execute(context.Background(), start); got.ErrorCode != "weights_unavailable" {
		t.Fatalf("start=%+v", got)
	}
	if f.effects[p.ID+":start"] != 0 {
		t.Fatal("failed preflight ran start")
	}
}
func TestStopDuringStartupAndGenerationDrift(t *testing.T) {
	c, f, p := controlFixture(t)
	f.starting = true
	start := actionRequest(c, p, "slow-start", "start")
	got := c.Execute(context.Background(), start)
	if got.Status != protocol.TargetRunning {
		t.Fatalf("start=%+v", got)
	}
	before := time.Now()
	stop := actionRequest(c, p, "stop-during-start", "stop")
	got = c.Execute(context.Background(), stop)
	if got.Status != protocol.TargetSucceeded || time.Since(before) > time.Second {
		t.Fatalf("stop=%+v", got)
	}
	old := actionRequest(c, p, "stale-start", "start")
	f.instances[p.ID] = Instance{State: "running", Identity: "manual-new", Available: true}
	if got = c.Execute(context.Background(), old); got.ErrorCode != "generation_drift" {
		t.Fatalf("stale=%+v", got)
	}
}
func TestNoEffectWithoutDurableJournalAndExpiredDispatch(t *testing.T) {
	c, f, p := controlFixture(t)
	r := actionRequest(c, p, "expired", "start")
	r.IssuedAt = time.Now().Add(-40 * time.Second)
	r.Deadline = time.Now().Add(-10 * time.Second)
	if got := c.Execute(context.Background(), r); got.ErrorCode != "dispatch_expired" {
		t.Fatalf("expired=%+v", got)
	}
	c.path = c.Policy.StateDir
	r = actionRequest(c, p, "disk-unavailable", "start")
	if got := c.Execute(context.Background(), r); got.ErrorCode != "journal_unavailable" {
		t.Fatalf("journal=%+v", got)
	}
	if len(f.effects) != 0 {
		t.Fatal("effects after rejected authorization/journal")
	}
}
func TestRestartUsesLinkedFreshStartNotOriginalLease(t *testing.T) {
	c, f, p := controlFixture(t)
	f.instances[p.ID] = Instance{State: "running", Identity: "before", Available: true}
	_, _ = c.Snapshot(context.Background())
	restart := actionRequest(c, p, "restart-parent", "restart")
	got := c.Execute(context.Background(), restart)
	if got.Stage != "restart_stopped" || got.Status != protocol.TargetAwaitingConfirmation {
		t.Fatalf("restart=%+v", got)
	}
	bad := actionRequest(c, p, "unlinked-stage", "start")
	bad.ParentJobID = "nonexistent"
	if got = c.Execute(context.Background(), bad); got.ErrorCode != "linked_stage_not_authorized" {
		t.Fatalf("unlinked=%+v", got)
	}
	stage := actionRequest(c, p, "fresh-stage", "start")
	stage.OperationID = restart.OperationID
	stage.ParentJobID = restart.JobID
	got = c.Execute(context.Background(), stage)
	if got.Status != protocol.TargetSucceeded || f.effects[p.ID+":start"] != 1 || f.effects[p.ID+":stop"] != 1 {
		t.Fatalf("stage=%+v effects=%v", got, f.effects)
	}
	if p.Revision != ProfileRevision(p) {
		t.Fatal("runtime changed profile revision")
	}
}
func TestUnknownReadinessDoesNotBecomeReadyFromPID(t *testing.T) {
	_, _, p := controlFixture(t)
	if state, reason := EngineReadiness(context.Background(), p); state != "unknown" || reason == "" {
		t.Fatalf("readiness=%s %s", state, reason)
	}
	p.HealthURL = "http://127.0.0.1:3000/health_generate"
	if ValidateLoopbackHealthURL(p.HealthURL) == nil {
		t.Fatal("health_generate allowed")
	}
	p.HealthURL = "http://0.0.0.0:3000/health"
	if ValidateLoopbackHealthURL(p.HealthURL) == nil {
		t.Fatal("wildcard probe allowed")
	}
}
