package spark

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

type existingRoundFake struct {
	*existingFake
	batchCalls, inspectCalls, exactCalls int
	mutate                               func([]existingSnapshot)
	err                                  error
	beforeInspect                        func()
}

func (f *existingRoundFake) Inspect(ctx context.Context, p Profile) (Instance, error) {
	f.inspectCalls++
	if f.beforeInspect != nil {
		f.beforeInspect()
	}
	return f.existingFake.Inspect(ctx, p)
}

func (f *existingRoundFake) ExistingObserve(ctx context.Context, p Profile) (existingSnapshot, error) {
	f.exactCalls++
	return f.existingFake.ExistingObserve(ctx, p)
}

func (f *existingRoundFake) ExistingObserveMany(ctx context.Context, profiles []Profile) ([]existingSnapshot, error) {
	f.batchCalls++
	if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 5*time.Second {
		return nil, fmt.Errorf("observation round has no bounded deadline")
	}
	snapshots := make([]existingSnapshot, len(profiles))
	for index, p := range profiles {
		s, _ := f.existingFake.ExistingObserve(ctx, p)
		s.ModelID = p.ID
		s.ProfileRevision = p.Target.Existing.BackendRevision
		snapshots[index] = s
	}
	if f.mutate != nil {
		f.mutate(snapshots)
	}
	return snapshots, f.err
}

func existingRoundFixture(t *testing.T) (*Coordinator, *existingRoundFake, []Profile) {
	t.Helper()
	c, base, first := existingFixture(t)
	profiles := make([]Profile, 7)
	for index, id := range existingIDs[:7] {
		p := first
		p.ID = id
		a := *first.Target.Existing
		a.CodePins = nil
		p.Target.Existing = &a
		for _, path := range existingRequiredPaths(p) {
			a.CodePins = append(a.CodePins, ExistingCodePin{path, strings.Repeat("b", 64)})
		}
		p.Target.DefinitionHash = digest(&a)
		p.Revision = ProfileRevision(p)
		profiles[index] = p
	}
	c.Policy.Profiles = profiles
	f := &existingRoundFake{existingFake: base}
	c.supervisor = f
	return c, f, profiles
}

func TestExistingSnapshotSevenProfilesOneObservationRound(t *testing.T) {
	c, f, profiles := existingRoundFixture(t)
	report, err := c.Snapshot(context.Background())
	if err != nil || !report.Inventory.Complete || len(report.States) != 7 {
		t.Fatalf("batch report: %+v %v", report, err)
	}
	if f.batchCalls != 1 || f.inspectCalls != 0 || f.exactCalls != 0 {
		t.Fatalf("helper boundaries: batch=%d inspect=%d exact=%d", f.batchCalls, f.inspectCalls, f.exactCalls)
	}
	for index, p := range profiles {
		if report.States[index].ProfileID != p.ID || report.States[index].SupervisorState != "stopped" || c.journal.Profiles[p.ID].BackendObservedAtMS == 0 {
			t.Fatalf("member not independently observed: %+v", report.States[index])
		}
		if report.States[index].ObservedAt.UnixMilli() != c.journal.Profiles[p.ID].BackendObservedAtMS {
			t.Fatal("exported timestamp does not identify the real backend observation")
		}
	}
	// A new round must reach the transport again; no status/identity cache exists.
	f.mutate = func(s []existingSnapshot) { s[0].State = "FAILED" }
	report, err = c.Snapshot(context.Background())
	if err != nil || f.batchCalls != 2 || report.States[0].SupervisorState != "failed" {
		t.Fatalf("reused old round: %+v %v", report, err)
	}
	t.Log("seven profiles: observation helper invocations per Snapshot 7 -> 1; subsequent Snapshot invokes one fresh round")
}

func TestExistingSnapshotInvalidRoundFailsClosedWithoutRetry(t *testing.T) {
	for _, failure := range []string{"transport", "wrong-id", "wrong-revision", "stale", "future", "partial"} {
		t.Run(failure, func(t *testing.T) {
			c, f, _ := existingRoundFixture(t)
			before := digest(c.journal.Profiles)
			if failure == "transport" {
				f.err = fmt.Errorf("bounded helper failed")
			} else {
				f.mutate = func(s []existingSnapshot) {
					switch failure {
					case "wrong-id":
						s[6].ModelID = s[0].ModelID
					case "wrong-revision":
						s[6].ProfileRevision = strings.Repeat("f", 64)
					case "stale":
						s[0].ObservedAtMS = time.Now().Add(-6 * time.Second).UnixMilli()
					case "future":
						s[0].ObservedAtMS = time.Now().Add(2 * time.Second).UnixMilli()
					case "partial":
						s[6] = existingSnapshot{}
					}
				}
			}
			report, err := c.Snapshot(context.Background())
			if err != nil || report.Inventory.Complete || f.batchCalls != 1 || f.inspectCalls != 0 || digest(c.journal.Profiles) != before {
				t.Fatalf("invalid round advanced state or retried: %+v %v", report, err)
			}
			for _, state := range report.States {
				if state.SupervisorState != "unknown" || state.Quality != "error" {
					t.Fatalf("invalid round member accepted: %+v", state)
				}
			}
		})
	}
}

