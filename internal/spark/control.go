package spark

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/idgen"
	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
	"github.com/alinescafs3mp-afk/monik_v2/internal/secure"
)

type ActionRequest struct {
	JobID       string                     `json:"job_id"`
	OperationID string                     `json:"operation_id"`
	Action      string                     `json:"action"`
	Params      protocol.ModelActionParams `json:"params"`
	IssuedAt    time.Time                  `json:"issued_at"`
	Deadline    time.Time                  `json:"deadline"`
	ParentJobID string                     `json:"parent_job_id,omitempty"`
	Local       bool                       `json:"local,omitempty"`
}
type attempt struct {
	Request         ActionRequest       `json:"request"`
	Digest          string              `json:"digest"`
	Stage           string              `json:"stage"`
	Before          Instance            `json:"before"`
	Receipt         protocol.JobReceipt `json:"receipt"`
	At              time.Time           `json:"at"`
	StoppedByJobID  string              `json:"stopped_by_job_id,omitempty"`
	BackendRequest  *existingRequest    `json:"backend_request,omitempty"`
	BackendReceipt  *existingReceipt    `json:"backend_receipt,omitempty"`
	BackendSnapshot *existingSnapshot   `json:"backend_snapshot,omitempty"`
}
type runtimeProfile struct {
	Identity            string    `json:"identity"`
	Generation          int64     `json:"generation"`
	State               string    `json:"state"`
	Reserved            bool      `json:"reserved"`
	StartingAt          time.Time `json:"starting_at,omitempty"`
	ReservationUntil    time.Time `json:"reservation_until,omitempty"`
	BackendObservedAtMS int64     `json:"backend_observed_at_ms,omitempty"`
}
type journal struct {
	Epoch      string                    `json:"epoch"`
	PolicyHash string                    `json:"policy_hash"`
	Attempts   map[string]attempt        `json:"attempts"`
	Profiles   map[string]runtimeProfile `json:"profiles"`
}
type pendingStartPreflight struct {
	request ActionRequest
	token   uint64
}

type Coordinator struct {
	mu                   sync.Mutex
	Policy               *Policy
	supervisor           Supervisor
	journal              journal
	path                 string
	session              string
	logs                 *LogHub
	logLeases            map[string]localLogLease
	preflightBusy        map[string]bool
	fileInventory        map[string]modelFileMetadata
	fileInventoryRunning bool
	fileInventoryAt      time.Time
	preflightToken       map[string]uint64
	pendingStarts        map[string]*pendingStartPreflight
	scanWeights          func([]string) modelFileMetadata
	writeJournal         func(string, []byte, os.FileMode) error
	savedJournal         []byte
	savedJournalPath     string
	uncertainJournal     []byte
	uncertainPrior       []byte
}
type BrokerSnapshot struct {
	Inventory    protocol.SparkInventory    `json:"inventory"`
	States       []protocol.SparkModelState `json:"model_states"`
	Control      protocol.SparkControlState `json:"control_state"`
	AgentSession string                     `json:"agent_session"`
}

