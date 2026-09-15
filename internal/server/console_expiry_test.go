package server

import (
	"github.com/alinescafs3mp-afk/monik_v2/internal/clock"
	"github.com/alinescafs3mp-afk/monik_v2/internal/storage"
	"testing"
	"time"
)

func TestV18ConsoleExpiryUsesInstantNotLexicalTimestamp(t *testing.T) {
	f := v18New(t)
	var s storage.Session
	if e := f.a.Store.DB.QueryRow(`SELECT id,user_id FROM admin_sessions LIMIT 1`).Scan(&s.ID, &s.UserID); e != nil {
		t.Fatal(e)
	}
	now := time.Date(2026, 9, 15, 16, 0, 0, 250000000, time.UTC)
	f.a.Clock = clock.NewFake(now)
	for _, tc := range []struct {
		expiry string
		valid  bool
	}{
		{"2026-09-15T16:00:00Z", false},
		{"2026-09-15T16:00:00.250Z", false},
		{"2026-09-15T16:00:01Z", true},
		{"2026-09-15T15:00:01-01:00", true},
		{"invalid", false},
	} {
		if _, e := f.a.Store.DB.Exec(`UPDATE admin_sessions SET expires_at=? WHERE id=?`, tc.expiry, s.ID); e != nil {
			t.Fatal(e)
		}
		if got := f.a.consoleOwnerCurrent(&s); got != tc.valid {
			t.Fatalf("expiry %q valid=%v want %v", tc.expiry, got, tc.valid)
		}
	}
}
