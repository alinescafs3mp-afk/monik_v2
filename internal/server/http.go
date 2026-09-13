package server

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/actions"
	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
	"github.com/alinescafs3mp-afk/monik_v2/internal/rules"
	"github.com/alinescafs3mp-afk/monik_v2/internal/secure"
	"github.com/alinescafs3mp-afk/monik_v2/internal/storage"
	"github.com/alinescafs3mp-afk/monik_v2/internal/version"
)

func (a *App) routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /health", a.serveUI)
	mux.HandleFunc("GET /api/v1/setup/status", a.handleSetupStatus)
	mux.HandleFunc("POST /api/v1/setup", a.handleSetup)
	mux.HandleFunc("POST /api/v1/login", a.handleLogin)
	mux.HandleFunc("POST /api/v1/logout", a.handleLogout)
	mux.HandleFunc("GET /api/v1/me", a.needAuth(a.handleMe))
	mux.HandleFunc("GET /api/v1/overview", a.needAuth(a.handleOverview))
	mux.HandleFunc("GET /api/v1/agents", a.needAuth(a.handleAgents))
	mux.HandleFunc("GET /api/v1/agents/{id}", a.needAuth(a.handleAgent))
	mux.HandleFunc("GET /api/v1/services", a.needAuth(a.handleServices))
	mux.HandleFunc("GET /api/v1/services/{id}", a.needAuth(a.handleService))
	mux.HandleFunc("GET /api/v1/incidents", a.needAuth(a.handleIncidents))
	mux.HandleFunc("GET /api/v1/operations", a.needAuth(a.handleOperations))
	mux.HandleFunc("GET /api/v1/operations/{id}", a.needAuth(a.handleOperation))
	mux.HandleFunc("POST /api/v1/operations", a.needAuth(a.handleSubmitOp))
	mux.HandleFunc("POST /api/v1/operations/lookup", a.needAuth(a.handleLookupOp))
	mux.HandleFunc("GET /api/v1/events", a.needAuth(a.handleSSE))
	mux.HandleFunc("GET /api/v1/history/point", a.needAuth(a.handleHistoryPoint))
	mux.HandleFunc("GET /api/v1/history/series", a.needAuth(a.handleHistorySeries))
	mux.HandleFunc("GET /api/v1/settings", a.needAuth(a.handleSettings))
	mux.HandleFunc("POST /api/v1/settings", a.needAuth(a.handleSettingsPost))
	mux.HandleFunc("POST /api/v1/reauth", a.needAuth(a.handleReauth))
	mux.HandleFunc("GET /api/v1/diagnostics", a.needAuth(a.handleDiagnostics))
	mux.HandleFunc("GET /api/v1/releases", a.needAuth(a.handleReleases))
	mux.HandleFunc("POST /api/v1/releases/import", a.needAuth(a.handleReleaseImport))
	mux.HandleFunc("GET /api/v1/enrollment", a.needAuth(a.handleEnrollmentGet))
	mux.HandleFunc("GET /api/v1/secrets", a.needAuth(a.handleSecrets))
	mux.HandleFunc("GET /api/v1/backups", a.needAuth(a.handleBackups))
	mux.HandleFunc("GET /api/v1/actions", a.needAuth(func(w http.ResponseWriter, r *http.Request, s *storage.Session) {
		a.writeJSON(w, 200, map[string]any{"actions": actions.Registry})
	}))
	mux.HandleFunc("GET /api/v1/agent/identity", a.handleControllerIdentity)
	mux.HandleFunc("POST /api/v1/agent/enroll", a.handleEnroll)
	mux.HandleFunc("POST /api/v1/agent/report", a.handleReport)
	mux.HandleFunc("GET /api/v1/agent/tuf/{name}", a.handleTUF)
	mux.HandleFunc("GET /api/v1/agent/artifacts/{name}", a.handleArtifact)
	mux.HandleFunc("/", a.serveUI)
}

func (a *App) needAuth(fn func(http.ResponseWriter, *http.Request, *storage.Session)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s := a.sessionFrom(r)
		if s == nil {
			a.writeErr(w, 401, "unauthenticated", "login required")
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Header.Get("X-CSRF-Token") != s.CSRF {
			a.writeErr(w, 403, "csrf", "CSRF token missing or invalid")
			return
		}
		if s.Role == "viewer" && r.Method != http.MethodGet && r.Method != http.MethodHead {
			a.writeErr(w, 403, "forbidden", "viewer cannot mutate")
			return
		}
		fn(w, r, s)
	}
}