func OpenCoordinator(policy *Policy, s Supervisor) (*Coordinator, error) {
	if s == nil {
		s = LocalSupervisor{}
	}
	if e := os.MkdirAll(policy.StateDir, 0700); e != nil {
		return nil, e
	}
	c := &Coordinator{Policy: policy, supervisor: s, path: filepath.Join(policy.StateDir, "control-journal.json"), logs: NewLogHub(), logLeases: map[string]localLogLease{}, preflightBusy: map[string]bool{}, fileInventory: map[string]modelFileMetadata{}, preflightToken: map[string]uint64{}, pendingStarts: map[string]*pendingStartPreflight{}, scanWeights: scanModelFiles}
	b, e := readPrivateFile(c.path, 4<<20, uint32(os.Geteuid()))
	if e == nil {
		if len(b) > 4<<20 || strictDecode(b, &c.journal) != nil {
			return nil, fmt.Errorf("control journal invalid; preserve it for reconciliation")
		}
	} else if !os.IsNotExist(e) {
		return nil, e
	}
	if c.journal.Attempts == nil {
		c.journal.Attempts = map[string]attempt{}
	}
	if c.journal.Profiles == nil {
		c.journal.Profiles = map[string]runtimeProfile{}
	}
	ph := digest(policy)
	if c.journal.Epoch == "" || c.journal.PolicyHash != ph {
		c.journal.Epoch = idgen.New()
		c.journal.PolicyHash = ph
	}
	if e = c.saveLocked(); e != nil {
		return nil, e
	}
	return c, nil
}
func (c *Coordinator) saveLocked() error {
	b, e := json.Marshal(c.journal)
	if e != nil {
		return e
	}
	if len(b) > 4<<20 {
		return fmt.Errorf("control journal byte limit")
	}
	// Only a successful synced publication establishes the reusable record.
	// Verify the actual private file even when the in-memory bytes are unchanged;
	// deletion, replacement or corruption must not hide behind a cache hit.
	// Fault-injection writers always run so their publication/expiry semantics
	// remain observable. A failed publication may already have renamed the file,
	// but must be synced again rather than mistaken for a successful no-op.
	var current []byte
	if c.writeJournal == nil && c.savedJournal != nil {
		if c.path != c.savedJournalPath {
			return fmt.Errorf("control journal path changed")
		}
		var err error
		current, err = readPrivateFile(c.path, 4<<20, uint32(os.Geteuid()))
		if err != nil {
			return fmt.Errorf("control journal unavailable: %w", err)
		}
		knownUncertain := c.uncertainJournal != nil && (bytes.Equal(current, c.uncertainJournal) || c.uncertainPrior != nil && bytes.Equal(current, c.uncertainPrior))
		if !bytes.Equal(current, c.savedJournal) && !knownUncertain {
			return fmt.Errorf("control journal changed outside coordinator; preserve it for reconciliation")
		}
		if c.uncertainJournal == nil && bytes.Equal(current, b) {
			return nil
		}
	}
	if c.writeJournal != nil {
		// Remember the bounded private pre-write record for retries even when
		// an injected writer fails before replacing a prior uncertain record.
		current, _ = readPrivateFile(c.path, 4<<20, uint32(os.Geteuid()))
		e = c.writeJournal(c.path, b, 0600)
	} else {
		e = secure.AtomicWrite(c.path, b, 0600)
	}
	if e != nil {
		c.uncertainJournal = bytes.Clone(b)
		c.uncertainPrior = current
		return e
	}
	c.savedJournal = bytes.Clone(b)
	c.savedJournalPath = c.path
	c.uncertainJournal = nil
	c.uncertainPrior = nil
	return nil
}
func (c *Coordinator) Bind(agent, controller, session string) error {
	if agent != c.Policy.AgentID || controller != c.Policy.ControllerID || !protocol.SparkValidID(session) {
		return fmt.Errorf("local transport identity mismatch")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.bindExistingLocked(agent, controller, session); err != nil {
		return err
	}
	if c.session != "" && c.session != session {
		c.logs.CloseAll("agent_session_changed")
	}
	c.session = session
	return nil
}
func (c *Coordinator) profile(id string) (Profile, bool) {
	for _, p := range c.Policy.Profiles {
		if p.ID == id {
			return p, true
		}
	}
	return Profile{}, false
}
func (c *Coordinator) observeLocked(p Profile, i Instance) runtimeProfile {
	r := c.journal.Profiles[p.ID]
	if r.Identity != i.Identity {
		r.Generation++
		r.Identity = i.Identity
	}
	r.State = i.State
	if p.Target.Kind == existingCoordinatorKind {
		if i.Backend != nil {
			r.BackendObservedAtMS = i.Backend.ObservedAtMS
		}
		r.Reserved = false
		r.StartingAt = time.Time{}
		r.ReservationUntil = time.Time{}
		c.journal.Profiles[p.ID] = r
		return r
	}
	if i.State == "stopped" || i.State == "failed" {
		r.Reserved = !shutdownComplete(p, i)
		if shutdownComplete(p, i) && !r.ReservationUntil.IsZero() && time.Now().Before(r.ReservationUntil) {
			r.Reserved = true
		}
		r.StartingAt = time.Time{}
	}
	c.journal.Profiles[p.ID] = r
	return r
}
func (c *Coordinator) Snapshot(ctx context.Context) (BrokerSnapshot, error) {
	c.scheduleFileInventory()
	now := time.Now().UTC()
	out := BrokerSnapshot{Inventory: protocol.SparkInventory{Revision: digest(c.Policy.Profiles), Complete: true, ObservedAt: now, Profiles: []protocol.SparkProfile{}}, States: []protocol.SparkModelState{}, Control: protocol.SparkControlState{Enabled: false, ObservedAt: now, Reason: "local_control_disabled"}}
	c.mu.Lock()
	out.AgentSession = c.session
	enabled := c.Policy.Enabled && c.session != ""
	if enabled {
		epoch := c.journal.Epoch
		out.Control.Enabled = true
		out.Control.Epoch = &epoch
		out.Control.Reason = ""
	}
	c.mu.Unlock()
	existingRound, existingRoundErr := c.existingSnapshotRound(ctx)
	for _, p := range c.Policy.Profiles {
		public := p.Public(enabled)
		c.mu.Lock()
		metadata := c.fileInventory[p.ID]
		c.mu.Unlock()
		if len(p.Weights) > 0 && len(public.Settings) <= 125 {
			public.Settings = append(append([]protocol.SparkSetting{}, public.Settings...), metadata.settings()...)
		}
		out.Inventory.Profiles = append(out.Inventory.Profiles, public)
		var i Instance
		var e error
		if p.Target.Kind == existingCoordinatorKind && (existingRound != nil || existingRoundErr != nil) {
			i, e = existingRound[p.ID], existingRoundErr
		} else {
			probe, cancel := context.WithTimeout(ctx, 2*time.Second)
			i, e = c.supervisor.Inspect(probe, p)
			cancel()
		}
		st := protocol.SparkModelState{ProfileID: p.ID, ObservedAt: time.Now().UTC(), SupervisorState: "unknown", Readiness: "unknown", Quality: "error", Reason: "supervisor_unavailable"}
		if e == nil && p.Target.Kind == existingCoordinatorKind && i.Backend != nil {
			st.ObservedAt = time.UnixMilli(i.Backend.ObservedAtMS).UTC()
		}
		c.mu.Lock()
		if e == nil && p.Target.Kind == existingCoordinatorKind && existingRound != nil && (ctx.Err() != nil || i.Backend == nil || validateExistingSnapshot(*i.Backend, p) != nil) {
			e = fmt.Errorf("existing coordinator observation expired before consumption")
		}
		old := c.journal.Profiles[p.ID]
		if e == nil && p.Target.Kind == configuredCatalogKind {
			// A configured catalog is metadata, never a current runtime witness.
			// Keep it visible without creating a generation, refreshing readiness,
			// or writing an invented stopped/running instance into the journal.
			st.Generation = 0
			st.Quality = "unknown"
			st.Reason = "configured_catalog_runtime_unobserved"
		} else if e == nil {
			r := c.observeLocked(p, i)
			st.Generation = r.Generation
			st.SupervisorState = i.State
			st.Quality = "ok"
			st.Reason = ""
			if i.State == "failed" && shutdownComplete(p, i) {
				st.Reason = "failed_service_shutdown_verified"
			} else if member(i.State, "failed", "stopped") && !shutdownComplete(p, i) {
				st.Reason = "service_shutdown_unverified"
			}
			if pending := c.pendingStarts[p.ID]; enabled && i.State == "stopped" && pending != nil {
				request := pending.request
				session := c.session
				if request.Local {
					session = "local:" + c.journal.Epoch
				}
				attempt, prepared := c.journal.Attempts[request.JobID]
				if prepared && attempt.Stage == "preflight_pending" && request.Action == "model.start" && request.ParentJobID == "" && request.Params.ExpectedGeneration == r.Generation && request.Params.ProfileRevision == p.Revision && request.Params.ControlEpoch == c.journal.Epoch && request.Params.ExpectedAgentSession == session && pending.token == c.preflightToken[p.ID] && time.Now().Before(request.Deadline) {
					st.Reason = "start_preflight_pending"
				}
			}
		} else {
			st.Generation = old.Generation
			if p.Target.Kind == configuredCatalogKind {
				st.Generation = 0
			}
			out.Inventory.Complete = false
		}
		c.mu.Unlock()
		if p.Target.Kind != configuredCatalogKind && e == nil && (i.State == "running" || i.State == "starting") {
			if p.Target.Kind == existingCoordinatorKind && i.Backend != nil {
				st.Readiness = "unready"
				if i.Backend.APIReady && i.State == "running" {
					st.Readiness = "ready"
				}
			} else {
				st.Readiness, st.Reason = EngineReadiness(ctx, p)
			}
			if st.Readiness == "ready" {
				c.mu.Lock()
				r := c.journal.Profiles[p.ID]
				r.Reserved = false
				c.journal.Profiles[p.ID] = r
				c.mu.Unlock()
			}
		} else if p.Target.Kind != configuredCatalogKind && e == nil && shutdownComplete(p, i) {
			st.Readiness = "not_applicable"
		}
		out.States = append(out.States, st)
	}
	c.mu.Lock()
	e := c.saveLocked()
	c.mu.Unlock()
	if e != nil {
		return out, fmt.Errorf("observation journal unavailable")
	}
	return out, nil
}
func rejection(r ActionRequest, code string) protocol.JobReceipt {
	return protocol.JobReceipt{JobID: r.JobID, OperationID: r.OperationID, Status: protocol.TargetRejected, Stage: "model_rejected", Message: code, ErrorCode: code, Evidence: map[string]any{"profile_id": r.Params.ProfileID}}
}
func (c *Coordinator) validateLocked(r ActionRequest, p Profile, i Instance, checkLease bool) error {
	allowed := member(strings.TrimPrefix(r.Action, "model."), p.AllowedActions...)
	if r.Action == "model.start" && r.ParentJobID != "" {
		parent, ok := c.journal.Attempts[r.ParentJobID]
		allowed = ok && parent.Stage == "restart_stopped" && parent.Request.Action == "model.restart" && parent.Request.Params.ProfileID == p.ID && parent.Request.OperationID == r.OperationID && member("restart", p.AllowedActions...)
		if !allowed {
			return fmt.Errorf("linked_stage_not_authorized")
		}
		if parent.Receipt.VerifiedAt == nil || time.Since(*parent.Receipt.VerifiedAt) > 60*time.Second {
			return fmt.Errorf("linked_stage_expired")
		}
	}
	if !c.Policy.Enabled || p.ObservationOnly || p.Target.Kind == configuredCatalogKind || !allowed {
		return fmt.Errorf("local_action_not_approved")
	}
	session := c.session
	if r.Local {
		session = "local:" + c.journal.Epoch
	}
	if r.Params.ControlEpoch != c.journal.Epoch || r.Params.ExpectedAgentSession != session || session == "" || r.Params.ProfileRevision != p.Revision {
		return fmt.Errorf("authority_or_profile_drift")
	}
	current := c.observeLocked(p, i)
	if r.Params.ExpectedGeneration != current.Generation {
		return fmt.Errorf("generation_drift")
	}
	if r.Action == "model.start" && member(i.State, "stopped", "failed") && !shutdownComplete(p, i) {
		return fmt.Errorf("service_shutdown_unverified")
	}
	if r.Action == "model.start" && r.ParentJobID != "" && !shutdownComplete(p, i) {
		return fmt.Errorf("restart_stopped_stage_identity_unavailable")
	}
	if checkLease {
		now := time.Now()
		if r.IssuedAt.IsZero() || r.Deadline.IsZero() || !now.Before(r.Deadline) || r.Deadline.Sub(r.IssuedAt) > protocol.SparkDispatchLease || r.IssuedAt.After(now.Add(5*time.Second)) || now.Sub(r.IssuedAt) > protocol.SparkDispatchLease+5*time.Second {
			return fmt.Errorf("dispatch_expired")
		}
	}
	if r.Action != "model.start" && i.State != "stopped" && !r.Params.ConfirmDisruption {
		return fmt.Errorf("disruption_confirmation_required")
	}
	return nil
}
func (c *Coordinator) preflightLocked(ctx context.Context, p Profile, replace bool) error {
	available := uint64(0)
	b, e := os.Open("/proc/meminfo")
	if e == nil {
		m, err := ParseMeminfo(b)
		b.Close()
		if err == nil && m.AvailableBytes != nil {
			available = *m.AvailableBytes
		}
	}
	if p.RequiredMemoryBytes == 0 && p.BlockUnknownMemory {
		return fmt.Errorf("memory_estimate_unknown")
	}
	need := p.RequiredMemoryBytes + p.OSHeadroomBytes
	if need < p.RequiredMemoryBytes {
		return fmt.Errorf("memory_estimate_invalid")
	}
	if !replace && need > 0 && (available == 0 || available < need) {
		return fmt.Errorf("insufficient_memory")
	}
	// Replacement memory is deliberately unknown: the existing instance's RSS
	// is not a guarantee of reclaimable UMA capacity. Recheck after verified stop.
	for _, other := range c.Policy.Profiles {
		if other.ID == p.ID {
			continue
		}
		r := c.journal.Profiles[other.ID]
		conflict := false
		for _, g := range p.ExclusionGroups {
			if member(g, other.ExclusionGroups...) {
				conflict = true
			}
		}
		if conflict {
			probe, end := context.WithTimeout(ctx, 2*time.Second)
			observed, e := c.supervisor.Inspect(probe, other)
			end()
			if e != nil {
				return fmt.Errorf("exclusion_state_unknown")
			}
			r = c.observeLocked(other, observed)
		}
		if conflict && (r.Reserved || member(r.State, "running", "starting", "stopping")) {
			return fmt.Errorf("exclusion_group_busy")
		}
		if r.Reserved && other.RequiredMemoryBytes > 0 && !replace {
			if other.RequiredMemoryBytes > available || need > available-other.RequiredMemoryBytes {
				return fmt.Errorf("memory_reserved")
			}
			available -= other.RequiredMemoryBytes
		}
	}
	if p.Port != 0 && !replace {
		listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p.Port))
		if err != nil {
			return fmt.Errorf("launch_port_busy")
		}
		listener.Close()
	}
	return nil
}