func TestExistingObservationRoundNeverReusedForEffect(t *testing.T) {
	c, f, profiles := existingRoundFixture(t)
	if _, err := c.Snapshot(context.Background()); err != nil {
		t.Fatal(err)
	}
	r := actionRequest(c, profiles[0], "after-batch-start", "start")
	got := c.Execute(context.Background(), r)
	if got.Status == "rejected" || f.exactCalls == 0 || f.batchCalls != 1 || len(f.submitted) != 1 {
		t.Fatalf("effect did not obtain its independent exact observation: %+v batch=%d exact=%d submits=%d", got, f.batchCalls, f.exactCalls, len(f.submitted))
	}
}

func TestExistingObservationRoundExpiresBeforeMixedPolicyConsumption(t *testing.T) {
	c, f, profiles := existingRoundFixture(t)
	// The first profile is an unrelated, slower supervisor. Batch entries may
	// already exist, but their real timestamps must still be valid at commit.
	c.Policy.Profiles = append([]Profile{{ID: "unrelated-slow-catalog", Target: Target{Kind: configuredCatalogKind}}}, profiles...)
	f.mutate = func(s []existingSnapshot) {
		for index := range s {
			s[index].ObservedAtMS = time.Now().Add(-4800 * time.Millisecond).UnixMilli()
		}
	}
	f.beforeInspect = func() { time.Sleep(250 * time.Millisecond) }
	report, err := c.Snapshot(context.Background())
	if err != nil || report.Inventory.Complete || f.batchCalls != 1 || f.inspectCalls != 1 {
		t.Fatalf("expired mixed-policy round accepted/retried: %+v %v", report, err)
	}
	for _, state := range report.States[1:] {
		if state.SupervisorState != "unknown" || state.Quality != "error" {
			t.Fatalf("expired member committed: %+v", state)
		}
	}
}

func TestExistingObservationRoundRejectsCanceledCaller(t *testing.T) {
	c, f, _ := existingRoundFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	report, err := c.Snapshot(ctx)
	if err != nil || report.Inventory.Complete || f.batchCalls != 1 || f.inspectCalls != 0 {
		t.Fatalf("canceled round accepted/retried: %+v %v", report, err)
	}
}

func TestExistingBatchSelectorsPinsAndEveryResponseMemberValidated(t *testing.T) {
	_, _, profiles := existingRoundFixture(t)
	ids := make([]string, len(profiles))
	models := make([]map[string]any, len(profiles))
	for index, p := range profiles {
		ids[index] = p.ID
		models[index] = map[string]any{"model_id": p.ID, "state": "STOPPED", "process_active": false, "api_ready": false, "context": nil, "endpoint": "http://127.0.0.1:31001", "engine_log": "fixed-private", "action_log": "fixed-private", "message": "offline batch fixture", "generation": 7, "attempt": nil, "run_id": nil, "profile_revision": p.Target.Existing.BackendRevision, "instance_identity": "", "observed_at_ms": time.Now().UnixMilli(), "instance": nil}
	}
	envelope := existingEnvelope{SchemaVersion: 1, Method: "observe_many", ProfileIDs: ids}
	decode := func() error {
		raw, _ := json.Marshal(map[string]any{"schema_version": 1, "models": models})
		var result existingResponse
		return decodeExistingProfilesResponse(raw, envelope, profiles, &result)
	}
	if err := decode(); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"model_id", "observed_at_ms", "api_ready", "instance"} {
		old := models[6][key]
		delete(models[6], key)
		if decode() == nil {
			t.Fatalf("last member missing %s accepted", key)
		}
		models[6][key] = old
	}
	models[0], models[6] = models[6], models[0]
	if decode() == nil {
		t.Fatal("reordered/foreign response members accepted")
	}
	models[0], models[6] = models[6], models[0]
	models[6]["profile_revision"] = strings.Repeat("f", 64)
	if decode() == nil {
		t.Fatal("wrong last-member backend approval accepted")
	}
	models[6]["profile_revision"] = profiles[6].Target.Existing.BackendRevision
	for _, failure := range []string{"shared-pin", "selector", "duplicate", "effect-batch"} {
		t.Run(failure, func(t *testing.T) {
			copyProfiles := append([]Profile(nil), profiles...)
			copyEnvelope := envelope
			switch failure {
			case "shared-pin":
				a := *copyProfiles[6].Target.Existing
				a.CodePins = append([]ExistingCodePin(nil), a.CodePins...)
				a.CodePins[2].SHA256 = strings.Repeat("c", 64)
				copyProfiles[6].Target.Existing = &a
				copyProfiles[6].Target.DefinitionHash = digest(&a)
			case "selector":
				copyEnvelope.ProfileIDs = ids[:6]
			case "duplicate":
				copyProfiles[6] = copyProfiles[0]
			case "effect-batch":
				copyEnvelope.Method = "submit"
			}
			if validateExistingProfiles(copyProfiles, copyEnvelope) == nil {
				t.Fatal("incompatible batch accepted")
			}
		})
	}
}
