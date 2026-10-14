package spark

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
)

const existingCoordinatorKind = "existing-coordinator"
const existingProject = "/opt/spark/model-collection-v4"

var existingIDs = []string{"qwen3-coder-next-80b-huihui", "ling-3-flash-124b-heretic-text", "mistral-small-4-119b-heretic", "nemotron-3-super-120b-heretic-v2", "minimax-m27-229b-heretic", "qwen-existing-observation", "gpt-oss-120b-existing-configured"}

// Approval is fixed LOCAL policy, never a remote selector or telemetry-derived
// approval. BackendRevision has a separate domain from the Go profile revision.
type ExistingCoordinatorApproval struct {
	PythonPath      string            `json:"python_path"`
	BackendRevision string            `json:"backend_revision"`
	CodePins        []ExistingCodePin `json:"code_pins"`
	LogRoot         string            `json:"log_root,omitempty"`
	RedactionFile   string            `json:"redaction_file,omitempty"`
	RedactionSHA256 string            `json:"redaction_sha256,omitempty"`
}
type ExistingCodePin struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

func bareHash(s string) bool { return len(s) == 64 && isHex(s) && strings.ToLower(s) == s }
func existingRequiredPaths(p Profile) []string {
	unit := "/etc/systemd/system/spark-v4-" + p.ID + "@.service"
	if p.ID == "qwen-existing-observation" {
		unit = "/etc/systemd/system/spark-sglang.service"
	}
	paths := []string{p.Target.Existing.PythonPath, existingProject + "/bin/monik_bridge.py", existingProject + "/bin/modelctl.py", existingProject + "/bin/supervisor.py", existingProject + "/config/controller-manifest.json", "/usr/local/sbin/spark-modelctl-v4", unit, existingProject + "/bin/legacy_runtime.py", existingProject + "/bin/legacy_entry.py"}
	if p.ID == "qwen-existing-observation" {
		paths = append(paths, "/etc/systemd/system/spark-sglang.service.d/90-sol-cache2.conf", "/etc/systemd/system/spark-sglang.service.d/99-monik-shared-controller.conf")
	}
	return paths
}
func (p Profile) validateExistingCoordinator() error {
	a := p.Target.Existing
	if a == nil || !member(p.ID, existingIDs...) || !bareHash(a.BackendRevision) || !regexp.MustCompile(`^/usr/bin/python3\.[0-9]+$`).MatchString(a.PythonPath) || p.Target.DefinitionHash != digest(a) || p.ObservationOnly || !validExistingLogApproval(p) || len(p.RedactionFiles) > 0 || p.Target.SharedLock != "" || p.Target.Unit != "" || p.Target.UserUID != 0 || p.Target.Frozen != nil || p.Target.ContainerID != "" || p.Target.DockerSocket != "" || p.Target.AllowRootUnit || len(p.Target.TrustedFiles) > 0 || len(p.ExclusionGroups) > 0 || len(p.Weights) > 0 || p.RequiredMemoryBytes != 0 || p.OSHeadroomBytes != 0 || p.BlockUnknownMemory || p.Port != 0 {
		return fmt.Errorf("invalid fixed existing coordinator approval or duplicate admission owner")
	}
	required := existingRequiredPaths(p)
	if len(a.CodePins) != len(required) {
		return fmt.Errorf("complete existing coordinator source pins required")
	}
	for index, pin := range a.CodePins {
		if pin.Path != required[index] || !bareHash(pin.SHA256) {
			return fmt.Errorf("exact ordered protected coordinator source pins required")
		}
	}
	return nil
}

