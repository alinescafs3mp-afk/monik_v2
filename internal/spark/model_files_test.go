package spark

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
)

func TestBoundedModelMetadataDistinguishesLogicalAllocatedAndNoExternalSymlink(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "model.gguf")
	f, e := os.Create(file)
	if e != nil {
		t.Fatal(e)
	}
	if e = f.Truncate(1 << 20); e != nil {
		t.Fatal(e)
	}
	f.Close()
	m := scanModelFiles([]string{dir})
	if !m.Complete || !m.Available || m.Count != 1 || m.Logical != 1<<20 || m.Allocated >= m.Logical {
		t.Fatalf("sparse metadata=%+v", m)
	}
	other := filepath.Join(t.TempDir(), "secret.gguf")
	os.WriteFile(other, []byte("outside"), 0600)
	os.Symlink(other, filepath.Join(dir, "outside.gguf"))
	m = scanModelFiles([]string{dir})
	if m.Complete || m.Count != 1 {
		t.Fatalf("outside root symlink followed %+v", m)
	}
}
func TestStopCancelsPendingStartWithoutWaitingForWeightIO(t *testing.T) {
	c, f, p := controlFixture(t)
	p.Weights = []string{"/synthetic/gated/weights"}
	p.Revision = ProfileRevision(p)
	c.Policy.Profiles[0] = p
	entered := make(chan struct{})
	release := make(chan struct{})
	c.scanWeights = func([]string) modelFileMetadata {
		close(entered)
		<-release
		return modelFileMetadata{Complete: true, Available: true}
	}
	start := actionRequest(c, p, "pending-start", "start")
	answer := make(chan protocol.JobReceipt, 1)
	go func() { answer <- c.Execute(context.Background(), start) }()
	<-entered
	snapshot, err := c.Snapshot(context.Background())
	if err != nil || len(snapshot.States) != 1 || snapshot.States[0].SupervisorState != "stopped" || snapshot.States[0].Reason != "start_preflight_pending" {
		t.Fatalf("active standalone preflight is not cancelable in snapshot: %+v %v", snapshot.States, err)
	}
	stop := actionRequest(c, p, "stop-pending-start", "stop")
	at := time.Now()
	got := c.Execute(context.Background(), stop)
	if got.Status != protocol.TargetSucceeded || time.Since(at) > time.Second {
		t.Fatalf("stop waited weight I/O %+v", got)
	}
	snapshot, err = c.Snapshot(context.Background())
	if err != nil || snapshot.States[0].Reason != "" || snapshot.States[0].SupervisorState != "stopped" {
		t.Fatalf("canceled pending preflight remained exposed: %+v %v", snapshot.States, err)
	}
	close(release)
	select {
	case got = <-answer:
		if got.ErrorCode != "start_cancelled_by_stop" {
			t.Fatalf("pending start=%+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("start did not complete cancellation")
	}
	if len(f.effects) != 0 {
		t.Fatal("cancelled pending start ran effect")
	}
	if replay := c.Execute(context.Background(), start); replay.ErrorCode != "start_cancelled_by_stop" {
		t.Fatalf("cancelled preflight request was replayable: %+v", replay)
	}
}