// Execute durably marks an attempt before the one fixed supervisor call. The
// effect mutex is released before stop observation; stop remains available
// while readiness is unknown or a profile is starting.
func (c *Coordinator) Execute(ctx context.Context, r ActionRequest) protocol.JobReceipt {
	if !protocol.SparkValidID(r.JobID) || !protocol.SparkValidID(r.OperationID) || !member(r.Action, "model.start", "model.stop", "model.restart") {
		return rejection(r, "invalid_action_request")
	}
	requestDigest := digest(r)
	c.mu.Lock()
	p, ok := c.profile(r.Params.ProfileID)
	if !ok {
		c.mu.Unlock()
		return rejection(r, "unknown_profile")
	}
	if p.Target.Kind == existingCoordinatorKind {
		c.mu.Unlock()
		return c.executeExisting(ctx, r, p)
	}
	if prior, exists := c.journal.Attempts[r.JobID]; exists {
		if prior.Digest != requestDigest {
			c.mu.Unlock()
			return rejection(r, "idempotency_conflict")
		}
		// Completed duplicates do not inherit new lease/generation checks, but
		// still require the current profile/session/authority before disclosure.
		if !c.Policy.Enabled || r.Params.ControlEpoch != c.journal.Epoch || !r.Local && c.session == "" || r.Params.ProfileRevision != p.Revision {
			c.mu.Unlock()
			return rejection(r, "authority_changed")
		}
		if prior.Stage == "done" || prior.Stage == "restart_stopped" || prior.Stage == "start_attempted" {
			c.mu.Unlock()
			return prior.Receipt
		}
		if prior.Stage == "preflight_pending" {
			// Preflight never grants permission to replay an effect after a
			// broker restart. Preserve the accepted record while its original
			// invocation runs; after interruption, finish it without launching.
			if pending := c.pendingStarts[p.ID]; pending == nil || pending.request.JobID != r.JobID {
				prior.Stage = "done"
				prior.Receipt = rejection(r, "start_preflight_interrupted")
				c.journal.Attempts[r.JobID] = prior
				if c.saveLocked() != nil {
					c.mu.Unlock()
					return rejection(r, "journal_unavailable")
				}
			}
			c.mu.Unlock()
			return prior.Receipt
		}
		probe, cancel := context.WithTimeout(ctx, 2*time.Second)
		i, e := c.supervisor.Inspect(probe, p)
		cancel()
		if e == nil {
			prior.Receipt = c.reconcileLocked(&prior, p, i)
			c.journal.Attempts[r.JobID] = prior
			_ = c.saveLocked()
		}
		c.mu.Unlock()
		if e != nil {
			return rejection(r, "attempt_unresolved")
		}
		return prior.Receipt
	}
	// Keep unresolved attempts forever; only expired completed dedup records
	// may be pruned. IDs are never reused while a dispatch can still be valid.
	if len(c.journal.Attempts) >= 1024 {
		cut := time.Now().Add(-24 * time.Hour)
		for k, a := range c.journal.Attempts {
			if a.Stage == "done" && a.At.Before(cut) {
				delete(c.journal.Attempts, k)
			}
		}
	}
	if len(c.journal.Attempts) >= 2048 {
		c.mu.Unlock()
		return rejection(r, "journal_capacity")
	}
	probe, cancel := context.WithTimeout(ctx, 2*time.Second)
	i, e := c.supervisor.Inspect(probe, p)
	cancel()
	if e != nil {
		c.mu.Unlock()
		return rejection(r, "supervisor_identity_unavailable")
	}
	if e = c.validateLocked(r, p, i, true); e != nil {
		c.mu.Unlock()
		return rejection(r, e.Error())
	}
	probe, cancel = context.WithTimeout(ctx, 2*time.Second)
	definition, e := c.supervisor.Definition(probe, p)
	cancel()
	if e != nil || definition != p.Target.DefinitionHash {
		c.mu.Unlock()
		return rejection(r, "effective_definition_drift")
	}
	if r.Action == "model.restart" && !member(i.State, "running") {
		c.mu.Unlock()
		return rejection(r, "not_running_use_start")
	}
	if r.Action == "model.stop" {
		c.preflightToken[p.ID]++
		delete(c.pendingStarts, p.ID)
		for id, pending := range c.journal.Attempts {
			if pending.Request.Action == "model.start" && pending.Request.Params.ProfileID == p.ID && pending.Stage == "preflight_pending" {
				pending.Stage = "done"
				pending.Receipt = rejection(pending.Request, "start_cancelled_by_stop")
				c.journal.Attempts[id] = pending
			}
		}
		for id, parent := range c.journal.Attempts {
			if parent.Request.Params.ProfileID == p.ID && parent.Request.Action == "model.restart" && member(parent.Stage, "restart_stopped", "start_attempted") {
				parent.Stage = "done"
				parent.Receipt.Status = protocol.TargetFailed
				parent.Receipt.Stage = "restart_cancelled_after_stop"
				parent.Receipt.ErrorCode = "superseded_by_stop"
				parent.Receipt.Message = "restart superseded by explicit stop; stop outcome is reported separately"
				c.journal.Attempts[id] = parent
				for stageID, stage := range c.journal.Attempts {
					if stage.Request.ParentJobID == id && stage.Stage != "done" {
						stage.Stage = "done"
						stage.Receipt = rejection(stage.Request, "start_cancelled_by_stop")
						c.journal.Attempts[stageID] = stage
					}
				}
			}
		}
		rp := c.journal.Profiles[p.ID]
		rp.ReservationUntil = time.Time{}
		c.journal.Profiles[p.ID] = rp
	}
	if r.Action == "model.start" && i.State == "running" || r.Action == "model.stop" && shutdownComplete(p, i) {
		stage := "model_started"
		if r.Action == "model.stop" {
			stage = "model_stopped"
		}
		rec := c.evidenceReceipt(r, p, i, protocol.TargetSucceeded, stage)
		c.journal.Attempts[r.JobID] = attempt{Request: r, Digest: requestDigest, Stage: "done", Before: i, Receipt: rec, At: time.Now()}
		c.finishStoppedStartsLocked(c.journal.Attempts[r.JobID])
		e = c.saveLocked()
		c.mu.Unlock()
		if e != nil {
			return rejection(r, "journal_unavailable")
		}
		return rec
	}
	if r.Action == "model.start" && i.State == "starting" {
		rec := c.evidenceReceipt(r, p, i, protocol.TargetRunning, "model_starting")
		c.journal.Attempts[r.JobID] = attempt{Request: r, Digest: requestDigest, Stage: "effect_attempted", Before: i, Receipt: rec, At: time.Now()}
		e = c.saveLocked()
		c.mu.Unlock()
		if e != nil {
			return rejection(r, "journal_unavailable")
		}
		return rec
	}
	if r.Action != "model.stop" {
		token := c.preflightToken[p.ID]
		var pending *pendingStartPreflight
		if r.Action == "model.start" {
			pending = &pendingStartPreflight{request: r, token: token}
			if c.pendingStarts[p.ID] == nil {
				c.pendingStarts[p.ID] = pending
			}
			c.journal.Attempts[r.JobID] = attempt{Request: r, Digest: requestDigest, Stage: "preflight_pending", Before: i, At: time.Now(), Receipt: c.evidenceReceipt(r, p, i, protocol.TargetAccepted, "start_preflight_pending")}
			if c.saveLocked() != nil {
				delete(c.journal.Attempts, r.JobID)
				if c.pendingStarts[p.ID] == pending {
					delete(c.pendingStarts, p.ID)
				}
				c.mu.Unlock()
				return rejection(r, "journal_unavailable")
			}
		}
		c.mu.Unlock()
		e = c.weightsPreflight(ctx, p)
		c.mu.Lock()
		if pending != nil && c.pendingStarts[p.ID] == pending {
			delete(c.pendingStarts, p.ID)
		}
		if existing, exists := c.journal.Attempts[r.JobID]; exists && existing.Stage != "preflight_pending" {
			c.mu.Unlock()
			return c.Execute(ctx, r)
		}
		if c.preflightToken[p.ID] != token {
			receipt := rejection(r, "start_cancelled_by_stop")
			c.journal.Attempts[r.JobID] = attempt{Request: r, Digest: requestDigest, Stage: "done", Before: i, Receipt: receipt, At: time.Now()}
			if c.saveLocked() != nil {
				c.mu.Unlock()
				return rejection(r, "journal_unavailable")
			}
			c.mu.Unlock()
			return receipt
		}
		if e != nil {
			c.rejectPreflightLocked(r, e.Error())
			c.mu.Unlock()
			return rejection(r, e.Error())
		}
		probe, cancel := context.WithTimeout(ctx, 2*time.Second)
		i, e = c.supervisor.Inspect(probe, p)
		cancel()
		if e != nil {
			c.rejectPreflightLocked(r, "supervisor_identity_unavailable")
			c.mu.Unlock()
			return rejection(r, "supervisor_identity_unavailable")
		}
		if e = c.validateLocked(r, p, i, true); e != nil {
			c.rejectPreflightLocked(r, e.Error())
			c.mu.Unlock()
			return rejection(r, e.Error())
		}
		if e = c.preflightLocked(ctx, p, r.Action == "model.restart"); e != nil {
			c.rejectPreflightLocked(r, e.Error())
			c.mu.Unlock()
			return rejection(r, e.Error())
		}
	}
	var releaseShared func()
	if p.Target.SharedLock != "" {
		releaseShared, e = acquireSharedLock(p.Target.SharedLock)
		if e != nil {
			c.mu.Unlock()
			return rejection(r, "shared_entrypoint_busy")
		}
		defer releaseShared()
	}
	if r.ParentJobID != "" {
		parent, yes := c.journal.Attempts[r.ParentJobID]
		if !yes || parent.Stage != "restart_stopped" || parent.Request.OperationID != r.OperationID || parent.Request.Params.ProfileID != p.ID {
			c.mu.Unlock()
			return rejection(r, "linked_stage_not_authorized")
		}
	}
	// Unlocked metadata I/O and the shared-lock acquisition can span an
	// out-of-band unit/definition replacement. Freeze the effective definition
	// again at the final admission boundary, before recording any effect.
	probe, cancel = context.WithTimeout(ctx, 2*time.Second)
	definition, e = c.supervisor.Definition(probe, p)
	cancel()
	if e != nil || definition != p.Target.DefinitionHash {
		c.rejectPreflightLocked(r, "effective_definition_drift")
		c.mu.Unlock()
		return rejection(r, "effective_definition_drift")
	}
	if !time.Now().Before(r.Deadline) {
		c.rejectPreflightLocked(r, "dispatch_expired")
		c.mu.Unlock()
		return rejection(r, "dispatch_expired")
	}
	a := attempt{Request: r, Digest: requestDigest, Stage: "effect_attempted", Before: i, At: time.Now(), Receipt: c.evidenceReceipt(r, p, i, protocol.TargetUnknownResult, "effect_attempted")}
	c.journal.Attempts[r.JobID] = a
	// Cancellation binding and the admitted stop attempt share one publication,
	// after every shared-lock/definition/lease check. A rejected stop has no
	// authority to prevent a later separately admitted exact stop from resolving
	// this start. Preserve any older uncertain stop's own journal/outcome.
	previousStartLinks := map[string]string{}
	if r.Action == "model.stop" {
		for id, pending := range c.journal.Attempts {
			if pending.Request.Action == "model.start" && pending.Request.ParentJobID == "" && pending.Stage == "effect_attempted" && sameStartStopAuthority(pending.Request, r) && exactStartStopInstance(pending, r, i) {
				previousStartLinks[id] = pending.StoppedByJobID
				pending.StoppedByJobID = r.JobID
				c.journal.Attempts[id] = pending
			}
		}
	}
	if r.ParentJobID != "" {
		parent := c.journal.Attempts[r.ParentJobID]
		parent.Stage = "start_attempted"
		parent.Receipt.Status = protocol.TargetRunning
		parent.Receipt.Stage = "restart_start_attempted"
		c.journal.Attempts[r.ParentJobID] = parent
	}
	if r.Action != "model.stop" {
		rp := c.journal.Profiles[p.ID]
		rp.Reserved = true
		rp.StartingAt = time.Now()
		c.journal.Profiles[p.ID] = rp
	}
	if e = c.saveLocked(); e != nil {
		delete(c.journal.Attempts, r.JobID)
		c.restoreStartLinksLocked(r.JobID, previousStartLinks)
		c.mu.Unlock()
		return rejection(r, "journal_unavailable")
	}
	action := strings.TrimPrefix(r.Action, "model.")
	if action == "restart" {
		action = "stop"
	}
	// Lease is checked again immediately before effect, after disk/definition
	// work. A monotonic timer prevents a wall-clock adjustment extending this call.
	remaining := time.Until(r.Deadline)
	if remaining <= 0 {
		a.Stage = "done"
		a.Receipt = rejection(r, "dispatch_expired")
		c.journal.Attempts[r.JobID] = a
		c.restoreStartLinksLocked(r.JobID, previousStartLinks)
		if c.saveLocked() != nil {
			c.mu.Unlock()
			return rejection(r, "journal_unavailable")
		}
		c.mu.Unlock()
		return a.Receipt
	}
	effectCtx, end := context.WithTimeout(ctx, min(remaining, 5*time.Second))
	after, e := c.supervisor.Effect(effectCtx, p, action)
	end()
	if releaseShared != nil {
		releaseShared()
	}
	if e != nil {
		a.Receipt = c.evidenceReceipt(r, p, i, protocol.TargetUnknownResult, "supervisor_effect_unconfirmed")
		c.journal.Attempts[r.JobID] = a
		_ = c.saveLocked()
		c.mu.Unlock()
		return a.Receipt
	}
	c.observeLocked(p, after)
	a.Receipt = c.evidenceReceipt(r, p, after, protocol.TargetRunning, "supervisor_call_accepted")
	c.journal.Attempts[r.JobID] = a
	c.finishParentLocked(a)
	e = c.saveLocked()
	c.mu.Unlock()
	if e != nil {
		return rejection(r, "journal_post_effect_unavailable")
	}
	if action == "stop" {
		seconds := p.StopSeconds
		if seconds == 0 {
			seconds = 60
		}
		observeCtx, end := context.WithTimeout(ctx, time.Duration(seconds+5)*time.Second)
		defer end()
		t := time.NewTicker(200 * time.Millisecond)
		defer t.Stop()
		for {
			probe, cancel := context.WithTimeout(observeCtx, 2*time.Second)
			after, e = c.supervisor.Inspect(probe, p)
			cancel()
			if e == nil && shutdownComplete(p, after) {
				break
			}
			select {
			case <-observeCtx.Done():
				c.mu.Lock()
				a = c.journal.Attempts[r.JobID]
				a.Receipt = c.evidenceReceipt(r, p, after, protocol.TargetUnknownResult, "stop_observation_timeout")
				c.journal.Attempts[r.JobID] = a
				_ = c.saveLocked()
				c.mu.Unlock()
				return a.Receipt
			case <-t.C:
			}
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	a = c.journal.Attempts[r.JobID]
	if a.Stage == "done" && member(a.Receipt.ErrorCode, "start_cancelled_by_stop", "superseded_by_stop") {
		return a.Receipt
	}
	if a.StoppedByJobID != "" {
		return a.Receipt
	}
	c.observeLocked(p, after)
	stage := "model_started"
	if action == "stop" {
		stage = "model_stopped"
	}
	a.Receipt = c.evidenceReceipt(r, p, after, protocol.TargetSucceeded, stage)
	a.Stage = "done"
	if action == "start" && after.State != "running" {
		a.Stage = "effect_attempted"
		a.Receipt.Status = protocol.TargetRunning
		a.Receipt.Stage = "model_starting"
		a.Receipt.Message = "supervisor accepted; running instance not yet observed"
	}
	if r.Action == "model.restart" {
		a.Stage = "restart_stopped"
		a.Receipt.Status = protocol.TargetAwaitingConfirmation
		a.Receipt.Stage = "restart_stopped"
		a.Receipt.Message = "stopped; fresh linked start authorization required"
		rp := c.journal.Profiles[p.ID]
		rp.Reserved = true
		rp.ReservationUntil = time.Now().Add(60 * time.Second)
		c.journal.Profiles[p.ID] = rp
	}
	c.journal.Attempts[r.JobID] = a
	c.finishStoppedStartsLocked(a)
	c.finishParentLocked(a)
	if e = c.saveLocked(); e != nil {
		return rejection(r, "journal_post_effect_unavailable")
	}
	return a.Receipt
}

func (c *Coordinator) rejectPreflightLocked(r ActionRequest, code string) {
	if pending, ok := c.journal.Attempts[r.JobID]; ok && pending.Stage == "preflight_pending" {
		pending.Stage = "done"
		pending.Receipt = rejection(r, code)
		c.journal.Attempts[r.JobID] = pending
		_ = c.saveLocked()
	}
}

func (c *Coordinator) weightsPreflight(ctx context.Context, p Profile) error {
	if len(p.Weights) == 0 {
		return nil
	}
	c.mu.Lock()
	if c.preflightBusy[p.ID] {
		c.mu.Unlock()
		return fmt.Errorf("weights_preflight_pending")
	}
	c.preflightBusy[p.ID] = true
	c.mu.Unlock()
	answer := make(chan modelFileMetadata, 1)
	go func() {
		metadata := c.scanWeights(p.Weights)
		c.mu.Lock()
		delete(c.preflightBusy, p.ID)
		c.mu.Unlock()
		answer <- metadata
	}()
	probe, end := context.WithTimeout(ctx, 2*time.Second)
	defer end()
	select {
	case <-probe.Done():
		return fmt.Errorf("weights_preflight_pending")
	case metadata := <-answer:
		if !metadata.Complete || !metadata.Available {
			return fmt.Errorf("weights_unavailable")
		}
		return nil
	}
}
func (c *Coordinator) scheduleFileInventory() {
	c.mu.Lock()
	if c.fileInventoryRunning || !c.fileInventoryAt.IsZero() && time.Since(c.fileInventoryAt) < 60*time.Second {
		c.mu.Unlock()
		return
	}
	c.fileInventoryRunning = true
	profiles := append([]Profile{}, c.Policy.Profiles...)
	for i := range profiles {
		profiles[i].Weights = append([]string{}, profiles[i].Weights...)
	}
	c.mu.Unlock()
	go func() {
		for _, p := range profiles {
			if len(p.Weights) == 0 {
				continue
			}
			metadata := scanModelFiles(p.Weights)
			c.mu.Lock()
			c.fileInventory[p.ID] = metadata
			c.mu.Unlock()
		}
		c.mu.Lock()
		c.fileInventoryAt = time.Now()
		c.fileInventoryRunning = false
		c.mu.Unlock()
	}()
}
func (c *Coordinator) evidenceReceipt(r ActionRequest, p Profile, i Instance, status protocol.TargetStatus, stage string) protocol.JobReceipt {
	now := time.Now().UTC()
	rp := c.journal.Profiles[p.ID]
	session := c.session
	if r.Local {
		session = r.Params.ExpectedAgentSession
	}
	receipt := protocol.JobReceipt{JobID: r.JobID, OperationID: r.OperationID, Status: status, Stage: stage, Message: stage, AppliedAt: &now, VerifiedAt: &now, Evidence: map[string]any{"profile_id": p.ID, "profile_revision": p.Revision, "generation": rp.Generation, "control_epoch": c.journal.Epoch, "agent_session": session, "supervisor_state": i.State, "readiness": "unknown", "instance_identity": i.Identity, "observed_at": now}}
	if shutdownComplete(p, i) {
		receipt.Evidence["shutdown_verified"] = true
		receipt.Evidence["readiness"] = "not_applicable"
		if i.State == "failed" {
			receipt.Evidence["reason"] = "failed_service_shutdown_verified"
		}
	}
	return receipt
}

func shutdownComplete(p Profile, i Instance) bool {
	if !i.Available || i.Identity == "" || !member(i.State, "stopped", "failed") || i.PID != 0 {
		return false
	}
	if member(p.Target.Kind, "systemd-system", "systemd-user") {
		return i.ShutdownVerified && (i.State != "failed" || i.Identity != "stopped")
	}
	if p.Target.Kind == existingCoordinatorKind {
		return i.ShutdownVerified
	}
	return p.Target.Kind == "docker" && i.State == "stopped"
}

// Called only by the broker's verified UID-0 request path; the monitoring peer
// cannot opt into this separate local authority. All effects share the journal
// and coordinator, including when monitoring is stopped or uninstalled.
func (c *Coordinator) ExecuteLocal(ctx context.Context, id, profileID, action string, confirm bool) protocol.JobReceipt {
	c.mu.Lock()
	p, ok := c.profile(profileID)
	if !ok {
		c.mu.Unlock()
		return rejection(ActionRequest{JobID: id, OperationID: id}, "unknown_profile")
	}
	prior, exists := c.journal.Attempts[id]
	var request ActionRequest
	if exists {
		if !prior.Request.Local || prior.Request.Params.ProfileID != profileID || prior.Request.Action != "model."+action {
			c.mu.Unlock()
			return rejection(ActionRequest{JobID: id, OperationID: id}, "idempotency_conflict")
		}
		request = prior.Request
	} else {
		probe, end := context.WithTimeout(ctx, 2*time.Second)
		instance, e := c.supervisor.Inspect(probe, p)
		end()
		if e != nil {
			c.mu.Unlock()
			return rejection(ActionRequest{JobID: id, OperationID: id}, "supervisor_identity_unavailable")
		}
		state := c.observeLocked(p, instance)
		now := time.Now()
		request = ActionRequest{JobID: id, OperationID: id, Action: "model." + action, Local: true, Params: protocol.ModelActionParams{ProfileID: p.ID, ProfileRevision: p.Revision, ExpectedGeneration: state.Generation, ControlEpoch: c.journal.Epoch, ExpectedAgentSession: "local:" + c.journal.Epoch, ConfirmDisruption: confirm}, IssuedAt: now, Deadline: now.Add(30 * time.Second)}
	}
	if exists && prior.Stage == "start_attempted" {
		for _, stage := range c.journal.Attempts {
			if stage.Request.ParentJobID == id {
				request = stage.Request
				break
			}
		}
	}
	c.mu.Unlock()
	receipt := c.Execute(ctx, request)
	if request.ParentJobID != "" {
		c.mu.Lock()
		receipt = c.journal.Attempts[id].Receipt
		c.mu.Unlock()
		return receipt
	}
	if action != "restart" || receipt.Stage != "restart_stopped" {
		return receipt
	}
	c.mu.Lock()
	probe, end := context.WithTimeout(ctx, 2*time.Second)
	instance, e := c.supervisor.Inspect(probe, p)
	end()
	if e != nil || !shutdownComplete(p, instance) {
		c.mu.Unlock()
		return rejection(request, "restart_stopped_stage_identity_unavailable")
	}
	state := c.observeLocked(p, instance)
	now := time.Now()
	stageID := "stage-" + strings.TrimPrefix(digest([]string{id, "local-start"}), "sha256:")[:48]
	stage := ActionRequest{JobID: stageID, OperationID: id, Action: "model.start", Local: true, ParentJobID: id, Params: request.Params, IssuedAt: now, Deadline: now.Add(30 * time.Second)}
	stage.Params.ExpectedGeneration = state.Generation
	c.mu.Unlock()
	result := c.Execute(ctx, stage)
	if result.Status == protocol.TargetSucceeded {
		c.mu.Lock()
		result = c.journal.Attempts[id].Receipt
		c.mu.Unlock()
	}
	return result
}
func (c *Coordinator) PreviousAction(ctx context.Context, id, profileID, action string) (*protocol.JobReceipt, error) {
	c.mu.Lock()
	a, exists := c.journal.Attempts[id]
	if !exists {
		c.mu.Unlock()
		return nil, nil
	}
	p, ok := c.profile(profileID)
	if !ok || !c.Policy.Enabled || c.session == "" || a.Request.Params.ProfileID != profileID || a.Request.Action != "model."+action || a.Request.Params.ProfileRevision != p.Revision || a.Request.Params.ControlEpoch != c.journal.Epoch {
		c.mu.Unlock()
		return nil, fmt.Errorf("prior_action_binding_conflict")
	}
	request := a.Request
	if a.Stage == "start_attempted" {
		for _, stage := range c.journal.Attempts {
			if stage.Request.ParentJobID == id {
				request = stage.Request
				break
			}
		}
	}
	c.mu.Unlock()
	receipt := c.Execute(ctx, request)
	if request.ParentJobID != "" {
		c.mu.Lock()
		receipt = c.journal.Attempts[id].Receipt
		c.mu.Unlock()
	}
	return &receipt, nil
}
func (c *Coordinator) reconcileLocked(a *attempt, p Profile, i Instance) protocol.JobReceipt {
	c.observeLocked(p, i)
	action := a.Request.Action
	if a.StoppedByJobID != "" {
		stop, ok := c.journal.Attempts[a.StoppedByJobID]
		if ok {
			c.finishStoppedStartsLocked(stop)
			if closed := c.journal.Attempts[a.Request.JobID]; closed.Stage == "done" {
				*a = closed
				return a.Receipt
			}
		}
		return c.evidenceReceipt(a.Request, p, i, protocol.TargetUnknownResult, "stop_outcome_unconfirmed")
	}
	if action == "model.start" && i.State == "running" {
		a.Stage = "done"
		a.Receipt = c.evidenceReceipt(a.Request, p, i, protocol.TargetSucceeded, "model_started")
		c.finishParentLocked(*a)
		return a.Receipt
	}
	if member(action, "model.stop", "model.restart") && shutdownComplete(p, i) {
		a.Stage = "done"
		rec := c.evidenceReceipt(a.Request, p, i, protocol.TargetSucceeded, "model_stopped")
		if action == "model.restart" {
			a.Stage = "restart_stopped"
			rec.Status = protocol.TargetAwaitingConfirmation
			rec.Stage = "restart_stopped"
			rp := c.journal.Profiles[p.ID]
			rp.Reserved = true
			rp.ReservationUntil = time.Now().Add(60 * time.Second)
			c.journal.Profiles[p.ID] = rp
		}
		a.Receipt = rec
		c.finishStoppedStartsLocked(*a)
		return rec
	}
	return c.evidenceReceipt(a.Request, p, i, protocol.TargetUnknownResult, "attempt_unresolved")
}

func sameStartStopAuthority(start, stop ActionRequest) bool {
	a, b := start.Params, stop.Params
	return start.Local == stop.Local && a.ProfileID == b.ProfileID && a.ProfileRevision == b.ProfileRevision && a.ControlEpoch == b.ControlEpoch && a.ExpectedAgentSession == b.ExpectedAgentSession
}

func (c *Coordinator) restoreStartLinksLocked(stopID string, previous map[string]string) {
	for id, priorStopID := range previous {
		original := c.journal.Attempts[id]
		if original.StoppedByJobID == stopID {
			original.StoppedByJobID = priorStopID
			c.journal.Attempts[id] = original
		}
	}
}

func exactStartStopInstance(start attempt, stop ActionRequest, current Instance) bool {
	if !member(current.State, "starting", "running") || current.Identity == "" || start.Receipt.Evidence["instance_identity"] != current.Identity || fmt.Sprint(start.Receipt.Evidence["generation"]) != fmt.Sprint(stop.Params.ExpectedGeneration) {
		return false
	}
	if start.Before.State == "starting" {
		return start.Before.Identity == current.Identity && start.Request.Params.ExpectedGeneration == stop.Params.ExpectedGeneration
	}
	return member(start.Before.State, "stopped", "failed") && start.Before.Identity != current.Identity && stop.Params.ExpectedGeneration > 0 && start.Request.Params.ExpectedGeneration == stop.Params.ExpectedGeneration-1
}

func (c *Coordinator) finishStoppedStartsLocked(stop attempt) {
	state := stop.Receipt.Evidence["supervisor_state"]
	identity, identified := stop.Receipt.Evidence["instance_identity"].(string)
	failedProof := state == "failed" && stop.Receipt.Evidence["reason"] == "failed_service_shutdown_verified" && identified && len(identity) == 71 && strings.HasPrefix(identity, "sha256:") && strings.Trim(identity[7:], "0123456789abcdef") == ""
	if stop.Request.Action != "model.stop" || stop.Stage != "done" || stop.Receipt.Status != protocol.TargetSucceeded || stop.Receipt.Stage != "model_stopped" || stop.Receipt.Evidence["shutdown_verified"] != true || stop.Receipt.Evidence["readiness"] != "not_applicable" || (state != "stopped" && !failedProof) {
		return
	}
	for id, start := range c.journal.Attempts {
		if start.StoppedByJobID != stop.Request.JobID || start.Stage == "done" || !sameStartStopAuthority(start.Request, stop.Request) {
			continue
		}
		start.Stage = "done"
		start.Receipt = stop.Receipt
		start.Receipt.JobID, start.Receipt.OperationID = start.Request.JobID, start.Request.OperationID
		start.Receipt.Status = protocol.TargetFailed
		start.Receipt.Stage = "start_cancelled_after_stop"
		start.Receipt.ErrorCode = "superseded_by_stop"
		start.Receipt.Message = "start cancelled after exact safety stop verified shutdown by operation " + stop.Request.OperationID
		start.Receipt.Evidence = make(map[string]any, len(stop.Receipt.Evidence)+1)
		for key, value := range stop.Receipt.Evidence {
			start.Receipt.Evidence[key] = value
		}
		c.journal.Attempts[id] = start
	}
}
func (c *Coordinator) finishParentLocked(stage attempt) {
	if stage.Request.ParentJobID == "" || stage.Stage != "done" {
		return
	}
	parent, ok := c.journal.Attempts[stage.Request.ParentJobID]
	if !ok || parent.Stage != "start_attempted" || parent.Request.OperationID != stage.Request.OperationID || parent.Request.Params.ProfileID != stage.Request.Params.ProfileID {
		return
	}
	parent.Stage = "done"
	parent.Receipt = stage.Receipt
	parent.Receipt.JobID = parent.Request.JobID
	if stage.Receipt.Status == protocol.TargetSucceeded {
		parent.Receipt.Stage = "model_restarted"
		parent.Receipt.Message = "model_restarted"
		parent.Receipt.Evidence["stage_id"] = stage.Request.JobID
	}
	c.journal.Attempts[parent.Request.JobID] = parent
}
