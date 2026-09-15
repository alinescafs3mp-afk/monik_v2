package spool

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/jsonutil"
	"github.com/alinescafs3mp-afk/monik_v2/internal/secure"
)

const lossLedgerName = "loss-ledger-v1.meta"
const lossBatchLimit = 256

type lossLedger struct {
	Schema  int        `json:"schema"`
	Dropped int64      `json:"dropped"`
	From    *time.Time `json:"from,omitempty"`
	To      *time.Time `json:"to,omitempty"`
	// Record the retirement decision BEFORE deleting data. On restart these
	// same paths are removed without incrementing the loss count again.
	Pending []string `json:"pending,omitempty"`
}

func (s *Store) loadLossLocked() error {
	path := filepath.Join(s.dir, lossLedgerName)
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > 128<<10 {
		return fmt.Errorf("invalid spool loss ledger type/size")
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	var v lossLedger
	if err = jsonutil.ReadObject(f, 128<<10, &v); err != nil {
		return fmt.Errorf("invalid spool loss ledger: %w", err)
	}
	if v.Schema != 1 || v.Dropped < 0 || len(v.Pending) > lossBatchLimit || int64(len(v.Pending)) > v.Dropped {
		return fmt.Errorf("invalid spool loss ledger fields")
	}
	if v.Dropped > 0 && (v.From == nil || v.To == nil || v.From.IsZero() || v.To.Before(*v.From)) {
		return fmt.Errorf("invalid spool loss interval")
	}
	if v.Dropped == 0 && (v.From != nil || v.To != nil) {
		return fmt.Errorf("unexpected empty spool loss interval")
	}
	seen := map[string]bool{}
	for _, p := range v.Pending {
		if !validLossPath(p) || seen[p] {
			return fmt.Errorf("invalid spool loss retirement path")
		}
		seen[p] = true
	}
	s.dropped, s.from, s.to, s.pendingLoss = v.Dropped, v.From, v.To, v.Pending
	return s.finishLossLocked()
}
func validLossPath(p string) bool {
	if p == "" || len(p) > 180 || strings.Contains(p, "\\") || strings.HasPrefix(p, "/") || strings.Contains(p, ":") {
		return false
	}
	parts := strings.Split(p, "/")
	if len(parts) > 2 || len(parts) == 2 && parts[0] != "records-v2" {
		return false
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return strings.HasSuffix(parts[len(parts)-1], ".json")
}
func (s *Store) saveLossLocked(v lossLedger) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return secure.AtomicWrite(filepath.Join(s.dir, lossLedgerName), raw, 0600)
}
func (s *Store) lossSnapshotLocked() lossLedger {
	return lossLedger{Schema: 1, Dropped: s.dropped, From: s.from, To: s.to, Pending: append([]string(nil), s.pendingLoss...)}
}
func (s *Store) finishLossLocked() error {
	if len(s.pendingLoss) == 0 {
		return nil
	}
	touched := map[string]bool{}
	for _, rel := range s.pendingLoss {
		if !validLossPath(rel) {
			return fmt.Errorf("invalid pending retirement")
		}
		p := filepath.Join(s.dir, filepath.FromSlash(rel))
		parent, err := os.Lstat(filepath.Dir(p))
		if err != nil {
			return err
		}
		if !parent.IsDir() {
			return fmt.Errorf("spool retirement directory must not be a symbolic link")
		}
		info, err := os.Lstat(p)
		if os.IsNotExist(err) {
			touched[filepath.Dir(p)] = true
			continue
		}
		if err != nil {
			return err
		}
		// Do not follow a replaced record/symlink, even inside private state.
		if !info.Mode().IsRegular() {
			return fmt.Errorf("non-regular pending spool retirement")
		}
		if err = os.Remove(p); err != nil && !os.IsNotExist(err) {
			return err
		}
		touched[filepath.Dir(p)] = true
	}
	// On supported Unix filesystems the directory sync orders deletions before
	// clearing the journal. A crash can replay a pending batch, not recount it.
	for dir := range touched {
		if err := syncLossDirectory(dir); err != nil {
			return err
		}
	}
	v := s.lossSnapshotLocked()
	v.Pending = nil
	if err := s.saveLossLocked(v); err != nil {
		return err
	}
	s.pendingLoss = nil
	return nil
}
func (s *Store) retireLossLocked(entries []record) error {
	if len(entries) == 0 {
		return nil
	}
	if len(entries) > lossBatchLimit {
		return fmt.Errorf("spool retirement batch exceeds limit")
	}
	if err := s.finishLossLocked(); err != nil {
		return err
	}
	v := s.lossSnapshotLocked()
	for _, e := range entries {
		rel, err := filepath.Rel(s.dir, e.path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if !validLossPath(rel) {
			return fmt.Errorf("invalid spool record retirement path")
		}
		if v.Dropped == int64(^uint64(0)>>1) {
			return fmt.Errorf("spool loss counter exhausted")
		}
		v.Dropped++
		t := e.mod.UTC()
		if v.From == nil || t.Before(*v.From) {
			v.From = &t
		}
		if v.To == nil || t.After(*v.To) {
			v.To = &t
		}
		v.Pending = append(v.Pending, rel)
	}
	if err := s.saveLossLocked(v); err != nil {
		return err
	}
	s.dropped, s.from, s.to, s.pendingLoss = v.Dropped, v.From, v.To, v.Pending
	return s.finishLossLocked()
}
