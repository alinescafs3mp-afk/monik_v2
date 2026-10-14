package spark

import (
	"context"
	"testing"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
)

func TestExistingLogsRetentionMissingBackendReceiptNeverResubmitsOriginal(t *testing.T) {
	c, f, p := existingFixture(t)
	f.lost = true
	r := actionRequest(c, p, "retention-original", "start")
	if got := c.Execute(context.Background(), r); got.Status != protocol.TargetUnknownResult {
		t.Fatal(got)
	}
	f.mu.Lock()
	original := f.submitted[0]
	// A completed backend receipt can expire before a disconnected Go broker
	// learns the result. Receipt lookup then reports missing. Even with a still
	// valid browser deadline Go must preserve its journal and never submit again.
	delete(f.receipts, original.OperationID)
	f.mu.Unlock()
	before := c.journal.Attempts[r.JobID]
	reopened, e := OpenCoordinator(c.Policy, f)
	if e != nil {
		t.Fatal(e)
	}
	if e = reopened.Bind(c.Policy.AgentID, c.Policy.ControllerID, "session-after-retention"); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 3; i++ {
		got := reopened.Execute(context.Background(), r)
		if got.Status != protocol.TargetUnknownResult {
			t.Fatal(got)
		}
	}
	f.mu.Lock()
	submits, lookups := len(f.submitted), append([]existingRequest(nil), f.looked...)
	f.mu.Unlock()
	if submits != 1 || len(lookups) != 3 {
		t.Fatalf("retired request resubmitted: submits=%d lookups=%d", submits, len(lookups))
	}
	for _, looked := range lookups {
		if digest(looked) != digest(original) {
			t.Fatal("original immutable request changed")
		}
	}
	if digest(before) != digest(reopened.journal.Attempts[r.JobID]) {
		t.Fatal("missing receipt changed durable original")
	}
	changed := r
	changed.Deadline = changed.Deadline.Add(time.Millisecond)
	got := reopened.Execute(context.Background(), changed)
	f.mu.Lock()
	submits = len(f.submitted)
	f.mu.Unlock()
	if got.ErrorCode != "idempotency_conflict" || submits != 1 {
		t.Fatal("changed original digest admitted", got)
	}
}
