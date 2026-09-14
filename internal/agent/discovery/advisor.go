package discovery

import (
	"context"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/agent/checks"
	"github.com/alinescafs3mp-afk/monik_v2/internal/netutil"
	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
)

type adviceEntry struct {
	at     time.Time
	advice []protocol.CheckSuggestion
}
type Advisor struct {
	mu    sync.Mutex
	cache map[string]adviceEntry
}

func (a *Advisor) Reset() { a.mu.Lock(); defer a.mu.Unlock(); a.cache = map[string]adviceEntry{} }

var healthPaths = []struct{ path, purpose string }{{"/readyz", "readiness"}, {"/ready", "readiness"}, {"/healthz", "readiness"}, {"/health", "application"}, {"/actuator/health", "application"}, {"/livez", "liveness"}, {"/-/ready", "readiness"}, {"/-/healthy", "liveness"}, {"/api/health", "application"}}

// Suggest is bounded and never changes an existing check. All requests are local,
// unauthenticated GETs; no docs crawling, guessed tokens, inference or mutations.
func (a *Advisor) Suggest(ctx context.Context, ep protocol.DiscoveredEndpoint, locals []net.IP) []protocol.CheckSuggestion {
	key := ep.DialTarget + "|" + ep.URL + "|" + ep.HostHeader
	a.mu.Lock()
	cached, ok := a.cache[key]
	a.mu.Unlock()
	if ok && time.Since(cached.at) < 10*time.Minute {
		return append([]protocol.CheckSuggestion(nil), cached.advice...)
	}
	advice := SuggestHealth(ctx, ep, locals)
	a.mu.Lock()
	if a.cache == nil {
		a.cache = map[string]adviceEntry{}
	}
	if len(a.cache) >= 256 {
		for k, v := range a.cache {
			if time.Since(v.at) >= 10*time.Minute {
				delete(a.cache, k)
			}
		}
		if len(a.cache) >= 256 {
			a.cache = map[string]adviceEntry{}
		}
	}
	a.cache[key] = adviceEntry{time.Now(), advice}
	a.mu.Unlock()
	return advice
}