func (a *App) sessionFrom(r *http.Request) *storage.Session {
	c, err := r.Cookie("monik_session")
	if err != nil || c.Value == "" {
		return nil
	}
	s, err := a.Store.SessionByToken(c.Value)
	if err != nil {
		return nil
	}
	return s
}

func (a *App) setSessionCookie(w http.ResponseWriter, raw string, exp time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name: "monik_session", Value: raw, Path: "/", HttpOnly: true, Secure: a.TLS != nil,
		SameSite: http.SameSiteStrictMode, Expires: exp,
	})
}

func (a *App) handleSetupStatus(w http.ResponseWriter, r *http.Request) {
	a.writeJSON(w, 200, map[string]any{
		"setup_required":  !a.SetupComplete(),
		"advertised_url":  a.Cfg.AdvertisedURL,
		"default_url":     protocol.DefaultBootstrapURL,
		"listen":          a.Cfg.Listen,
		"version":         version.Version,
		"loopback_only_setup": !a.SetupComplete(),
	})
}

func (a *App) handleSetup(w http.ResponseWriter, r *http.Request) {
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		a.writeErr(w, 403, "setup_local_only", "initial setup is loopback-only")
		return
	}
	if a.SetupComplete() {
		a.writeErr(w, 409, "already_setup", "setup already completed")
		return
	}
	var req SetupRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		a.writeErr(w, 400, "malformed", "invalid json")
		return
	}
	if err := a.CompleteSetup(req); err != nil {
		a.writeErr(w, 400, "setup_failed", err.Error())
		return
	}
	a.writeJSON(w, 200, map[string]any{"ok": true, "advertised_url": a.Cfg.AdvertisedURL})
}

func (a *App) handleLogin(w http.ResponseWriter, r *http.Request) {
	if !a.rateLimit("login:"+clientIP(r), 8, time.Minute) {
		a.writeErr(w, 429, "rate_limited", "too many login attempts")
		return
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&body); err != nil {
		a.writeErr(w, 400, "malformed", "invalid json")
		return
	}
	u, err := a.Store.UserByName(body.Username)
	if err != nil || !secure.VerifyPassword(u.PasswordHash, body.Password) {
		a.writeErr(w, 401, "invalid_credentials", "invalid username or password")
		return
	}
	raw, sess, err := a.Store.CreateSession(u, 12*time.Hour, 10*time.Minute)
	if err != nil {
		a.writeErr(w, 500, "session", "could not create session")
		return
	}
	a.setSessionCookie(w, raw, sess.ExpiresAt)
	a.writeJSON(w, 200, map[string]any{"ok": true, "username": u.Username, "role": u.Role, "csrf": sess.CSRF})
}

func (a *App) handleLogout(w http.ResponseWriter, r *http.Request) {
	if s := a.sessionFrom(r); s != nil {
		_ = a.Store.DeleteSession(s.ID)
	}
	a.setSessionCookie(w, "", time.Unix(0, 0))
	a.writeJSON(w, 200, map[string]any{"ok": true})
}

func (a *App) handleMe(w http.ResponseWriter, r *http.Request, s *storage.Session) {
	recent := s.RecentAuthUntil != nil && a.Clock.Now().Before(*s.RecentAuthUntil)
	a.writeJSON(w, 200, map[string]any{
		"username": s.Username, "role": s.Role, "csrf": s.CSRF,
		"recent_auth": recent, "locale_default": "ru",
		"controller_id": a.ControllerID(), "advertised_url": a.Cfg.AdvertisedURL,
		"restore_mode": a.Cfg.RestoreMode, "version": version.Version,
	})
}

