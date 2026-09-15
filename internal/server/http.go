package server

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/actions"
	"github.com/alinescafs3mp-afk/monik_v2/internal/netutil"
	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
	"github.com/alinescafs3mp-afk/monik_v2/internal/rules"
	"github.com/alinescafs3mp-afk/monik_v2/internal/secure"
	"github.com/alinescafs3mp-afk/monik_v2/internal/storage"
	"github.com/alinescafs3mp-afk/monik_v2/internal/version"
)

func (a *App) routes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/agents/{id}/console-config", a.needAuth(a.handleConsoleConfigure))
	mux.HandleFunc("GET /api/v1/agent/console-channel", a.handleAgentConsoleChannel)
	mux.HandleFunc("GET /api/v1/agents/{id}/agent-console", a.needAuth(a.handleAgentConsoleInfo))
	mux.HandleFunc("POST /api/v1/agents/{id}/agent-console-ticket", a.needAuth(a.handleAgentConsoleTicket))
	mux.HandleFunc("GET /api/v1/agents/{id}/agent-console-stream", a.needAuth(a.handleAgentConsoleSocket))
	mux.HandleFunc("GET /api/v1/agents/{id}/console", a.needAuth(a.handleConsoleInfo))
	mux.HandleFunc("POST /api/v1/agents/{id}/console-ticket", a.needAuth(a.handleConsoleTicket))
	mux.HandleFunc("GET /api/v1/agents/{id}/console-stream", a.needAuth(a.handleConsoleSocket))
	mux.HandleFunc("GET /health", a.serveUI)
	mux.HandleFunc("GET /api/v1/setup/status", a.handleSetupStatus)
	mux.HandleFunc("POST /api/v1/setup", a.handleSetup)
	mux.HandleFunc("POST /api/v1/login", a.handleLogin)
	mux.HandleFunc("POST /api/v1/logout", a.needAuth(func(w http.ResponseWriter, r *http.Request, s *storage.Session) { a.handleLogout(w, r) }))
	mux.HandleFunc("GET /api/v1/sessions", a.needAuth(func(w http.ResponseWriter, r *http.Request, s *storage.Session) {
		rows, err := a.Store.BrowserSessions(s.UserID, s.ID)
		if err != nil {
			a.writeErr(w, 500, "sessions", "could not read sessions")
			return
		}
		truncated := len(rows) > 100
		if truncated {
			rows = rows[:100]
		}
		a.writeJSON(w, 200, map[string]any{"sessions": rows, "truncated": truncated})
	}))
	mux.HandleFunc("GET /api/v1/me", a.needAuth(a.handleMe))
	mux.HandleFunc("GET /api/v1/overview", a.needAuth(a.handleOverview))
	mux.HandleFunc("GET /api/v1/agents", a.needAuth(a.handleAgents))
	mux.HandleFunc("GET /api/v1/agents/{id}", a.needAuth(a.handleAgent))
	mux.HandleFunc("GET /api/v1/services", a.needAuth(a.handleServices))
	mux.HandleFunc("GET /api/v1/services/{id}", a.needAuth(a.handleService))
	mux.HandleFunc("GET /api/v1/incidents", a.needAuth(a.handleIncidents))
	mux.HandleFunc("GET /api/v1/operations/summary", a.needAuth(a.handleOperationCounts))
	mux.HandleFunc("POST /api/v1/operations/read", a.needAuth(a.handleOperationReads))
	mux.HandleFunc("GET /api/v1/operations", a.needAuth(a.handleOperations))
	mux.HandleFunc("GET /api/v1/operations/{id}", a.needAuth(a.handleOperation))
	mux.HandleFunc("POST /api/v1/operations", a.needAuth(a.handleSubmitOp))
	mux.HandleFunc("POST /api/v1/operations/lookup", a.needAuth(a.handleLookupOp))
	mux.HandleFunc("GET /api/v1/events", a.needAuth(a.handleSSE))
	mux.HandleFunc("GET /api/v1/exports/{id}", a.needAuth(a.handleExportDownload))
	mux.HandleFunc("GET /api/v1/history/point", a.needAuth(a.handleHistoryPoint))
	mux.HandleFunc("GET /api/v1/history/series", a.needAuth(a.handleHistorySeries))
	mux.HandleFunc("GET /api/v1/monitoring", a.needAuth(a.handleMonitoring))
	mux.HandleFunc("GET /api/v1/enrollment/policy", a.needAuth(a.handleAdmissionPolicy))
	mux.HandleFunc("GET /api/v1/settings", a.needAuth(a.handleSettings))
	mux.HandleFunc("POST /api/v1/settings", a.needAuth(a.handleSettingsPost))
	mux.HandleFunc("POST /api/v1/reauth", a.needAuth(a.handleReauth))
	mux.HandleFunc("POST /api/v1/account/password", a.needAuth(a.handlePasswordChange))
	mux.HandleFunc("GET /api/v1/diagnostics", a.needAuth(a.handleDiagnostics))
	mux.HandleFunc("GET /api/v1/releases", a.needAuth(a.handleReleases))
	mux.HandleFunc("GET /api/v1/rollouts", a.needAuth(a.handleRollouts))
	mux.HandleFunc("POST /api/v1/releases/import", a.needAuth(a.handleReleaseImport))
	mux.HandleFunc("GET /api/v1/enrollment", a.needAuth(a.handleEnrollmentGet))
	mux.HandleFunc("GET /api/v1/installers", a.needAuth(a.handleInstallers))
	mux.HandleFunc("POST /api/v1/installers/{platform}", a.needAuth(a.handleInstallerDownload))
	mux.HandleFunc("GET /api/v1/agent/installation", a.handleInstallationStatus)
	mux.HandleFunc("GET /api/v1/secrets", a.needAuth(a.handleSecrets))
	mux.HandleFunc("GET /api/v1/backups", a.needAuth(a.handleBackups))
	mux.HandleFunc("GET /api/v1/actions", a.needAuth(func(w http.ResponseWriter, r *http.Request, s *storage.Session) {
		a.writeJSON(w, 200, map[string]any{"actions": actions.Registry, "unavailable": actionAvailability()})
	}))
	mux.HandleFunc("GET /api/v1/agent/identity", a.handleControllerIdentity)
	mux.HandleFunc("POST /api/v1/agent/enroll", a.handleEnroll)
	mux.HandleFunc("POST /api/v1/agent/announce", a.handleAnnounce)
	mux.HandleFunc("GET /api/v1/enrollment/pending", a.needAuth(a.handlePendingAgents))
	mux.HandleFunc("POST /api/v1/agent/report", a.handleReport)
	mux.HandleFunc("GET /api/v1/agent/tuf/{name}", a.handleTUF)
	mux.HandleFunc("GET /api/v1/agent/artifacts/{name...}", a.handleArtifact)
	mux.HandleFunc("GET /api/v1/agent/releases/{release}/tuf/{name}", a.handlePublishedMetadata)
	mux.HandleFunc("GET /api/v1/agent/releases/{release}/artifacts/{name...}", a.handlePublishedArtifact)
	mux.HandleFunc("GET /api/v1/agent/secrets/{id}", a.handleAgentSecret)
	mux.HandleFunc("GET /api/v1/agent/update-root", a.handleAgentUpdateRoot)
	mux.HandleFunc("POST /api/v1/agent/credential/next", a.handleCredentialNext)
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
		"setup_required":      !a.SetupComplete(),
		"advertised_url":      a.Cfg.AdvertisedURL,
		"default_url":         protocol.DefaultBootstrapURL,
		"listen":              a.Cfg.Listen,
		"version":             version.Version,
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
	if err := parseJSONLimit(r, &req, 1<<20); err != nil {
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
		Remember bool   `json:"remember"`
	}
	if err := parseJSONLimit(r, &body, 1<<16); err != nil {
		a.writeErr(w, 400, "malformed", "invalid json")
		return
	}
	if len(body.Username) > 255 || len(body.Password) > 1024 || body.Username == "" || body.Password == "" {
		a.writeErr(w, 400, "invalid_credentials", "username and password are required within size limits")
		return
	}
	u, err := a.Store.UserByName(body.Username)
	if err != nil || !secure.VerifyPassword(u.PasswordHash, body.Password) {
		a.writeErr(w, 401, "invalid_credentials", "invalid username or password")
		return
	}
	ttl := 12 * time.Hour
	if body.Remember {
		ttl = 30 * 24 * time.Hour
	}
	raw, sess, err := a.Store.CreateSession(u, ttl, 10*time.Minute)
	if err != nil {
		a.writeErr(w, 500, "session", "could not create session")
		return
	}
	cookie := &http.Cookie{Name: "monik_session", Value: raw, Path: "/", HttpOnly: true,
		Secure: a.TLS != nil || r.TLS != nil || strings.HasPrefix(a.Cfg.AdvertisedURL, "https://"), SameSite: http.SameSiteStrictMode}
	if body.Remember {
		cookie.Expires = sess.ExpiresAt
		cookie.MaxAge = int(ttl.Seconds())
	}
	http.SetCookie(w, cookie)
	a.writeJSON(w, 200, map[string]any{"ok": true, "username": u.Username, "role": u.Role, "csrf": sess.CSRF, "expires_at": sess.ExpiresAt, "remembered": body.Remember})
}

