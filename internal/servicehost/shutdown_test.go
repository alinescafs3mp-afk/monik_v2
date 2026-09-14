package servicehost

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestAudit8RunWaitsForWorkerShutdown(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX process fixture")
	}
	dir := t.TempDir()
	worker := filepath.Join(dir, "worker")
	if e := os.WriteFile(worker, []byte("#!/bin/sh\nexec sleep 30\n"), 0755); e != nil {
		t.Fatal(e)
	}
	h := &Host{StateDir: dir, WorkerBin: worker}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	returned := make(chan error, 1)
	go func() { returned <- h.Run(ctx) }()
	ready := false
	for until := time.Now().Add(3 * time.Second); time.Now().Before(until); {
		h.mu.Lock()
		ready = h.alive
		h.mu.Unlock()
		if ready {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !ready {
		t.Fatal("worker did not start")
	}
	h.updateMu.Lock()
	cancel()
	early := false
	select {
	case <-returned:
		early = true
	case <-time.After(100 * time.Millisecond):
	}
	h.updateMu.Unlock()
	if !early {
		select {
		case e := <-returned:
			if e != nil {
				t.Fatal(e)
			}
		case <-time.After(8 * time.Second):
			t.Fatal("shutdown did not finish")
		}
	}
	// Ensure baseline cleanup too, so a deliberately failing regression leaves no process.
	h.stopWorker()
	if early {
		t.Error("Run returned while worker shutdown was still blocked; main can orphan the worker")
	}
	h.mu.Lock()
	alive := h.alive
	h.mu.Unlock()
	if alive {
		t.Fatal("worker alive after Run returned")
	}
}