func (a *App) handleOverview(w http.ResponseWriter, r *http.Request, s *storage.Session) {
	agents, _ := a.Store.Agents()
	svcs, _ := a.Store.Services("")
	incs, _ := a.Store.OpenIncidents()
	ops, _ := a.Store.Operations(20)
	attn := 0
	for _, o := range ops {
		if o.Status == protocol.OpAttentionRequired || o.Status == protocol.OpCompletedWithErrs {
			attn++
		}
	}
	reporting := 0
	now := a.Clock.Now()
	var cards []map[string]any
	for _, ag := range agents {
		st, reason, _, _ := a.Store.State("agent", ag.ID)
		if st == "ok" {
			reporting++
		}
		host, obs, err := a.Store.LatestHost(ag.ID)
		card := map[string]any{
			"id": ag.ID, "name": display(ag), "os": ag.OS, "arch": ag.Arch,
			"state": st, "reason": reason, "pinned": ag.Pinned, "managed_ready": ag.ManagedReady,
			"version": ag.WorkerVersion, "last_live_at": ag.LastLiveAt,
		}
		if err == nil && host != nil {
			card["cpu"] = host.CPUPercent
			card["ram_used"] = host.RAMUsed
			card["ram_total"] = host.RAMTotal
			card["disks"] = host.Disks
			card["ping"] = host.Ping
			card["temperatures"] = host.Temperatures
			card["observed_at"] = obs
			card["age_seconds"] = now.Sub(obs).Seconds()
			card["breaches"] = rules.EvaluateHost(host, rules.DefaultRules())
		}
		cards = append(cards, card)
	}
	a.writeJSON(w, 200, map[string]any{
		"agents_total": len(agents), "agents_reporting": reporting,
		"services": len(svcs), "open_incidents": len(incs),
		"operations_attention": attn, "cards": cards, "incidents": incs,
		"server_time": now,
	})
}

func display(ag *storage.AgentRow) string {
	if ag.DisplayName != "" {
		return ag.DisplayName
	}
	if ag.Hostname != "" {
		return ag.Hostname
	}
	return ag.ID
}

func (a *App) handleAgents(w http.ResponseWriter, r *http.Request, s *storage.Session) {
	agents, err := a.Store.Agents()
	if err != nil {
		a.writeErr(w, 500, "db", err.Error())
		return
	}
	out := make([]map[string]any, 0, len(agents))
	for _, ag := range agents {
		st, reason, _, _ := a.Store.State("agent", ag.ID)
		out = append(out, map[string]any{
			"id": ag.ID, "display_name": display(ag), "hostname": ag.Hostname, "os": ag.OS, "arch": ag.Arch,
			"worker_version": ag.WorkerVersion, "service_host_version": ag.ServiceHostVersion,
			"managed_ready": ag.ManagedReady, "desired_revision": ag.DesiredRevision, "applied_revision": ag.AppliedRevision,
			"desired_hash": ag.DesiredHash, "applied_hash": ag.AppliedHash,
			"last_live_at": ag.LastLiveAt, "state": st, "reason": reason,
			"pinned": ag.Pinned, "hidden": ag.Hidden, "archived": ag.Archived, "revoked": ag.Revoked,
			"conflict": ag.Conflict, "addresses": json.RawMessage(orJSON(ag.Addresses)),
			"capabilities": json.RawMessage(orJSON(ag.Capabilities)),
		})
	}
	a.writeJSON(w, 200, map[string]any{"agents": out})
}

func orJSON(s string) string {
	if s == "" {
		return "null"
	}
	return s
}

func (a *App) handleAgent(w http.ResponseWriter, r *http.Request, s *storage.Session) {
	id := r.PathValue("id")
	ag, err := a.Store.Agent(id)
	if err != nil {
		a.writeErr(w, 404, "not_found", "agent not found")
		return
	}
	host, obs, _ := a.Store.LatestHost(id)
	svcs, _ := a.Store.Services(id)
	st, reason, since, _ := a.Store.State("agent", id)
	a.writeJSON(w, 200, map[string]any{
		"agent": ag, "host": host, "observed_at": obs, "state": st, "reason": reason, "since": since, "services": svcs,
	})
}

func (a *App) handleServices(w http.ResponseWriter, r *http.Request, s *storage.Session) {
	svcs, err := a.Store.Services(r.URL.Query().Get("agent_id"))
	if err != nil {
		a.writeErr(w, 500, "db", err.Error())
		return
	}
	type row struct {
		*storage.ServiceRow
		Observation *protocol.CheckObservation `json:"observation,omitempty"`
		AgentState  string                     `json:"agent_state,omitempty"`
	}
	var out []row
	for _, sv := range svcs {
		obs, _ := a.Store.LatestCheckObs(sv.ID)
		st, _, _, _ := a.Store.State("agent", sv.AgentID)
		out = append(out, row{sv, obs, st})
	}
	a.writeJSON(w, 200, map[string]any{"services": out})
}