func (a *App) handleLogout(w http.ResponseWriter, r *http.Request) {
	if s := a.sessionFrom(r); s != nil {
		if err := a.Store.DeleteSession(s.ID); err != nil {
			a.writeErr(w, 503, "logout_failed", "session could not be revoked; retry sign out")
			return
		}
	}
	http.SetCookie(w, &http.Cookie{Name: "monik_session", Value: "", Path: "/", HttpOnly: true,
		Secure: a.TLS != nil || r.TLS != nil || strings.HasPrefix(a.Cfg.AdvertisedURL, "https://"), SameSite: http.SameSiteStrictMode, Expires: time.Unix(1, 0), MaxAge: -1})
	a.writeJSON(w, 200, map[string]any{"ok": true})
}

func (a *App) handleMe(w http.ResponseWriter, r *http.Request, s *storage.Session) {
	recent := s.RecentAuthUntil != nil && a.Clock.Now().Before(*s.RecentAuthUntil)
	a.writeJSON(w, 200, map[string]any{
		"username": s.Username, "role": s.Role, "csrf": s.CSRF,
		"recent_auth": recent, "locale_default": "ru", "session_expires_at": s.ExpiresAt,
		"controller_id": a.ControllerID(), "advertised_url": a.Cfg.AdvertisedURL,
		"restore_mode": a.Cfg.RestoreMode, "version": version.Version,
	})
}

