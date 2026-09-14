package storage

import "database/sql"

// BrowserSessions never returns bearer tokens, token hashes or CSRF material.
func (s *Store) BrowserSessions(user, current string) ([]map[string]any, error) {
	rows, err := s.db().Query(`SELECT id,created_at,expires_at FROM admin_sessions WHERE user_id=? AND expires_at>? ORDER BY created_at DESC LIMIT 101`, user, s.now().UTC().Format(dbTimeFormat))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, created, expires string
		if err = rows.Scan(&id, &created, &expires); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"current": id == current, "created_at": created, "expires_at": expires})
	}
	return out, rows.Err()
}
func (s *Store) RevokeOtherSessions(user, current, actor string) (int64, error) {
	var count int64
	err := s.WithTx(func(tx *sql.Tx) error {
		var owner string
		if err := tx.QueryRow(`SELECT user_id FROM admin_sessions WHERE id=? AND expires_at>?`, current, s.now().UTC().Format(dbTimeFormat)).Scan(&owner); err != nil {
			return err
		}
		if owner != user {
			return ErrConflict
		}
		r, err := tx.Exec(`DELETE FROM admin_sessions WHERE user_id=? AND id<>?`, user, current)
		if err != nil {
			return err
		}
		count, err = r.RowsAffected()
		if err != nil {
			return err
		}
		_, err = tx.Exec(`INSERT INTO audit_events(at,actor,action,entity,detail) VALUES(?,?,'session.revoke_others',?,'other browser sessions revoked; agent credentials unchanged')`, s.now().UTC().Format(dbTimeFormat), actor, user)
		return err
	})
	return count, err
}