type existingAuthority struct {
	ControlEpoch string `json:"control_epoch"`
	AgentSession string `json:"agent_session"`
	BootID       string `json:"boot_id"`
}
type existingBinding struct {
	AgentID      string `json:"agent_id"`
	ControllerID string `json:"controller_id"`
	existingAuthority
}
type existingRequest struct {
	SchemaVersion      int    `json:"schema_version"`
	OperationID        string `json:"operation_id"`
	ProfileID          string `json:"profile_id"`
	Action             string `json:"action"`
	ProfileRevision    string `json:"profile_revision"`
	ExpectedGeneration int64  `json:"expected_generation"`
	ExpectedInstance   string `json:"expected_instance"`
	existingAuthority
	IssuedAtMS        int64  `json:"issued_at_ms"`
	DeadlineMS        int64  `json:"deadline_ms"`
	ParentOperationID string `json:"parent_operation_id,omitempty"`
}
type existingOwner struct {
	Unit         string `json:"unit"`
	InvocationID string `json:"invocation_id"`
	PID          int    `json:"pid"`
	Birth        string `json:"birth"`
}
type existingPhysicalInstance struct {
	RunID                   *string        `json:"run_id"`
	Owner                   *existingOwner `json:"owner"`
	ContainerID             *string        `json:"container_id"`
	ContainerCreated        *string        `json:"container_created"`
	EnginePID               *int           `json:"engine_pid"`
	EngineBirth             *string        `json:"engine_birth"`
	LegacyDeclarationSHA256 *string        `json:"legacy_declaration_sha256,omitempty"`
}
type existingResult struct {
	ModelID          string                   `json:"model_id,omitempty"`
	State            string                   `json:"state,omitempty"`
	ProcessActive    bool                     `json:"process_active,omitempty"`
	APIReady         bool                     `json:"api_ready,omitempty"`
	Context          *int64                   `json:"context,omitempty"`
	Endpoint         string                   `json:"endpoint,omitempty"`
	EngineLog        string                   `json:"engine_log,omitempty"`
	ActionLog        string                   `json:"action_log,omitempty"`
	Message          string                   `json:"message,omitempty"`
	Generation       *int64                   `json:"generation,omitempty"`
	Attempt          *string                  `json:"attempt,omitempty"`
	RunID            *string                  `json:"run_id,omitempty"`
	Reason           string                   `json:"reason,omitempty"`
	EffectInvoked    *bool                    `json:"effect_invoked,omitempty"`
	ShutdownVerified bool                     `json:"shutdown_verified,omitempty"`
	ShutdownWitness  *existingShutdownWitness `json:"shutdown_witness,omitempty"`
	InstanceIdentity string                   `json:"instance_identity,omitempty"`
}
type existingShutdownWitness struct {
	Kind                    string         `json:"kind"`
	ModelID                 string         `json:"model_id"`
	RunID                   *string        `json:"run_id"`
	Generation              int64          `json:"generation"`
	Attempt                 *string        `json:"attempt"`
	InstanceIdentity        string         `json:"instance_identity"`
	Owner                   *existingOwner `json:"owner"`
	ContainerID             *string        `json:"container_id"`
	ContainerCreated        *string        `json:"container_created"`
	EnginePID               *int           `json:"engine_pid"`
	EngineBirth             *string        `json:"engine_birth"`
	LegacyDeclarationSHA256 *string        `json:"legacy_declaration_sha256,omitempty"`
}
type existingSnapshot struct {
	existingResult
	ProfileRevision string                    `json:"profile_revision"`
	ObservedAtMS    int64                     `json:"observed_at_ms"`
	Instance        *existingPhysicalInstance `json:"instance"`
	existingAuthority
}
type existingReceipt struct {
	OperationID string          `json:"operation_id"`
	ProfileID   string          `json:"profile_id"`
	Action      string          `json:"action"`
	Phase       string          `json:"phase"`
	RunID       *string         `json:"run_id"`
	Generation  int64           `json:"generation"`
	Attempt     *string         `json:"attempt,omitempty"`
	Result      *existingResult `json:"result,omitempty"`
	Stage       string          `json:"stage,omitempty"`
}
type existingEnvelope struct {
	SchemaVersion int                `json:"schema_version"`
	Method        string             `json:"method"`
	ProfileIDs    []string           `json:"profile_ids,omitempty"`
	Request       *existingRequest   `json:"request,omitempty"`
	Authority     *existingAuthority `json:"authority,omitempty"`
	Binding       *existingBinding   `json:"binding,omitempty"`
}
type existingResponse struct {
	SchemaVersion int                `json:"schema_version"`
	Models        []existingSnapshot `json:"models,omitempty"`
	Receipt       *existingReceipt   `json:"receipt,omitempty"`
	Authority     *existingAuthority `json:"authority,omitempty"`
	Error         string             `json:"error,omitempty"`
}

