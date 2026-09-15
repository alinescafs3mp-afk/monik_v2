package spool

import (
	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestV13SpoolRejectsChangedPayloadForSameIdentity(t *testing.T) {
	s, _ := Open(t.TempDir())
	a := report(1)
	a.Host = &protocol.HostMetrics{RAMUsed: 1}
	if err := s.Push(a); err != nil {
		t.Fatal(err)
	}
	b := a
	b.Host = &protocol.HostMetrics{RAMUsed: 2}
	if err := s.Push(b); err == nil {
		t.Fatal("same transport identity silently swallowed different measurements")
	}
	a.IsLive = false
	a.ReportedAt = time.Now()
	a.JobReceipts = []protocol.JobReceipt{{JobID: "new-receipt"}}
	if err := s.Push(a); err != nil {
		t.Fatal("legitimate transport-only retry rejected", err)
	}
}
func TestV13HealthyDrainExpiresAgedQueueWithoutANewFailure(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)
	old := report(1)
	fresh := report(2)
	if err := s.Push(old); err != nil {
		t.Fatal(err)
	}
	if err := s.Push(fresh); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-protocol.SpoolMaxAge - time.Minute)
	if err := os.Chtimes(filepath.Join(dir, "records-v2", recordName(old)), past, past); err != nil {
		t.Fatal(err)
	}
	got, err := s.ListLimit(4)
	if err != nil || len(got) != 1 || got[0].Sequence != 2 {
		t.Fatalf("aged head kept blocking a healthy drain: %+v %v", got, err)
	}
	if s.Status().Dropped != 1 {
		t.Fatal("expired data discarded without a loss count")
	}
}

func TestV13LossEvidenceSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	old := report(1)
	if err = s.Push(old); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-protocol.SpoolMaxAge - time.Minute)
	if err = os.Chtimes(filepath.Join(dir, "records-v2", recordName(old)), past, past); err != nil {
		t.Fatal(err)
	}
	if err = s.Push(report(2)); err != nil {
		t.Fatal(err)
	}
	before := s.Status()
	if before.Dropped != 1 || before.DropFrom == nil {
		t.Fatal(before)
	}
	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	after := reopened.Status()
	if after.Dropped != before.Dropped || after.DropFrom == nil || !after.DropFrom.Equal(*before.DropFrom) {
		t.Fatalf("restart erased loss evidence: before=%+v after=%+v", before, after)
	}
}
