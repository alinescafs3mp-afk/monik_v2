package spark

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
	"github.com/alinescafs3mp-afk/monik_v2/internal/secure"
)

func TestDirectStartCancellationClosesOnlyObservedExactInstance(t *testing.T) {
	c, f, p := controlFixture(t)
	f.starting = true
	start := actionRequest(c, p, "direct-start", "start")
	if got := c.Execute(context.Background(), start); got.Status != protocol.TargetRunning {
		t.Fatalf("start=%+v", got)
	}
	stop := actionRequest(c, p, "distinct-stop", "stop")
	if got := c.Execute(context.Background(), stop); got.Status != protocol.TargetSucceeded {
		t.Fatalf("stop=%+v", got)
	}
	for _, reopen := range []bool{false, true} {
		if reopen {
			var err error
			c, err = OpenCoordinator(c.Policy, f)
			if err != nil {
				t.Fatal(err)
			}
			if err = c.Bind(c.Policy.AgentID, c.Policy.ControllerID, "session-a"); err != nil {
				t.Fatal(err)
			}
		}
		got := c.Execute(context.Background(), start)
		if got.Status != protocol.TargetFailed || got.ErrorCode != "superseded_by_stop" || got.Stage != "start_cancelled_after_stop" || got.Evidence["supervisor_state"] != "stopped" {
			t.Fatalf("cancelled direct start remained unresolved (reopen=%v): %+v", reopen, got)
		}
	}
	f.mu.Lock()
	f.starting = false
	f.mu.Unlock()
	if got := c.Execute(context.Background(), actionRequest(c, p, "fresh-start", "start")); got.Status != protocol.TargetSucceeded {
		t.Fatalf("fresh start=%+v", got)
	}
	if got := c.Execute(context.Background(), start); got.Status != protocol.TargetFailed {
		t.Fatalf("later live instance resurrected cancelled start: %+v", got)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.effects[p.ID+":start"] != 2 || f.effects[p.ID+":stop"] != 1 {
		t.Fatalf("effects=%v", f.effects)
	}
}

type cancelAdmissionSupervisor struct {
	*fakeSupervisor
	definitionCalls int
	finalDefinition func(Profile) (string, error)
}

func (s *cancelAdmissionSupervisor) Definition(_ context.Context, p Profile) (string, error) {
	s.definitionCalls++
	if s.definitionCalls == 2 && s.finalDefinition != nil {
		return s.finalDefinition(p)
	}
	return p.Target.DefinitionHash, nil
}

func TestDirectStartRejectedStopDoesNotPoisonLaterVerifiedStop(t *testing.T) {
	for _, kind := range []string{"final-definition-drift", "final-definition-expiry", "journal-path-failure"} {
		t.Run(kind, func(t *testing.T) {
			c, f, p := controlFixture(t)
			f.starting = true
			start := actionRequest(c, p, "start-before-rejected-stop", "start")
			if got := c.Execute(context.Background(), start); got.Status != protocol.TargetRunning {
				t.Fatalf("start=%+v", got)
			}
			stop := actionRequest(c, p, "known-rejected-stop", "stop")
			wrapped := &cancelAdmissionSupervisor{fakeSupervisor: f}
			originalPath := c.path
			want := ""
			switch kind {
			case "final-definition-drift":
				wrapped.finalDefinition = func(Profile) (string, error) { return "sha256:admission-drift", nil }
				want = "effective_definition_drift"
			case "final-definition-expiry":
				stop.Deadline = time.Now().Add(150 * time.Millisecond)
				wrapped.finalDefinition = func(p Profile) (string, error) {
					timer := time.NewTimer(max(time.Until(stop.Deadline), time.Millisecond))
					defer timer.Stop()
					<-timer.C
					return p.Target.DefinitionHash, nil
				}
				want = "dispatch_expired"
			case "journal-path-failure":
				c.path = t.TempDir()
				want = "journal_unavailable"
			}
			c.supervisor = wrapped
			if got := c.Execute(context.Background(), stop); got.Status != protocol.TargetRejected || got.ErrorCode != want {
				t.Fatalf("rejected stop=%+v want=%s", got, want)
			}
			c.mu.Lock()
			original := c.journal.Attempts[start.JobID]
			c.mu.Unlock()
			if original.StoppedByJobID != "" {
				t.Fatalf("known no-effect stop poisoned original: %+v", original)
			}
			f.mu.Lock()
			stopEffects := f.effects[p.ID+":stop"]
			f.mu.Unlock()
			if stopEffects != 0 {
				t.Fatalf("rejected stop applied %d effects", stopEffects)
			}
			c.path = originalPath
			c.supervisor = f
			if got := c.Execute(context.Background(), actionRequest(c, p, "verified-later-stop", "stop")); got.Status != protocol.TargetSucceeded {
				t.Fatalf("later stop=%+v", got)
			}
			if got := c.Execute(context.Background(), start); got.Status != protocol.TargetFailed || got.Stage != "start_cancelled_after_stop" {
				t.Fatalf("later verified stop could not cancel original: %+v", got)
			}
		})
	}
}

func TestDirectStartSharedLockRejectionDoesNotBindStop(t *testing.T) {
	c, f, p := controlFixture(t)
	p.Target.SharedLock = filepath.Join(t.TempDir(), "missing-private-lock")
	p.Revision = ProfileRevision(p)
	c.Policy.Profiles = []Profile{p}
	f.mu.Lock()
	f.instances[p.ID] = Instance{State: "starting", Identity: "pre-existing-start", Available: true}
	f.mu.Unlock()
	if _, err := c.Snapshot(context.Background()); err != nil {
		t.Fatal(err)
	}
	// A start waiting on an existing starting instance does not need a new
	// effect or shared lock. The later explicit stop still must admit its lock.
	start := actionRequest(c, p, "waiting-start", "start")
	if got := c.Execute(context.Background(), start); got.Status != protocol.TargetRunning {
		t.Fatalf("waiting start=%+v", got)
	}
	if got := c.Execute(context.Background(), actionRequest(c, p, "lock-rejected-stop", "stop")); got.ErrorCode != "shared_entrypoint_busy" {
		t.Fatalf("stop=%+v", got)
	}
	c.mu.Lock()
	original := c.journal.Attempts[start.JobID]
	c.mu.Unlock()
	if original.StoppedByJobID != "" || original.Stage == "done" {
		t.Fatalf("lock rejection poisoned start: %+v", original)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.effects) != 0 {
		t.Fatalf("lock rejection applied effects: %v", f.effects)
	}
}

func TestDirectStartStopJournalAmbiguityAndLateExpiryRemainRecoverable(t *testing.T) {
	for _, kind := range []string{"after-publication-error", "after-save-expiry", "expiry-rollback-write-error"} {
		t.Run(kind, func(t *testing.T) {
			c, f, p := controlFixture(t)
			f.starting = true
			start := actionRequest(c, p, "journal-start", "start")
			c.Execute(context.Background(), start)
			stop := actionRequest(c, p, "journal-denied-stop", "stop")
			if kind != "after-publication-error" {
				stop.Deadline = time.Now().Add(150 * time.Millisecond)
			}
			calls := 0
			c.writeJournal = func(path string, data []byte, mode os.FileMode) error {
				calls++
				if calls == 2 && kind == "expiry-rollback-write-error" {
					return errors.New("synthetic rollback write failure before publication")
				}
				if err := secure.AtomicWrite(path, data, mode); err != nil {
					return err
				}
				if calls == 1 {
					if kind == "after-publication-error" {
						return errors.New("synthetic error after journal publication")
					}
					timer := time.NewTimer(max(time.Until(stop.Deadline), time.Millisecond))
					defer timer.Stop()
					<-timer.C
				}
				return nil
			}
			got := c.Execute(context.Background(), stop)
			wantCode := "journal_unavailable"
			if kind == "after-save-expiry" {
				wantCode = "dispatch_expired"
			}
			if got.Status != protocol.TargetRejected || got.ErrorCode != wantCode || calls == 0 {
				t.Fatalf("no-effect save/expiry result=%+v", got)
			}
			f.mu.Lock()
			stopEffects := f.effects[p.ID+":stop"]
			f.mu.Unlock()
			if stopEffects != 0 {
				t.Fatal("save/expiry failed stop reached effect")
			}
			reopened, err := OpenCoordinator(c.Policy, f)
			if err != nil {
				t.Fatal(err)
			}
			if err = reopened.Bind(c.Policy.AgentID, c.Policy.ControllerID, "session-a"); err != nil {
				t.Fatal(err)
			}
			// An ambiguous durable old stop cannot be declared successful. A
			// separate exact newly admitted stop can still prove actual stopped.
			if got := reopened.Execute(context.Background(), start); got.Status == protocol.TargetFailed {
				t.Fatalf("unconfirmed old stop fabricated cancellation=%+v", got)
			}
			reopened.mu.Lock()
			priorLink := reopened.journal.Attempts[start.JobID].StoppedByJobID
			priorStop := reopened.journal.Attempts[stop.JobID]
			reopened.mu.Unlock()
			if kind != "after-save-expiry" && (priorLink != stop.JobID || priorStop.Stage != "effect_attempted" || priorStop.Receipt.Status != protocol.TargetUnknownResult) {
				t.Fatalf("ambiguous durable record silently discarded: link=%s stop=%+v", priorLink, priorStop)
			}
			wrapped := &cancelAdmissionSupervisor{fakeSupervisor: f, finalDefinition: func(Profile) (string, error) { return "sha256:later-admission-drift", nil }}
			reopened.supervisor = wrapped
			if got := reopened.Execute(context.Background(), actionRequest(reopened, p, "later-rejected-stop", "stop")); got.ErrorCode != "effective_definition_drift" {
				t.Fatalf("later denied stop=%+v", got)
			}
			reopened.mu.Lock()
			keptLink := reopened.journal.Attempts[start.JobID].StoppedByJobID
			reopened.mu.Unlock()
			if keptLink != priorLink {
				t.Fatal("rejected later stop cleared/replaced older uncertainty")
			}
			reopened.supervisor = f
			if got := reopened.Execute(context.Background(), actionRequest(reopened, p, "journal-verified-stop", "stop")); got.Status != protocol.TargetSucceeded {
				t.Fatalf("later exact stop=%+v", got)
			}
			if got := reopened.Execute(context.Background(), start); got.Status != protocol.TargetFailed || got.Stage != "start_cancelled_after_stop" {
				t.Fatalf("durable journal ambiguity poisoned later stop: %+v", got)
			}
			reopened.mu.Lock()
			retainedStop := reopened.journal.Attempts[stop.JobID]
			reopened.mu.Unlock()
			if retainedStop.Stage != priorStop.Stage || retainedStop.Receipt.Status != priorStop.Receipt.Status {
				t.Fatal("new proven stop fabricated old stop outcome")
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.effects[p.ID+":start"] != 1 || f.effects[p.ID+":stop"] != 1 {
				t.Fatalf("old effect replayed: %v", f.effects)
			}
		})
	}
}

func TestDirectStartCancellationReconcilesLostStopReplyWithoutReplay(t *testing.T) {
	c, f, p := controlFixture(t)
	f.starting = true
	start := actionRequest(c, p, "direct-start", "start")
	c.Execute(context.Background(), start)
	f.mu.Lock()
	f.failAfter = true
	f.mu.Unlock()
	stop := actionRequest(c, p, "distinct-stop", "stop")
	if got := c.Execute(context.Background(), stop); got.Status != protocol.TargetUnknownResult {
		t.Fatalf("stop=%+v", got)
	}
	if got := c.Execute(context.Background(), start); got.Status != protocol.TargetUnknownResult || got.Stage != "stop_outcome_unconfirmed" {
		t.Fatalf("start prematurely resolved=%+v", got)
	}
	reopened, err := OpenCoordinator(c.Policy, f)
	if err != nil {
		t.Fatal(err)
	}
	if err = reopened.Bind(c.Policy.AgentID, c.Policy.ControllerID, "session-a"); err != nil {
		t.Fatal(err)
	}
	// Keep the exact requests and deadlines. An old dispatch can reconcile its
	// durable outcome; it cannot gain a new effect lease after a broker restart.
	reopened.mu.Lock()
	a := reopened.journal.Attempts[stop.JobID]
	if !a.Request.Deadline.Equal(stop.Deadline) || !a.Request.IssuedAt.Equal(stop.IssuedAt) {
		t.Fatal("original lease changed on reopen")
	}
	reopened.mu.Unlock()
	if got := reopened.Execute(context.Background(), stop); got.Status != protocol.TargetSucceeded || got.Stage != "model_stopped" {
		t.Fatalf("stop reconciliation=%+v", got)
	}
	if got := reopened.Execute(context.Background(), start); got.Status != protocol.TargetFailed || got.Evidence["supervisor_state"] != "stopped" {
		t.Fatalf("start reconciliation=%+v", got)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.effects[p.ID+":start"] != 1 || f.effects[p.ID+":stop"] != 1 {
		t.Fatalf("effects replayed: %v", f.effects)
	}
}

func TestDirectStartStopCannotClaimUnknownOrForeignOutcome(t *testing.T) {
	for _, kind := range []string{"unknown-effect", "foreign-generation", "revision", "epoch", "session", "expired-stop", "unconfirmed-stop"} {
		t.Run(kind, func(t *testing.T) {
			c, f, p := controlFixture(t)
			f.starting = true
			start := actionRequest(c, p, "direct-start", "start")
			if kind == "unknown-effect" {
				f.failAfter = true
			}
			c.Execute(context.Background(), start)
			f.mu.Lock()
			f.failAfter = false
			if kind == "foreign-generation" {
				f.instances[p.ID] = Instance{State: "stopped", Identity: "foreign-stopped"}
			}
			if kind == "unknown-effect" {
				f.instances[p.ID] = Instance{State: "stopped", Identity: "stopped"}
			}
			f.mu.Unlock()
			if kind == "foreign-generation" {
				c.Snapshot(context.Background())
				f.mu.Lock()
				f.instances[p.ID] = Instance{State: "starting", Identity: "foreign-instance"}
				f.mu.Unlock()
				c.Snapshot(context.Background())
				// A newer observation on the old job must not rebind its original
				// dispatch to this later/manual generation.
				c.Execute(context.Background(), start)
			}
			if kind == "unconfirmed-stop" {
				f.mu.Lock()
				f.failAfter = true
				f.stopGate = make(chan struct{})
				f.mu.Unlock()
			}
			stop := actionRequest(c, p, "distinct-stop", "stop")
			switch kind {
			case "revision":
				stop.Params.ProfileRevision = "different"
			case "epoch":
				stop.Params.ControlEpoch = "different"
			case "session":
				stop.Params.ExpectedAgentSession = "different"
			case "expired-stop":
				stop.Deadline = time.Now().Add(-time.Second)
			}
			if kind == "unconfirmed-stop" {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				c.Execute(ctx, stop)
			} else {
				c.Execute(context.Background(), stop)
			}
			c.mu.Lock()
			original := c.journal.Attempts[start.JobID]
			c.mu.Unlock()
			if original.Stage == "done" || original.Receipt.ErrorCode == "superseded_by_stop" {
				t.Fatalf("%s fabricated cancelled terminal result: %+v", kind, original)
			}
		})
	}
}
