//go:build !windows

package servicehost

import (
	"context"
	"github.com/alinescafs3mp-afk/monik_v2/internal/update"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestV12RollbackRequiresJournaledPreviousDigest(t *testing.T) {
	dir := t.TempDir()
	worker := filepath.Join(dir, "worker")
	prev := filepath.Join(dir, "updates", "prev.bin")
	os.MkdirAll(filepath.Dir(prev), 0700)
	os.WriteFile(worker, []byte("#!/bin/sh\nexec sleep 30\n"), 0755)
	os.WriteFile(prev, []byte("#!/bin/sh\n# unverified previous file\nexec sleep 30\n"), 0755)
	j := &update.Journal{Stage: update.StageConfirmed, Current: worker, PrevPath: prev}
	if err := j.Save(dir); err != nil {
		t.Fatal(err)
	}
	h := &Host{WorkerBin: worker, StateDir: dir}
	defer h.stopWorker()
	old, _ := update.SHA256File(worker)
	got := h.rollbackUpdate(nil)
	after, _ := update.SHA256File(worker)
	if got.OK || old != after {
		t.Fatal("unverified previous file was restored/launched", got)
	}
}
func TestV12StoppedSupervisorCannotRestartWorkerFromLateRequest(t *testing.T) {
	dir := t.TempDir()
	worker := filepath.Join(dir, "worker")
	if err := os.WriteFile(worker, []byte("#!/bin/sh\nexec sleep 30\n"), 0755); err != nil {
		t.Fatal(err)
	}
	h := &Host{StateDir: dir, WorkerBin: worker}
	defer h.stopWorker()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- h.Run(ctx) }()
	until := time.Now().Add(5 * time.Second)
	for {
		h.mu.Lock()
		alive := h.alive
		h.mu.Unlock()
		if alive {
			break
		}
		if time.Now().After(until) {
			t.Fatal("worker never started")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("supervisor did not stop")
	}
	got := h.dispatch(Request{Action: "restart_worker"}, false)
	if got.OK {
		t.Fatal("late accepted request resurrected worker after supervisor shutdown")
	}
}

func TestV12FailedRollbackJournalIsNotReportedAsVerifiedRecovery(t *testing.T) {
	dir := t.TempDir()
	worker := filepath.Join(dir, "worker")
	incoming := filepath.Join(dir, "incoming")
	old := []byte("#!/bin/sh\nexec sleep 30\n")
	if err := os.WriteFile(worker, old, 0755); err != nil {
		t.Fatal(err)
	}
	// This disposable candidate makes only its own journal unwritable, then fails.
	candidate := "#!/bin/sh\nrm -- '" + update.Path(dir) + "'\nmkdir -- '" + update.Path(dir) + "'\nexit 1\n"
	if err := os.WriteFile(incoming, []byte(candidate), 0755); err != nil {
		t.Fatal(err)
	}
	sum, err := update.SHA256File(incoming)
	if err != nil {
		t.Fatal(err)
	}
	h := &Host{StateDir: dir, WorkerBin: worker, Probation: time.Second}
	defer h.stopWorker()
	res := h.activateUpdate(map[string]string{"path": incoming, "sha256": sum})
	if res.OK || res.Stage == string(update.StageRollback) {
		t.Fatalf("failed durable recovery was reported verified: %+v", res)
	}
}
