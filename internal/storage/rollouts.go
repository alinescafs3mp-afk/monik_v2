package storage

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
)

// RolloutOptions controls controller-side delivery, never agent permissions.
type RolloutOptions struct {
	BatchSize      int `json:"batch_size"`
	ObserveSeconds int `json:"observe_seconds"`
}

func (o RolloutOptions) Valid() bool {
	return o.BatchSize >= 1 && o.BatchSize <= 10 && o.ObserveSeconds >= 15 && o.ObserveSeconds <= 300
}

type RolloutMember struct {
	AgentID    string                `json:"agent_id"`
	Platform   string                `json:"platform"`
	Wave       int                   `json:"wave"`
	ReleasedAt string                `json:"released_at,omitempty"`
	Status     protocol.TargetStatus `json:"status"`
	Stage      string                `json:"stage"`
	Message    string                `json:"message"`
	// Internal evidence is not returned by the rollout UI endpoint.
	jobID, jobStatus, envelope, receipt string
	liveAt, digest, session             string
	revoked, archived, managed          bool
	capabilities, currentPlatform       string
}
type Rollout struct {
	OperationID string `json:"operation_id"`
	ReleaseID   string `json:"release_id"`
	State       string `json:"state"`
	Revision    int64  `json:"revision"`
	RolloutOptions
	Wave        int             `json:"wave"`
	CanaryWaves int             `json:"canary_waves"`
	StableSince string          `json:"stable_since,omitempty"`
	LastTick    string          `json:"-"`
	Reason      string          `json:"reason"`
	Deadline    string          `json:"deadline"`
	CreatedAt   string          `json:"created_at"`
	UpdatedAt   string          `json:"updated_at"`
	Members     []RolloutMember `json:"members"`
}

