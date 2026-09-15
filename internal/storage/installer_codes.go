package storage

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/idgen"
	"github.com/alinescafs3mp-afk/monik_v2/internal/secure"
)

// CreateInstallerCode authorizes exactly one ordinary enrollment, not arbitrary
// admission. The verifier and credential-free audit commit together. The secret
// exists only in the returned prepared executable and expires after one hour.
func (s *Store) CreateInstallerCode(actor, build, platform string) (string, time.Time, error) {
	secret, e := idgen.Secret(16)
	if e != nil {
		return "", time.Time{}, e
	}
	code := strings.ToUpper(secret)
	expires := s.now().Add(time.Hour)
	e = s.WithTx(func(tx *sql.Tx) error {
		now := s.now().Format(dbTimeFormat)
		var pending int
		if e := tx.QueryRow(`SELECT COUNT(*) FROM enrollment_codes WHERE created_by=? AND consumed_at IS NULL AND expires_at>?`, actor, now).Scan(&pending); e != nil {
			return e
		}
		if pending >= 32 {
			return fmt.Errorf("too many unused enrollment profiles; allow them to expire or finish existing installations")
		}
		if _, e := tx.Exec(`INSERT INTO enrollment_codes(id,code_hash,created_by,created_at,expires_at) VALUES(?,?,?,?,?)`, idgen.New(), secure.HashToken(code), actor, now, expires.Format(dbTimeFormat)); e != nil {
			return e
		}
		_, e := tx.Exec(`INSERT INTO audit_events(at,actor,action,entity,detail) VALUES(?,?,?,?,?)`, now, actor, "installer.download", platform, "single-machine, expires="+expires.Format(time.RFC3339)+", build="+build)
		return e
	})
	if e != nil {
		return "", time.Time{}, e
	}
	return code, expires, nil
}
