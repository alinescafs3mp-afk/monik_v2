package storage

import (
	"database/sql"
	"errors"
	"fmt"
	"github.com/alinescafs3mp-afk/monik_v2/internal/idgen"
	"strings"
)

// Initialize identity only in a genuinely empty controller, never as recovery
// from a failed settings read or a missing row on an enrolled deployment.
func (s *Store) EnsureControllerIdentity() error {
	return s.WithTx(func(tx *sql.Tx) error {
		var id string
		err := tx.QueryRow(`SELECT value FROM settings WHERE key='controller_id'`).Scan(&id)
		if err == nil {
			if strings.TrimSpace(id) == "" {
				return fmt.Errorf("controller identity is empty; restore protected state")
			}
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		var existing int
		if err := tx.QueryRow(`SELECT (SELECT COUNT(*) FROM agents)+(SELECT COUNT(*) FROM admin_users)`).Scan(&existing); err != nil {
			return err
		}
		if existing != 0 {
			return fmt.Errorf("controller identity is missing on an existing deployment; restore protected state")
		}
		_, err = tx.Exec(`INSERT INTO settings(key,value,revision) VALUES('controller_id',?,1)`, idgen.New())
		return err
	})
}