func (a *App) handleOverview(w http.ResponseWriter, r *http.Request, s *storage.Session) {
	agents, err := a.Store.Agents()
	if err != nil {
		a.writeErr(w, 500, "db", "could not load agents")
		return
	}
	now := a.Clock.Now()
	svcs, err := a.serviceSummaries("", now)
	if err != nil {
		a.writeErr(w, 500, "db", "could not load services")
		return
	}
	svcs, inactiveServices := currentServices(svcs)
	incs, err := a.Store.OpenIncidents()
	if err != nil {
		a.writeErr(w, 500, "db", "could not load incidents")
		return
	}
	operationCounts, err := a.Store.OperationCounts()
	if err != nil {
		a.writeErr(w, 500, "db", "could not load operation counts")
		return
	}
	attention := operationCounts.UnreadAttention
	if err := a.annotateIncidents(incs, now); err != nil {
		a.writeErr(w, 500, "maintenance", "could not determine maintenance status")
		return
	}
	ruleVersion, err := a.Store.HostRulesAt(now)
	if err != nil {
		a.writeErr(w, 500, "rules", "could not load monitoring rules")
		return
	}
	unread, actionable := 0, 0
	for _, inc := range incs {
		if inc["acked_at"] == "" {
			unread++
			if inc["maintenance_active"] != true {
				actionable++
			}
		}
	}
	reporting, total := 0, 0
	cards := make([]map[string]any, 0, len(agents))
	for _, ag := range agents {
		if ag.Archived {
			continue
		}
		total++
		st, reason := contact(ag, now)
		if st == "ok" {
			reporting++
		}
		if ag.Hidden {
			continue
		}
		card := map[string]any{"id": ag.ID, "name": display(ag), "os": ag.OS, "arch": ag.Arch, "state": st, "reason": reason, "pinned": ag.Pinned, "managed_ready": ag.ManagedReady, "version": ag.WorkerVersion, "last_live_at": ag.LastLiveAt, "has_problem": st != "ok"}
		host, obs, err := a.Store.LatestHost(ag.ID)
		if err != nil && err != storage.ErrNotFound {
			a.writeErr(w, 500, "db", "could not load measurements")
			return
		}
		if host != nil {
			fresh := st == "ok" && now.Sub(obs) <= protocol.StaleContact && !obs.After(now.Add(5*time.Second))
			card["cpu"] = host.CPUPercent
			card["ram_used"] = host.RAMUsed
			card["ram_total"] = host.RAMTotal
			card["ram_available"] = host.RAMAvailable
			card["disks"] = host.Disks
			card["ping"] = host.Ping
			card["temperatures"] = host.Temperatures
			card["observed_at"] = obs
			card["age_seconds"] = now.Sub(obs).Seconds()
			card["fresh_for_seconds"] = protocol.StaleContact.Seconds()
			card["metrics_fresh"] = fresh
			breaches := rules.EvaluateHost(host, rules.ThresholdRules(ruleVersion.Rules))
			card["breaches"] = breaches
			if fresh && len(breaches) > 0 || !fresh {
				card["has_problem"] = true
			}
		}
		services := make([]serviceSummary, 0)
		serviceTotal, otherProblems := 0, 0
		for _, sv := range svcs {
			if sv.AgentID != ag.ID {
				continue
			}
			serviceTotal++
			visible := sv.Pinned && !sv.Hidden
			if visible {
				services = append(services, sv)
			}
			// Presentation cannot mute monitored failures. Conversely, inventory-only
			// services and pending configuration are not proven service failures.
			if serviceHasProblem(sv.State) {
				card["has_problem"] = true
				if !visible {
					otherProblems++
				}
			}
		}
		active, err := a.Store.InMaintenance("agent", ag.ID, now)
		if err != nil {
			a.writeErr(w, 500, "maintenance", "could not load maintenance")
			return
		}
		card["maintenance_active"] = active
		card["services"] = services
		card["services_total"] = serviceTotal
		card["services_selected"] = len(services)
		card["unselected_service_problems"] = otherProblems
		cards = append(cards, card)
	}
	a.writeJSON(w, 200, map[string]any{"agents_total": total, "agents_reporting": reporting, "services": len(svcs), "inactive_services": inactiveServices, "open_incidents": len(incs), "unread_incidents": unread, "actionable_incidents": actionable, "operations_attention": attention, "cards": cards, "incidents": incs, "server_time": now, "unavailable_actions": actionAvailability()})
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
		st, reason := contact(ag, a.Clock.Now())
		migration, err := a.Store.AgentMigrationStatus(ag.ID)
		if err != nil {
			a.writeErr(w, 500, "db", "migration lookup failed")
			return
		}
		out = append(out, map[string]any{
			"id": ag.ID, "display_name": display(ag), "hostname": ag.Hostname, "os": ag.OS, "arch": ag.Arch,
			"worker_version": ag.WorkerVersion, "service_host_version": ag.ServiceHostVersion,
			"managed_ready": ag.ManagedReady, "desired_revision": ag.DesiredRevision, "applied_revision": ag.AppliedRevision,
			"desired_hash": ag.DesiredHash, "applied_hash": ag.AppliedHash,
			"last_live_at": ag.LastLiveAt, "state": st, "reason": reason,
			"pinned": ag.Pinned, "hidden": ag.Hidden, "archived": ag.Archived, "revoked": ag.Revoked,
			"migration": migration, "conflict": ag.Conflict, "addresses": json.RawMessage(orJSON(ag.Addresses)),
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
	host, obs, hostErr := a.Store.LatestHost(id)
	if hostErr != nil && hostErr != storage.ErrNotFound {
		a.writeErr(w, 500, "db", "could not read host data")
		return
	}
	svcs, err := a.serviceSummaries(id, a.Clock.Now())
	if err != nil {
		a.writeErr(w, 500, "db", "could not load services")
		return
	}
	st, reason := contact(ag, a.Clock.Now())
	_, _, since, _ := a.Store.State("agent", id)
	discovery, err := a.Store.DiscoveryForAgent(id)
	if err != nil {
		a.writeErr(w, 500, "db", "discovery lookup failed")
		return
	}
	maintenance, err := a.Store.InMaintenance("agent", id, a.Clock.Now())
	if err != nil {
		a.writeErr(w, 500, "maintenance", "could not read maintenance")
		return
	}
	a.writeJSON(w, 200, map[string]any{
		"maintenance_active": maintenance, "agent": ag, "host": host, "observed_at": obs, "state": st, "reason": reason, "since": since, "services": svcs,
		"discovery": discovery, "desired_config": json.RawMessage(orJSON(ag.DesiredConfig)),
		"age_seconds": a.Clock.Now().Sub(obs).Seconds(), "unavailable_actions": actionAvailability(),
		"server_time": a.Clock.Now(), "fresh_for_seconds": protocol.StaleContact.Seconds(),
	})
}

func (a *App) handleServices(w http.ResponseWriter, r *http.Request, s *storage.Session) {
	rows, err := a.serviceSummaries(r.URL.Query().Get("agent_id"), a.Clock.Now())
	if err != nil {
		a.writeErr(w, 500, "db", "could not load services")
		return
	}
	all := rows
	rows, inactive := currentServices(rows)
	selection := r.URL.Query().Get("inventory")
	if selection != "" && selection != "current" && selection != "all" {
		a.writeErr(w, 400, "invalid_inventory", "inventory must be current or all")
		return
	}
	if selection == "all" {
		rows = all
	}
	a.writeJSON(w, 200, map[string]any{"services": rows, "inactive_services": inactive, "server_time": a.Clock.Now()})
}

func (a *App) handleService(w http.ResponseWriter, r *http.Request, s *storage.Session) {
	sv, err := a.Store.Service(r.PathValue("id"))
	if err != nil {
		a.writeErr(w, 404, "not_found", "service not found")
		return
	}
	obs, err := a.Store.LatestCheckObs(sv.ID)
	if err != nil && err != storage.ErrNotFound {
		a.writeErr(w, 500, "db", "could not read valid service observation")
		return
	}
	a.writeJSON(w, 200, map[string]any{"service": sv, "observation": obs})
}

func (a *App) handleIncidents(w http.ResponseWriter, r *http.Request, s *storage.Session) {
	if r.URL.Query().Has("from") || r.URL.Query().Has("to") {
		a.searchHistoricalIncidents(w, r)
		return
	}
	incs, err := a.Store.OpenIncidents()
	if err != nil {
		a.writeErr(w, 500, "db", "incident lookup failed")
		return
	}
	a.writeJSON(w, 200, map[string]any{"incidents": incs})
}

func (a *App) handleOperations(w http.ResponseWriter, r *http.Request, s *storage.Session) {
	q := r.URL.Query()
	limit := 50
	if q.Has("limit") {
		n, err := strconv.Atoi(q.Get("limit"))
		if err != nil || n < 1 || n > 200 {
			a.writeErr(w, 400, "query", "invalid page limit")
			return
		}
		limit = n
	}
	page, err := a.Store.QueryOperations(storage.OperationQuery{Filter: q.Get("filter"), Read: q.Get("read"), Search: q.Get("q"), Before: q.Get("before"), Limit: limit})
	if errors.Is(err, storage.ErrInvalidOperationQuery) {
		a.writeErr(w, 400, "query", "invalid operation filter or cursor")
		return
	}
	if err != nil {
		a.writeErr(w, 500, "db", "could not load operations")
		return
	}
	a.writeJSON(w, 200, page)
}

func (a *App) handleOperation(w http.ResponseWriter, r *http.Request, s *storage.Session) {
	op, err := a.Store.Operation(r.PathValue("id"))
	if errors.Is(err, storage.ErrNotFound) {
		a.writeErr(w, 404, "not_found", "operation not found")
		return
	}
	if err != nil {
		a.writeErr(w, 500, "db", "could not read operation evidence")
		return
	}
	a.writeJSON(w, 200, op)
}

func (a *App) handleLookupOp(w http.ResponseWriter, r *http.Request, s *storage.Session) {
	var body struct {
		ClientRequestKey string          `json:"client_request_key"`
		Action           string          `json:"action"`
		TargetIDs        []string        `json:"target_ids"`
		TargetMode       string          `json:"target_mode"`
		Params           json.RawMessage `json:"params"`
	}
	if err := parseJSON(r, &body); err != nil {
		a.writeErr(w, 400, "malformed", "invalid json")
		return
	}
	if body.Action == "" {
		op, err := a.Store.OperationByRequestKey(s.Username, body.ClientRequestKey)
		if errors.Is(err, storage.ErrNotFound) {
			a.writeJSON(w, 200, map[string]any{"found": false})
			return
		}
		if err != nil {
			a.writeErr(w, 500, "db", "operation lookup failed")
			return
		}
		a.writeJSON(w, 200, map[string]any{"found": true, "operation": op})
		return
	}
	h := storage.RequestHash(body.Action+"|"+body.TargetMode, body.TargetIDs, body.Params)
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
	min, max, boundsErr := a.Store.EventBounds()
	if boundsErr != nil {
		a.writeErr(w, 500, "db", "could not read event bounds")
		return
	}
	if cursor < 0 || cursor > max || cursor > 0 && (cursor < max-10000 || cursor < min-1) {
		cursor = max
		_, _ = w.Write([]byte("event: resnapshot\ndata: {\"reason\":\"cursor_expired\"}\n\n"))
		fl.Flush()
	}
	if cursor == 0 {
		cursor = max
	}
	_, _ = w.Write([]byte("retry: 3000\n: connected\n\n"))
	fl.Flush()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-tick.C:
			if a.sessionFrom(r) == nil {
				return
			}
			_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(5 * time.Second))
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
			if len(evs) == 0 {
				if _, err := w.Write([]byte(": heartbeat\n\n")); err != nil {
					return
				}
			}
			fl.Flush()
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
	if err != nil && !errors.Is(err, storage.ErrNotFound) {
		a.writeErr(w, 500, "db", "historical lookup failed")
		return
	}
	if err != nil {
		a.writeJSON(w, 200, map[string]any{"at": at, "found": false})
		return
	}
	age := at.Sub(obs)
	a.writeJSON(w, 200, map[string]any{
		"at": at, "found": true, "observed_at": obs, "age_seconds": age.Seconds(), "fresh": age <= protocol.StaleContact,
		"host": host, "precision": "raw", "view": "event_time_corrected",
	})
}

func (a *App) handleHistorySeries(w http.ResponseWriter, r *http.Request, s *storage.Session) {
	from, err1 := time.Parse(time.RFC3339, r.URL.Query().Get("from"))
	to, err2 := time.Parse(time.RFC3339, r.URL.Query().Get("to"))
	if err1 != nil || err2 != nil || !to.After(from) {
		a.writeErr(w, 400, "bad_range", "from/to must be RFC3339 and to>from")
		return
	}
	if to.Sub(from) > 24*time.Hour {
		a.writeErr(w, 422, "range_limit", "raw history is limited to 24 hours per query; long-term aggregates are not implemented")
		return
	}
	agentID := r.URL.Query().Get("agent_id")
	serviceID := r.URL.Query().Get("service_id")
	step := plotStep(to.Sub(from))
	out := map[string]any{"from": from, "to": to, "step_seconds": int(step.Seconds()), "mode": r.URL.Query().Get("mode"), "preserve_extrema": true}
	if agentID != "" {
		pts, err := a.Store.HostSeries(agentID, from, to)
		if err != nil {
			a.writeErr(w, 500, "db", "history query failed")
			return
		}
		if len(pts) > 20000 {
			a.writeErr(w, 422, "point_budget", "choose a smaller interval")
			return
		}
		out["host"] = downsample(pts, step)
		out["raw_available"] = len(pts) > 0
		out["retention_window_contains_range"] = !from.Before(a.Clock.Now().Add(-protocol.RawRetention))
		out["long_term_aggregates_available"] = false
	}
	if serviceID != "" {
		obs, err := a.Store.CheckSeries(serviceID, from, to)
		if err != nil {
			a.writeErr(w, 500, "db", "history query failed")
			return
		}
		if len(obs) > 20000 {
			a.writeErr(w, 422, "point_budget", "choose a smaller interval")
			return
		}
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
	out := make([]map[string]any, 0)
	if step <= 0 {
		step = 5 * time.Second
	}
	var bucket []map[string]any
	var anchor time.Time
	flush := func() {
		if len(bucket) == 0 {
			return
		}
		last := make(map[string]any, len(bucket[len(bucket)-1])+4)
		for k, v := range bucket[len(bucket)-1] {
			last[k] = v
		}
		minima, maxima := map[string]any{}, map[string]any{}
		keys := map[string]bool{}
		for _, point := range bucket {
			for key, value := range point {
				switch value.(type) {
				case float64, int64:
					keys[key] = true
				}
			}
		}
		for key := range keys {
			var lo, hi float64
			count := 0
			for _, p := range bucket {
				var v float64
				switch n := p[key].(type) {
				case float64:
					v = n
				case int64:
					v = float64(n)
				default:
					continue
				}
				if count == 0 || v < lo {
					lo = v
				}
				if count == 0 || v > hi {
					hi = v
				}
				count++
			}
			if count > 0 {
				minima[key] = lo
				maxima[key] = hi
			}
		}
		last["min"] = minima
		last["max"] = maxima
		last["sample_count"] = len(bucket)
		last["bucket_start"] = anchor.UTC().Format(time.RFC3339Nano)
		out = append(out, last)
		bucket = nil
	}
	for _, p := range pts {
		raw, ok := p["observed_at"].(string)
		if !ok {
			continue
		}
		at, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			continue
		}
		start := at.Truncate(step)
		if len(bucket) > 0 && !start.Equal(anchor) {
			flush()
		}
		anchor = start
		bucket = append(bucket, p)
	}
	flush()
	return out
}

func (a *App) handleSettings(w http.ResponseWriter, r *http.Request, s *storage.Session) {
	var exp any
	if a.TLS != nil {
		if t, err := a.TLS.LeafExpiry(); err == nil {
			exp = t
		}
	}
	_, tufErr := a.updateRoot()
	a.writeJSON(w, 200, map[string]any{
		"advertised_url":      a.Cfg.AdvertisedURL,
		"listen":              a.Cfg.Listen,
		"controller_id":       a.ControllerID(),
		"tls_leaf_expiry":     exp,
		"retention_raw_hours": 48,
		"restore_mode":        a.Cfg.RestoreMode,
		"telegram_deferred":   true,
		"tuf_root_enrolled":   tufErr == nil,
		"ca_cert_pem":         string(a.CACertPEM()),
	})
}

func (a *App) handleSettingsPost(w http.ResponseWriter, r *http.Request, s *storage.Session) {
	if s.Role != "owner" {
		a.writeErr(w, 403, "forbidden", "owner role required")
		return
	}
	// A listener or advertised URL is not an ordinary live setting. Changing it
	// without certificate preparation and agent migration can strand the fleet.
	a.writeErr(w, 501, "not_implemented", "Online listener/address changes are unavailable. Prepare TLS/routing and use the documented local deployment procedure; this endpoint has changed nothing.")
}

func (a *App) handleReauth(w http.ResponseWriter, r *http.Request, s *storage.Session) {
	if !a.rateLimit("reauth:"+s.UserID, 8, time.Minute) {
		a.writeErr(w, 429, "rate_limited", "too many authentication attempts")
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	if err := parseJSONLimit(r, &body, 16<<10); err != nil {
		a.writeErr(w, 400, "malformed", "invalid json")
		return
	}
	u, err := a.Store.UserByName(s.Username)
	if err != nil || len(body.Password) > 1024 || !secure.VerifyPassword(u.PasswordHash, body.Password) {
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
	d, err := a.Store.StorageDiagnostics(protocol.RawRetention)
	if err != nil {
		a.writeErr(w, 500, "diagnostics", "could not read storage diagnostics")
		return
	}
	d["version"], d["commit"], d["setup_complete"] = version.Version, version.Commit, a.SetupComplete()
	d["server_time"] = a.Clock.Now()
	for _, file := range []struct{ name, path string }{{"database_bytes", a.Store.Path()}, {"wal_bytes", a.Store.Path() + "-wal"}} {
		info, e := os.Stat(file.path)
		if e == nil {
			d[file.name] = info.Size()
		} else if os.IsNotExist(e) {
			d[file.name] = 0
		} else {
			d[file.name] = nil
		}
	}
	a.writeJSON(w, 200, d)
}

func (a *App) handleReleases(w http.ResponseWriter, r *http.Request, s *storage.Session) {
	rels, err := a.Store.Releases()
	if err != nil {
		a.writeErr(w, 500, "catalogue", "could not read release catalogue")
		return
	}
	a.writeJSON(w, 200, map[string]any{"releases": rels, "server_time": a.Clock.Now().UTC()})
}

func (a *App) handleEnrollmentGet(w http.ResponseWriter, r *http.Request, s *storage.Session) {
	selected := r.URL.Query().Get("controller_url")
	if selected == "" {
		selected = protocol.DefaultBootstrapURL
	}
	parsed, err := netutil.ValidateControllerURL(selected)
	if err != nil {
		a.writeErr(w, 400, "invalid_profile_url", "Profile URL must be an HTTPS origin without credentials, path, query or fragment")
		return
	}
	selected = strings.TrimRight(selected, "/")
	tlsInfo := map[string]any{"matches": false, "route_verified": false, "reason": "No loaded server certificate"}
	if a.TLS != nil {
		if cert, e := a.TLS.LeafCertificate(); e == nil {
			ips := make([]string, 0, len(cert.IPAddresses))
			for _, ip := range cert.IPAddresses {
				ips = append(ips, ip.String())
			}
			tlsInfo["dns_names"], tlsInfo["ip_addresses"], tlsInfo["expires_at"] = cert.DNSNames, ips, cert.NotAfter
			e = cert.VerifyHostname(parsed.Hostname())
			if e == nil && a.Clock.Now().Before(cert.NotAfter) && !a.Clock.Now().Before(cert.NotBefore) {
				tlsInfo["matches"] = true
				tlsInfo["reason"] = "Loaded certificate matches the profile host; remote routing is not tested"
			} else {
				tlsInfo["reason"] = "Loaded certificate does not match this host or its validity period; prepare TLS before distributing the profile"
			}
		}
	}
	a.writeJSON(w, 200, map[string]any{
		"advertised_url": a.Cfg.AdvertisedURL,
		"profile_url":    selected, "tls": tlsInfo,
		"ca_cert_pem":       string(a.CACertPEM()),
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
	root := filepath.Join(a.Cfg.DataDir, "tuf")
	if name == "root.json" {
		b, e := a.updateRoot()
		if e != nil {
			a.writeErr(w, 404, "not_found", "no enrolled root")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(b)
		return
	}
	switch name {
	case "timestamp.json", "snapshot.json", "targets.json":
	default:
		a.writeErr(w, 404, "not_found", "metadata not found")
		return
	}
	http.ServeFile(w, r, filepath.Join(root, "repository", name))
}

func (a *App) handleArtifact(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := a.agentFrom(r); !ok {
		a.writeErr(w, 401, "unauthenticated", "agent credential required")
		return
	}
	name := r.PathValue("name")
	path, err := confinedJoin(filepath.Join(a.Cfg.DataDir, "tuf", "targets"), name)
	if err != nil {
		a.writeErr(w, 400, "bad_name", "invalid artifact name")
		return
	}
	http.ServeFile(w, r, path)
}

func (a *App) handleAgentSecret(w http.ResponseWriter, r *http.Request) {
	ag, cred, ok := a.agentFrom(r)
	if !ok {
		a.writeErr(w, 401, "unauthenticated", "agent credential required")
		return
	}
	_ = cred
	id := r.PathValue("id")
	name, header, checkID, version, nonce, ct, err := a.Store.SecretForAgent(id, ag.ID)
	if err != nil {
		a.writeErr(w, 404, "not_found", "secret not found")
		return
	}
	plain, err := secure.Open(a.Master, nonce, ct)
	if err != nil {
		a.writeErr(w, 500, "secret", "could not open secret")
		return
	}
	a.writeJSON(w, 200, map[string]any{
		"id": id, "name": name, "header": header, "check_id": checkID, "version": version, "value": string(plain),
	})
}

func (a *App) handleAgentUpdateRoot(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := a.agentFrom(r); !ok {
		a.writeErr(w, 401, "unauthenticated", "agent credential required")
		return
	}
	b, err := a.updateRoot()
	if err != nil {
		a.writeErr(w, 404, "not_found", "no enrolled TUF root")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(b)
}

func (a *App) agentFrom(r *http.Request) (*storage.AgentRow, string, bool) {
	return a.lookupAgent(r, true)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func confinedJoin(root, name string) (string, error) {
	if name == "" || strings.Contains(name, "..") || strings.ContainsRune(name, 0) {
		return "", os.ErrInvalid
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	full := filepath.Join(absRoot, filepath.FromSlash(name))
	rel, err := filepath.Rel(absRoot, full)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", os.ErrInvalid
	}
	return full, nil
}

func recentOK(s *storage.Session, now time.Time) bool {
	return s.RecentAuthUntil != nil && now.Before(*s.RecentAuthUntil)
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
		"schema_version":  3,
		"controller_url":  url,
		"ca_cert_pem":     ca,
		"enrollment_code": code,
	}
}