type existingTransport interface {
	ExistingObserve(context.Context, Profile) (existingSnapshot, error)
	ExistingBind(context.Context, Profile, existingBinding) error
	ExistingSubmit(context.Context, Profile, existingRequest) (existingReceipt, error)
	ExistingReceipt(context.Context, Profile, existingRequest, existingAuthority) (existingReceipt, error)
}

// Monitoring alone can share one bounded helper invocation. Effects and receipt
// reconciliation deliberately retain their independent exact-profile probes.
type existingBatchObserver interface {
	ExistingObserveMany(context.Context, []Profile) ([]existingSnapshot, error)
}

func (c *Coordinator) existingSnapshotRound(ctx context.Context) (map[string]Instance, error) {
	transport, ok := c.supervisor.(existingBatchObserver)
	if !ok {
		return nil, nil
	}
	var profiles []Profile
	for _, p := range c.Policy.Profiles {
		if p.Target.Kind == existingCoordinatorKind {
			profiles = append(profiles, p)
		}
	}
	if len(profiles) == 0 {
		return nil, nil
	}
	// The existing wire freshness limit remains five seconds for every member;
	// a slow/incomplete round fails closed without singleton fallback retries.
	probe, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	snapshots, err := transport.ExistingObserveMany(probe, profiles)
	if err != nil || probe.Err() != nil || len(snapshots) != len(profiles) {
		return nil, fmt.Errorf("existing coordinator observation round unavailable")
	}
	instances := make(map[string]Instance, len(profiles))
	for index, p := range profiles {
		if validateExistingSnapshot(snapshots[index], p) != nil || snapshots[index].ProfileRevision != p.Target.Existing.BackendRevision {
			return nil, fmt.Errorf("existing coordinator observation round invalid")
		}
		instances[p.ID] = snapshots[index].instance(p)
	}
	return instances, nil
}

func (s existingSnapshot) instance(p Profile) Instance {
	state := map[string]string{"READY": "running", "STARTING": "starting", "STOPPING": "stopping", "STOPPED": "stopped", "BLOCKED": "stopped", "FAILED": "failed"}[s.State]
	if state == "" {
		state = "unknown"
	}
	identity := digest([]any{existingCoordinatorKind, p.ID, s.Generation, s.InstanceIdentity})
	return Instance{State: state, Identity: identity, Available: state != "unknown", DefinitionHash: p.Target.DefinitionHash, Backend: &s, ShutdownVerified: validExistingShutdown(s.existingResult, p.ID, s.RunID, valueGeneration(s.Generation))}
}
func existingBoot() (string, error) {
	b, e := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if e != nil {
		return "", fmt.Errorf("boot identity unavailable")
	}
	s := strings.TrimSpace(string(b))
	if !regexp.MustCompile(`^[a-f0-9-]{36}$`).MatchString(s) {
		return "", fmt.Errorf("boot identity invalid")
	}
	return s, nil
}
func (c *Coordinator) bindExistingLocked(agent, controller, session string) error {
	if !c.Policy.Enabled {
		return nil
	}
	var first *Profile
	for _, p := range c.Policy.Profiles {
		if p.Target.Kind == existingCoordinatorKind {
			q := p
			first = &q
			break
		}
	}
	if first == nil {
		return nil
	}
	transport, ok := c.supervisor.(existingTransport)
	if !ok {
		return fmt.Errorf("typed existing coordinator transport required")
	}
	boot, e := existingBoot()
	if e != nil {
		return e
	}
	ctx, end := context.WithTimeout(context.Background(), 5*time.Second)
	defer end()
	return transport.ExistingBind(ctx, *first, existingBinding{AgentID: agent, ControllerID: controller, existingAuthority: existingAuthority{c.journal.Epoch, session, boot}})
}
func (c *Coordinator) existingAuthorityLocked() (existingAuthority, error) {
	boot, e := existingBoot()
	if e != nil || c.session == "" {
		return existingAuthority{}, fmt.Errorf("existing_coordinator_current_authority_required")
	}
	return existingAuthority{c.journal.Epoch, c.session, boot}, nil
}

