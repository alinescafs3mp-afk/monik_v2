package spool

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestV13LossLedgerWriteFailureKeepsUnretiredReport(t *testing.T) {
	dir := t.TempDir()
	s, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	r := report(1)
	if e = s.Push(r); e != nil {
		t.Fatal(e)
	}
	p := filepath.Join(dir, "records-v2", recordName(r))
	past := time.Now().Add(-time.Hour)
	if e = os.Chtimes(p, past, past); e != nil {
		t.Fatal(e)
	}
	if e = os.Mkdir(filepath.Join(dir, lossLedgerName), 0700); e != nil {
		t.Fatal(e)
	}
	if _, e = s.List(); e == nil {
		t.Fatal("failed loss publication reported success")
	}
	if _, e = os.Stat(p); e != nil || s.Status().Dropped != 0 {
		t.Fatal("data deleted before loss was durable", e, s.Status())
	}
}

func TestV13PendingLossRecoveryIsConfinedAndExactlyCounted(t *testing.T) {
	for _, alreadyRemoved := range []bool{false, true} {
		t.Run(map[bool]string{false: "before_unlink", true: "after_unlink"}[alreadyRemoved], func(t *testing.T) {
			dir := t.TempDir()
			s, e := Open(dir)
			if e != nil {
				t.Fatal(e)
			}
			r := report(1)
			if e = s.Push(r); e != nil {
				t.Fatal(e)
			}
			rel := "records-v2/" + recordName(r)
			now := time.Now().UTC()
			// Simulate a process stopping between journal publication, unlink and clear.
			v := lossLedger{Schema: 1, Dropped: 1, From: &now, To: &now, Pending: []string{rel}}
			if e = s.saveLossLocked(v); e != nil {
				t.Fatal(e)
			}
			if alreadyRemoved {
				if e = os.Remove(filepath.Join(dir, filepath.FromSlash(rel))); e != nil {
					t.Fatal(e)
				}
			}
			s, e = Open(dir)
			if e != nil {
				t.Fatal(e)
			}
			if s.Status().Dropped != 1 || s.Status().Items != 0 {
				t.Fatalf("wrong resumed accounting: %+v", s.Status())
			}
			if rows, e := s.List(); e != nil || len(rows) != 0 {
				t.Fatal(rows, e)
			}
			s, e = Open(dir)
			if e != nil || s.Status().Dropped != 1 {
				t.Fatal("retirement double counted", e)
			}
			snapshot := s.Status()
			*snapshot.DropFrom = time.Time{}
			if s.Status().DropFrom.IsZero() {
				t.Fatal("caller mutated internal loss interval")
			}
		})
	}
}

func TestV13InvalidLossLedgerNeverResetsOrFollowsPath(t *testing.T) {
	for _, body := range []string{`null`, `{}`, `{"schema":1,"dropped":-1}`, `{"schema":1,"schema":1}`, strings.Repeat(" ", 129<<10)} {
		dir := t.TempDir()
		path := filepath.Join(dir, lossLedgerName)
		if e := os.WriteFile(path, []byte(body), 0600); e != nil {
			t.Fatal(e)
		}
		if _, e := Open(dir); e == nil {
			t.Fatal("invalid loss state reset")
		}
		after, e := os.ReadFile(path)
		if e != nil || string(after) != body {
			t.Fatal("damaged state overwritten", e)
		}
	}
	for _, p := range []string{"../outside.json", "records-v2/../../outside.json", "/tmp/outside.json", "C:/outside.json", "records-v2\\other.json"} {
		dir := t.TempDir()
		now := time.Now().UTC()
		b, _ := json.Marshal(lossLedger{Schema: 1, Dropped: 1, From: &now, To: &now, Pending: []string{p}})
		if e := os.WriteFile(filepath.Join(dir, lossLedgerName), b, 0600); e != nil {
			t.Fatal(e)
		}
		if _, e := Open(dir); e == nil {
			t.Fatal("accepted unconstrained pending loss", p)
		}
	}
}

func TestV13LossRetirementIsChunked(t *testing.T) {
	dir := t.TempDir()
	s, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	now := time.Now().UTC()
	old := now.Add(-time.Hour)
	for i := int64(1); i <= 260; i++ {
		r := report(i)
		b, _ := json.Marshal(r)
		p := filepath.Join(dir, "records-v2", recordName(r))
		if e = os.WriteFile(p, b, 0600); e != nil {
			t.Fatal(e)
		}
		if e = os.Chtimes(p, old, old); e != nil {
			t.Fatal(e)
		}
	}
	if rows, e := s.List(); e != nil || len(rows) != 0 {
		t.Fatal(rows, e)
	}
	s, e = Open(dir)
	if e != nil || s.Status().Dropped != 260 {
		t.Fatal("multi-batch accounting", e)
	}
}

func TestV13PendingLossDoesNotFollowDirectorySymlink(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	s, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	name := "outside.json"
	target := filepath.Join(outside, name)
	if e = os.WriteFile(target, []byte("not a spool record"), 0600); e != nil {
		t.Fatal(e)
	}
	if e = os.Remove(filepath.Join(dir, "records-v2")); e != nil {
		t.Fatal(e)
	}
	if e = os.Symlink(outside, filepath.Join(dir, "records-v2")); e != nil {
		t.Skip("symlinks unavailable", e)
	}
	now := time.Now().UTC()
	if e = s.saveLossLocked(lossLedger{Schema: 1, Dropped: 1, From: &now, To: &now, Pending: []string{"records-v2/" + name}}); e != nil {
		t.Fatal(e)
	}
	if _, e = Open(dir); e == nil {
		t.Error("pending loss followed a directory symlink")
	}
	if b, e := os.ReadFile(target); e != nil || string(b) != "not a spool record" {
		t.Fatalf("retirement escaped its state directory: %v", e)
	}
}
