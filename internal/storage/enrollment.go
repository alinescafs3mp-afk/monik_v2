package storage

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/alinescafs3mp-afk/monik_v2/internal/secure"
)

// EnrollAgent binds the one-use code, identity, verifier and initial config in
// one transaction. A retry needs the same unexpired code AND persisted random
// credential; knowing an agent ID alone cannot recover or replace credentials.
func (s *Store) EnrollAgent(code string, row *AgentRow, verifier string, retryable bool) (bool, error) {
	replay := false
	err := s.WithTx(func(tx *sql.Tx) error {
		var expires string
		var consumed, consumer sql.NullString
		err := tx.QueryRow(`SELECT expires_at,consumed_at,consumed_by FROM enrollment_codes WHERE code_hash=?`, secure.HashToken(code)).Scan(&expires, &consumed, &consumer)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		now := s.now().Format(dbTimeFormat)
		if errors.Is(err, sql.ErrNoRows) || expires <= now {
			return fmt.Errorf("enrollment code invalid or expired")
		}
		if consumed.Valid {
			if !retryable || !consumer.Valid || consumer.String != row.ID {
				return fmt.Errorf("enrollment code already used")
			}
			var stored string
			var revoked int
			if err = tx.QueryRow(`SELECT credential_hash,revoked FROM agents WHERE id=?`, row.ID).Scan(&stored, &revoked); err != nil {
				return err
			}
			if revoked != 0 || stored != verifier {
				return fmt.Errorf("enrollment retry identity/proof mismatch")
			}
			replay = true
			return nil
		}
		ts := &Store{DB: s.DB, Clock: s.Clock, executor: tx}
		if err = ts.InsertAgent(row, verifier); err != nil {
			return fmt.Errorf("agent identity is already enrolled or unavailable: %w", err)
		}
		if _, err = tx.Exec(`INSERT INTO config_revisions(agent_id,revision,hash,body,created_at,actor) VALUES(?,?,?,?,?,?)`, row.ID, row.DesiredRevision, row.DesiredHash, row.DesiredConfig, now, "enrollment"); err != nil {
			return err
		}
		result, err := tx.Exec(`UPDATE enrollment_codes SET consumed_at=?,consumed_by=? WHERE code_hash=? AND consumed_at IS NULL`, now, row.ID, secure.HashToken(code))
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return ErrConflict
		}
		return nil
	})
	return replay, err
}