// Go retains an immutable transport journal, but never takes the backend's lock,
// reserves model resources, starts Docker, or performs a second launch preflight.
func (c *Coordinator) executeExisting(ctx context.Context, r ActionRequest, p Profile) protocol.JobReceipt {
	transport, ok := c.supervisor.(existingTransport)
	if !ok {
		return rejection(r, "typed_existing_transport_required")
	}
	requestDigest := digest(r)
	c.mu.Lock()
	authority, e := c.existingAuthorityLocked()
	if e != nil || !c.Policy.Enabled || r.Params.ProfileRevision != p.Revision {
		c.mu.Unlock()
		return rejection(r, "authority_changed")
	}
	prior, exists := c.journal.Attempts[r.JobID]
	if exists {
		if prior.Digest != requestDigest {
			c.mu.Unlock()
			return rejection(r, "idempotency_conflict")
		}
		if prior.BackendRequest == nil {
			c.mu.Unlock()
			return rejection(r, "attempt_unresolved")
		}
		if prior.Stage == "restart_stopped" || prior.Stage == "done" {
			c.mu.Unlock()
			return prior.Receipt
		}
		if prior.Stage == "start_attempted" && prior.Request.Action == "model.restart" {
			var child *ActionRequest
			for _, stage := range c.journal.Attempts {
				if stage.Request.ParentJobID == r.JobID && stage.Request.OperationID == r.OperationID && stage.Request.Params.ProfileID == p.ID {
					if child != nil {
						c.mu.Unlock()
						return prior.Receipt
					}
					copy := stage.Request
					child = &copy
				}
			}
			c.mu.Unlock()
			if child != nil {
				_ = c.executeExisting(ctx, *child, p)
			}
			c.mu.Lock()
			current := c.journal.Attempts[r.JobID].Receipt
			c.mu.Unlock()
			return current
		}
		original := *prior.BackendRequest
		c.mu.Unlock()
		receipt, err := transport.ExistingReceipt(ctx, p, original, authority)
		if err != nil {
			return prior.Receipt
		}
		if prior.Stage == "done" {
			return prior.Receipt
		}
		return c.finishExisting(ctx, transport, r, p, receipt, authority)
	}
	c.mu.Unlock()
	snap, e := transport.ExistingObserve(ctx, p)
	if e != nil {
		return rejection(r, "supervisor_identity_unavailable")
	}
	before := snap.instance(p)
	c.mu.Lock()
	// Another caller may have recorded this ID while observation was in flight.
	if _, exists = c.journal.Attempts[r.JobID]; exists {
		c.mu.Unlock()
		return c.executeExisting(ctx, r, p)
	}
	current := c.observeLocked(p, before)
	session := c.session
	if r.Local {
		session = "local:" + c.journal.Epoch
	}
	allowed := member(strings.TrimPrefix(r.Action, "model."), p.AllowedActions...)
	if r.ParentJobID != "" {
		parent, yes := c.journal.Attempts[r.ParentJobID]
		allowed = yes && parent.Stage == "restart_stopped" && parent.Request.Action == "model.restart" && parent.Request.OperationID == r.OperationID && parent.Request.Params.ProfileID == p.ID && parent.Receipt.Evidence["shutdown_verified"] == true && parent.Receipt.VerifiedAt != nil && time.Since(*parent.Receipt.VerifiedAt) < 60*time.Second && r.Action == "model.start" && member("restart", p.AllowedActions...)
	}
	now := time.Now()
	code := ""
	if !allowed || !c.Policy.Enabled {
		code = "local_action_not_approved"
	} else if r.Params.ControlEpoch != c.journal.Epoch || r.Params.ExpectedAgentSession != session || r.Params.ProfileRevision != p.Revision || snap.ControlEpoch != authority.ControlEpoch || snap.AgentSession != authority.AgentSession || snap.BootID != authority.BootID {
		code = "authority_or_profile_drift"
	} else if r.Params.ExpectedGeneration != current.Generation {
		code = "generation_drift"
	} else if snap.ProfileRevision != p.Target.Existing.BackendRevision {
		code = "effective_definition_drift"
	} else if before.State == "unknown" {
		code = "supervisor_identity_unavailable"
	} else if r.Action == "model.restart" && before.State != "running" {
		code = "not_running_use_start"
	} else if r.Action != "model.start" && before.State != "stopped" && !r.Params.ConfirmDisruption {
		code = "disruption_confirmation_required"
	} else if r.IssuedAt.IsZero() || !now.Before(r.Deadline) || !r.Deadline.After(r.IssuedAt) || r.Deadline.Sub(r.IssuedAt) > protocol.SparkDispatchLease || r.IssuedAt.After(now.Add(time.Second)) {
		code = "dispatch_expired"
	}
	if code != "" {
		c.mu.Unlock()
		return rejection(r, code)
	}
	if len(c.journal.Attempts) >= 2048 {
		c.mu.Unlock()
		return rejection(r, "journal_capacity")
	}
	action := strings.TrimPrefix(r.Action, "model.")
	if action == "restart" {
		action = "stop"
	}
	generation := int64(0)
	if snap.Generation != nil {
		generation = *snap.Generation
	}
	request := existingRequest{SchemaVersion: 1, OperationID: r.JobID, ProfileID: p.ID, Action: action, ProfileRevision: p.Target.Existing.BackendRevision, ExpectedGeneration: generation, ExpectedInstance: snap.InstanceIdentity, existingAuthority: authority, IssuedAtMS: r.IssuedAt.UnixMilli(), DeadlineMS: r.Deadline.UnixMilli(), ParentOperationID: r.ParentJobID}
	a := attempt{Request: r, Digest: requestDigest, Before: before, Stage: "existing_attempted", At: now, BackendRequest: &request, Receipt: c.evidenceReceipt(r, p, before, protocol.TargetUnknownResult, "effect_attempted")}
	c.journal.Attempts[r.JobID] = a
	if r.ParentJobID != "" {
		parent := c.journal.Attempts[r.ParentJobID]
		parent.Stage = "start_attempted"
		parent.Receipt.Status = protocol.TargetRunning
		parent.Receipt.Stage = "restart_start_attempted"
		parent.Receipt.VerifiedAt = nil
		c.journal.Attempts[r.ParentJobID] = parent
	}
	if c.saveLocked() != nil {
		c.mu.Unlock()
		return rejection(r, "journal_unavailable")
	}
	c.mu.Unlock()
	remaining := time.Until(r.Deadline)
	if remaining <= 0 {
		return a.Receipt
	}
	effectCtx, end := context.WithTimeout(ctx, min(remaining, 5*time.Second))
	defer end()
	receipt, e := transport.ExistingSubmit(effectCtx, p, request)
	if e != nil {
		return a.Receipt
	} // Reconcile only. Even a refusal can follow an attempted effect.
	return c.finishExisting(ctx, transport, r, p, receipt, authority)
}

