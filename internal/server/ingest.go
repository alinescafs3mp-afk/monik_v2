package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/idgen"
	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
	"github.com/alinescafs3mp-afk/monik_v2/internal/rules"
	"github.com/alinescafs3mp-afk/monik_v2/internal/secure"
	"github.com/alinescafs3mp-afk/monik_v2/internal/storage"
)

func (a *App) handleEnroll(w http.ResponseWriter, r *http.Request) {
	if !a.rateLimit("enroll:"+clientIP(r), 10, time.Minute) {
		a.writeErr(w, 429, "rate_limited", "too many enrollment attempts")
		return
	}
	if !a.SetupComplete() {
		a.writeErr(w, 409, "setup_required", "server setup is not complete")
		return
	}
	var req protocol.EnrollRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		a.writeErr(w, 400, "malformed", "invalid json")
		return
	}
	if req.Code == "" || req.AgentID == "" {
		a.writeErr(w, 400, "missing", "code and agent_id required")
		return
	}
	if err := a.Store.ConsumeEnrollmentCode(strings.TrimSpace(req.Code)); err != nil {
		a.writeErr(w, 409, "code", err.Error())
		return
	}
	cred, err := idgen.Secret(32)
	if err != nil {
		a.writeErr(w, 500, "cred", "could not issue credential")
		return
	}
	cfg := protocol.DefaultAgentConfig()
	cfg.DisplayName = req.DisplayName
	body, _ := json.Marshal(cfg)
	hash := secure.SHA256Bytes(body)
	row := &storage.AgentRow{
		ID: req.AgentID, DisplayName: req.DisplayName, Hostname: req.Hostname,
		OS: req.OS, Arch: req.Arch, DesiredRevision: 1, DesiredHash: hash, DesiredConfig: string(body),
		WorkerVersion: req.Version,
	}
	if err := a.Store.InsertAgent(row, secure.HashToken(cred)); err != nil {
		a.writeErr(w, 409, "identity", "agent id already enrolled; reset identity to re-enroll")
		return
	}
	_ = a.Store.SetDesired(req.AgentID, 1, hash, string(body))
	a.Store.Audit("agent", "enroll", req.AgentID, req.Hostname)
	_ = a.Store.AppendEvent("agent", "agent", req.AgentID, 1, map[string]any{"event": "enrolled"})
	a.writeJSON(w, 200, protocol.EnrollResponse{
		AgentID: req.AgentID, Credential: cred, ControllerID: a.ControllerID(),
		AdvertisedURL: a.Cfg.AdvertisedURL, CACertPEM: string(a.CACertPEM()),
		ConfigRevision: 1, EndpointGeneration: 1,
	})
}

func (a *App) handleReport(w http.ResponseWriter, r *http.Request) {
	agentID, cred, ok := bearer(r)
	if !ok {
		a.writeErr(w, 401, "unauthenticated", "agent credential required")
		return
	}
	ag, err := a.Store.Agent(agentID)
	if err != nil || ag.Revoked || !secure.EqualHash(ag.CredentialHash, secure.HashToken(cred)) {
		a.writeErr(w, 401, "unauthenticated", "invalid agent credential")
		return
	}
	var rep protocol.AgentReport
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<20)).Decode(&rep); err != nil {
		a.writeErr(w, 400, "malformed", "invalid report")
		return
	}
	if rep.AgentID != agentID {
		a.writeErr(w, 403, "forbidden", "cannot submit for another agent")
		return
	}
	if ag.SessionID != "" && rep.SessionID != "" && ag.SessionID != rep.SessionID && rep.IsLive {
		if ag.LastLiveAt != nil && a.Clock.Now().Sub(*ag.LastLiveAt) < 30*time.Second {
			_ = a.Store.MarkConflict(ag.ID)
		}
	}
	versions := map[string]string{
		"worker": rep.WorkerVersion, "worker_digest": rep.WorkerDigest,
		"service_host": rep.ServiceHostVersion, "service_host_digest": rep.ServiceHostDigest,
	}
	if rep.ManagedReady {
		versions["managed"] = "1"
	}
	if err := a.Store.TouchAgent(ag.ID, rep.SessionID, rep.Sequence, rep.IsLive, rep.Host, rep.Capabilities, versions); err != nil {
		a.writeErr(w, 500, "db", "touch failed")
		return
	}
	if rep.IsLive && rep.Host != nil {
		_ = a.Store.InsertHostSample(ag.ID, rep.Sequence, rep.SessionID, rep.ObservedAt, rep.Host)
		_ = a.Store.SetState("agent", ag.ID, "ok", "")
		a.evalHostIncidents(ag.ID, rep.Host)
		_ = a.Store.AppendEvent("metrics", "agent", ag.ID, rep.Sequence, map[string]any{"seq": rep.Sequence, "live": true})
	}
	for _, c := range rep.Checks {
		_ = a.Store.InsertCheckObs(c, ag.ID)
		a.evalCheck(ag.ID, c)
	}
	if rep.Discovery != nil {
		for _, ep := range rep.Discovery.Confirmed {
			if ep.ServiceID == "" {
				ep.ServiceID = idgen.New()
			}
			_ = a.Store.UpsertService(ep, ag.ID)
			if ep.SpeaksHTTP {
				a.ensureBaselineCheck(ag.ID, ep)
			}
		}
		_ = a.Store.AppendEvent("discovery", "agent", ag.ID, 0, rep.Discovery)
	}
	if rep.ConfigRevision > 0 && rep.ConfigHash != "" {
		_ = a.Store.SetApplied(ag.ID, rep.ConfigRevision, rep.ConfigHash)
	}
	for _, rec := range rep.JobReceipts {
		_ = a.Store.ApplyReceipt(ag.ID, rec)
	}

	fresh, _ := a.Store.Agent(ag.ID)
	var desired *protocol.DesiredConfig
	if fresh != nil && fresh.DesiredConfig != "" {
		var body protocol.AgentConfig
		_ = json.Unmarshal([]byte(fresh.DesiredConfig), &body)
		desired = &protocol.DesiredConfig{Revision: fresh.DesiredRevision, Hash: fresh.DesiredHash, Body: body}
	}
	jobs, _ := a.Store.PendingJobs(ag.ID, 8)
	for _, j := range jobs {
		_ = a.Store.MarkJobDelivered(j.JobID)
		_ = a.Store.UpdateTarget(j.OperationID, ag.ID, protocol.TargetAccepted, "delivered", "delivered on control channel", "", true, nil)
	}
	resp := protocol.ControlResponse{
		Ack: &protocol.IngestAck{UpToSequence: rep.Sequence, Committed: true},
		DesiredConfig: desired, Jobs: jobs,
		ControllerID: a.ControllerID(), ServerTime: a.Clock.Now().UTC(),
		Migration: pendingMigration(a.Store, ag.ID),
	}
	a.writeJSON(w, 200, resp)
}

