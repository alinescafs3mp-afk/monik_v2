package storage

import (
	"database/sql"
	"fmt"
	"github.com/alinescafs3mp-afk/monik_v2/internal/idgen"
)

// CommitInitialSetup publishes the first owner, controller settings and audit
// together. TLS files are prepared separately and are not a database transaction.
func (s *Store) CommitInitialSetup(username, passwordHash, advertised, listen string) error {
	return s.WithTx(func(tx *sql.Tx) error {
		var n int
		if e := tx.QueryRow(`SELECT count(*) FROM admin_users`).Scan(&n); e != nil {
			return e
		}
		if n != 0 {
			return fmt.Errorf("setup already completed")
		}
		for _, v := range [][2]string{{"advertised_url", advertised}, {"listen", listen}} {
			if _, e := tx.Exec(`INSERT INTO settings(key,value,revision) VALUES(?,?,1) ON CONFLICT(key) DO UPDATE SET value=excluded.value,revision=revision+1`, v[0], v[1]); e != nil {
				return e
			}
		}
		at := s.now().UTC().Format(dbTimeFormat)
		if _, e := tx.Exec(`INSERT INTO admin_users(id,username,password_hash,role,created_at) VALUES(?,?,?,'owner',?)`, idgen.New(), username, passwordHash, at); e != nil {
			return e
		}
		_, e := tx.Exec(`INSERT INTO audit_events(at,actor,action,entity,detail) VALUES(?,?,'setup','server','initial owner created')`, at, username)
		return e
	})
}