func (c *Coordinator) finishExisting(ctx context.Context, transport existingTransport, r ActionRequest, p Profile, backend existingReceipt, authority existingAuthority) protocol.JobReceipt {
	c.mu.Lock()
	a, ok := c.journal.Attempts[r.JobID]
	c.mu.Unlock()
	if !ok || a.BackendRequest == nil || validateExistingReceipt(backend, *a.BackendRequest) != nil {
		return rejection(r, "backend_receipt_invalid")
	}
	// Receipt authority authenticates lookup, not a later independent observation.
	// Obtain one exact physical snapshot and recheck all authority/approval and
	// known identity fields before mapping it into the Go observation counter.
	var current *existingSnapshot
	if backend.Result != nil && backend.Result.ModelID != "" || backend.RunID != nil && member(backend.Phase, "dispatched", "completed") && backend.Result == nil {
		snap, err := transport.ExistingObserve(ctx, p)
		if err == nil && validateExistingSnapshot(snap, p) == nil && snap.existingAuthority == authority && snap.ProfileRevision == p.Target.Existing.BackendRevision && equalString(snap.RunID, backend.RunID) && equalString(snap.Attempt, backend.Attempt) && valueGeneration(snap.Generation) == backend.Generation && compatibleExistingPhysical(a.BackendSnapshot, &snap) {
			if a.Before.Backend == nil || !equalString(a.Before.Backend.RunID, backend.RunID) || compatibleExistingPhysical(a.Before.Backend, &snap) {
				current = &snap
			}
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	a = c.journal.Attempts[r.JobID]
	if a.Stage == "done" || a.Stage == "restart_stopped" || a.Stage == "start_attempted" && r.Action == "model.restart" {
		return a.Receipt
	}
	nowAuthority, err := c.existingAuthorityLocked()
	currentProfile, approved := c.profile(p.ID)
	if err != nil || nowAuthority != authority || !c.Policy.Enabled || !approved || currentProfile.Revision != p.Revision || currentProfile.Target.DefinitionHash != p.Target.DefinitionHash {
		return a.Receipt
	}
	if a.BackendReceipt != nil && (!equalString(a.BackendReceipt.RunID, backend.RunID) || !equalString(a.BackendReceipt.Attempt, backend.Attempt) || a.BackendReceipt.Generation != backend.Generation) {
		return a.Receipt
	}
	if current != nil && !compatibleExistingPhysical(a.BackendSnapshot, current) {
		current = nil
	}
	if current != nil {
		observed := c.journal.Profiles[p.ID]
		if observed.Identity != current.instance(p).Identity && observed.Identity != a.Before.Identity && observed.BackendObservedAtMS >= current.ObservedAtMS {
			current = nil
		}
	}
	// Backend-only identities stay in this private journal, never the public
	// receipt evidence whitelist. The proof uses the same snapshot identity and
	// Go generation as an ordinary subsequent BrokerSnapshot report.
	instance := a.Before
	instance.State = "unknown"
	instance.Available = false
	instance.ShutdownVerified = false
	if current != nil {
		instance = current.instance(p)
		c.observeLocked(p, instance)
		a.BackendSnapshot = current
	}
	rec := c.existingEvidenceLocked(r, p, instance, protocol.TargetUnknownResult, "attempt_unresolved")
	rec.VerifiedAt = nil
	result := backend.Result
	if result != nil && result.EffectInvoked != nil && !*result.EffectInvoked {
		a.Stage = "done"
		rec = rejection(r, "backend_effect_not_invoked")
	} else if current != nil && backend.Phase == "completed" && result != nil && backend.Action == "stop" && validExistingShutdown(*result, p.ID, backend.RunID, backend.Generation) && current.InstanceIdentity == result.InstanceIdentity && equalString(result.Attempt, backend.Attempt) && equalString(current.Attempt, backend.Attempt) && validExistingShutdown(current.existingResult, p.ID, backend.RunID, backend.Generation) {
		rec = c.existingEvidenceLocked(r, p, instance, protocol.TargetSucceeded, "model_stopped")
		a.Stage = "done"
		if r.Action == "model.restart" {
			a.Stage = "restart_stopped"
			rec.Status = protocol.TargetAwaitingConfirmation
			rec.Stage = "restart_stopped"
			rec.Message = "stopped; fresh linked start authorization required"
		}
	} else if current != nil && backend.Phase == "completed" && result != nil && backend.Action == "start" && member(result.State, "READY", "STARTING") && current.State == "READY" && current.ProcessActive && current.APIReady && existingReadyPhysical(*current, p) {
		rec = c.existingEvidenceLocked(r, p, instance, protocol.TargetSucceeded, "model_started")
		rec.Evidence["readiness"] = "ready"
		a.Stage = "done"
	} else if current != nil && backend.Action == "start" && member(current.State, "STARTING", "READY") {
		rec.Status = protocol.TargetRunning
		rec.Stage = "model_starting"
	}
	a.Receipt = rec
	a.BackendReceipt = &backend
	if backend.Action == "start" && a.Stage != "done" {
		for _, stop := range c.journal.Attempts {
			if stop.Request.Action == "model.stop" && stop.Request.Params.ProfileID == p.ID && stop.Stage == "done" && stop.Receipt.Status == protocol.TargetSucceeded && stop.Receipt.Evidence["shutdown_verified"] == true && sameExistingRun(stop.BackendReceipt, &backend) {
				a.Stage = "done"
				a.Receipt = existingCancelledReceipt(stop.Receipt, r)
				rec = a.Receipt
				break
			}
		}
	}
	c.journal.Attempts[r.JobID] = a
	if backend.Action == "stop" && rec.Status == protocol.TargetSucceeded {
		for id, start := range c.journal.Attempts {
			if start.Request.Action == "model.start" && start.Stage != "done" && sameExistingRun(start.BackendReceipt, &backend) && start.Request.Params.ProfileID == p.ID {
				start.Stage = "done"
				start.Receipt = existingCancelledReceipt(rec, start.Request)
				c.journal.Attempts[id] = start
			}
		}
	}
	c.finishParentLocked(a)
	if c.saveLocked() != nil {
		return rejection(r, "journal_post_effect_unavailable")
	}
	return rec
}
func (c *Coordinator) existingEvidenceLocked(r ActionRequest, p Profile, i Instance, status protocol.TargetStatus, stage string) protocol.JobReceipt {
	rec := c.evidenceReceipt(r, p, i, status, stage)
	rec.Evidence["control_epoch"] = r.Params.ControlEpoch
	rec.Evidence["agent_session"] = r.Params.ExpectedAgentSession
	return rec
}
func existingCancelledReceipt(stop protocol.JobReceipt, r ActionRequest) protocol.JobReceipt {
	stop.JobID = r.JobID
	stop.OperationID = r.OperationID
	stop.Status = protocol.TargetFailed
	stop.Stage = "start_cancelled_after_stop"
	stop.ErrorCode = "superseded_by_stop"
	return stop
}
func sameExistingRun(a, b *existingReceipt) bool {
	return a != nil && b != nil && a.RunID != nil && equalString(a.RunID, b.RunID) && equalString(a.Attempt, b.Attempt) && a.Generation == b.Generation
}
func compatibleExistingPhysical(previous, current *existingSnapshot) bool {
	if previous == nil {
		return true
	}
	if current == nil || !equalString(previous.RunID, current.RunID) || !equalString(previous.Attempt, current.Attempt) || valueGeneration(previous.Generation) != valueGeneration(current.Generation) {
		return false
	}
	if previous.Instance == nil {
		return true
	}
	if current.Instance == nil {
		return false
	}
	before, after := previous.Instance, current.Instance
	return (before.Owner == nil || after.Owner != nil && *before.Owner == *after.Owner) && (before.ContainerID == nil || equalString(before.ContainerID, after.ContainerID)) && (before.ContainerCreated == nil || equalString(before.ContainerCreated, after.ContainerCreated)) && (before.EnginePID == nil || after.EnginePID != nil && *before.EnginePID == *after.EnginePID) && (before.EngineBirth == nil || equalString(before.EngineBirth, after.EngineBirth)) && (before.LegacyDeclarationSHA256 == nil || equalString(before.LegacyDeclarationSHA256, after.LegacyDeclarationSHA256))
}
func existingPhysicalIdentity(i *existingPhysicalInstance) string {
	if i == nil {
		return ""
	}
	var owner any
	if i.Owner != nil {
		owner = map[string]any{"unit": i.Owner.Unit, "invocation_id": i.Owner.InvocationID, "pid": i.Owner.PID, "birth": i.Owner.Birth}
	}
	physical := map[string]any{"run_id": i.RunID, "owner": owner, "container_id": i.ContainerID, "container_created": i.ContainerCreated, "engine_pid": i.EnginePID, "engine_birth": i.EngineBirth}
	if i.LegacyDeclarationSHA256 != nil {
		physical["legacy_declaration_sha256"] = *i.LegacyDeclarationSHA256
	}
	return strings.TrimPrefix(digest(physical), "sha256:")
}
func existingReadyPhysical(s existingSnapshot, p Profile) bool {
	i := s.Instance
	if i == nil || i.Owner == nil || s.RunID == nil || !validExistingDeclaration(p.ID, i.LegacyDeclarationSHA256) || existingLegacyID(p.ID) && i.LegacyDeclarationSHA256 == nil {
		return false
	}
	unit := "spark-v4-" + p.ID + "@" + *s.RunID + ".service"
	if p.ID == "qwen-existing-observation" {
		unit = "spark-sglang.service"
	}
	return i.Owner.Unit == unit && lowerHex(i.Owner.InvocationID, 32) && i.Owner.PID > 0 && i.Owner.Birth != "" && i.ContainerID != nil && lowerHex(*i.ContainerID, 64) && i.ContainerCreated != nil && *i.ContainerCreated != "" && i.EnginePID != nil && *i.EnginePID > 0 && i.EngineBirth != nil && *i.EngineBirth != ""
}
func existingLegacyID(id string) bool {
	return id == "qwen-existing-observation" || id == "gpt-oss-120b-existing-configured"
}
func validExistingDeclaration(id string, declaration *string) bool {
	return declaration == nil || existingLegacyID(id) && bareHash(*declaration)
}
func lowerHex(value string, length int) bool {
	return len(value) == length && strings.ToLower(value) == value && isHex(value)
}

func equalString(a, b *string) bool { return a == nil && b == nil || a != nil && b != nil && *a == *b }
func validateExistingReceipt(r existingReceipt, q existingRequest) error {
	if r.OperationID != q.OperationID || r.ProfileID != q.ProfileID || r.Action != q.Action || !member(r.Phase, "completed", "dispatched", "unresolved") || r.Generation < 0 || r.Generation > 9007199254740991 || r.RunID != nil && !lowerHex(*r.RunID, 32) || r.Stage != "" && !member(r.Stage, "prepared", "effect_attempted") {
		return fmt.Errorf("invalid exact backend receipt")
	}
	if r.RunID != nil && (r.Attempt == nil || !lowerHex(*r.Attempt, 32)) || r.RunID == nil && r.Attempt != nil {
		return fmt.Errorf("receipt attempt identity required")
	}
	if r.Result != nil {
		result := r.Result
		if result.ModelID == "" {
			if result.State != "" || result.ProcessActive || result.APIReady || result.RunID != nil || result.Generation != nil || result.Attempt != nil || result.ShutdownVerified || result.ShutdownWitness != nil {
				return fmt.Errorf("runtime result identity required")
			}
		} else {
			if result.ModelID != q.ProfileID || result.Generation == nil || *result.Generation != r.Generation || !equalString(result.RunID, r.RunID) || !equalString(result.Attempt, r.Attempt) || !member(result.State, "READY", "STARTING", "STOPPING", "STOPPED", "BLOCKED", "FAILED", "UNKNOWN") || r.RunID == nil && member(result.State, "READY", "STARTING", "STOPPING", "FAILED") || result.APIReady && (result.State != "READY" || !result.ProcessActive) || result.ProcessActive && member(result.State, "STOPPED", "BLOCKED") {
				return fmt.Errorf("runtime result does not match original receipt")
			}
		}
	} else if r.Phase == "completed" {
		return fmt.Errorf("completed result required")
	}

	if r.Action == "stop" && r.Generation != q.ExpectedGeneration || r.Action == "start" && (r.Generation < q.ExpectedGeneration || r.Generation > q.ExpectedGeneration+1) {
		return fmt.Errorf("receipt generation does not match original request")
	}
	return nil
}
func valueGeneration(g *int64) int64 {
	if g == nil {
		return -1
	}
	return *g
}
func validExistingShutdown(result existingResult, profile string, runID *string, generation int64) bool {
	witness := result.ShutdownWitness
	if !result.ShutdownVerified || result.ProcessActive || !member(result.State, "STOPPED", "BLOCKED", "FAILED") || witness == nil || witness.ModelID != profile || witness.Generation != generation || !equalString(witness.RunID, runID) || !equalString(witness.Attempt, result.Attempt) || result.InstanceIdentity != witness.InstanceIdentity || !validExistingDeclaration(profile, witness.LegacyDeclarationSHA256) {
		return false
	}
	if runID == nil {
		return witness.Kind == "no_owned_run" && witness.InstanceIdentity == "" && witness.Owner == nil && witness.ContainerID == nil && witness.ContainerCreated == nil && witness.EnginePID == nil && witness.EngineBirth == nil && witness.LegacyDeclarationSHA256 == nil
	}
	if witness.Kind != "exact_run_resources_released" || !bareHash(witness.InstanceIdentity) {
		return false
	}
	expected := existingPhysicalIdentity(&existingPhysicalInstance{RunID: witness.RunID, Owner: witness.Owner, ContainerID: witness.ContainerID, ContainerCreated: witness.ContainerCreated, EnginePID: witness.EnginePID, EngineBirth: witness.EngineBirth, LegacyDeclarationSHA256: witness.LegacyDeclarationSHA256})
	return expected == witness.InstanceIdentity
}
