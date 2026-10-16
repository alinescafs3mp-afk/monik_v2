package spark

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func parallelObservationProfiles(t *testing.T, count int) []Profile {
	t.Helper()
	_, _, profiles := existingRoundFixture(t)
	for _, id := range existingIDs[7:] {
		profiles = append(profiles, midrangeProfile(t, profiles[0], id))
	}
	return profiles[:count]
}
func parallelObservationResponse(profiles []Profile) existingResponse {
	g := int64(7)
	response := existingResponse{SchemaVersion: 1}
	for _, p := range profiles {
		response.Models = append(response.Models, existingSnapshot{existingResult: existingResult{ModelID: p.ID, State: "STOPPED", Generation: &g}, ProfileRevision: p.Target.Existing.BackendRevision, ObservedAtMS: time.Now().UnixMilli()})
	}
	return response
}

func TestExistingObservationParallelCommonDeadlineAndCanonicalOrder(t *testing.T) {
	profiles := parallelObservationProfiles(t, 10)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	deadline, _ := ctx.Deadline()
	arrived := make(chan int, 2)
	release := make(chan struct{})
	var active, peak, calls atomic.Int32
	call := func(probe context.Context, group []Profile, envelope existingEnvelope) (existingResponse, error) {
		calls.Add(1)
		n := active.Add(1)
		defer active.Add(-1)
		for old := peak.Load(); n > old && !peak.CompareAndSwap(old, n); old = peak.Load() {
		}
		d, ok := probe.Deadline()
		if !ok || !d.Equal(deadline) {
			return existingResponse{}, fmt.Errorf("deadline changed")
		}
		if len(group) != 5 || len(envelope.ProfileIDs) != 5 {
			return existingResponse{}, fmt.Errorf("unbounded group")
		}
		for i, p := range group {
			if envelope.ProfileIDs[i] != p.ID {
				return existingResponse{}, fmt.Errorf("selector changed")
			}
		}
		arrived <- len(group)
		select {
		case <-release:
		case <-probe.Done():
			return existingResponse{}, probe.Err()
		}
		// The latter group completes first; output must still follow the catalog.
		if group[0].ID == profiles[0].ID {
			time.Sleep(10 * time.Millisecond)
		}
		return parallelObservationResponse(group), nil
	}
	done := make(chan struct{})
	var got []existingSnapshot
	var err error
	go func() { got, err = existingObserveMany(ctx, profiles, call); close(done) }()
	for range 2 {
		select {
		case <-arrived:
		case <-ctx.Done():
			t.Fatal("helpers did not overlap")
		}
	}
	close(release)
	<-done
	if err != nil || len(got) != 10 || calls.Load() != 2 || peak.Load() != 2 || active.Load() != 0 {
		t.Fatalf("pair %+v %v calls%d peak%d active%d", got, err, calls.Load(), peak.Load(), active.Load())
	}
	for i, p := range profiles {
		if got[i].ModelID != p.ID {
			t.Fatal("completion order replaced catalog order")
		}
	}
}

func TestExistingObservationParallelValidatesWholeSetBeforeChildren(t *testing.T) {
	for _, kind := range []string{"duplicate-across-groups", "shared-pin-conflict", "foreign-selector", "foreign-unit"} {
		t.Run(kind, func(t *testing.T) {
			profiles := parallelObservationProfiles(t, 10)
			switch kind {
			case "duplicate-across-groups":
				profiles[9] = profiles[0]
			case "shared-pin-conflict":
				profiles[9].Target.Existing.CodePins[1].SHA256 = strings.Repeat("c", 64)
				profiles[9].Target.DefinitionHash = digest(profiles[9].Target.Existing)
			case "foreign-selector":
				profiles[9].ID = "unapproved-model"
			case "foreign-unit":
				profiles[9].Target.Existing.CodePins[6].Path = "/etc/systemd/system/foreign.service"
				profiles[9].Target.DefinitionHash = digest(profiles[9].Target.Existing)
			}
			var calls atomic.Int32
			got, err := existingObserveMany(context.Background(), profiles, func(context.Context, []Profile, existingEnvelope) (existingResponse, error) {
				calls.Add(1)
				return existingResponse{}, nil
			})
			if err == nil || len(got) != 0 || calls.Load() != 0 {
				t.Fatal("invalid whole set reached helper")
			}
		})
	}
}