func bearer(r *http.Request) (agentID, cred string, ok bool) {
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Bearer ") {
		return "", "", false
	}
	tok := strings.TrimPrefix(h, "Bearer ")
	agentID = r.Header.Get("X-Monik-Agent-Id")
	if agentID == "" {
		return "", "", false
	}
	return agentID, tok, true
}

func (a *App) ensureBaselineCheck(agentID string, ep protocol.DiscoveredEndpoint) {
	ag, err := a.Store.Agent(agentID)
	if err != nil {
		return
	}
	var cfg protocol.AgentConfig
	if ag.DesiredConfig != "" {
		_ = json.Unmarshal([]byte(ag.DesiredConfig), &cfg)
	} else {
		cfg = protocol.DefaultAgentConfig()
	}
	for _, c := range cfg.Checks {
		if c.ServiceID == ep.ServiceID || c.URL == ep.URL {
			return
		}
	}
	cfg.Checks = append(cfg.Checks, protocol.CheckDefinition{
		ID: idgen.New(), ServiceID: ep.ServiceID, Kind: "baseline_http",
		URL: ep.URL, DialTarget: ep.DialTarget, Method: "HEAD",
		HostHeader: ep.HostHeader, TLSServerName: ep.TLSServerName,
		TimeoutSeconds: 2, IntervalSeconds: 5,
	})
	body, _ := json.Marshal(cfg)
	hash := sha256.Sum256(body)
	_ = a.Store.SetDesired(agentID, ag.DesiredRevision+1, hex.EncodeToString(hash[:]), string(body))
}

func (a *App) evalHostIncidents(agentID string, h *protocol.HostMetrics) {
	for _, b := range rules.EvaluateHost(h, rules.DefaultRules()) {
		id, err := a.Store.FindOpenIncident(agentID, b.Metric)
		if err != nil {
			_ = a.Store.InsertIncident(map[string]any{
				"id": idgen.New(), "entity_type": "agent", "entity_id": agentID,
				"metric": b.Metric, "severity": b.Severity, "status": "pending", "reason": b.Reason,
			})
			_ = a.Store.AppendEvent("incident", "agent", agentID, 0, b)
		} else {
			_ = a.Store.ConfirmIncident(id)
		}
	}
}

func (a *App) evalCheck(agentID string, c protocol.CheckObservation) {
	st := "ok"
	reason := ""
	if c.Quality != protocol.QualityOK {
		st, reason = "unknown", string(c.Quality)
	} else if c.Transport != "ok" {
		st, reason = "transport_fail", c.Transport
	} else if c.AppResult == "fail" {
		st, reason = "app_fail", c.AppReason
	} else if c.HTTPStatus != nil && *c.HTTPStatus >= 500 {
		st, reason = "http_error", "server error"
	}
	_ = a.Store.SetState("service", c.ServiceID, st, reason)
	if st == "ok" {
		if id, err := a.Store.FindOpenIncident(c.ServiceID, "http"); err == nil {
			_ = a.Store.ResolveIncident(id)
		}
		return
	}
	if st == "unknown" {
		return
	}
	if _, err := a.Store.FindOpenIncident(c.ServiceID, "http"); err != nil {
		_ = a.Store.InsertIncident(map[string]any{
			"id": idgen.New(), "entity_type": "service", "entity_id": c.ServiceID,
			"metric": "http", "severity": "warning", "status": "pending", "reason": reason,
		})
	}
}

func (a *App) handleReleaseImport(w http.ResponseWriter, r *http.Request, s *storage.Session) {
	if !a.requireRecent(w, s) {
		return
	}
	var body map[string]any
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		a.writeErr(w, 400, "malformed", "invalid json")
		return
	}
	key := r.Header.Get("X-Idempotency-Key")
	if key == "" {
		key = idgen.New()
	}
	a.processSubmit(w, s, protocol.SubmitOperation{Action: "update.import", ClientRequestKey: key, Params: body})
}