func readRollout(q sqlExecutor, id string) (*Rollout, error) {
	r := &Rollout{Members: []RolloutMember{}}
	e := q.QueryRow(`SELECT operation_id,release_id,state,revision,batch_size,observe_seconds,wave,canary_waves,stable_since,last_tick,reason,deadline,created_at,updated_at FROM update_rollouts WHERE operation_id=?`, id).Scan(&r.OperationID, &r.ReleaseID, &r.State, &r.Revision, &r.BatchSize, &r.ObserveSeconds, &r.Wave, &r.CanaryWaves, &r.StableSince, &r.LastTick, &r.Reason, &r.Deadline, &r.CreatedAt, &r.UpdatedAt)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if e != nil {
		return nil, e
	}
	if !r.RolloutOptions.Valid() {
		return nil, fmt.Errorf("invalid persisted rollout policy")
	}
	rows, e := q.Query(`SELECT m.agent_id,m.platform,m.wave,COALESCE(m.released_at,''),t.status,t.stage,t.message,j.job_id,j.status,j.envelope,COALESCE(j.result,''),COALESCE(a.last_live_at,''),COALESCE(a.worker_digest,''),COALESCE(a.session_id,''),COALESCE(a.revoked,1),COALESCE(a.archived,1),COALESCE(a.managed_ready,0),COALESCE(a.capabilities,'{}'),COALESCE(a.os,'')||'/'||COALESCE(a.arch,'')
 FROM update_rollout_members m JOIN operation_targets t ON t.operation_id=m.operation_id AND t.agent_id=m.agent_id JOIN agent_jobs j ON j.job_id=t.job_id AND j.operation_id=m.operation_id AND j.agent_id=m.agent_id LEFT JOIN agents a ON a.id=m.agent_id WHERE m.operation_id=? ORDER BY m.wave,m.platform,m.agent_id`, id)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	for rows.Next() {
		var m RolloutMember
		if e = rows.Scan(&m.AgentID, &m.Platform, &m.Wave, &m.ReleasedAt, &m.Status, &m.Stage, &m.Message, &m.jobID, &m.jobStatus, &m.envelope, &m.receipt, &m.liveAt, &m.digest, &m.session, &m.revoked, &m.archived, &m.managed, &m.capabilities, &m.currentPlatform); e != nil {
			return nil, e
		}
		r.Members = append(r.Members, m)
	}
	if e := rows.Err(); e != nil {
		return nil, e
	}
	if e := rows.Close(); e != nil {
		return nil, e
	}
	var members, targets int
	if e := q.QueryRow(`SELECT COUNT(*) FROM update_rollout_members WHERE operation_id=?`, id).Scan(&members); e != nil {
		return nil, e
	}
	if e := q.QueryRow(`SELECT COUNT(*) FROM operation_targets WHERE operation_id=?`, id).Scan(&targets); e != nil {
		return nil, e
	}
	if members == 0 || len(r.Members) != members || members != targets {
		return nil, fmt.Errorf("corrupt rollout membership: frozen targets/jobs are missing or mismatched")
	}
	return r, nil
}
func (s *Store) Rollout(id string) (result *Rollout, err error) {
	// Revision, state and target rows must describe one committed snapshot.
	err = s.WithTx(func(tx *sql.Tx) error {
		var e error
		result, e = readRollout(tx, id)
		return e
	})
	return result, err
}
func (s *Store) Rollouts(limit int) ([]*Rollout, error) {
	if limit < 1 || limit > 50 {
		limit = 30
	}
	rows, e := s.db().Query(`SELECT operation_id FROM update_rollouts ORDER BY created_at DESC,operation_id DESC LIMIT ?`, limit)
	if e != nil {
		return nil, e
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return nil, e
		}
		ids = append(ids, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	out := []*Rollout{}
	for _, id := range ids {
		r, e := s.Rollout(id)
		if e != nil {
			return nil, e
		}
		out = append(out, r)
	}
	return out, nil
}

// A separate canary wave for EACH OS/architecture precedes bounded regular waves.
// Stable IDs, not live UI sorting or a changing group, determine the frozen order.
func assignWaves(members []RolloutMember, batch int) ([]RolloutMember, int) {
	sort.Slice(members, func(i, j int) bool {
		if members[i].Platform != members[j].Platform {
			return members[i].Platform < members[j].Platform
		}
		return members[i].AgentID < members[j].AgentID
	})
	first := []RolloutMember{}
	rest := []RolloutMember{}
	seen := map[string]bool{}
	for _, m := range members {
		if !seen[m.Platform] {
			seen[m.Platform] = true
			first = append(first, m)
		} else {
			rest = append(rest, m)
		}
	}
	for i := range first {
		first[i].Wave = i
	}
	for i := range rest {
		rest[i].Wave = len(first) + i/batch
	}
	return append(first, rest...), len(first)
}

func (s *Store) PublishBatchedUpdatePlan(opID, releaseID string, plan []PreparedUpdateTarget, opts RolloutOptions) error {
	if !opts.Valid() || len(plan) == 0 || len(plan) > 500 {
		return fmt.Errorf("invalid rollout options or target count")
	}
	return s.WithTx(func(tx *sql.Tx) error {
		var count int
		if e := tx.QueryRow(`SELECT COUNT(*) FROM update_rollouts WHERE state IN ('running','paused','cancelling')`).Scan(&count); e != nil {
			return e
		}
		if count >= 16 {
			return fmt.Errorf("at most 16 active rollouts; finish or cancel existing plans")
		}
		var deadline string
		var total int
		if e := tx.QueryRow(`SELECT deadline FROM operations WHERE id=? AND action='update.rollout'`, opID).Scan(&deadline); e != nil {
			return e
		}
		if e := tx.QueryRow(`SELECT COUNT(*) FROM operation_targets WHERE operation_id=?`, opID).Scan(&total); e != nil {
			return e
		}
		if total != len(plan) {
			return fmt.Errorf("all frozen targets must pass preflight; submit a new eligible selection")
		}
		now := s.now().Format(dbTimeFormat)
		// Initial visibility, pinned job bytes and all wave membership are atomic.
		if _, e := tx.Exec(`INSERT INTO update_rollouts(operation_id,release_id,state,batch_size,observe_seconds,canary_waves,deadline,created_at,updated_at) VALUES(?,?,'running',?,?,0,?,?,?)`, opID, releaseID, opts.BatchSize, opts.ObserveSeconds, deadline, now, now); e != nil {
			return e
		}
		ms := []RolloutMember{}
		seen := map[string]bool{}
		for _, p := range plan {
			if seen[p.AgentID] {
				return fmt.Errorf("duplicate rollout target")
			}
			seen[p.AgentID] = true
			if p.Status != protocol.TargetQueued && p.Status != protocol.TargetWaitingOffline {
				return fmt.Errorf("all targets must support this release; %s: %s", p.AgentID, p.Message)
			}
			var jobID, raw, previous string
			if e := tx.QueryRow(`SELECT job_id,envelope,status FROM agent_jobs WHERE operation_id=? AND agent_id=? AND action='update.rollout'`, opID, p.AgentID).Scan(&jobID, &raw, &previous); e != nil {
				return e
			}
			if previous != "preparing" {
				return ErrConflict
			}
			var env protocol.JobEnvelope
			if e := json.Unmarshal([]byte(raw), &env); e != nil {
				return e
			}
			if env.JobID != jobID || env.OperationID != opID || env.Action != "update.rollout" {
				return fmt.Errorf("invalid prepared update identity")
			}
			if env.Params == nil {
				env.Params = map[string]any{}
			}
			for k, v := range p.Evidence {
				env.Params[k] = v
			}
			if env.Params["release_id"] != releaseID {
				return fmt.Errorf("release mismatch")
			}
			b, e := json.Marshal(env)
			if e != nil {
				return e
			}
			if _, e = tx.Exec(`UPDATE agent_jobs SET envelope=?,status='rollout_held' WHERE job_id=?`, string(b), jobID); e != nil {
				return e
			}
			if e = s.updateTargetTx(tx, opID, p.AgentID, protocol.TargetQueued, "rollout.held", "Waiting for earlier wave and fresh worker observation", "", false, p.Evidence); e != nil {
				return e
			}
			platform := fmt.Sprint(p.Evidence["os"]) + "/" + fmt.Sprint(p.Evidence["arch"])
			ms = append(ms, RolloutMember{AgentID: p.AgentID, Platform: platform})
		}
		ms, n := assignWaves(ms, opts.BatchSize)
		for _, m := range ms {
			if _, e := tx.Exec(`INSERT INTO update_rollout_members(operation_id,agent_id,platform,wave) VALUES(?,?,?,?)`, opID, m.AgentID, m.Platform, m.Wave); e != nil {
				return e
			}
		}
		if _, e := tx.Exec(`UPDATE update_rollouts SET canary_waves=? WHERE operation_id=?`, n, opID); e != nil {
			return e
		}
		r, e := readRollout(tx, opID)
		if e != nil {
			return e
		}
		if e = s.releaseWaveTx(tx, r); e != nil {
			return e
		}
		return s.refreshOperationTx(tx, opID)
	})
}
func (s *Store) setRolloutStateTx(tx *sql.Tx, r *Rollout, state, reason string) error {
	r.State = state
	r.Reason = reason
	r.StableSince = ""
	_, e := tx.Exec(`UPDATE update_rollouts SET state=?,reason=?,stable_since='',revision=revision+1,updated_at=? WHERE operation_id=?`, state, reason, s.now().Format(dbTimeFormat), r.OperationID)
	return e
}
func (s *Store) releaseWaveTx(tx *sql.Tx, r *Rollout) error {
	now := s.now()
	var expiry string
	if e := tx.QueryRow(`SELECT expires_at FROM release_publications WHERE release_id=?`, r.ReleaseID).Scan(&expiry); e != nil {
		return e
	}
	until, e := time.Parse(time.RFC3339Nano, expiry)
	if e != nil {
		return e
	}
	deadline, e := time.Parse(time.RFC3339Nano, r.Deadline)
	if e != nil {
		return e
	}
	if !until.After(now) || !deadline.After(now) {
		return s.setRolloutStateTx(tx, r, "blocked", "Release metadata or rollout deadline expired; no new wave dispatched")
	}
	// Check every member before releasing any job of this wave.
	for _, m := range r.Members {
		if m.Wave != r.Wave || m.ReleasedAt != "" {
			continue
		}
		var caps map[string]protocol.Capability
		if m.jobStatus != "rollout_held" || m.revoked || m.archived || !m.managed || m.Platform != m.currentPlatform || json.Unmarshal([]byte(m.capabilities), &caps) != nil || caps["immutable_release_v1"].Status != "supported" {
			return s.setRolloutStateTx(tx, r, "blocked", "Target prerequisites changed; review the frozen selection and create a new rollout")
		}
	}
	for _, m := range r.Members {
		if m.Wave != r.Wave || m.ReleasedAt != "" {
			continue
		}
		var env protocol.JobEnvelope
		if e := json.Unmarshal([]byte(m.envelope), &env); e != nil {
			return e
		}
		if env.Params == nil || env.Params["release_id"] != r.ReleaseID {
			return fmt.Errorf("corrupt pinned update job")
		}
		env.NotBefore = now
		env.Deadline = now.Add(protocol.OneShotExpiry)
		if deadline.Before(env.Deadline) {
			env.Deadline = deadline
		}
		if until.Before(env.Deadline) {
			env.Deadline = until
		}
		env.Params["previous_session"] = m.session
		b, e := json.Marshal(env)
		if e != nil {
			return e
		}
		st := protocol.TargetQueued
		live, err := time.Parse(time.RFC3339Nano, m.liveAt)
		if err != nil || now.Sub(live) > protocol.StaleContact {
			st = protocol.TargetWaitingOffline
		}
		if _, e = tx.Exec(`UPDATE agent_jobs SET status=?,envelope=?,deadline=? WHERE job_id=? AND status='rollout_held'`, st, string(b), env.Deadline.Format(dbTimeFormat), m.jobID); e != nil {
			return e
		}
		if _, e = tx.Exec(`UPDATE update_rollout_members SET released_at=? WHERE operation_id=? AND agent_id=?`, now.Format(dbTimeFormat), r.OperationID, m.AgentID); e != nil {
			return e
		}
		if e = s.updateTargetTx(tx, r.OperationID, m.AgentID, st, "rollout.released", "Wave authorized; waiting for verified worker update", "", st == protocol.TargetWaitingOffline, env.Params); e != nil {
			return e
		}
	}
	_, e = tx.Exec(`UPDATE update_rollouts SET stable_since='',last_tick=?,revision=revision+1,updated_at=? WHERE operation_id=?`, now.Format(dbTimeFormat), now.Format(dbTimeFormat), r.OperationID)
	return e
}
func rolloutFailure(st protocol.TargetStatus) bool {
	switch st {
	case protocol.TargetFailed, protocol.TargetRejected, protocol.TargetUnsupported, protocol.TargetExpired, protocol.TargetUnknownResult, protocol.TargetRolledBack:
		return true
	}
	return false
}
func targetTerminal(st protocol.TargetStatus) bool {
	return rolloutFailure(st) || st == protocol.TargetSucceeded || st == protocol.TargetCancelledBeforeExec
}

// Advance is also run before job reads, so a newly arrived failure gates the next
// wave before it could be delivered. It never repeats previously released jobs.
func (s *Store) AdvanceRollouts() error {
	if e := s.expireAndBlockJobs(); e != nil {
		return e
	}
	return s.WithTx(func(tx *sql.Tx) error {
		rows, e := tx.Query(`SELECT operation_id FROM update_rollouts WHERE state IN ('running','paused','cancelling') ORDER BY created_at LIMIT 16`)
		if e != nil {
			return e
		}
		ids := []string{}
		for rows.Next() {
			var id string
			if e = rows.Scan(&id); e != nil {
				rows.Close()
				return e
			}
			ids = append(ids, id)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		for _, id := range ids {
			r, e := readRollout(tx, id)
			if e != nil {
				return e
			}
			if e = s.advanceRolloutTx(tx, r); e != nil {
				return e
			}
		}
		return nil
	})
}
func (s *Store) advanceRolloutTx(tx *sql.Tx, r *Rollout) error {
	now := s.now()
	deadline, e := time.Parse(time.RFC3339Nano, r.Deadline)
	if e != nil {
		return e
	}
	if r.State == "cancelling" {
		all := true
		for _, m := range r.Members {
			if !targetTerminal(m.Status) {
				all = false
			}
		}
		if all {
			if e = s.setRolloutStateTx(tx, r, "cancelled", "Undispatched jobs cancelled; delivered results retained"); e != nil {
				return e
			}
			return s.refreshOperationTx(tx, r.OperationID)
		}
		return nil
	}
	if !deadline.After(now) {
		if e = s.setRolloutStateTx(tx, r, "blocked", "Rollout deadline expired; no further jobs will be released"); e != nil {
			return e
		}
		return s.refreshOperationTx(tx, r.OperationID)
	}
	// Any failed released target blocks later waves, including late failures while paused.
	for _, m := range r.Members {
		if rolloutFailure(m.Status) {
			if e = s.setRolloutStateTx(tx, r, "blocked", "Update failed or result unknown for "+m.AgentID+"; cancel pending targets and review a new selection"); e != nil {
				return e
			}
			return s.refreshOperationTx(tx, r.OperationID)
		}
	}
	if r.State == "paused" {
		return nil
	}
	// Only matching current worker identity AND recent live contact count as stability.
	for _, m := range r.Members {
		if m.Status != protocol.TargetSucceeded || m.ReleasedAt == "" {
			continue
		}
		var env protocol.JobEnvelope
		var rec protocol.JobReceipt
		if json.Unmarshal([]byte(m.envelope), &env) != nil || json.Unmarshal([]byte(m.receipt), &rec) != nil {
			return fmt.Errorf("invalid rollout observation evidence")
		}
		live, e := time.Parse(time.RFC3339Nano, m.liveAt)
		if m.revoked || m.archived || e != nil || now.Sub(live) > protocol.StaleContact || live.After(now.Add(5*time.Second)) || m.digest == "" || m.digest != env.Params["sha256"] || m.session != rec.Evidence["session_id"] {
			if e = s.setRolloutStateTx(tx, r, "blocked", "Confirmed worker identity or fresh contact lost during wave observation: "+m.AgentID); e != nil {
				return e
			}
			return s.refreshOperationTx(tx, r.OperationID)
		}
	}
	all := true
	current := 0
	for _, m := range r.Members {
		if m.Wave != r.Wave {
			continue
		}
		current++
		if m.Status != protocol.TargetSucceeded {
			all = false
		}
	}
	if current == 0 {
		return fmt.Errorf("rollout has no current wave")
	}
	if !all {
		return nil
	}
	since, e := time.Parse(time.RFC3339Nano, r.StableSince)
	last, le := time.Parse(time.RFC3339Nano, r.LastTick)
	if e != nil || le != nil || now.Before(last) || now.Sub(last) > 15*time.Second {
		since = now
	}
	if now.Sub(since) < time.Duration(r.ObserveSeconds)*time.Second {
		_, e = tx.Exec(`UPDATE update_rollouts SET stable_since=?,last_tick=? WHERE operation_id=?`, since.Format(dbTimeFormat), now.Format(dbTimeFormat), r.OperationID)
		return e
	}
	maxWave := r.Wave
	for _, m := range r.Members {
		if m.Wave > maxWave {
			maxWave = m.Wave
		}
	}
	if r.Wave == maxWave {
		if e = s.setRolloutStateTx(tx, r, "completed", "Every frozen target confirmed the pinned worker and passed fresh-contact observation"); e != nil {
			return e
		}
		return s.refreshOperationTx(tx, r.OperationID)
	}
	r.Wave++
	if _, e = tx.Exec(`UPDATE update_rollouts SET wave=?,stable_since='' WHERE operation_id=?`, r.Wave, r.OperationID); e != nil {
		return e
	}
	if e = s.releaseWaveTx(tx, r); e != nil {
		return e
	}
	return s.refreshOperationTx(tx, r.OperationID)
}

// Controller-only pause/resume: already claimed jobs can finish and retransmit.
// A blocked failed canary cannot be waved through with an acknowledgement button.
func (s *Store) ControlRollout(id string, expected int64, pause bool, controlOpID string) error {
	return s.WithTx(func(tx *sql.Tx) error {
		r, e := readRollout(tx, id)
		if e != nil {
			return e
		}
		if r.Revision != expected {
			return ErrConflict
		}
		next, reason := "running", "Resumed original frozen rollout; no job replay"
		if pause {
			if r.State != "running" {
				return fmt.Errorf("only a running rollout can be paused")
			}
			next = "paused"
			reason = "Owner paused dispatch; already claimed jobs may still execute"
		} else {
			if r.State != "paused" {
				return fmt.Errorf("only a manually paused rollout can resume; failed waves require a new reviewed selection")
			}
			deadline, e := time.Parse(time.RFC3339Nano, r.Deadline)
			if e != nil || !deadline.After(s.now()) {
				return fmt.Errorf("rollout deadline invalid or expired")
			}
			var expiry string
			if e = tx.QueryRow(`SELECT expires_at FROM release_publications WHERE release_id=?`, r.ReleaseID).Scan(&expiry); e != nil {
				return e
			}
			expires, e := time.Parse(time.RFC3339Nano, expiry)
			if e != nil || !expires.After(s.now()) {
				return fmt.Errorf("release metadata invalid or expired")
			}
			for _, m := range r.Members {
				if rolloutFailure(m.Status) {
					return fmt.Errorf("failed target cannot be silently retried")
				}
			}
		}
		if e = s.setRolloutStateTx(tx, r, next, reason); e != nil {
			return e
		}
		if e = s.refreshOperationTx(tx, id); e != nil {
			return e
		}
		if controlOpID != "" {
			if e = s.updateTargetTx(tx, controlOpID, "server", protocol.TargetSucceeded, "rollout.control_committed", reason, "", false, map[string]any{"operation_id": id, "state": next, "rollout_revision": expected + 1}); e != nil {
				return e
			}
			return s.refreshOperationTx(tx, controlOpID)
		}
		return nil
	})
}
