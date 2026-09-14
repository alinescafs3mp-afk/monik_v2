package storage

import "database/sql"

// SetServicePinned changes presentation only, never the check or agent settings.
func (s *Store) SetServicePinned(id string, pinned bool, expected *bool) error {
	return s.WithTx(func(tx *sql.Tx) error {
		var old int
		if err := tx.QueryRow(`SELECT pinned FROM services WHERE id=?`, id).Scan(&old); err != nil {
			if err == sql.ErrNoRows {
				return ErrNotFound
			}
			return err
		}
		if expected != nil && (old == 1) != *expected {
			return ErrConflict
		}
		value := 0
		if pinned {
			value = 1
		}
		_, err := tx.Exec(`UPDATE services SET pinned=? WHERE id=?`, value, id)
		return err
	})
}