func SuggestHealth(ctx context.Context, ep protocol.DiscoveredEndpoint, locals []net.IP) []protocol.CheckSuggestion {
	var out []protocol.CheckSuggestion
	ambiguousUnready := false
	// An auth wall or invalid TLS is a prerequisite, not an invitation to bypass it.
	base := protocol.CheckDefinition{ID: "suggest", ServiceID: ep.ServiceID, URL: ep.URL, DialTarget: ep.DialTarget, HostHeader: ep.HostHeader, TLSServerName: ep.TLSServerName, Method: "GET", Kind: "baseline_http", TimeoutSeconds: 1, IntervalSeconds: 5}
	probe := func(d protocol.CheckDefinition) protocol.CheckObservation {
		c, cancel := context.WithTimeout(ctx, 650*time.Millisecond)
		defer cancel()
		return checks.Run(c, d, locals, "", "")
	}
	root := probe(base)
	if root.Transport != "ok" {
		return nil
	}
	if root.HTTPStatus != nil && (*root.HTTPStatus == 401 || *root.HTTPStatus == 403) {
		return nil
	}
	paths := append([]struct{ path, purpose string }{}, healthPaths...)
	if root.Feedback != nil && root.Feedback.Health != "" {
		paths = append([]struct{ path, purpose string }{{"/", "application"}}, paths...)
	}
	for _, candidate := range paths {
		if ctx.Err() != nil {
			break
		}
		d := base
		d.Path = candidate.path
		obs := root
		if candidate.path != "/" {
			obs = probe(d)
		}
		if obs.Transport != "ok" || obs.HTTPStatus == nil || obs.Feedback == nil {
			continue
		}
		code := *obs.HTTPStatus
		health := obs.Feedback.Health
		if (code >= 500 || code == 401 || code == 403) && health == "" && candidate.purpose != "liveness" {
			// Even without a standard body, a failed readiness route must not be
			// masked by automatically preferring a successful liveness endpoint.
			ambiguousUnready = true
			d.Kind = "http_health"
			d.ExpectedStatus = []int{200}
			d.RequestVersion = 1
			d.Purpose = candidate.purpose
			d.Origin = "suggested"
			out = append(out, protocol.CheckSuggestion{Definition: d, Confidence: "medium", Reason: "candidate readiness/health route requires authentication or returned a server error without an interpretable health body; review before choosing another route", Transport: obs.Transport, HTTPStatus: obs.HTTPStatus, ObservedAt: obs.ObservedAt})
			continue
		}
		if code >= 300 && code < 400 || code == 404 || code == 401 || code == 403 {
			continue
		}
		if health == "" {
			if code == 200 && obs.Feedback.BodyState == "empty" {
				d.Kind = "http_health"
				d.ExpectedStatus = []int{200}
				d.RequestVersion = 1
				d.Purpose = "responsiveness"
				d.Origin = "suggested"
				out = append(out, protocol.CheckSuggestion{Definition: d, Confidence: "medium", Reason: "HTTP 200 with empty body on a common route; application meaning is unknown, verify documentation before using", Transport: obs.Transport, HTTPStatus: obs.HTTPStatus, ObservedAt: obs.ObservedAt})
			}
			continue
		}
		if obs.Feedback.BodyState != "sampled" {
			continue
		}
		// Require a recognizable health body, not a login page/SPA or merely 200.
		d.Kind = "http_health"
		d.ExpectedStatus = []int{http.StatusOK}
		d.RequestVersion = 1
		d.ExpectHealth = true
		d.Purpose = candidate.purpose
		d.Origin = "auto_detected"
		out = append(out, protocol.CheckSuggestion{Definition: d, Confidence: "high", Reason: "bounded local health endpoint with explicit health vocabulary; all future checks must remain healthy", Transport: obs.Transport, HTTPStatus: obs.HTTPStatus, Health: health, ObservedAt: obs.ObservedAt, AutoEligible: true})
	}
	// A catch-all JSON success is not evidence of a health route. Negative controls
	// contain no random user data, cannot leave the origin and share the same budget.
	if len(out) > 0 && ctx.Err() == nil {
		d := base
		d.Path = "/__monik_nonexistent_health_probe__"
		control := probe(d)
		if control.Transport != "ok" || control.HTTPStatus == nil || (*control.HTTPStatus != 404 && *control.HTTPStatus != 410) {
			for i := range out {
				out[i].AutoEligible = false
				out[i].Confidence = "low"
				out[i].Reason = "negative control did not return a verified missing-route status; owner review required"
			}
		}
	} else if len(out) > 0 {
		for i := range out {
			out[i].AutoEligible = false
			out[i].Confidence = "medium"
			out[i].Reason = "discovery budget ended before negative-control validation"
		}
	}
	if ambiguousUnready {
		for i := range out {
			out[i].AutoEligible = false
			out[i].Reason += "; another readiness/health candidate has unresolved error or authentication requirements"
		}
	}
	// Never choose a passing liveness URL to mask a failing readiness/health URL.
	sort.SliceStable(out, func(i, j int) bool {
		ni, nj := adverseSuggestion(out[i]), adverseSuggestion(out[j])
		if ni != nj {
			return ni
		}
		if (out[i].Health != "") != (out[j].Health != "") {
			return out[i].Health != ""
		}
		return purposeRank(out[i].Definition.Purpose) < purposeRank(out[j].Definition.Purpose)
	})
	if len(out) > 4 {
		out = out[:4]
	}
	return out
}
func adverseSuggestion(s protocol.CheckSuggestion) bool {
	return checks.NegativeHealth(s.Health) || s.HTTPStatus != nil && (*s.HTTPStatus >= 500 || *s.HTTPStatus == 401 || *s.HTTPStatus == 403)
}
func purposeRank(s string) int {
	switch s {
	case "readiness":
		return 0
	case "application":
		return 1
	default:
		return 2
	}
}
func knownNonHTTP(process string) bool {
	process = strings.TrimSuffix(strings.ToLower(process), ".exe")
	switch process {
	case "sshd", "postgres", "postgresql", "redis-server", "mysqld", "mariadbd", "mongod":
		return true
	}
	return false
}

