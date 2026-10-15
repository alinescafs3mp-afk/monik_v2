package storage

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
)

func (s *Store) ValidateSparkControl(agentID string, p protocol.ModelActionParams, action string) (*SparkSnapshot, *protocol.SparkModelState, error) {
	ag, err := s.Agent(agentID)
	if err != nil {
		return nil, nil, err
	}
	fresh := func(at time.Time, limit time.Duration) bool {
		age := s.now().Sub(at)
		return age >= -5*time.Second && age <= limit
	}
	if ag.Revoked || ag.Archived || ag.Conflict || ag.LastLiveAt == nil || !fresh(*ag.LastLiveAt, protocol.SparkControlFreshness) || ag.SessionID != p.ExpectedAgentSession {
		return nil, nil, fmt.Errorf("agent_offline_or_session_changed")
	}
	var caps map[string]protocol.Capability
	if err = json.Unmarshal([]byte(ag.Capabilities), &caps); err != nil {
		return nil, nil, err
	}
	if caps["spark_control_v1"].Status != protocol.CapSupported {
		return nil, nil, fmt.Errorf("model_control_unsupported")
	}
	v, err := s.SparkSnapshot(agentID)
	if err != nil {
		return nil, nil, err
	}
	c := v.Spark.ControlState
	if v.AgentSession != ag.SessionID || !fresh(v.ObservedAt, protocol.SparkControlFreshness) || c == nil || !c.Enabled || c.Epoch == nil || *c.Epoch != p.ControlEpoch || !fresh(c.ObservedAt, protocol.SparkObservationFreshness) {
		return nil, nil, fmt.Errorf("stale_or_disabled_control")
	}
	if v.Spark.Inventory == nil {
		return nil, nil, fmt.Errorf("profile_unknown")
	}
	var profile *protocol.SparkProfile
	for i := range v.Spark.Inventory.Profiles {
		if v.Spark.Inventory.Profiles[i].ProfileID == p.ProfileID {
			profile = &v.Spark.Inventory.Profiles[i]
			break
		}
	}
	if profile == nil || profile.ProfileRevision != p.ProfileRevision {
		return nil, nil, fmt.Errorf("profile_revision_changed")
	}
	act := strings.TrimPrefix(action, "model.")
	allowed := false
	for _, a := range profile.AllowedActions {
		if a == act {
			allowed = true
		}
	}
	if !profile.ControlAllowed || !allowed {
		return nil, nil, fmt.Errorf("local_control_not_enabled")
	}
	var state *protocol.SparkModelState
	for i := range v.Spark.ModelStates {
		if v.Spark.ModelStates[i].ProfileID == p.ProfileID {
			state = &v.Spark.ModelStates[i]
			break
		}
	}
	if state == nil || state.Generation != p.ExpectedGeneration || state.Quality != "ok" || !fresh(state.ObservedAt, protocol.SparkObservationFreshness) {
		return nil, nil, fmt.Errorf("stale_generation_or_state")
	}
	if action == "model.restart" {
		if state.SupervisorState == "stopped" {
			return nil, nil, fmt.Errorf("not_running_use_start")
		}
		if state.SupervisorState != "running" {
			return nil, nil, fmt.Errorf("model_transition_in_progress")
		}
	}
	if (action == "model.stop" && state.SupervisorState != "stopped" || action == "model.restart") && !p.ConfirmDisruption {
		return nil, nil, fmt.Errorf("disruption_confirmation_required")
	}
	return v, state, nil
}

func (s *Store) HasActiveSparkProfile(agentID, profile, action string) (bool, error) {
	busy, _, err := s.sparkProfileReservation(agentID, profile, action)
	return busy, err
}

