package spool

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
)

type Store struct {
	dir     string
	mu      sync.Mutex
	dropped int64
	from    *time.Time
	to      *time.Time
}

func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &Store{dir: dir}, nil
}

func (s *Store) Push(rep protocol.AgentReport) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := json.Marshal(rep)
	if err != nil {
		return err
	}
	name := filepath.Join(s.dir, rep.ObservedAt.UTC().Format("20060102T150405.000000000")+".json")
	if err := os.WriteFile(name, b, 0o600); err != nil {
		return err
	}
	return s.trimLocked()
}

func (s *Store) List() ([]protocol.AgentReport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ents, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	sort.Slice(ents, func(i, j int) bool { return ents[i].Name() < ents[j].Name() })
	var out []protocol.AgentReport
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		b, err := os.ReadFile(filepath.Join(s.dir, e.Name()))
		if err != nil {
			continue
		}
		var r protocol.AgentReport
		if json.Unmarshal(b, &r) == nil {
			out = append(out, r)
		}
	}
	return out, nil
}

func (s *Store) Drop(observed time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	name := filepath.Join(s.dir, observed.UTC().Format("20060102T150405.000000000")+".json")
	_ = os.Remove(name)
}

func (s *Store) Status() protocol.SpoolStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	var bytes int64
	n := 0
	ents, _ := os.ReadDir(s.dir)
	for _, e := range ents {
		info, err := e.Info()
		if err == nil {
			bytes += info.Size()
			n++
		}
	}
	return protocol.SpoolStatus{Bytes: bytes, Items: n, Dropped: s.dropped, DropFrom: s.from, DropTo: s.to}
}

func (s *Store) trimLocked() error {
	ents, err := os.ReadDir(s.dir)
	if err != nil {
		return err
	}
	type rec struct {
		name string
		mod  time.Time
		size int64
	}
	var rs []rec
	var total int64
	for _, e := range ents {
		info, err := e.Info()
		if err != nil {
			continue
		}
		rs = append(rs, rec{e.Name(), info.ModTime(), info.Size()})
		total += info.Size()
	}
	sort.Slice(rs, func(i, j int) bool { return rs[i].mod.Before(rs[j].mod) })
	cut := time.Now().Add(-protocol.SpoolMaxAge)
	for len(rs) > 0 {
		if total <= protocol.SpoolMaxBytes && rs[0].mod.After(cut) {
			break
		}
		_ = os.Remove(filepath.Join(s.dir, rs[0].name))
		total -= rs[0].size
		s.dropped++
		t := rs[0].mod
		if s.from == nil {
			s.from = &t
		}
		s.to = &t
		rs = rs[1:]
	}
	return nil
}
