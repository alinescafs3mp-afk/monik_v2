package server

import (
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/alinescafs3mp-afk/monik_v2/internal/actions"
	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
	"github.com/alinescafs3mp-afk/monik_v2/internal/storage"
)

// Bounds expensive password work across source addresses, and long-lived event
// streams across sessions. Neither limiter queues untrusted work in memory.
const maxPasswordWork = 2
const maxEventStreams = 128
const maxSessionEventStreams = 8

type networkLimits struct {
	mu        sync.Mutex
	passwords int
	streams   int
	sessions  map[string]int
}

func (a *App) passwordWork(w http.ResponseWriter) (func(), bool) {
	a.network.mu.Lock()
	if a.network.passwords >= maxPasswordWork {
		a.network.mu.Unlock()
		w.Header().Set("Retry-After", "2")
		a.writeErr(w, http.StatusServiceUnavailable, "authentication_busy", "authentication capacity is busy; retry later")
		return nil, false
	}
	a.network.passwords++
	a.network.mu.Unlock()
	var once sync.Once
	return func() { once.Do(func() { a.network.mu.Lock(); a.network.passwords--; a.network.mu.Unlock() }) }, true
}

func (a *App) eventStreamSlot(sessionID string) (func(), bool) {
	a.network.mu.Lock()
	defer a.network.mu.Unlock()
	if sessionID == "" || a.network.streams >= maxEventStreams || a.network.sessions[sessionID] >= maxSessionEventStreams {
		return nil, false
	}
	if a.network.sessions == nil {
		a.network.sessions = make(map[string]int)
	}
	a.network.streams++
	a.network.sessions[sessionID]++
	var once sync.Once
	return func() {
		once.Do(func() {
			a.network.mu.Lock()
			defer a.network.mu.Unlock()
			a.network.streams--
			a.network.sessions[sessionID]--
			if a.network.sessions[sessionID] == 0 {
				delete(a.network.sessions, sessionID)
			}
		})
	}, true
}

// Browsers supply Origin for fetch/POST. Do not infer trust from forwarded
// headers supplied by a client. Absent browser metadata is allowed for native
// JSON clients; it is NOT a substitute for a cookie, CSRF token, or role check.
func sameBrowserOrigin(r *http.Request, value string, isOrigin bool) bool {
	u, err := url.Parse(value)
	if err != nil || u.User != nil || u.Opaque != "" || u.Host == "" || u.Hostname() == "" || u.Fragment != "" {
		return false
	}
	if isOrigin && (u.Path != "" || u.RawQuery != "" || u.ForceQuery || strings.ContainsAny(value, "?#")) {
		return false
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	expected, err := url.Parse(scheme + "://" + r.Host)
	if err != nil || expected.User != nil || expected.Host == "" {
		return false
	}
	authority := func(v *url.URL) string {
		port := v.Port()
		if port == "" {
			if v.Scheme == "https" {
				port = "443"
			} else {
				port = "80"
			}
		}
		return net.JoinHostPort(strings.ToLower(v.Hostname()), port)
	}
	return u.Scheme == scheme && authority(u) == authority(expected)
}

func (a *App) browserRequestAllowed(w http.ResponseWriter, r *http.Request) bool {
	safe := r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions
	if safe || !strings.HasPrefix(r.URL.Path, "/api/v1/") || strings.HasPrefix(r.URL.Path, "/api/v1/agent/") {
		return true
	}
	origins := r.Header.Values("Origin")
	site := r.Header.Get("Sec-Fetch-Site")
	if len(origins) > 1 || len(origins) == 1 && !sameBrowserOrigin(r, origins[0], true) || site == "cross-site" || site == "same-site" {
		a.writeErr(w, http.StatusForbidden, "origin", "same-origin browser request required")
		return false
	}
	if len(origins) == 0 {
		refs := r.Header.Values("Referer")
		if len(refs) > 1 || len(refs) == 1 && !sameBrowserOrigin(r, refs[0], false) {
			a.writeErr(w, http.StatusForbidden, "origin", "same-origin browser request required")
			return false
		}
	}
	// Login has no existing CSRF token; setup has no existing owner. A simple
	// cross-site form must not be allowed to create either kind of authority.
	if r.URL.Path == "/api/v1/login" || r.URL.Path == "/api/v1/setup" {
		ct, _, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if e != nil || ct != "application/json" {
			a.writeErr(w, http.StatusUnsupportedMediaType, "json_required", "Content-Type application/json is required")
			return false
		}
	}
	return true
}

// The operation journal is readable by viewers, but an enrollment code is an
// active capability, not harmless historical metadata. Copy before redaction;
// the owner and the durable result must retain the original evidence.
func operationView(op *protocol.Operation, s *storage.Session) *protocol.Operation {
	if op == nil || s.Role == "owner" {
		return op
	}
	def, err := actions.Lookup(op.Action)
	if err == nil && def.Risk != "sensitive" {
		return op
	}
	out := *op
	out.Params = map[string]any{"redacted": true}
	out.Targets = append([]protocol.TargetResult(nil), op.Targets...)
	for i := range out.Targets {
		out.Targets[i].Evidence = map[string]any{"redacted": true}
	}
	return &out
}

func (a *App) currentOwner(w http.ResponseWriter, previous *storage.Session, recent bool) *storage.Session {
	current, err := a.Store.RevalidateSession(previous)
	if err != nil || current.Role != "owner" {
		a.writeErr(w, http.StatusUnauthorized, "unauthorized", "session or permissions changed; sign in again")
		return nil
	}
	if recent && !a.requireRecent(w, current) {
		return nil
	}
	return current
}
