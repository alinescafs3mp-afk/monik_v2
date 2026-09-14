package spool

import (
	"encoding/json"
	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func report(seq int64) protocol.AgentReport {
	return protocol.AgentReport{AgentID: "agent", SessionID: "session", Sequence: seq, ObservedAt: time.Now().UTC().Truncate(time.Second)}
}
func TestSameTimestampRetainsDistinctReports(t *testing.T) {
	s, e := Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	a := report(1)
	b := a
	b.Sequence = 2
	if e = s.Push(a); e != nil {
		t.Fatal(e)
	}
	if e = s.Push(b); e != nil {
		t.Fatal(e)
	}
	rows, e := s.List()
	if e != nil || len(rows) != 2 {
		t.Fatalf("same timestamp lost data: count=%d error=%v", len(rows), e)
	}
}
func TestAcknowledgementRemovesOnlyItsReportAndRetriesDoNotOverwrite(t *testing.T) {
	s, _ := Open(t.TempDir())
	a := report(1)
	b := a
	b.Sequence = 2
	for _, r := range []protocol.AgentReport{a, b, a} {
		if e := s.Push(r); e != nil {
			t.Fatal(e)
		}
	}
	a.IsLive = false
	if e := s.DropReport(a); e != nil {
		t.Fatal(e)
	}
	rows, e := s.List()
	if e != nil || len(rows) != 1 || rows[0].Sequence != 2 {
		t.Fatalf("wrong record removed: %+v %v", rows, e)
	}
	if e = s.DropReport(a); e != nil {
		t.Fatal(e)
	}
}
func TestLegacyQueueReadableAndCorruptionVisible(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)
	a := report(1)
	raw, _ := json.Marshal(a)
	path := filepath.Join(dir, stamp(a.ObservedAt)+".json")
	if e := os.WriteFile(path, raw, 0600); e != nil {
		t.Fatal(e)
	}
	rows, e := s.List()
	if e != nil || len(rows) != 1 {
		t.Fatal(rows, e)
	}
	if e = s.DropReport(a); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(path); !os.IsNotExist(e) {
		t.Fatal("legacy record not removed")
	}
	if e = os.WriteFile(filepath.Join(dir, "broken.json"), []byte("{broken"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = s.List(); e == nil || s.Status().Error == "" {
		t.Fatal("corrupt file was hidden")
	}
}
