package storage

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
	"github.com/alinescafs3mp-afk/monik_v2/internal/tufutil"
)

type ReleaseTrust struct {
	Revision int64
	Root     []byte
	Versions tufutil.HighWater
}

func (s *Store) ReleaseTrust() (ReleaseTrust, error) {
	var v ReleaseTrust
	var raw string
	e := s.db().QueryRow(`SELECT revision,root_json,versions_json FROM release_trust WHERE singleton=1`).Scan(&v.Revision, &v.Root, &raw)
	if errors.Is(e, sql.ErrNoRows) {
		return v, ErrNotFound
	}
	if e != nil {
		return v, e
	}
	if len(v.Root) == 0 || !json.Valid([]byte(raw)) || raw == "null" {
		return v, fmt.Errorf("corrupt committed update trust")
	}
	h, e := tufutil.DecodeHighWater([]byte(raw))
	if e != nil {
		return v, e
	}
	v.Versions = h
	if h.Root < 1 || h.Targets < 1 || h.Snapshot < 1 || h.Timestamp < 1 {
		return v, fmt.Errorf("invalid committed update versions")
	}
	return v, nil
}
func (s *Store) Publication(id string) (digest string, files []tufutil.PublicationFile, err error) {
	var raw string
	err = s.db().QueryRow(`SELECT object_digest,file_inventory FROM release_publications WHERE release_id=? AND format_version=1`, id).Scan(&digest, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	if err != nil {
		return
	}
	err = json.Unmarshal([]byte(raw), &files)
	if err == nil && (len(files) == 0 || !tufutil.ValidReleaseID(digest)) {
		err = fmt.Errorf("invalid release publication inventory")
	}
	return
}

// The files already exist under a content-addressed directory. Their visibility,
// trust high-water and successful operation outcome are committed together.
// A failed commit leaves at most unreferenced files, never a partly visible release.
func (s *Store) CommitPublication(p *tufutil.Publication, expected int64, opID string) error {
	if p == nil {
		return fmt.Errorf("missing publication")
	}
	r := p.Result
	if r == nil || r.ID != r.Digest || !tufutil.ValidReleaseID(r.ID) || !json.Valid([]byte(r.MetadataJSON)) {
		return fmt.Errorf("invalid publication")
	}
	inv, e := json.Marshal(p.Files)
	if e != nil {
		return e
	}
	vers, e := json.Marshal(p.HighWater)
	if e != nil {
		return e
	}
	plats, e := json.Marshal(r.Platforms)
	if e != nil {
		return e
	}
	return s.WithTx(func(tx *sql.Tx) error {
		var revision int64
		e := tx.QueryRow(`SELECT revision FROM release_trust WHERE singleton=1`).Scan(&revision)
		if !errors.Is(e, sql.ErrNoRows) && e != nil {
			return e
		}
		if revision != expected {
			return ErrConflict
		}
		var existing string
		e = tx.QueryRow(`SELECT object_digest FROM release_publications WHERE release_id=?`, r.ID).Scan(&existing)
		duplicate := e == nil
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		if duplicate && existing != r.Digest {
			return ErrConflict
		}
		if !duplicate {
			if _, e = tx.Exec(`INSERT INTO releases(id,version,digest,notes,metadata,imported_at,trust_ok,platforms) VALUES(?,?,?,?,?,?,1,?)`, r.ID, r.Version, r.Digest, r.Notes, r.MetadataJSON, s.now().Format(dbTimeFormat), string(plats)); e != nil {
				return e
			}
			for _, a := range r.Artifacts {
				if _, e = tx.Exec(`INSERT INTO release_artifacts(release_id,os,arch,name,sha256,length,path) VALUES(?,?,?,?,?,?,?)`, r.ID, a.OS, a.Arch, a.Name, a.SHA256, a.Length, a.Path); e != nil {
					return e
				}
			}
			if _, e = tx.Exec(`INSERT INTO release_publications(release_id,object_digest,file_inventory,expires_at,format_version) VALUES(?,?,?,?,1)`, r.ID, r.Digest, string(inv), p.ExpiresAt.UTC().Format(dbTimeFormat)); e != nil {
				return e
			}
		}
		if _, e = tx.Exec(`INSERT INTO release_trust(singleton,revision,root_json,versions_json) VALUES(1,1,?,?) ON CONFLICT(singleton) DO UPDATE SET revision=revision+1,root_json=excluded.root_json,versions_json=excluded.versions_json`, p.TrustedRoot, string(vers)); e != nil {
			return e
		}
		if opID != "" {
			if e = s.updateTargetTx(tx, opID, "server", protocol.TargetSucceeded, "verified_catalog_commit", "immutable release verified and published", "", false, map[string]any{"release_id": r.ID, "digest": r.Digest, "version": r.Version, "immutable": true, "already_imported": duplicate}); e != nil {
				return e
			}
			return s.refreshOperationTx(tx, opID)
		}
		return nil
	})
}

type PreparedUpdateTarget struct {
	AgentID        string
	Status         protocol.TargetStatus
	Stage, Message string
	Evidence       map[string]any
}

// PublishUpdatePlan makes every eligible target visible at once, together with
// the complete envelope and summary. A failed target write rolls back the plan.
func (s *Store) PublishUpdatePlan(opID string, plan []PreparedUpdateTarget) error {
	return s.WithTx(func(tx *sql.Tx) error {
		for _, p := range plan {
			var id, raw, action, previous string
			if e := tx.QueryRow(`SELECT job_id,envelope,action,status FROM agent_jobs WHERE operation_id=? AND agent_id=?`, opID, p.AgentID).Scan(&id, &raw, &action, &previous); e != nil {
				return e
			}
			if previous != "preparing" || (action != "update.rollout" && action != "update.rollback") {
				return ErrConflict
			}
			if p.Status != protocol.TargetQueued && p.Status != protocol.TargetWaitingOffline && p.Status != protocol.TargetUnsupported && p.Status != protocol.TargetRejected {
				return fmt.Errorf("invalid update plan state")
			}
			var env protocol.JobEnvelope
			if e := json.Unmarshal([]byte(raw), &env); e != nil {
				return e
			}
			if env.JobID != id || env.OperationID != opID || env.Action != action {
				return fmt.Errorf("invalid prepared job")
			}
			if env.Params == nil {
				env.Params = map[string]any{}
			}
			for k, v := range p.Evidence {
				env.Params[k] = v
			}
			b, e := json.Marshal(env)
			if e != nil {
				return e
			}
			if _, e = tx.Exec(`UPDATE agent_jobs SET envelope=?,status=? WHERE job_id=?`, string(b), string(p.Status), id); e != nil {
				return e
			}
			if e = s.updateTargetTx(tx, opID, p.AgentID, p.Status, p.Stage, p.Message, "", p.Status == protocol.TargetWaitingOffline, p.Evidence); e != nil {
				return e
			}
		}
		return s.refreshOperationTx(tx, opID)
	})
}
