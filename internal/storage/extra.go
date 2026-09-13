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
