package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/agent/checks"
	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
)

type scheduleEntry struct {
	last    time.Time
	running bool
}
type checkScheduler struct {
	mu      sync.Mutex
	entries map[string]scheduleEntry
}

// reserve is nonblocking: a slow check occupies one slot, not the entire fleet
// of checks. Oldest eligible work gets the next slot; no overlapping same ID.
func (s *checkScheduler) reserve(now time.Time, defs []protocol.CheckDefinition) []protocol.CheckDefinition {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.entries == nil {
		s.entries = map[string]scheduleEntry{}
	}
	active := map[string]bool{}
	running := 0
	for _, d := range defs {
		active[d.ID] = true
	}
	for id, e := range s.entries {
		if e.running {
			running++
		} else if !active[id] {
			delete(s.entries, id)
		}
	}
	due := []protocol.CheckDefinition{}
	for _, d := range defs {
		e := s.entries[d.ID]
		interval := d.IntervalSeconds
		if interval < 5 {
			interval = 5
		}
		if !d.Paused && !d.Ignored && !e.running && (e.last.IsZero() || now.Sub(e.last) >= time.Duration(interval)*time.Second) {
			due = append(due, d)
		}
	}
	sort.SliceStable(due, func(i, j int) bool { return s.entries[due[i].ID].last.Before(s.entries[due[j].ID].last) })
	free := 16 - running
	if free < 0 {
		free = 0
	}
	if len(due) > free {
		due = due[:free]
	}
	for _, d := range due {
		s.entries[d.ID] = scheduleEntry{last: now, running: true}
	}
	return due
}
func (s *checkScheduler) complete(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.entries[id]
	e.running = false
	s.entries[id] = e
}
func (a *Agent) scheduleChecks(ctx context.Context, now time.Time, locals []net.IP) {
	defs := a.scheduler.reserve(now, a.cfg.Checks)
	revision := a.cfgRev
	for _, d := range defs {
		a.workerTasks.Add(1)
		go func(d protocol.CheckDefinition) {
			defer a.workerTasks.Done()
			defer a.scheduler.complete(d.ID)
			timeout := d.TimeoutSeconds
			if timeout < 1 {
				timeout = 2
			}
			if timeout > 30 {
				timeout = 30
			}
			c, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
			defer cancel()
			hdr, val := a.requestSecret(c, d.SecretID)
			body := ""
			if d.BodySecretID != "" {
				_, body = a.requestSecret(c, d.BodySecretID)
			}
			obs := checks.RunRequest(c, d, locals, hdr, val, body)
			obs.ConfigRev = revision
			select {
			case a.observations <- obs:
			case <-ctx.Done():
			}
		}(d)
	}
}

// Secret fetches do not hold the agent's control mutex while waiting for network.
// Plaintext is memory-only; offline restart persistence remains a release task.
func (a *Agent) requestSecret(ctx context.Context, id string) (string, string) {
	if id == "" {
		return "", ""
	}
	a.mu.Lock()
	if s, ok := a.secrets[id]; ok {
		a.mu.Unlock()
		return s.Header, s.Value
	}
	client, base, cred, agentID := a.client, a.State.File.ControllerURL, a.Cred, a.State.File.AgentID
	a.mu.Unlock()
	s, err := fetchRequestSecret(ctx, client, base, cred, agentID, id)
	if err != nil {
		return "", ""
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.secrets == nil {
		a.secrets = map[string]storedSecret{}
	}
	if old, ok := a.secrets[id]; ok && old.Version > s.Version {
		s = old
	} else {
		a.secrets[id] = s
	}
	return s.Header, s.Value
}
func fetchRequestSecret(ctx context.Context, client *http.Client, base, cred, agentID, id string) (storedSecret, error) {
	var result storedSecret
	req, err := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(base, "/")+"/api/v1/agent/secrets/"+url.PathEscape(id), nil)
	if err != nil {
		return result, err
	}
	req.Header.Set("Authorization", "Bearer "+cred)
	req.Header.Set("X-Monik-Agent-Id", agentID)
	res, err := client.Do(req)
	if err != nil {
		return result, err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return result, fmt.Errorf("secret unavailable")
	}
	var body struct {
		Header  string `json:"header"`
		Value   string `json:"value"`
		Version int    `json:"version"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 128<<10)).Decode(&body); err != nil {
		return result, err
	}
	if body.Header == "" || len(body.Value) > protocol.MaxRequestBody {
		return result, fmt.Errorf("invalid secret material")
	}
	return storedSecret{ID: id, Header: body.Header, Value: body.Value, Version: body.Version}, nil
}