// A safety stop may interrupt the exact issued start stage of a restart. An
// unresolved restart by itself is not authority to stop a later/manual instance.
// The returned parents are closed atomically with publication of the new stop.
func (s *Store) sparkProfileReservation(agentID, profile, action string) (bool, []string, error) {
	rows, err := s.db().Query(`SELECT i.operation_id,o.action,i.profile_revision,i.control_epoch,i.agent_session,i.stage_id,i.stage_authorization FROM spark_intents i JOIN operations o ON o.id=i.operation_id JOIN operation_targets t ON t.operation_id=o.id AND t.agent_id=i.agent_id WHERE i.agent_id=? AND i.profile_id=? AND t.status IN ('queued','accepted','running','awaiting_confirmation','unknown_result')`, agentID, profile)
	if err != nil {
		return false, nil, err
	}
	type reservation struct{ op, action, revision, epoch, session, stage, authorization string }
	var pending []reservation
	for rows.Next() {
		var v reservation
		if err = rows.Scan(&v.op, &v.action, &v.revision, &v.epoch, &v.session, &v.stage, &v.authorization); err != nil {
			rows.Close()
			return false, nil, err
		}
		pending = append(pending, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return false, nil, err
	}
	var snapshot *SparkSnapshot
	var superseded []string
	for _, v := range pending {
		if action != "model.stop" {
			return true, nil, nil
		}
		// Preserve single-stage startup cancellation/readiness semantics.
		if v.action == "model.start" {
			continue
		}
		if v.action != "model.restart" || v.authorization == "" {
			return true, nil, nil
		}
		var auth protocol.SparkStageAuthorization
		if json.Unmarshal([]byte(v.authorization), &auth) != nil || auth.ParentOperationID != v.op || auth.StageID == "" || auth.StageID != v.stage || auth.Stage != "start" || auth.Params.ProfileID != profile || auth.Params.ProfileRevision != v.revision || auth.Params.ControlEpoch != v.epoch || auth.Params.ExpectedAgentSession != v.session || auth.Params.ExpectedGeneration < 0 {
			return true, nil, nil
		}
		if snapshot == nil {
			snapshot, err = s.SparkSnapshot(agentID)
			if err != nil {
				return false, nil, err
			}
		}
		// Validate all current authority/freshness fences, even for callers that
		// only inspect reservations. The typed stop itself is revalidated on insert.
		p := auth.Params
		var state *protocol.SparkModelState
		for i := range snapshot.Spark.ModelStates {
			if snapshot.Spark.ModelStates[i].ProfileID == profile {
				state = &snapshot.Spark.ModelStates[i]
				break
			}
		}
		if state == nil {
			return true, nil, nil
		}
		p.ExpectedGeneration = state.Generation
		if _, _, err = s.ValidateSparkControl(agentID, p, "model.stop"); err != nil {
			return true, nil, nil
		}
		startedInstance := (state.SupervisorState == "starting" || state.SupervisorState == "running") && state.Generation > 0 && state.Generation-1 == auth.Params.ExpectedGeneration
		// An issued but unattempted stage can race a stopped-generation stop
		// while in transit. The six-field stop does not bind its stage identity,
		// so retain that reservation until a fresh started instance is observed.
		if !startedInstance {
			return true, nil, nil
		}
		superseded = append(superseded, v.op)
	}
	return false, superseded, nil
}

func (s *Store) supersedeSparkRestartTx(tx *sql.Tx, agentID, parentID, stopID string) error {
	// This is cancellation of the restart workflow, not proof that the new stop
	// has executed. Keep its original worker receipt and track the stop separately.
	var raw string
	if err := tx.QueryRow(`SELECT evidence FROM operation_targets WHERE operation_id=? AND agent_id=?`, parentID, agentID).Scan(&raw); err != nil {
		return err
	}
	var evidence map[string]any
	if err := json.Unmarshal([]byte(raw), &evidence); err != nil {
		return err
	}
	if evidence == nil {
		evidence = map[string]any{}
	}
	evidence["reason"] = "superseded by safety stop operation " + stopID
	if err := s.updateTargetTx(tx, parentID, agentID, protocol.TargetFailed, "restart_start_superseded", "Restart start stage superseded by a safety stop; its separate operation determines the stop outcome", "superseded_by_stop", false, evidence); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE agent_jobs SET status='failed' WHERE operation_id=? AND agent_id=?`, parentID, agentID); err != nil {
		return err
	}
	return s.refreshOperationTx(tx, parentID)
}

func (s *Store) SparkOperations(agentID string, limit int) ([]*protocol.Operation, error) {
	if limit < 1 || limit > 30 {
		limit = 30
	}
	rows, err := s.db().Query(`SELECT operation_id FROM spark_intents WHERE agent_id=? ORDER BY rowid DESC LIMIT ?`, agentID, limit)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	out := []*protocol.Operation{}
	for _, id := range ids {
		op, e := s.Operation(id)
		if e != nil {
			return nil, e
		}
		out = append(out, op)
	}
	return out, nil
}

func (s *Store) sparkDispatchAuthority(q sqlExecutor, agentID string, job protocol.JobEnvelope) (bool, error) {
	var expires, role, user, expectedUser, session string
	var recent sql.NullString
	err := q.QueryRow(`SELECT b.expires_at,b.recent_auth_until,u.role,b.user_id,i.owner_user_id,i.agent_session FROM spark_intents i JOIN admin_sessions b ON b.id=i.owner_session_id JOIN admin_users u ON u.id=b.user_id WHERE i.operation_id=? AND i.agent_id=?`, job.OperationID, agentID).Scan(&expires, &recent, &role, &user, &expectedUser, &session)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	exp, e := time.Parse(time.RFC3339Nano, expires)
	if e != nil || !s.now().Before(exp) || role != "owner" || user != expectedUser || !recent.Valid {
		return false, nil
	}
	auth, e := time.Parse(time.RFC3339Nano, recent.String)
	if e != nil || !s.now().Before(auth) {
		return false, nil
	}
	ts := &Store{DB: s.DB, Clock: s.Clock, executor: q}
	ag, e := ts.Agent(agentID)
	if e != nil {
		return false, e
	}
	if ag.Revoked || ag.Archived || ag.SessionID != session {
		return false, nil
	}
	return true, nil
}

func (s *Store) AuthorizeSparkStartStage(agentID, opID, jobID string) (*protocol.SparkStageAuthorization, error) {
	var result *protocol.SparkStageAuthorization
	err := s.WithTx(func(tx *sql.Tx) error {
		ts := &Store{DB: s.DB, Clock: s.Clock, executor: tx}
		var ownerSession, ownerUser, profile, revision, epoch, session, stage, prior, action, status, targetStatus, raw, expires, role, user string
		var recent sql.NullString
		if err := tx.QueryRow(`SELECT i.owner_session_id,i.owner_user_id,i.profile_id,i.profile_revision,i.control_epoch,i.agent_session,i.stage_id,i.stage_authorization,o.action,j.status,t.status,COALESCE(j.result,'') FROM spark_intents i JOIN operations o ON o.id=i.operation_id JOIN agent_jobs j ON j.operation_id=i.operation_id AND j.agent_id=i.agent_id JOIN operation_targets t ON t.operation_id=i.operation_id AND t.agent_id=i.agent_id WHERE i.operation_id=? AND i.agent_id=? AND j.job_id=?`, opID, agentID, jobID).Scan(&ownerSession, &ownerUser, &profile, &revision, &epoch, &session, &stage, &prior, &action, &status, &targetStatus, &raw); err != nil {
			return err
		}
		if err := tx.QueryRow(`SELECT s.expires_at,s.recent_auth_until,u.role,s.user_id FROM admin_sessions s JOIN admin_users u ON u.id=s.user_id WHERE s.id=?`, ownerSession).Scan(&expires, &recent, &role, &user); err != nil {
			return fmt.Errorf("owner_authority_lost")
		}
		exp, e := time.Parse(time.RFC3339Nano, expires)
		if e != nil || !s.now().Before(exp) || role != "owner" || user != ownerUser || !recent.Valid {
			return fmt.Errorf("owner_authority_lost")
		}
		auth, e := time.Parse(time.RFC3339Nano, recent.String)
		if e != nil || !s.now().Before(auth) {
			return fmt.Errorf("reauth_required")
		}
		activeStage := func(st string) bool {
			return st == string(protocol.TargetAwaitingConfirmation) || st == string(protocol.TargetRunning) || st == string(protocol.TargetAccepted)
		}
		if action != "model.restart" || !activeStage(status) || !activeStage(targetStatus) {
			return fmt.Errorf("restart_stage_not_ready")
		}
		if prior != "" {
			var v protocol.SparkStageAuthorization
			if e = json.Unmarshal([]byte(prior), &v); e != nil {
				return e
			}
			if !s.now().Before(v.Deadline) {
				return fmt.Errorf("stage_authorization_expired")
			}
			result = &v
			return nil
		}
		if status != string(protocol.TargetAwaitingConfirmation) {
			return fmt.Errorf("restart_stage_not_ready")
		}
		var rec protocol.JobReceipt
		if e = json.Unmarshal([]byte(raw), &rec); e != nil || rec.Stage != "restart_stopped" {
			return fmt.Errorf("restart_stop_unverified")
		}
		v, e := ts.SparkSnapshot(agentID)
		if e != nil {
			return e
		}
		var generation int64 = -1
		for _, st := range v.Spark.ModelStates {
			if st.ProfileID == profile && sparkShutdownProven(st, rec) {
				generation = st.Generation
			}
		}
		p := protocol.ModelActionParams{ProfileID: profile, ProfileRevision: revision, ExpectedGeneration: generation, ControlEpoch: epoch, ExpectedAgentSession: session, ConfirmDisruption: true}
		current, state, e := ts.ValidateSparkControl(agentID, p, "model.start")
		if e != nil {
			return e
		}
		// Cached observations may propose a new operation, but minting this
		// restart's next effect lease still requires fresh stop/control proof.
		stageNow := s.now()
		if age := stageNow.Sub(current.Spark.ControlState.ObservedAt); age < -5*time.Second || age > protocol.SparkControlFreshness {
			return fmt.Errorf("stale_or_disabled_control")
		}
		if age := stageNow.Sub(state.ObservedAt); age < -5*time.Second || age > protocol.SparkControlFreshness {
			return fmt.Errorf("restart_stop_unverified")
		}
		if rec.Evidence["profile_id"] != profile || rec.Evidence["profile_revision"] != revision || rec.Evidence["control_epoch"] != epoch || rec.Evidence["agent_session"] != session || fmt.Sprint(rec.Evidence["generation"]) != fmt.Sprint(generation) {
			return fmt.Errorf("restart_stop_unverified")
		}
		for _, st := range v.Spark.ModelStates {
			if st.ProfileID == profile && st.SupervisorState == "failed" && !ts.rawFailedShutdownProven(agentID, p, rec, s.now().Add(-protocol.SparkControlFreshness)) {
				return fmt.Errorf("restart_stop_unverified")
			}
		}
		// The worker validates the exact lease length. Two clock reads can make
		// a real-time lease exceed the maximum even by a few nanoseconds.
		issuedAt := s.now()
		result = &protocol.SparkStageAuthorization{ParentOperationID: opID, StageID: stage, Stage: "start", Params: p, IssuedAt: issuedAt, Deadline: issuedAt.Add(protocol.SparkDispatchLease)}
		b, e := json.Marshal(result)
		if e != nil {
			return e
		}
		_, e = tx.Exec(`UPDATE spark_intents SET stage_authorization=? WHERE operation_id=? AND stage_authorization=''`, string(b), opID)
		return e
	})
	return result, err
}

func (s *Store) sparkReceiptProven(tx *sql.Tx, agentID string, job protocol.JobEnvelope, rec protocol.JobReceipt) bool {
	p, e := protocol.DecodeModelActionParams(job.Params)
	if e != nil {
		return false
	}
	if rec.Evidence["profile_id"] != p.ProfileID || rec.Evidence["profile_revision"] != p.ProfileRevision || rec.Evidence["control_epoch"] != p.ControlEpoch || rec.Evidence["agent_session"] != p.ExpectedAgentSession {
		return false
	}
	var stage *protocol.SparkStageAuthorization
	if job.Action == "model.restart" {
		var auth string
		if tx.QueryRow(`SELECT stage_authorization FROM spark_intents WHERE operation_id=?`, job.OperationID).Scan(&auth) != nil || auth == "" {
			return false
		}
		var v protocol.SparkStageAuthorization
		if json.Unmarshal([]byte(auth), &v) != nil {
			return false
		}
		stage = &v
		if rec.Evidence["stage_id"] != stage.StageID {
			return false
		}
	}
	matches := func(spark protocol.SparkExtensionV1, allowFailed bool) bool {
		for _, st := range spark.ModelStates {
			if st.ProfileID != p.ProfileID || st.Quality != "ok" || st.ObservedAt.Before(job.CreatedAt) || fmt.Sprint(rec.Evidence["generation"]) != fmt.Sprint(st.Generation) {
				continue
			}
			switch job.Action {
			case "model.start":
				return rec.Stage == "model_started" && st.SupervisorState == "running" && st.Generation >= p.ExpectedGeneration
			case "model.stop":
				if st.SupervisorState == "failed" && !allowFailed {
					continue // Merged latest views are not an exact failed proof.
				}
				return rec.Stage == "model_stopped" && sparkShutdownProven(st, rec) && (st.SupervisorState != "failed" || sparkFailedShutdownAuthorityMatches(spark, p)) && st.Generation >= p.ExpectedGeneration
			case "model.restart":
				return rec.Stage == "model_restarted" && st.SupervisorState == "running" && st.Generation > stage.Params.ExpectedGeneration
			}
		}
		return false
	}
	ts := &Store{DB: s.DB, Clock: s.Clock, executor: tx}
	if v, e := ts.SparkSnapshot(agentID); e == nil && v.AgentSession == p.ExpectedAgentSession && matches(v.Spark, false) {
		return true
	}
	// A later manual action/session must not destroy proof of an original effect.
	// Historical samples prove its outcome only; they never refresh current control.
	rows, e := tx.Query(`SELECT payload FROM spark_samples WHERE agent_id=? AND session_id=? AND observed_at>=? AND EXISTS(SELECT 1 FROM json_each(payload,'$.model_states') st WHERE json_extract(st.value,'$.profile_id')=? AND CAST(json_extract(st.value,'$.generation') AS TEXT)=?) ORDER BY observed_at DESC LIMIT 8`, agentID, p.ExpectedAgentSession, job.CreatedAt.UTC().Format(dbTimeFormat), p.ProfileID, fmt.Sprint(rec.Evidence["generation"]))
	if e != nil {
		return false
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		if rows.Scan(&raw) != nil {
			return false
		}
		var spark protocol.SparkExtensionV1
		if json.Unmarshal([]byte(raw), &spark) == nil && matches(spark, true) {
			return true
		}
	}
	return false
}

// A cancelled systemd ExecStartPre can leave a failed unit after every process
// and job has been reaped. Keep that failure visible; the separate shutdown
// witness must agree with the exact authenticated sample and receipt generation.
func sparkShutdownProven(st protocol.SparkModelState, rec protocol.JobReceipt) bool {
	if st.Quality != "ok" {
		return false
	}
	if st.SupervisorState == "stopped" {
		return true
	}
	identity, ok := rec.Evidence["instance_identity"].(string)
	return st.SupervisorState == "failed" && st.Readiness == "not_applicable" && st.Reason == "failed_service_shutdown_verified" && rec.Evidence["shutdown_verified"] == true && rec.Evidence["reason"] == "failed_service_shutdown_verified" && rec.Evidence["readiness"] == "not_applicable" && rec.Evidence["supervisor_state"] == "failed" && fmt.Sprint(rec.Evidence["generation"]) == fmt.Sprint(st.Generation) && ok && len(identity) == 71 && strings.HasPrefix(identity, "sha256:") && strings.Trim(identity[7:], "0123456789abcdef") == ""
}

// Generations can repeat under a different profile or control epoch. The new
// failed-unit witness must belong to the original authority, including when a
// historical sample confirms its outcome without refreshing current control.
func sparkFailedShutdownAuthorityMatches(spark protocol.SparkExtensionV1, p protocol.ModelActionParams) bool {
	if spark.ControlState == nil || !spark.ControlState.Enabled || spark.ControlState.Epoch == nil || *spark.ControlState.Epoch != p.ControlEpoch || spark.Inventory == nil {
		return false
	}
	for _, profile := range spark.Inventory.Profiles {
		if profile.ProfileID == p.ProfileID && profile.ProfileRevision == p.ProfileRevision {
			return true
		}
	}
	return false
}

// Never borrow control/inventory fields from an earlier sample to qualify a new
// failed shutdown. This query reads exact report payloads, not the merged view.
func (s *Store) rawFailedShutdownProven(agentID string, p protocol.ModelActionParams, rec protocol.JobReceipt, since time.Time) bool {
	rows, err := s.db().Query(`SELECT payload FROM spark_samples WHERE agent_id=? AND session_id=? AND observed_at>=? AND EXISTS(SELECT 1 FROM json_each(payload,'$.model_states') st WHERE json_extract(st.value,'$.profile_id')=? AND CAST(json_extract(st.value,'$.generation') AS TEXT)=?) ORDER BY observed_at DESC LIMIT 8`, agentID, p.ExpectedAgentSession, since.UTC().Format(dbTimeFormat), p.ProfileID, fmt.Sprint(rec.Evidence["generation"]))
	if err != nil {
		return false
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		var sample protocol.SparkExtensionV1
		if rows.Scan(&raw) != nil {
			return false
		}
		if json.Unmarshal([]byte(raw), &sample) != nil || !sparkFailedShutdownAuthorityMatches(sample, p) {
			continue
		}
		for _, st := range sample.ModelStates {
			if st.ProfileID == p.ProfileID && st.SupervisorState == "failed" && !st.ObservedAt.Before(since) && st.ObservedAt.Before(s.now().Add(5*time.Second)) && sparkShutdownProven(st, rec) {
				return true
			}
		}
	}
	return false
}

func validateSparkReceipt(r protocol.JobReceipt) error {
	if len(r.Stage) > 128 || len(r.ErrorCode) > 128 || len(r.Message) > 512 || len(r.Evidence) > 16 {
		return fmt.Errorf("model receipt exceeds metadata budget")
	}
	allowed := map[string]bool{"profile_id": true, "profile_revision": true, "control_epoch": true, "agent_session": true, "generation": true, "stage_id": true, "supervisor_state": true, "readiness": true, "instance_identity": true, "observed_at": true, "no_op": true, "effect_attempted": true, "reason": true, "stop_observed": true, "start_observed": true, "shutdown_verified": true}
	for key, value := range r.Evidence {
		if key == "restart_stop_receipt" || key == "start_stage_attempted" {
			if r.Status != protocol.TargetFailed || r.Stage != "restart_start_not_attempted" || r.Evidence["start_stage_attempted"] != false {
				return fmt.Errorf("unattempted start evidence requires a failed restart stage")
			}
			if key == "start_stage_attempted" {
				continue
			}
			raw, ok := value.(string)
			var stopped protocol.JobReceipt
			if !ok || len(raw) > 32<<10 || json.Unmarshal([]byte(raw), &stopped) != nil || stopped.Status != protocol.TargetAwaitingConfirmation || stopped.Stage != "restart_stopped" || stopped.JobID != r.JobID || stopped.OperationID != r.OperationID {
				return fmt.Errorf("unattempted start requires its original bounded stop receipt")
			}
			if err := validateSparkReceipt(stopped); err != nil {
				return err
			}
			continue
		}
		if !allowed[key] {
			return fmt.Errorf("model receipt contains unapproved evidence field")
		}
		if key == "shutdown_verified" {
			if _, ok := value.(bool); !ok {
				return fmt.Errorf("shutdown witness must be boolean")
			}
		}
		switch v := value.(type) {
		case string:
			if len(v) > 1024 {
				return fmt.Errorf("oversized model evidence")
			}
		case float64, int, int64, bool, nil, time.Time:
		default:
			return fmt.Errorf("model evidence must be scalar metadata")
		}
	}
	b, e := json.Marshal(r.Evidence)
	evidenceBudget := 16 << 10
	if r.Status == protocol.TargetFailed && r.Stage == "restart_start_not_attempted" {
		// Quoting the original bounded receipt adds bytes without accepting
		// unrestricted nested evidence or changing ordinary receipt budgets.
		evidenceBudget = 64 << 10
	}
	if e != nil || len(b) > evidenceBudget {
		return fmt.Errorf("model receipt evidence exceeds budget")
	}
	return nil
}
