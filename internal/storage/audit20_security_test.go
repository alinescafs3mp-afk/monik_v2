package storage

import (
	"github.com/alinescafs3mp-afk/monik_v2/internal/clock"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestV20CredentialPromotionRevalidatesOverlap(t *testing.T) {
	for _, kind := range []string{"superseded", "expired", "revoked", "missing"} {
		t.Run(kind, func(t *testing.T) {
			s := auditStore(t)
			auditAgent(t, s, "h")
			before, e := s.Agent("h")
			if e != nil {
				t.Fatal(e)
			}
			old, next := strings.Repeat("a", 64), strings.Repeat("b", 64)
			if kind != "missing" {
				until := s.now().Add(time.Hour)
				if kind == "expired" {
					until = s.now().Add(-time.Second)
				}
				if e := s.SetPendingCredential("h", old, "j1", until); e != nil {
					t.Fatal(e)
				}
			}
			if kind == "superseded" {
				if e := s.SetPendingCredential("h", next, "j2", s.now().Add(time.Hour)); e != nil {
					t.Fatal(e)
				}
			}
			if kind == "revoked" {
				if _, e := s.DB.Exec(`UPDATE agents SET revoked=1 WHERE id='h'`); e != nil {
					t.Fatal(e)
				}
			}
			if e := s.PromoteCredential("h", old); e == nil {
				t.Error("invalid pending credential promoted")
			}
			after, e := s.Agent("h")
			if e != nil || after.CredentialHash != before.CredentialHash {
				t.Error("rejected promotion changed active credential", e)
			}
			if kind == "superseded" {
				hash, job, e := s.PendingCredential("h")
				if e != nil || hash != next || job != "j2" {
					t.Error("stale promotion deleted newer pending credential", e)
				}
			}
		})
	}
}

func TestV20CredentialExpiryIsExclusive(t *testing.T) {
	now := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	s, e := Open(filepath.Join(t.TempDir(), "db"), clock.NewFake(now))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	auditAgent(t, s, "h")
	if e = s.SetPendingCredential("h", strings.Repeat("a", 64), "j", now); e != nil {
		t.Fatal(e)
	}
	if _, _, e = s.PendingCredential("h"); e == nil {
		t.Fatal("overlap accepted exactly at expiry")
	}
}
