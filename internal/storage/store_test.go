package storage

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/clock"
	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
)

func TestUserSessionIdempotency(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "m.db"), clock.Real{})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	u, err := st.CreateUser("owner", "hash", "owner")
	if err != nil {
		t.Fatal(err)
	}
	tok, sess, err := st.CreateSession(u, time.Hour, 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	got, err := st.SessionByToken(tok)
	if err != nil || got.Username != "owner" {
		t.Fatalf("%v %v", got, err)
	}
	op := &protocol.Operation{
		ID: "op1", Action: "preference.save", Status: protocol.OpQueued, Revision: 1,
		ClientRequestKey: "k1", Actor: sess.UserID, CreatedAt: time.Now().UTC(), Params: map[string]any{"a": 1},
		Targets: []protocol.TargetResult{{AgentID: "server", Status: protocol.TargetSucceeded, Stage: "commit"}},
	}
	h := RequestHash(op.Action, nil, []byte(`{"a":1}`))
	if err := st.InsertOperation(op, h); err != nil {
		t.Fatal(err)
	}
	again, err := st.LookupIdempotency(sess.UserID, "k1", h)
	if err != nil || again.ID != "op1" {
		t.Fatal(err)
	}
	if _, err := st.LookupIdempotency(sess.UserID, "k1", "other"); err != ErrIdempotencyConflict {
		t.Fatalf("want conflict, got %v", err)
	}
}

func TestEnrollmentConsumeOnce(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "m.db"), clock.Real{})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	code, _, err := st.CreateEnrollmentCode("owner", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.ConsumeEnrollmentCode(code); err != nil {
		t.Fatal(err)
	}
	if err := st.ConsumeEnrollmentCode(code); err == nil {
		t.Fatal("reuse")
	}
}
