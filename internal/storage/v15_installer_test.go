package storage

import (
	"strings"
	"testing"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/clock"
)

func TestV15InstallerCodeAndAuditAreAtomic(t *testing.T) {
	s, e := Open(t.TempDir()+"/test.db", clock.Real{})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if _, e = s.DB.Exec(`CREATE TRIGGER fail_installer_audit BEFORE INSERT ON audit_events BEGIN SELECT RAISE(ABORT,'audit unavailable'); END`); e != nil {
		t.Fatal(e)
	}
	code, _, e := s.CreateInstallerCode("owner", "fixture", "linux-amd64")
	if e == nil || code != "" {
		t.Fatal("reported success", e)
	}
	var n int
	if e = s.DB.QueryRow(`SELECT COUNT(*) FROM enrollment_codes`).Scan(&n); e != nil || n != 0 {
		t.Fatal("code committed without audit", n, e)
	}
	if _, e = s.DB.Exec(`DROP TRIGGER fail_installer_audit`); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 32; i++ {
		code, expiry, e := s.CreateInstallerCode("owner", "fixture", "linux-amd64")
		if e != nil || len(code) != 32 || strings.ToUpper(code) != code || time.Until(expiry) > 61*time.Minute {
			t.Fatal(i, e)
		}
	}
	if _, _, e = s.CreateInstallerCode("owner", "fixture", "linux-amd64"); e == nil {
		t.Fatal("unused code quota not enforced")
	}
}
