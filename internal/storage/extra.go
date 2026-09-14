package storage

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
)

func (s *Store) TouchRecentAuth(sessionID string, until time.Time) error {
	_, err := s.db().Exec(`UPDATE admin_sessions SET recent_auth_until=? WHERE id=?`, until.UTC().Format(dbTimeFormat), sessionID)
	return err
}

func (s *Store) InsertMigration(plan *protocol.MigrationPlan, opID string) error {
	_, err := s.db().Exec(`INSERT INTO controller_migrations(id,operation_id,controller_id,current_url,candidate_url,generation,mode,payload_hash,trust_pem,expires_at,arm_fallback,created_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
		plan.PlanID, opID, plan.ControllerID, plan.CurrentURL, plan.CandidateURL, plan.Generation, plan.Mode,
		plan.PayloadHash, plan.TrustPEM, plan.ExpiresAt.UTC().Format(dbTimeFormat), boolInt(plan.ArmFallback),
		s.now().UTC().Format(dbTimeFormat))
	return err
}

func (s *Store) Migration(id string) (*protocol.MigrationPlan, error) {
	p := &protocol.MigrationPlan{}
	var exp string
	var arm int
	err := s.db().QueryRow(`SELECT id,controller_id,current_url,candidate_url,generation,mode,payload_hash,IFNULL(trust_pem,''),expires_at,arm_fallback
		FROM controller_migrations WHERE id=?`, id).
		Scan(&p.PlanID, &p.ControllerID, &p.CurrentURL, &p.CandidateURL, &p.Generation, &p.Mode, &p.PayloadHash, &p.TrustPEM, &exp, &arm)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	p.ExpiresAt, _ = time.Parse(time.RFC3339Nano, exp)
	p.ArmFallback = arm == 1
	return p, nil
}

func (s *Store) ArmMigration(id string, arm bool) error {
	_, err := s.db().Exec(`UPDATE controller_migrations SET arm_fallback=? WHERE id=?`, boolInt(arm), id)
	return err
}

func (s *Store) SetMigrationMode(id, mode string) error {
	_, err := s.db().Exec(`UPDATE controller_migrations SET mode=? WHERE id=?`, mode, id)
	return err
}

func (s *Store) SetMigrationTarget(planID, agentID, state, reason string) error {
	_, err := s.db().Exec(`INSERT INTO migration_targets(plan_id,agent_id,state,reason,updated_at) VALUES(?,?,?,?,?)
		ON CONFLICT(plan_id,agent_id) DO UPDATE SET state=excluded.state, reason=excluded.reason, updated_at=excluded.updated_at`,
		planID, agentID, state, reason, s.now().UTC().Format(dbTimeFormat))
	return err
}

func (s *Store) ArtifactsByRelease(releaseID string) ([]map[string]any, error) {
	rows, err := s.db().Query(`SELECT os,arch,name,sha256,length,path FROM release_artifacts WHERE release_id=?`, releaseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var osn, arch, name, sha, path string
		var length int64
		if err := rows.Scan(&osn, &arch, &name, &sha, &length, &path); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"os": osn, "arch": arch, "name": name, "sha256": sha, "length": length, "path": path})
	}
	return out, rows.Err()
}

func (s *Store) ArtifactJSON(releaseID string) json.RawMessage {
	arts, _ := s.ArtifactsByRelease(releaseID)
	b, _ := json.Marshal(arts)
	return b
}

func (s *Store) NextMigrationGeneration() int64 {
	var n int64
	if err := s.db().QueryRow(`SELECT MAX(g) FROM (SELECT COALESCE(MAX(generation),0) g FROM controller_migrations UNION ALL SELECT COALESCE(MAX(endpoint_generation),0) g FROM agents)`).Scan(&n); err != nil {
		return 0
	}
	return n + 1
}

func (s *Store) HasActiveLifecycle(agentID string) (string, bool) {
	var action string
	err := s.db().QueryRow(`SELECT action FROM agent_jobs WHERE agent_id=? AND action IN ('update.rollout','update.rollback','rebind.activate','agent.restart','credential.rotate','trust.retire') AND status IN ('queued','waiting_offline','delivered','accepted','running','awaiting_confirmation') LIMIT 1`, agentID).Scan(&action)
	if err != nil {
		return "", false
	}
	return action, true
}

func (s *Store) JobIDFor(opID, agentID string) (string, error) {
	var id string
	err := s.db().QueryRow(`SELECT job_id FROM operation_targets WHERE operation_id=? AND agent_id=?`, opID, agentID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return id, err
}

func (s *Store) PatchJobParams(jobID string, extra map[string]any) error {
	var raw string
	if err := s.db().QueryRow(`SELECT envelope FROM agent_jobs WHERE job_id=?`, jobID).Scan(&raw); err != nil {
		return err
	}
	var env protocol.JobEnvelope
	if err := json.Unmarshal([]byte(raw), &env); err != nil {
		return err
	}
	if env.Params == nil {
		env.Params = map[string]any{}
	}
	for k, v := range extra {
		env.Params[k] = v
	}
	b, err := json.Marshal(env)
	if err != nil {
		return err
	}
	_, err = s.db().Exec(`UPDATE agent_jobs SET envelope=? WHERE job_id=?`, string(b), jobID)
	return err
}

func (s *Store) Release(id string) (map[string]any, error) {
	var ver, dig, notes, meta, plat, at string
	var trust int
	err := s.db().QueryRow(`SELECT version,digest,notes,metadata,imported_at,trust_ok,platforms FROM releases WHERE id=?`, id).
		Scan(&ver, &dig, &notes, &meta, &at, &trust, &plat)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return map[string]any{"id": id, "version": ver, "digest": dig, "notes": notes, "metadata": json.RawMessage(meta), "imported_at": at, "trust_ok": trust == 1, "platforms": json.RawMessage(plat)}, nil
}

func (s *Store) SaveRule(name, body string) error {
	_, err := s.db().Exec(`INSERT INTO rule_versions(name,body,effective_from) VALUES(?,?,?)`, name, body, s.now().UTC().Format(dbTimeFormat))
	return err
}

func (s *Store) SaveMaintenance(id, entityType, entityID, purpose, start, end, actor string) error {
	_, err := s.db().Exec(`INSERT INTO maintenance_windows(id,entity_type,entity_id,purpose,start_at,end_at,created_by) VALUES(?,?,?,?,?,?,?)`,
		id, entityType, entityID, purpose, start, end, actor)
	return err
}

func (s *Store) SecretForAgent(id, agentID string) (name, header, checkID string, version int, nonce, ct []byte, err error) {
	err = s.db().QueryRow(`SELECT name,header_name,IFNULL(check_id,''),version,nonce,ciphertext FROM check_secrets WHERE id=? AND agent_id=?`, id, agentID).
		Scan(&name, &header, &checkID, &version, &nonce, &ct)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return
}

func (s *Store) SetPendingCredential(agentID, hash, jobID string, until time.Time) error {
	_, err := s.db().Exec(`INSERT INTO agent_credential_overlap(agent_id,pending_hash,job_id,expires_at,created_at)
		VALUES(?,?,?,?,?)
		ON CONFLICT(agent_id) DO UPDATE SET pending_hash=excluded.pending_hash, job_id=excluded.job_id, expires_at=excluded.expires_at`,
		agentID, hash, jobID, until.UTC().Format(dbTimeFormat), s.now().UTC().Format(dbTimeFormat))
	return err
}

func (s *Store) PendingCredential(agentID string) (hash, jobID string, err error) {
	var exp string
	err = s.db().QueryRow(`SELECT pending_hash,job_id,expires_at FROM agent_credential_overlap WHERE agent_id=?`, agentID).Scan(&hash, &jobID, &exp)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", ErrNotFound
	}
	if err != nil {
		return "", "", err
	}
	until, _ := time.Parse(time.RFC3339Nano, exp)
	if until.Before(s.now().UTC()) {
		_, _ = s.db().Exec(`DELETE FROM agent_credential_overlap WHERE agent_id=?`, agentID)
		return "", "", ErrNotFound
	}
	return hash, jobID, nil
}

func (s *Store) PromoteCredential(agentID, hash string) error {
	return s.WithTx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`UPDATE agents SET credential_hash=? WHERE id=?`, hash, agentID); err != nil {
			return err
		}
		_, err := tx.Exec(`DELETE FROM agent_credential_overlap WHERE agent_id=?`, agentID)
		return err
	})
}

func (s *Store) MarkTargetAndJob(opID, agentID string, st protocol.TargetStatus, stage, msg string, retry bool, evidence map[string]any) error {
	if err := s.UpdateTarget(opID, agentID, st, stage, msg, "", retry, evidence); err != nil {
		return err
	}
	jobID, err := s.JobIDFor(opID, agentID)
	if err != nil {
		return nil
	}
	_, err = s.db().Exec(`UPDATE agent_jobs SET status=? WHERE job_id=?`, string(st), jobID)
	return err
}

// Only already selected targets may update migration progress. Never upsert
// membership from a device report, and never let backlog refresh this state.
func (s *Store) RecordMigrationStatus(agentID string, m *protocol.MigrationStatus, generation int64) error {
	if m == nil {
		return nil
	}
	switch m.State {
	case "prepared", "armed", "activating", "confirmed", "expired":
	default:
		return ErrConflict
	}
	var candidate, state string
	var expected int64
	err := s.db().QueryRow(`SELECT c.candidate_url,c.generation,t.state FROM controller_migrations c JOIN migration_targets t ON t.plan_id=c.id WHERE c.id=? AND t.agent_id=?`, m.PlanID, agentID).Scan(&candidate, &expected, &state)
	if err != nil {
		return err
	}
	if candidate != m.Candidate || expected != m.Generation || m.State == "confirmed" && generation != expected {
		return ErrConflict
	}
	if state == "retired" {
		return nil
	}
	reason := m.Reason
	if len(reason) > 512 {
		reason = reason[:512]
	}
	_, err = s.db().Exec(`UPDATE migration_targets SET state=?,reason=?,updated_at=? WHERE plan_id=? AND agent_id=?`, m.State, reason, s.now().UTC().Format(dbTimeFormat), m.PlanID, agentID)
	return err
}
func (s *Store) AgentMigrationStatus(agentID string) (*protocol.MigrationStatus, error) {
	m := &protocol.MigrationStatus{}
	err := s.db().QueryRow(`SELECT c.id,c.generation,c.candidate_url,t.state,COALESCE(t.reason,'') FROM controller_migrations c JOIN migration_targets t ON t.plan_id=c.id WHERE t.agent_id=? ORDER BY c.generation DESC LIMIT 1`, agentID).Scan(&m.PlanID, &m.Generation, &m.Candidate, &m.State, &m.Reason)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return m, err
}
