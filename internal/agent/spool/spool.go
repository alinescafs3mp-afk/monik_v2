package spool

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
	"github.com/alinescafs3mp-afk/monik_v2/internal/secure"
)

const maxReportBytes = 8 << 20

type Store struct {
	dir       string
	mu        sync.Mutex
	dropped   int64
	from, to  *time.Time
	lastError string
}
type record struct {
	path string
	mod  time.Time
	size int64
}

func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(dir, "records-v2"), 0700); err != nil {
		return nil, err
	}
	return &Store{dir: dir}, nil
}
func stamp(t time.Time) string { return t.UTC().Format("20060102T150405.000000000") }
func recordName(r protocol.AgentReport) string {
	// The transport identity is independent of mutable live/backfill flags.
	b, _ := json.Marshal([]any{r.AgentID, r.SessionID, r.Sequence})
	h := sha256.Sum256(b)
	return stamp(r.ObservedAt) + "-" + hex.EncodeToString(h[:]) + ".json"
}
func sameReport(a, b protocol.AgentReport) bool {
	return a.AgentID == b.AgentID && a.SessionID == b.SessionID && a.Sequence == b.Sequence && a.ObservedAt.Equal(b.ObservedAt)
}
func (s *Store) Push(rep protocol.AgentReport) (err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	defer func() {
		if err != nil {
			s.lastError = "spool write/retention failed"
		}
	}()
	b, err := json.Marshal(rep)
	if err != nil {
		return err
	}
	if len(b) > maxReportBytes {
		return fmt.Errorf("spool report exceeds 8 MiB")
	}
	// A separate versioned directory leaves old workers' legacy queue untouched.
	// Old workers cannot drain v2 records; upgrading again recovers them.
	path := filepath.Join(s.dir, "records-v2", recordName(rep))
	if old, e := readRecord(path); e == nil {
		if !sameReport(old, rep) {
			return fmt.Errorf("spool identity conflict")
		}
		return s.trimLocked() // Keep the original immutable observation on retry.
	} else if !os.IsNotExist(e) {
		return e
	}
	if err = secure.AtomicWrite(path, b, 0600); err != nil {
		return err
	}
	return s.trimLocked()
}
func readRecord(path string) (protocol.AgentReport, error) {
	var r protocol.AgentReport
	info, err := os.Lstat(path)
	if err != nil {
		return r, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxReportBytes {
		return r, fmt.Errorf("invalid spool record size/type")
	}
	f, err := os.Open(path)
	if err != nil {
		return r, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxReportBytes+1))
	if err != nil {
		return r, err
	}
	if len(b) > maxReportBytes {
		return r, fmt.Errorf("spool record exceeds limit")
	}
	err = json.Unmarshal(b, &r)
	return r, err
}
func (s *Store) recordsLocked() ([]record, error) {
	var out []record
	for _, dir := range []string{s.dir, filepath.Join(s.dir, "records-v2")} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
				continue
			}
			info, err := e.Info()
			if err != nil {
				return nil, err
			}
			if !info.Mode().IsRegular() {
				return nil, fmt.Errorf("non-regular spool entry")
			}
			out = append(out, record{filepath.Join(dir, e.Name()), info.ModTime(), info.Size()})
		}
	}
	sort.Slice(out, func(i, j int) bool { return filepath.Base(out[i].path) < filepath.Base(out[j].path) })
	return out, nil
}
func (s *Store) List() ([]protocol.AgentReport, error) { return s.ListLimit(0) }
func (s *Store) ListLimit(limit int) ([]protocol.AgentReport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := s.recordsLocked()
	if err != nil {
		s.lastError = "spool directory read failed"
		return nil, err
	}
	var out []protocol.AgentReport
	var firstErr error
	for _, e := range entries {
		if limit > 0 && len(out) >= limit {
			break
		}
		r, err := readRecord(e.path)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("unreadable or corrupt spool record")
			}
			continue
		}
		out = append(out, r)
	}
	if firstErr != nil {
		s.lastError = firstErr.Error()
	} else {
		s.lastError = ""
	}
	return out, firstErr
}

// DropReport removes only a durably acknowledged transport identity, including
// a matching pre-v2 file. A timestamp alone is not an acknowledgement identity.
func (s *Store) DropReport(rep protocol.AgentReport) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, path := range []string{filepath.Join(s.dir, "records-v2", recordName(rep)), filepath.Join(s.dir, stamp(rep.ObservedAt)+".json")} {
		old, err := readRecord(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			s.lastError = "spool acknowledgement read failed"
			return err
		}
		if !sameReport(old, rep) {
			continue
		}
		if err = os.Remove(path); err != nil && !os.IsNotExist(err) {
			s.lastError = "spool acknowledgement removal failed"
			return err
		}
	}
	return nil
}
func (s *Store) Status() protocol.SpoolStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := s.recordsLocked()
	if err != nil {
		s.lastError = "spool directory read failed"
	}
	var bytes int64
	for _, e := range entries {
		bytes += e.size
	}
	return protocol.SpoolStatus{Bytes: bytes, Items: len(entries), Dropped: s.dropped, DropFrom: s.from, DropTo: s.to, Error: s.lastError}
}
func (s *Store) trimLocked() error {
	entries, err := s.recordsLocked()
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].mod.Before(entries[j].mod) })
	var total int64
	for _, e := range entries {
		total += e.size
	}
	cut := time.Now().Add(-protocol.SpoolMaxAge)
	for _, e := range entries {
		if total <= protocol.SpoolMaxBytes && e.mod.After(cut) {
			break
		}
		if err = os.Remove(e.path); err != nil {
			return err
		}
		total -= e.size
		s.dropped++
		t := e.mod
		if s.from == nil || t.Before(*s.from) {
			s.from = &t
		}
		if s.to == nil || t.After(*s.to) {
			s.to = &t
		}
	}
	return nil
}
