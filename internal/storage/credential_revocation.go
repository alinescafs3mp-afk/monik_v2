package storage

import (
	"database/sql"

	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
)

// RevokeOperationAgents changes a frozen set and publishes its results together.
// A storage failure rolls back every eligible revocation, including pending
// rotations; the caller can then report a failed attempt without inventing success.
func (s *Store) RevokeOperationAgents(opID string) error {
	return s.WithTx(func(tx *sql.Tx) error {
		var action string
		if e := tx.QueryRow(`SELECT action FROM operations WHERE id=?`, opID).Scan(&action); e != nil {
			return e
		}
		if action != "credential.revoke" {
			return ErrConflict
		}
		targets, e := targetsWith(tx, opID)
		if e != nil {
			return e
		}
		for _, target := range targets {
			if target.AgentID == "server" || target.Status == protocol.TargetRejected {
				continue
			}
			res, e := tx.Exec(`UPDATE agents SET revoked=1,credential_hash='' WHERE id=?`, target.AgentID)
			if e != nil {
				return e
			}
			n, e := res.RowsAffected()
			if e != nil {
				return e
			}
			if n != 1 {
				return ErrNotFound
			}
			if _, e = tx.Exec(`DELETE FROM agent_credential_overlap WHERE agent_id=?`, target.AgentID); e != nil {
				return e
			}
			if e = s.updateTargetTx(tx, opID, target.AgentID, protocol.TargetSucceeded, "revoked", "Credential revoked on controller; live channels close on their next authorization check", "", false, nil); e != nil {
				return e
			}
		}
		if len(targets) == 0 {
			return ErrNotFound
		}
		return s.refreshOperationTx(tx, opID)
	})
}