func (a *App) handleService(w http.ResponseWriter, r *http.Request, s *storage.Session) {
	sv, err := a.Store.Service(r.PathValue("id"))
	if err != nil {
		a.writeErr(w, 404, "not_found", "service not found")
		return
	}
	obs, _ := a.Store.LatestCheckObs(sv.ID)
	a.writeJSON(w, 200, map[string]any{"service": sv, "observation": obs})
}

func (a *App) handleIncidents(w http.ResponseWriter, r *http.Request, s *storage.Session) {
	incs, _ := a.Store.OpenIncidents()
	a.writeJSON(w, 200, map[string]any{"incidents": incs})
}

func (a *App) handleOperations(w http.ResponseWriter, r *http.Request, s *storage.Session) {
	ops, err := a.Store.Operations(200)
	if err != nil {
		a.writeErr(w, 500, "db", err.Error())
		return
	}
	a.writeJSON(w, 200, map[string]any{"operations": ops})
}

func (a *App) handleOperation(w http.ResponseWriter, r *http.Request, s *storage.Session) {
	op, err := a.Store.Operation(r.PathValue("id"))
	if err != nil {
		a.writeErr(w, 404, "not_found", "operation not found")
		return
	}
	a.writeJSON(w, 200, op)
}

func (a *App) handleLookupOp(w http.ResponseWriter, r *http.Request, s *storage.Session) {
	var body struct {
		ClientRequestKey string          `json:"client_request_key"`
		Action           string          `json:"action"`
		TargetIDs        []string        `json:"target_ids"`
		Params           json.RawMessage `json:"params"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		a.writeErr(w, 400, "malformed", "invalid json")
		return
	}
	h := storage.RequestHash(body.Action, body.TargetIDs, body.Params)
	op, err := a.Store.LookupIdempotency(s.Username, body.ClientRequestKey, h)
	if errors.Is(err, storage.ErrIdempotencyConflict) {
		a.writeErr(w, 409, "idempotency_conflict", "same key used with a different request")
		return
	}
	if err != nil {
		a.writeJSON(w, 200, map[string]any{"found": false})
		return
	}
	a.writeJSON(w, 200, map[string]any{"found": true, "operation": op})
}

func (a *App) handleSSE(w http.ResponseWriter, r *http.Request, s *storage.Session) {
	fl, ok := w.(http.Flusher)
	if !ok {
		a.writeErr(w, 500, "sse", "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	cursor := int64(0)
	if v := r.Header.Get("Last-Event-ID"); v != "" {
		cursor, _ = strconv.ParseInt(v, 10, 64)
	}
	if q := r.URL.Query().Get("cursor"); q != "" {
		cursor, _ = strconv.ParseInt(q, 10, 64)
	}
	max, _ := a.Store.MaxEventID()
	if cursor > 0 && cursor < max-10000 {
		cursor = 0
		_, _ = w.Write([]byte("event: resnapshot\ndata: {\"reason\":\"cursor_expired\"}\n\n"))
		fl.Flush()
	}
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-tick.C:
			evs, err := a.Store.EventsAfter(cursor, 100)
			if err != nil {
				return
			}
			for _, e := range evs {
				cursor = e.ID
				_, _ = w.Write([]byte("id: " + strconv.FormatInt(e.ID, 10) + "\n"))
				_, _ = w.Write([]byte("event: " + e.Type + "\n"))
				_, _ = w.Write([]byte("data: " + e.Payload + "\n\n"))
			}
			if len(evs) > 0 {
				fl.Flush()
			}
		}
	}
}

func (a *App) handleHistoryPoint(w http.ResponseWriter, r *http.Request, s *storage.Session) {
	at, err := time.Parse(time.RFC3339, r.URL.Query().Get("at"))
	if err != nil {
		a.writeErr(w, 400, "bad_time", "at must be RFC3339 UTC")
		return
	}
	agentID := r.URL.Query().Get("agent_id")
	host, obs, err := a.Store.HostAt(agentID, at)
	if err != nil {
		a.writeJSON(w, 200, map[string]any{"at": at, "found": false})
		return
	}
	age := at.Sub(obs)
	a.writeJSON(w, 200, map[string]any{
		"at": at, "observed_at": obs, "age_seconds": age.Seconds(),
		"host": host, "precision": "raw_if_within_48h", "view": "event_time_corrected",
	})
}

func (a *App) handleHistorySeries(w http.ResponseWriter, r *http.Request, s *storage.Session) {
	from, err1 := time.Parse(time.RFC3339, r.URL.Query().Get("from"))
	to, err2 := time.Parse(time.RFC3339, r.URL.Query().Get("to"))
	if err1 != nil || err2 != nil || !to.After(from) {
		a.writeErr(w, 400, "bad_range", "from/to must be RFC3339 and to>from")
		return
	}
	if to.Sub(from) > 24*time.Hour+time.Minute {
		// still allowed; label precision
	}
	agentID := r.URL.Query().Get("agent_id")
	serviceID := r.URL.Query().Get("service_id")
	step := plotStep(to.Sub(from))
	out := map[string]any{"from": from, "to": to, "step_seconds": int(step.Seconds()), "mode": r.URL.Query().Get("mode"), "preserve_extrema": true}
	if agentID != "" {
		pts, _ := a.Store.HostSeries(agentID, from, to)
		out["host"] = downsample(pts, step)
		out["raw_available"] = to.Sub(from) <= protocol.RawRetention
	}
	if serviceID != "" {
		obs, _ := a.Store.CheckSeries(serviceID, from, to)
		out["service"] = obs
	}
	a.writeJSON(w, 200, out)
}

func plotStep(d time.Duration) time.Duration {
	h := d.Hours()
	switch {
	case h <= 1.01:
		return 5 * time.Second
	case h <= 2.01:
		return 10 * time.Second
	case h <= 3.01:
		return 15 * time.Second
	case h <= 6.01:
		return 30 * time.Second
	case h <= 12.01:
		return time.Minute
	default:
		return 2 * time.Minute
	}
}

func downsample(pts []map[string]any, step time.Duration) []map[string]any {
	if len(pts) == 0 {
		return pts
	}
	// Keep extrema: for each bucket retain min/max cpu plus last sample.
	type bkt struct {
		samples []map[string]any
	}
	var buckets []bkt
	var cur *bkt
	var start time.Time
	for _, p := range pts {
		t, _ := time.Parse(time.RFC3339Nano, p["observed_at"].(string))
		if cur == nil || t.Sub(start) >= step {
			buckets = append(buckets, bkt{})
			cur = &buckets[len(buckets)-1]
			start = t
		}
		cur.samples = append(cur.samples, p)
	}
	var out []map[string]any
	for _, b := range buckets {
		if len(b.samples) == 0 {
			continue
		}
		minP, maxP := b.samples[0], b.samples[0]
		for _, s := range b.samples {
			cv, ok1 := s["cpu_pct"].(float64)
			mn, ok2 := minP["cpu_pct"].(float64)
			mx, ok3 := maxP["cpu_pct"].(float64)
			if ok1 && ok2 && cv < mn {
				minP = s
			}
			if ok1 && ok3 && cv > mx {
				maxP = s
			}
		}
		last := b.samples[len(b.samples)-1]
		last["min"] = minP
		last["max"] = maxP
		last["count"] = len(b.samples)
		out = append(out, last)
	}
	return out
}

func (a *App) handleSettings(w http.ResponseWriter, r *http.Request, s *storage.Session) {
	var exp any
	if a.TLS != nil {
		if t, err := a.TLS.LeafExpiry(); err == nil {
			exp = t
		}
	}
	a.writeJSON(w, 200, map[string]any{
		"advertised_url":      a.Cfg.AdvertisedURL,
		"listen":              a.Cfg.Listen,
		"controller_id":       a.ControllerID(),
		"tls_leaf_expiry":     exp,
		"retention_raw_hours": 48,
		"restore_mode":        a.Cfg.RestoreMode,
		"telegram_deferred":   true,
	})
}

func (a *App) handleSettingsPost(w http.ResponseWriter, r *http.Request, s *storage.Session) {
	if s.Role != "owner" {
		a.writeErr(w, 403, "forbidden", "owner role required")
		return
	}
	var body struct {
		AdvertisedURL string `json:"advertised_url"`
		Listen        string `json:"listen"`
	}
	if err := parseJSON(r, &body); err != nil {
		a.writeErr(w, 400, "malformed", "invalid json")
		return
	}
	if body.AdvertisedURL != "" {
		if !strings.HasPrefix(body.AdvertisedURL, "https://") {
			a.writeErr(w, 400, "bad_url", "advertised_url must be https")
			return
		}
		_ = a.Store.SetSetting("advertised_url", body.AdvertisedURL)
		a.Cfg.AdvertisedURL = body.AdvertisedURL
	}
	if body.Listen != "" {
		_ = a.Store.SetSetting("listen", body.Listen)
		a.Cfg.Listen = body.Listen
	}
	a.Store.Audit(s.Username, "settings.save", "server", "settings updated")
	a.handleSettings(w, r, s)
}

func (a *App) handleReauth(w http.ResponseWriter, r *http.Request, s *storage.Session) {
	var body struct {
		Password string `json:"password"`
	}
	if err := parseJSON(r, &body); err != nil {
		a.writeErr(w, 400, "malformed", "invalid json")
		return
	}
	u, err := a.Store.UserByName(s.Username)
	if err != nil || !secure.VerifyPassword(u.PasswordHash, body.Password) {
		a.writeErr(w, 401, "invalid_credentials", "invalid password")
		return
	}
	until := a.Clock.Now().Add(10 * time.Minute)
	if err := a.Store.TouchRecentAuth(s.ID, until); err != nil {
		a.writeErr(w, 500, "session", "could not refresh recent authentication")
		return
	}
	a.writeJSON(w, 200, map[string]any{"ok": true, "recent_auth_until": until})
}

func (a *App) handleDiagnostics(w http.ResponseWriter, r *http.Request, s *storage.Session) {
	maxE, _ := a.Store.MaxEventID()
	a.writeJSON(w, 200, map[string]any{
		"version": version.Version, "commit": version.Commit,
		"event_cursor": maxE, "db": a.Store.Path(),
		"setup_complete": a.SetupComplete(),
	})
}

func (a *App) handleReleases(w http.ResponseWriter, r *http.Request, s *storage.Session) {
	rels, _ := a.Store.Releases()
	a.writeJSON(w, 200, map[string]any{"releases": rels})
}

func (a *App) handleEnrollmentGet(w http.ResponseWriter, r *http.Request, s *storage.Session) {
	a.writeJSON(w, 200, map[string]any{
		"advertised_url": a.Cfg.AdvertisedURL,
		"ca_cert_pem":    string(a.CACertPEM()),
		"bootstrap_default": protocol.DefaultBootstrapURL,
		"instructions": map[string]string{
			"linux":   "monik-agent setup --profile enrollment.yaml",
			"windows": "monik-agent.exe setup --profile enrollment.yaml",
		},
	})
}

func (a *App) handleSecrets(w http.ResponseWriter, r *http.Request, s *storage.Session) {
	list, _ := a.Store.ListSecrets()
	a.writeJSON(w, 200, map[string]any{"secrets": list})
}

func (a *App) handleBackups(w http.ResponseWriter, r *http.Request, s *storage.Session) {
	list, _ := a.Store.Backups()
	a.writeJSON(w, 200, map[string]any{"backups": list})
}

func (a *App) handleControllerIdentity(w http.ResponseWriter, r *http.Request) {
	a.writeJSON(w, 200, map[string]any{
		"controller_id": a.ControllerID(),
		"protocol_min":  version.ProtocolMin,
		"protocol_max":  version.ProtocolMax,
		"writable":      !a.Cfg.RestoreMode,
		"product":       version.Product,
	})
}

func (a *App) handleTUF(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if strings.Contains(name, "..") || strings.ContainsAny(name, `/\`) {
		a.writeErr(w, 400, "bad_name", "invalid metadata name")
		return
	}
	http.ServeFile(w, r, a.Cfg.DataDir+"/tuf/repository/"+name)
}

func (a *App) handleArtifact(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if strings.Contains(name, "..") || strings.ContainsAny(name, `/\`) {
		a.writeErr(w, 400, "bad_name", "invalid artifact name")
		return
	}
	http.ServeFile(w, r, a.Cfg.DataDir+"/tuf/targets/"+name)
}

func recentOK(s *storage.Session, now time.Time) bool {
	return s.RecentAuthUntil != nil && now.Before(*s.RecentAuthUntil)
}

func parseJSON(r *http.Request, v any) error {
	return json.NewDecoder(io.LimitReader(r.Body, 8<<20)).Decode(v)
}

func (a *App) requireRecent(w http.ResponseWriter, s *storage.Session) bool {
	if !recentOK(s, a.Clock.Now()) {
		a.writeErr(w, 401, "recent_auth_required", "re-authenticate to perform this action")
		return false
	}
	return true
}

func newEnrollmentProfile(url, ca, code string) map[string]any {
	return map[string]any{
		"schema_version": 3,
		"controller_url": url,
		"ca_cert_pem":    ca,
		"enrollment_code": code,
	}
}
