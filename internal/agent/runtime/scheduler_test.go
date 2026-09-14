package runtime

import (
	"fmt"
	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
	"testing"
	"time"
)

func TestAudit4SchedulerIndependentIntervalsAndNoOverlap(t *testing.T) {
	s := checkScheduler{}
	now := time.Now()
	defs := []protocol.CheckDefinition{{ID: "slow", IntervalSeconds: 30}, {ID: "fast", IntervalSeconds: 5}}
	if len(s.reserve(now, defs)) != 2 {
		t.Fatal("initial")
	}
	s.complete("fast")
	got := s.reserve(now.Add(5*time.Second), defs)
	if len(got) != 1 || got[0].ID != "fast" {
		t.Fatal("slow check blocks fast or overlaps")
	}
	s.complete("fast")
	s.complete("slow")
	got = s.reserve(now.Add(10*time.Second), defs)
	if len(got) != 1 || got[0].ID != "fast" {
		t.Fatal("30 second interval not respected")
	}
	s.complete("fast")
	if len(s.reserve(now.Add(30*time.Second), defs)) != 2 {
		t.Fatal("slow not due")
	}
}
func TestAudit4SchedulerCapacityFairnessAndPause(t *testing.T) {
	s := checkScheduler{}
	now := time.Now()
	var ds []protocol.CheckDefinition
	for i := 0; i < 20; i++ {
		ds = append(ds, protocol.CheckDefinition{ID: fmt.Sprint(i), IntervalSeconds: 5})
	}
	first := s.reserve(now, ds)
	if len(first) != 16 {
		t.Fatal(len(first))
	}
	for _, d := range first {
		s.complete(d.ID)
	}
	second := s.reserve(now.Add(5*time.Second), ds)
	if second[0].ID != "16" {
		t.Fatal("starved old unexecuted checks")
	}
	for _, d := range second {
		s.complete(d.ID)
	}
	for i := range ds {
		ds[i].Paused = true
	}
	if len(s.reserve(now.Add(time.Minute), ds)) != 0 {
		t.Fatal("paused checks ran")
	}
}
func waitAudit4Trial(t *testing.T, a *Agent, id string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		a.mu.Lock()
		r := a.jobs[id]
		a.mu.Unlock()
		if r.Status != protocol.TargetAccepted && r.Status != protocol.TargetRunning {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("trial did not produce a receipt")
}
