package storage

import (
	"database/sql"
	"fmt"
	"time"
)

// ChangePassword is compare-and-swap against the hash that was actually verified.
// Credential replacement and revocation of ALL browser sessions are one commit.
func (s *Store) ChangePassword(userID, expected, replacement, actor string) error {
	if userID == "" || expected == "" || replacement == "" || expected == replacement {
		return fmt.Errorf("invalid password replacement")
	}
	return s.WithTx(func(tx *sql.Tx) error {
		result, err := tx.Exec(`UPDATE admin_users SET password_hash=? WHERE id=? AND password_hash=?`, replacement, userID, expected)
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
		if _, err = tx.Exec(`DELETE FROM admin_sessions WHERE user_id=?`, userID); err != nil {
			return err
		}
		_, err = tx.Exec(`INSERT INTO audit_events(at,actor,action,entity,detail) VALUES(?,?,'account.password.change',?,'all browser sessions revoked; agent credentials unchanged')`, s.now().Format(dbTimeFormat), actor, userID)
		return err
	})
}

// SessionStillValid rechecks sessions that may have waited for the control lock.
func (s *Store) SessionStillValid(id string) error {
	var n int
	if err := s.db().QueryRow(`SELECT EXISTS(SELECT 1 FROM admin_sessions WHERE id=? AND expires_at>?)`, id, s.now().Format(dbTimeFormat)).Scan(&n); err != nil {
		return err
	}
	if n != 1 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) EventBounds() (min, max int64, err error) {
	err = s.db().QueryRow(`SELECT COALESCE((SELECT id FROM event_log ORDER BY id LIMIT 1),0),COALESCE((SELECT id FROM event_log ORDER BY id DESC LIMIT 1),0)`).Scan(&min, &max)
	return
}

// StorageDiagnostics is bounded metadata, NOT an expensive full-table COUNT.
func (s *Store) StorageDiagnostics(rawRetention time.Duration) (map[string]any, error) {
	out := map[string]any{"retention_target_hours": rawRetention.Hours(), "raw_aggregation_implemented": false}
	var mode string
	var syncMode int
	if err := s.db().QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil {
		return nil, err
	}
	if err := s.db().QueryRow(`PRAGMA synchronous`).Scan(&syncMode); err != nil {
		return nil, err
	}
	out["journal_mode"], out["synchronous"] = mode, syncMode
	for _, table := range []string{"host_samples", "service_observations"} {
		var first, last sql.NullString
		if err := s.db().QueryRow(`SELECT (SELECT observed_at FROM `+table+` ORDER BY observed_at LIMIT 1),(SELECT observed_at FROM `+table+` ORDER BY observed_at DESC LIMIT 1)`).Scan(&first, &last); err != nil {
			return nil, err
		}
		lag := 0.0
		if first.Valid {
			at, e := time.Parse(time.RFC3339Nano, first.String)
			if e != nil {
				return nil, e
			}
			lag = s.now().Add(-rawRetention).Sub(at).Seconds()
			if lag < 0 {
				lag = 0
			}
		}
		out[table] = map[string]any{"earliest": first.String, "latest": last.String, "retention_lag_seconds": lag, "bounds_do_not_prove_continuous_coverage": true}
	}
	var lagged int
	if err := s.db().QueryRow(`SELECT COUNT(*) FROM agents WHERE desired_revision<>applied_revision AND revoked=0`).Scan(&lagged); err != nil {
		return nil, err
	}
	out["agents_with_configuration_lag"] = lagged
	min, max, err := s.EventBounds()
	if err != nil {
		return nil, err
	}
	out["events"] = map[string]any{"first_retained_id": min, "last_retained_id": max}
	return out, nil
}
