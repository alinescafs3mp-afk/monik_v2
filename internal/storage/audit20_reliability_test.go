package storage

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
)

func TestV20SetupRollsBackEveryPublicationStep(t *testing.T) {
	for _, stage := range []string{"settings", "admin_users", "audit_events"} {
		t.Run(stage, func(t *testing.T) {
			s := auditStore(t)
			if _, e := s.DB.Exec(`CREATE TRIGGER v20_setup_failure BEFORE INSERT ON ` + stage + ` BEGIN SELECT RAISE(ABORT,'injected setup failure'); END`); e != nil {
				t.Fatal(e)
			}
			if e := s.CommitInitialSetup("owner", "hash", "https://localhost:8777", "127.0.0.1:0"); e == nil {
				t.Fatal("injection did not fail")
			}
			n, e := s.UserCount()
			if e != nil || n != 0 {
				t.Fatal("partial owner", n, e)
			}
			for _, k := range []string{"advertised_url", "listen"} {
				if _, e = s.Setting(k); !errors.Is(e, ErrNotFound) {
					t.Fatal("partial setting", k, e)
				}
			}
			if _, e = s.DB.Exec(`DROP TRIGGER v20_setup_failure`); e != nil {
				t.Fatal(e)
			}
			if e = s.CommitInitialSetup("owner", "hash", "https://localhost:8777", "127.0.0.1:0"); e != nil {
				t.Fatal(e)
			}
			if e = s.CommitInitialSetup("other", "hash", "https://other.invalid", "127.0.0.1:0"); e == nil {
				t.Fatal("second initial owner accepted")
			}
		})
	}
}

func TestV20PromotionIsIdempotentAndPreservesNewOverlap(t *testing.T) {
	s := auditStore(t)
	auditAgent(t, s, "h")
	candidate, next := strings.Repeat("c", 64), strings.Repeat("d", 64)
	if e := s.SetPendingCredential("h", candidate, "job1", s.now().Add(time.Hour)); e != nil {
		t.Fatal(e)
	}
	errors := make(chan error, 12)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); errors <- s.PromoteCredential("h", candidate) }()
	}
	wg.Wait()
	close(errors)
	for e := range errors {
		if e != nil {
			t.Fatal(e)
		}
	}
	if e := s.SetPendingCredential("h", next, "job2", s.now().Add(time.Hour)); e != nil {
		t.Fatal(e)
	}
	if e := s.PromoteCredential("h", candidate); e != nil {
		t.Fatal(e)
	}
	got, job, e := s.PendingCredential("h")
	if e != nil || got != next || job != "job2" {
		t.Fatal("idempotent promotion consumed newer overlap", e)
	}
	if e = s.PromoteCredential("h", next); e != nil {
		t.Fatal(e)
	}
	a, e := s.Agent("h")
	if e != nil || a.CredentialHash != next {
		t.Fatal("next rotation failed", e)
	}
}

func TestV20RevocationPublishesFrozenBatchAtomically(t *testing.T) {
	for _, stage := range []string{"second_agent", "result", "aggregate", "event"} {
		t.Run(stage, func(t *testing.T) {
			s := auditStore(t)
			op := &protocol.Operation{ID: "revoke-batch", Action: "credential.revoke", Status: protocol.OpQueued, Revision: 1, ClientRequestKey: "revoke-key", Actor: "owner", CreatedAt: s.now(), Params: map[string]any{}}
			before := map[string]string{}
			for _, id := range []string{"one", "two"} {
				auditAgent(t, s, id)
				ag, e := s.Agent(id)
				if e != nil {
					t.Fatal(e)
				}
				before[id] = ag.CredentialHash
				if e = s.SetPendingCredential(id, "pending-"+id, "rotation-"+id, s.now().Add(time.Hour)); e != nil {
					t.Fatal(e)
				}
				op.Targets = append(op.Targets, protocol.TargetResult{AgentID: id, Status: protocol.TargetQueued})
			}
			if e := s.InsertOperation(op, "request-hash"); e != nil {
				t.Fatal(e)
			}
			var condition string
			switch stage {
			case "second_agent":
				condition = "BEFORE UPDATE OF revoked ON agents WHEN OLD.id='two'"
			case "result":
				condition = "BEFORE UPDATE ON operation_targets"
			case "aggregate":
				condition = "BEFORE UPDATE ON operations"
			case "event":
				condition = "BEFORE INSERT ON event_log"
			}
			if _, e := s.DB.Exec("CREATE TRIGGER v20_revoke_tx " + condition + " BEGIN SELECT RAISE(ABORT,'injected revoke publication failure'); END"); e != nil {
				t.Fatal(e)
			}
			if e := s.RevokeOperationAgents(op.ID); e == nil {
				t.Fatal("fault not detected")
			}
			for id, hash := range before {
				ag, e := s.Agent(id)
				if e != nil || ag.Revoked || ag.CredentialHash != hash {
					t.Fatal("partial revocation", id, e)
				}
				pending, _, e := s.PendingCredential(id)
				if e != nil || pending != "pending-"+id {
					t.Fatal("partial overlap deletion", id, e)
				}
			}
			got, e := s.Operation(op.ID)
			if e != nil {
				t.Fatal(e)
			}
			for _, target := range got.Targets {
				if target.Status == protocol.TargetSucceeded {
					t.Fatal("false success")
				}
			}
			if _, e = s.DB.Exec("DROP TRIGGER v20_revoke_tx"); e != nil {
				t.Fatal(e)
			}
			if e = s.RevokeOperationAgents(op.ID); e != nil {
				t.Fatal(e)
			}
			for _, id := range []string{"one", "two"} {
				ag, e := s.Agent(id)
				if e != nil || !ag.Revoked || ag.CredentialHash != "" {
					t.Fatal("revocation not applied", e)
				}
				if _, _, e = s.PendingCredential(id); e == nil {
					t.Fatal("pending credential remained")
				}
			}
			got, e = s.Operation(op.ID)
			if e != nil || got.Status != protocol.OpCompleted {
				t.Fatal("successful revoke not published", e)
			}
		})
	}
}