func TestExistingObservationParallelFailureCancelsAndJoinsPeer(t *testing.T) {
	profiles := parallelObservationProfiles(t, 10)
	ready := make(chan struct{})
	peerExited := make(chan struct{})
	var calls atomic.Int32
	got, err := existingObserveMany(context.Background(), profiles, func(ctx context.Context, group []Profile, _ existingEnvelope) (existingResponse, error) {
		calls.Add(1)
		if group[0].ID == profiles[0].ID {
			<-ready
			return existingResponse{}, fmt.Errorf("injected child failure")
		}
		close(ready)
		<-ctx.Done()
		close(peerExited)
		return existingResponse{}, ctx.Err()
	})
	if err == nil || len(got) != 0 || calls.Load() != 2 {
		t.Fatal("failure accepted or retried")
	}
	select {
	case <-peerExited:
	default:
		t.Fatal("returned before owned peer joined")
	}
}

func TestExistingObservationParallelDeadlineCancelsAndJoinsBoth(t *testing.T) {
	profiles := parallelObservationProfiles(t, 10)
	var exited, calls atomic.Int32
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	got, err := existingObserveMany(ctx, profiles, func(probe context.Context, _ []Profile, _ existingEnvelope) (existingResponse, error) {
		calls.Add(1)
		<-probe.Done()
		exited.Add(1)
		return existingResponse{}, probe.Err()
	})
	if err == nil || len(got) != 0 || calls.Load() != 2 || exited.Load() != 2 {
		t.Fatal("deadline result accepted, retried, or unjoined")
	}
}

func TestExistingObservationParallelBadMemberPoisonsWholeBatch(t *testing.T) {
	for _, kind := range []string{"partial", "foreign-id", "foreign-revision", "stale", "future", "invalid-generation"} {
		t.Run(kind, func(t *testing.T) {
			profiles := parallelObservationProfiles(t, 10)
			var calls atomic.Int32
			got, err := existingObserveMany(context.Background(), profiles, func(_ context.Context, group []Profile, _ existingEnvelope) (existingResponse, error) {
				calls.Add(1)
				r := parallelObservationResponse(group)
				if group[0].ID != profiles[0].ID {
					switch kind {
					case "partial":
						r.Models = r.Models[:4]
					case "foreign-id":
						r.Models[4].ModelID = profiles[0].ID
					case "foreign-revision":
						r.Models[4].ProfileRevision = strings.Repeat("f", 64)
					case "stale":
						r.Models[4].ObservedAtMS = time.Now().Add(-6 * time.Second).UnixMilli()
					case "future":
						r.Models[4].ObservedAtMS = time.Now().Add(2 * time.Second).UnixMilli()
					case "invalid-generation":
						r.Models[4].Generation = nil
					}
				}
				return r, nil
			})
			if err == nil || len(got) != 0 || calls.Load() != 2 {
				t.Fatal("bad member accepted or retried")
			}
		})
	}
}

func TestExistingObservationParallelRechecksAtJoinWithoutRefreshing(t *testing.T) {
	profiles := parallelObservationProfiles(t, 10)
	firstReturned := make(chan struct{})
	got, err := existingObserveMany(context.Background(), profiles, func(_ context.Context, group []Profile, _ existingEnvelope) (existingResponse, error) {
		r := parallelObservationResponse(group)
		if group[0].ID == profiles[0].ID {
			for i := range r.Models {
				r.Models[i].ObservedAtMS = time.Now().Add(-4900 * time.Millisecond).UnixMilli()
			}
			close(firstReturned)
		} else {
			<-firstReturned
			time.Sleep(150 * time.Millisecond)
		}
		return r, nil
	})
	if err == nil || len(got) != 0 {
		t.Fatal("expired first helper was refreshed or accepted after join")
	}
}

func TestExistingObservationParallelOldSevenAndSingletonRemainOneCall(t *testing.T) {
	for _, n := range []int{1, 7} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			profiles := parallelObservationProfiles(t, n)
			calls := 0
			got, err := existingObserveMany(context.Background(), profiles, func(_ context.Context, group []Profile, _ existingEnvelope) (existingResponse, error) {
				calls++
				return parallelObservationResponse(group), nil
			})
			if err != nil || len(got) != n || calls != 1 {
				t.Fatal("original one-call path changed")
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	got, err := existingObserveMany(ctx, parallelObservationProfiles(t, 10), func(context.Context, []Profile, existingEnvelope) (existingResponse, error) {
		calls++
		return existingResponse{}, nil
	})
	if err == nil || len(got) != 0 || calls != 0 {
		t.Fatal("canceled caller launched a helper")
	}
}
