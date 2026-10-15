package spark

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/agent/configfile"
	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
)

func TestWorkerBackgroundModelObservationInterval(t *testing.T) {
	start := time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC)
	w := &Worker{}
	if !w.brokerRefreshDueLocked(start) {
		t.Fatal("first model observation was delayed")
	}
	w.lastBrokerAttempt = start
	for _, elapsed := range []time.Duration{0, 5 * time.Second, 29 * time.Second, 30*time.Second - time.Nanosecond, 30 * time.Second} {
		if got, want := w.brokerRefreshDueLocked(start.Add(elapsed)), elapsed >= 30*time.Second; got != want {
			t.Fatalf("model observation at %v: due=%v want=%v", elapsed, got, want)
		}
	}
	// Old acknowledged terminal receipts must not keep passive polling fast.
	w.jobs = map[string]*workerJob{"completed": {Acked: true, Receipt: protocol.JobReceipt{Status: protocol.TargetSucceeded}}}
	if w.brokerRefreshDueLocked(start.Add(5 * time.Second)) {
		t.Fatal("acknowledged completed job shortened the background interval")
	}
}

func TestWorkerActiveModelObservationBypassesBackgroundDelay(t *testing.T) {
	now := time.Now()
	for _, scenario := range []struct {
		name  string
		setup func(*Worker)
	}{
		{"unacknowledged-terminal-receipt", func(w *Worker) {
			w.jobs = map[string]*workerJob{"job": {Receipt: protocol.JobReceipt{Status: protocol.TargetSucceeded}}}
		}},
		{"running-job", func(w *Worker) { w.jobs = map[string]*workerJob{"job": {Acked: true, running: true}} }},
		{"stage-renewal", func(w *Worker) { w.jobs = map[string]*workerJob{"job": {Acked: true, stageRenewing: true}} }},
		{"accepted-job", func(w *Worker) {
			w.jobs = map[string]*workerJob{"job": {Acked: true, Receipt: protocol.JobReceipt{Status: protocol.TargetAccepted}}}
		}},
		{"acknowledged-running-receipt", func(w *Worker) {
			w.jobs = map[string]*workerJob{"job": {Acked: true, Receipt: protocol.JobReceipt{Status: protocol.TargetRunning}}}
		}},
		{"unknown-result", func(w *Worker) {
			w.jobs = map[string]*workerJob{"job": {Acked: true, Receipt: protocol.JobReceipt{Status: protocol.TargetUnknownResult}}}
		}},
		{"acknowledged-restart-waiting", func(w *Worker) {
			w.jobs = map[string]*workerJob{"job": {Acked: true, Receipt: protocol.JobReceipt{Status: protocol.TargetAwaitingConfirmation, Stage: "restart_stopped"}}}
		}},
		{"starting-model", func(w *Worker) { w.brokerSnapshot.States = []protocol.SparkModelState{{SupervisorState: "starting"}} }},
		{"stopping-model", func(w *Worker) { w.brokerSnapshot.States = []protocol.SparkModelState{{SupervisorState: "stopping"}} }},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			w := &Worker{lastBrokerAttempt: now}
			scenario.setup(w)
			if !w.brokerRefreshDueLocked(now.Add(5 * time.Second)) {
				t.Fatal("active observation waited for the background interval")
			}
			w.brokerRunning = true
			if w.brokerRefreshDueLocked(now.Add(time.Minute)) {
				t.Fatal("active observation allowed an overlapping broker probe")
			}
		})
	}
}

func TestWorkerFailedModelProbeStillUsesBackgroundInterval(t *testing.T) {
	w := &Worker{ctx: context.Background(), state: &configfile.State{}, broker: BrokerClient{Socket: filepath.Join(t.TempDir(), "missing.sock")}}
	w.refreshBroker()
	w.wg.Wait()
	if w.lastBrokerAttempt.IsZero() || !w.lastBroker.IsZero() || w.brokerRunning {
		t.Fatalf("failed probe lost attempt accounting: attempted=%v success=%v running=%v", w.lastBrokerAttempt, w.lastBroker, w.brokerRunning)
	}
	attempt := w.lastBrokerAttempt
	w.refreshBroker()
	w.wg.Wait()
	if !w.lastBrokerAttempt.Equal(attempt) {
		t.Fatal("failed background probe retried before thirty seconds")
	}
	w.lastBrokerAttempt = time.Now().Add(-30 * time.Second)
	w.refreshBroker()
	w.wg.Wait()
	if !w.lastBrokerAttempt.After(attempt) {
		t.Fatal("failed background probe did not retry at the next interval")
	}
}

func TestWorkerModelCacheDoesNotDelayReportsOrRefreshEvidence(t *testing.T) {
	w, reports, mu := workerFixture(t, true)
	w.cfg.Paused = false
	w.cfg.Checks = nil
	w.discovering = true
	host, caps, extension := w.collector.Snapshot(time.Now().UTC())
	w.collection = collectedSnapshot{host: host, caps: caps, spark: extension, at: time.Now()}
	w.collectRunning = true
	w.features = protocol.SparkFeatures
	observed := time.Now().UTC().Add(-29 * time.Second)
	epoch := "poll-epoch"
	w.brokerSnapshot = BrokerSnapshot{
		Inventory: protocol.SparkInventory{Revision: digest("poll-fixture"), Complete: true, ObservedAt: observed, Profiles: []protocol.SparkProfile{}},
		States:    []protocol.SparkModelState{{ProfileID: "model-a", ObservedAt: observed, SupervisorState: "running", Readiness: "ready", Quality: "ok"}},
		Control:   protocol.SparkControlState{Enabled: true, Epoch: &epoch, ObservedAt: observed},
	}
	w.lastBroker = observed
	w.tick()
	w.tick()
	w.lastBroker = time.Now().Add(-protocol.SparkObservationFreshness - time.Second)
	w.tick()
	mu.Lock()
	defer mu.Unlock()
	if len(*reports) != 3 {
		t.Fatalf("model cache prevented independent reporting: reports=%d", len(*reports))
	}
	for _, report := range (*reports)[:2] {
		if report.Host == nil || report.Spark == nil || report.Spark.Inventory == nil || len(report.Spark.ModelStates) != 1 || report.Spark.ControlState == nil || !report.Spark.ControlState.Enabled {
			t.Fatalf("valid background observation was dropped between model probes: %+v", report)
		}
		if !report.Spark.ModelStates[0].ObservedAt.Equal(observed) || !report.Spark.ControlState.ObservedAt.Equal(observed) || !report.Spark.Inventory.ObservedAt.Equal(observed) {
			t.Fatal("reporting a cached model snapshot refreshed its evidence timestamps")
		}
	}
	expired := (*reports)[2]
	if expired.Host == nil || expired.Spark == nil || expired.Spark.Inventory != nil || len(expired.Spark.ModelStates) != 0 || expired.Spark.ControlState == nil || expired.Spark.ControlState.Enabled {
		t.Fatalf("expired model snapshot remained usable or blocked host reporting: %+v", expired)
	}
}

func TestWorkerLogViewingKeepsBackgroundModelInterval(t *testing.T) {
	now := time.Now()
	w := &Worker{lastBrokerAttempt: now, logSessions: map[string]context.CancelFunc{"stream": func() {}}}
	if w.brokerRefreshDueLocked(now.Add(5*time.Second)) || w.brokerRefreshDueLocked(now.Add(29*time.Second)) {
		t.Fatal("log viewing increased model health polling")
	}
	if !w.brokerRefreshDueLocked(now.Add(30 * time.Second)) {
		t.Fatal("log viewing suppressed the due background observation")
	}
}