// InspectListeners limits concurrency and total elapsed discovery work. Inventory
// gaps are retained explicitly; they never imply deletion of a monitored service.
func InspectListeners(ctx context.Context, ls []Listener, locals []net.IP, budget int, advisor *Advisor) *protocol.DiscoveryDelta {
	if advisor == nil {
		advisor = &Advisor{}
	}
	return InspectListenersPolicy(ctx, ls, locals, budget, advisor, nil, true)
}

// InspectListenersPolicy inventories disabled targets from their configuration without
// sending HTTP, TLS, or health-advice requests. It never broadens local probe scope.
func InspectListenersPolicy(ctx context.Context, ls []Listener, locals []net.IP, budget int, advisor *Advisor, disabled map[string]protocol.CheckDefinition, advise bool) *protocol.DiscoveryDelta {
	if advisor == nil {
		advisor = &Advisor{}
	}
	start := time.Now().UTC()
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	targets := DialTargets(ls, locals)
	sort.Strings(targets)
	delta := &protocol.DiscoveryDelta{Kind: "snapshot", StartedAt: start, ListenerCount: len(ls), CoverageComplete: true}
	if budget < 0 {
		budget = 0
	}
	if len(targets) > budget {
		targets = targets[:budget]
		delta.Truncated = true
		delta.CoverageComplete = false
	}
	type result struct {
		ep *protocol.DiscoveredEndpoint
		un *protocol.UnresolvedCandidate
	}
	jobs := make(chan string)
	results := make(chan result, len(targets))
	var wg sync.WaitGroup
	processes := map[string]Listener{}
	for _, l := range ls {
		for _, d := range DialTargets([]Listener{l}, locals) {
			processes[d] = l
		}
	}
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for target := range jobs {
				l := processes[target]
				if _, blocked := disabled[target]; blocked {
					results <- result{un: &protocol.UnresolvedCandidate{DialTarget: target, ProcessName: l.Process, Reason: "monitoring disabled; listener present; no network probe sent"}}
					continue
				}
				if knownNonHTTP(l.Process) {
					results <- result{un: &protocol.UnresolvedCandidate{DialTarget: target, ProcessName: l.Process, Reason: "known non-HTTP process; no HTTP payload sent; native protocol check required"}}
					continue
				}
				if ctx.Err() != nil {
					return
				}
				if _, err := netutil.AllowedDial(target, netutil.DefaultPolicy(), locals); err != nil {
					results <- result{un: &protocol.UnresolvedCandidate{DialTarget: target, Reason: "outside local listener policy"}}
					continue
				}
				ep, un := identifyOne(target, 500*time.Millisecond)
				if ep != nil {
					ep.ProcessName = l.Process
					ep.PID = l.PID
					if ep.SpeaksTLS {
						ep.IdentificationNote = "TLS protocol observed; certificate validity is checked separately by the health probe"
					}
					if advise {
						c, stop := context.WithTimeout(ctx, 6*time.Second)
						ep.Suggestions = advisor.Suggest(c, *ep, locals)
						stop()
					}
				} else if un != nil {
					un.ProcessName = l.Process
				}
				results <- result{ep: ep, un: un}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for _, t := range targets {
			select {
			case jobs <- t:
			case <-ctx.Done():
				return
			}
		}
	}()
	wg.Wait()
	close(results)
	count := 0
	for v := range results {
		count++
		if v.ep != nil {
			delta.Confirmed = append(delta.Confirmed, *v.ep)
		}
		if v.un != nil {
			delta.Unresolved = append(delta.Unresolved, *v.un)
		}
	}
	if count < len(targets) {
		delta.Truncated = true
		delta.CoverageComplete = false
		delta.PermissionGaps = append(delta.PermissionGaps, "discovery elapsed-time budget reached")
	}
	sort.Slice(delta.Confirmed, func(i, j int) bool { return delta.Confirmed[i].DialTarget < delta.Confirmed[j].DialTarget })
	delta.EndedAt = time.Now().UTC()
	return delta
}
