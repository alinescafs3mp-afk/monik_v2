package storage

import (
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

// RegisterPendingCredential checks the still-current authenticator and overlap
// inside the same serialized transaction. A delayed request cannot stage a key
// after revocation/rotation or overwrite another live candidate. The legacy
// SetPendingCredential setter is retained for trusted fixtures, not HTTP input.
func (s *Store) RegisterPendingCredential(agentID, expectedCurrent, verifier, jobID string, until time.Time) (expires time.Time, alreadyCurrent bool, err error) {
	if agentID == "" || expectedCurrent == "" || jobID == "" || len(jobID) > 128 || strings.TrimSpace(jobID) != jobID || len(verifier) != 64 || verifier != strings.ToLower(verifier) {
		return expires, false, ErrConflict
	}
	if _, e := hex.DecodeString(verifier); e != nil {
		return expires, false, ErrConflict
	}
	err = s.WithTx(func(tx *sql.Tx) error {
		now := s.now()
		if !now.Before(until) {
			return ErrConflict
		}
		var current string
		var revoked bool
		if e := tx.QueryRow(`SELECT credential_hash,revoked FROM agents WHERE id=?`, agentID).Scan(&current, &revoked); e != nil {
			if errors.Is(e, sql.ErrNoRows) {
				return ErrNotFound
			}
			return e
		}
		if revoked || current != expectedCurrent {
			return ErrConflict
		}
		if verifier == current {
			alreadyCurrent = true
			return nil
		}
		var pending, job, expiration string
		e := tx.QueryRow(`SELECT pending_hash,job_id,expires_at FROM agent_credential_overlap WHERE agent_id=?`, agentID).Scan(&pending, &job, &expiration)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		if e == nil {
			previous, parseErr := time.Parse(time.RFC3339Nano, expiration)
			if parseErr != nil {
				return ErrConflict
			}
			if now.Before(previous) {
				if pending != verifier || job != jobID {
					return ErrConflict
				}
				expires = previous // An identical retry must not extend the overlap lifetime.
				return nil
			}
		}
		_, e = tx.Exec(`INSERT INTO agent_credential_overlap(agent_id,pending_hash,job_id,expires_at,created_at) VALUES(?,?,?,?,?) ON CONFLICT(agent_id) DO UPDATE SET pending_hash=excluded.pending_hash,job_id=excluded.job_id,expires_at=excluded.expires_at,created_at=excluded.created_at`, agentID, verifier, jobID, until.UTC().Format(dbTimeFormat), now.UTC().Format(dbTimeFormat))
		if e == nil {
			expires = until
		}
		return e
	})
	return
}
