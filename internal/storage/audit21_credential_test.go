package storage

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/clock"
)

func v21CredentialStore(t *testing.T) (*Store, string) {
	t.Helper()
	s := auditStore(t)
	current := strings.Repeat("a", 64)
	if err := s.InsertAgent(&AgentRow{ID: "v21-cred"}, current); err != nil {
		t.Fatal(err)
	}
	return s, current
}

func TestV21RegisterCredentialRevalidatesSnapshot(t *testing.T) {
	for _, kind := range []string{"revoked", "rotated", "deleted"} {
		t.Run(kind, func(t *testing.T) {
			s, current := v21CredentialStore(t)
			var err error
			switch kind {
			case "revoked":
				_, err = s.DB.Exec(`UPDATE agents SET revoked=1 WHERE id='v21-cred'`)
			case "rotated":
				_, err = s.DB.Exec(`UPDATE agents SET credential_hash=? WHERE id='v21-cred'`, strings.Repeat("b", 64))
			case "deleted":
				_, err = s.DB.Exec(`DELETE FROM agents WHERE id='v21-cred'`)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err = s.RegisterPendingCredential("v21-cred", current, strings.Repeat("c", 64), "job", s.now().Add(time.Minute)); err == nil {
				t.Fatal("outdated authenticator staged candidate")
			}
			var n int
			if err = s.DB.QueryRow(`SELECT count(*) FROM agent_credential_overlap`).Scan(&n); err != nil || n != 0 {
				t.Fatal(n, err)
			}
		})
	}
}

func TestV21RegisterCredentialRetryKeepsExpiryAndCannotReplaceLiveCandidate(t *testing.T) {
	s, current := v21CredentialStore(t)
	clk := clock.NewFake(time.Now())
	s.Clock = clk
	until := clk.Now().Add(time.Minute)
	hash := strings.Repeat("b", 64)
	expiry, already, e := s.RegisterPendingCredential("v21-cred", current, hash, "job", until)
	if e != nil || already || !expiry.Equal(until) {
		t.Fatal(expiry, already, e)
	}
	clk.Advance(10 * time.Second)
	expiry, already, e = s.RegisterPendingCredential("v21-cred", current, hash, "job", clk.Now().Add(time.Minute))
	if e != nil || already || !expiry.Equal(until) {
		t.Fatal("retry extended candidate lifetime", expiry, already, e)
	}
	for _, v := range []struct{ hash, job string }{{strings.Repeat("c", 64), "job"}, {hash, "other-job"}} {
		if _, _, e = s.RegisterPendingCredential("v21-cred", current, v.hash, v.job, clk.Now().Add(time.Minute)); !errors.Is(e, ErrConflict) {
			t.Fatal("live candidate replaced", e)
		}
	}
	if _, already, e = s.RegisterPendingCredential("v21-cred", current, current, "job", clk.Now().Add(time.Minute)); e != nil || !already {
		t.Fatal(already, e)
	}
	clk.Advance(time.Minute)
	if _, _, e = s.RegisterPendingCredential("v21-cred", current, strings.Repeat("c", 64), "job2", clk.Now().Add(time.Minute)); e != nil {
		t.Fatal("expired candidate cannot be replaced", e)
	}
}

func TestV21ConcurrentDifferentCandidatesCannotOverwriteWinner(t *testing.T) {
	s, current := v21CredentialStore(t)
	start := make(chan struct{})
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, hash := range []string{strings.Repeat("b", 64), strings.Repeat("c", 64)} {
		wg.Add(1)
		go func(hash string) {
			defer wg.Done()
			<-start
			_, _, err := s.RegisterPendingCredential("v21-cred", current, hash, "job", s.now().Add(time.Minute))
			results <- err
		}(hash)
	}
	close(start)
	wg.Wait()
	close(results)
	ok, conflict := 0, 0
	for err := range results {
		if err == nil {
			ok++
		} else if errors.Is(err, ErrConflict) {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	if ok != 1 || conflict != 1 {
		t.Fatal(ok, conflict)
	}
}

func TestV21CredentialWriteFailurePublishesNothing(t *testing.T) {
	s, current := v21CredentialStore(t)
	if _, e := s.DB.Exec(`CREATE TEMP TRIGGER v21_reject BEFORE INSERT ON agent_credential_overlap BEGIN SELECT RAISE(ABORT,'fixture'); END`); e != nil {
		t.Fatal(e)
	}
	if _, _, e := s.RegisterPendingCredential("v21-cred", current, strings.Repeat("b", 64), "job", s.now().Add(time.Minute)); e == nil {
		t.Fatal("write failure declared successful")
	}
	var n int
	if e := s.DB.QueryRow(`SELECT count(*) FROM agent_credential_overlap`).Scan(&n); e != nil || n != 0 {
		t.Fatal(n, e)
	}
}
