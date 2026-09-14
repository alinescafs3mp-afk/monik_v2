package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
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
	if err := parseJSONLimit(r, &req, 1<<20); err != nil {
		a.writeErr(w, 400, "malformed", "invalid json")
		return
	}
	if req.Code == "" || req.AgentID == "" {
		a.writeErr(w, 400, "missing", "code and agent_id required")
		return
	}
	if len(req.AgentID) > 128 || len(req.Hostname) > 255 || len(req.DisplayName) > 255 || len(req.Code) > 128 {
		a.writeErr(w, 400, "invalid_identity", "enrollment field too long")
		return
	}
	cred := req.Credential
	if cred != "" {
		decoded, err := hex.DecodeString(cred)
		if err != nil || len(decoded) != 32 {
			a.writeErr(w, 400, "invalid_proof", "credential must encode 32 random bytes")
			return
		}
	} else {
		var err error
		cred, err = idgen.Secret(32)
		if err != nil {
			a.writeErr(w, 500, "cred", "could not issue credential")
			return
		}
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
	replayed, err := a.Store.EnrollAgent(strings.TrimSpace(req.Code), row, secure.HashToken(cred), req.Credential != "")
	if err != nil {
		a.writeErr(w, 409, "enrollment_conflict", "code expired, used by another identity, or identity unavailable; no partial registration committed")
		return
	}
	if !replayed {
		a.Store.Audit("agent", "enroll", req.AgentID, req.Hostname)
		_ = a.Store.AppendEvent("agent", "agent", req.AgentID, 1, map[string]any{"event": "enrolled"})
	}
	rootJSON := ""
	if b, err := a.updateRoot(); err == nil {
		rootJSON = string(b)
	}
	a.writeJSON(w, 200, protocol.EnrollResponse{
		AgentID: req.AgentID, Credential: cred, ControllerID: a.ControllerID(),
		AdvertisedURL: a.Cfg.AdvertisedURL, CACertPEM: string(a.CACertPEM()),
		ConfigRevision: 1, EndpointGeneration: 1, UpdateRootJSON: rootJSON,
	})
}

func (a *App) handleReport(w http.ResponseWriter, r *http.Request) {
	agentID, _, ok := bearer(r)
	if !ok {
		a.writeErr(w, 401, "unauthenticated", "agent credential required")
		return
	}
	ag, cred, ok := a.lookupAgent(r, true)
	if !ok {
		a.writeErr(w, 401, "unauthenticated", "invalid agent credential")
		return
	}
	_ = cred
	agentID = ag.ID
	var rep protocol.AgentReport
	if err := parseJSONLimit(r, &rep, 4<<20); err != nil {
		a.writeErr(w, 400, "malformed", "invalid report")
		return
	}
	if rep.AgentID != agentID {
		a.writeErr(w, 403, "forbidden", "cannot submit for another agent")
		return
	}
	// Serialize config publication against discovery-generated desired revisions.
	a.controlMu.Lock()
	defer a.controlMu.Unlock()
	accepted, err := a.Store.AcceptReport(rep)
	if err != nil {
		a.writeErr(w, 409, "report_rejected", "report not committed: "+err.Error())
		return
	}
	if accepted.Live {
		if err := a.Store.RecordMigrationStatus(ag.ID, rep.Migration, rep.EndpointGeneration); err != nil {
			a.Log.Warn("migration status rejected", "agent_id", ag.ID)
		}
		if rep.Host != nil && !rep.ObservedAt.After(a.Clock.Now().Add(5*time.Second)) && a.Clock.Now().Sub(rep.ObservedAt) <= protocol.StaleContact {
			_ = a.Store.SetState("agent", ag.ID, "ok", "")
			a.evalHostIncidents(ag.ID, rep.Host, rep.ObservedAt)
		}
		_ = a.Store.AppendEvent("metrics", "agent", ag.ID, rep.Sequence, map[string]any{"seq": rep.Sequence, "live": true})
		for _, c := range rep.Checks {
			if a.Clock.Now().Sub(c.ObservedAt) <= protocol.CheckFreshness(c.IntervalSeconds) {
				a.evalCheck(ag.ID, c)
			}
		}
		for _, ep := range accepted.Endpoints {
			if ep.SpeaksHTTP {
				a.ensureBaselineCheck(ag.ID, ep)
			}
		}
		if rep.Discovery != nil {
			_ = a.Store.AppendEvent("discovery", "agent", ag.ID, 0, rep.Discovery)
		}
	}
	// Historical replay must never deliver cached commands or rewrite current config.
	if !rep.IsLive {
		a.writeJSON(w, 200, protocol.ControlResponse{Ack: &protocol.IngestAck{UpToSequence: rep.Sequence, Committed: true}, ControllerID: a.ControllerID(), ServerTime: a.Clock.Now().UTC()})
		return
	}
	receiptAcks := make([]string, 0, len(rep.JobReceipts))
	for _, rec := range rep.JobReceipts {
		if err := a.Store.ApplyReceipt(ag.ID, rec); err != nil {
			a.Store.Audit("agent", "receipt_rejected", ag.ID, rec.JobID)
		} else {
			receiptAcks = append(receiptAcks, rec.JobID)
		}
	}
	fresh, err := a.Store.Agent(ag.ID)
	if err != nil {
		a.writeErr(w, 500, "db", "control lookup failed")
		return
	}
	var desired *protocol.DesiredConfig
	if fresh.DesiredConfig != "" {
		var body protocol.AgentConfig
		if err := json.Unmarshal([]byte(fresh.DesiredConfig), &body); err != nil {
			a.writeErr(w, 500, "config", "stored config invalid")
			return
		}
		desired = &protocol.DesiredConfig{Revision: fresh.DesiredRevision, Hash: fresh.DesiredHash, Body: body}
	}
	jobs, err := a.Store.PendingJobs(ag.ID, 8)
	if err != nil {
		a.writeErr(w, 500, "db", "job lookup failed")
		return
	}
	for i := range jobs {
		jobs[i].ControllerID = a.ControllerID()
		if jobs[i].Action == "profile.apply" || jobs[i].Action == "check.apply" || jobs[i].Action == "service.pause" || jobs[i].Action == "service.ignore" {
			targets, _ := a.Store.Targets(jobs[i].OperationID)
			for _, target := range targets {
				if target.AgentID == ag.ID {
					if v, ok := target.Evidence["revision"].(float64); ok {
						rev := int64(v)
						jobs[i].ExpectedRevision = &rev
					}
					if jobs[i].Params == nil {
						jobs[i].Params = map[string]any{}
					}
					jobs[i].Params["_expected_config_hash"] = target.Evidence["hash"]
				}
			}
		}
		if err := a.Store.MarkJobDelivered(jobs[i].JobID); err != nil {
			a.writeErr(w, 500, "db", "job delivery recording failed")
			return
		}
	}
	a.writeJSON(w, 200, protocol.ControlResponse{Ack: &protocol.IngestAck{UpToSequence: rep.Sequence, Committed: true}, DesiredConfig: desired, Jobs: jobs, ControllerID: a.ControllerID(), ServerTime: a.Clock.Now().UTC(), ReceiptAcks: receiptAcks})
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
		if c.ServiceID == ep.ServiceID {
			return
		}
	}
	d := protocol.CheckDefinition{ID: idgen.New(), ServiceID: ep.ServiceID, Kind: "baseline_http", URL: ep.URL, DialTarget: ep.DialTarget, Method: "GET", HostHeader: ep.HostHeader, TLSServerName: ep.TLSServerName, TimeoutSeconds: 2, IntervalSeconds: 5}
	// New inventory only. Never replace an existing custom or failing baseline check.
	if supportsCustom(ag) {
		for _, suggestion := range ep.Suggestions {
			if suggestion.AutoEligible && suggestion.Confidence == "high" {
				d = suggestion.Definition
				d.ID = idgen.New()
				d.ServiceID = ep.ServiceID
				break
			}
		}
	}
	d.Paused = !cfg.AutoMonitorNew
	cfg.Checks = append(cfg.Checks, d)
	if err := a.monitoringExclusions(ag, &cfg); err != nil {
		return
	}
	if protocol.ValidateAgentConfig(cfg) != nil {
		return
	}
	body, _ := json.Marshal(cfg)
	hash := sha256.Sum256(body)
	_ = a.Store.SetDesired(agentID, ag.DesiredRevision+1, hex.EncodeToString(hash[:]), string(body))
}

func (a *App) evalHostIncidents(agentID string, h *protocol.HostMetrics, at time.Time) {
	now := a.Clock.Now()
	if h == nil || at.After(now.Add(5*time.Second)) || now.Sub(at) > protocol.StaleContact {
		return
	}
	version, err := a.Store.HostRulesAt(at)
	if err != nil {
		a.Log.Error("host rules unavailable", "error", err)
		return
	}
	current, err := a.Store.HostRulesAt(now)
	if err != nil {
		a.Log.Error("current host rules unavailable", "error", err)
		return
	}
	if current.Revision != version.Revision {
		return
	} // Backlog cannot reopen a superseded rule incident.
	maintenance, err := a.Store.InMaintenance("agent", agentID, at)
	if err != nil {
		a.Log.Error("maintenance lookup failed", "error", err)
		return
	}
	breaches := map[string]rules.Breach{}
	for _, b := range rules.EvaluateHost(h, rules.ThresholdRules(version.Rules)) {
		breaches[b.Metric] = b
	}
	for _, rule := range version.Rules {
		value, known := 0.0, false
		switch rule.Metric {
		case "cpu":
			if h.CPUPercent != nil {
				value, known = *h.CPUPercent, true
			}
		case "ram":
			if h.RAMTotal > 0 {
				value, known = float64(h.RAMUsed)/float64(h.RAMTotal)*100, true
			}
		case "disk":
			for _, d := range h.Disks {
				known = true
				if d.UsedPct > value {
					value = d.UsedPct
				}
			}
		}
		b, failed := breaches[rule.Metric]
		reason := fmt.Sprintf("%s %.1f%%; warning >= %.1f%%, critical >= %.1f%%; sustained %ds; rule revision %d", rule.Metric, value, rule.Warning, rule.Critical, rule.PersistSeconds, version.Revision)
		p := storage.IncidentPolicy{PersistFor: time.Duration(rule.PersistSeconds) * time.Second, RecoverFor: time.Duration(rule.RecoverSeconds) * time.Second, MaxGap: protocol.StaleContact, Failures: 1, Successes: 1, RuleVersion: version.Revision, HoldRecovery: value > rule.Recovery, Maintenance: maintenance}
		if err := a.Store.ObserveIncident("agent", agentID, rule.Metric, at, known, failed, b.Severity, reason, p); err != nil {
			a.Log.Error("host incident evaluation failed", "error", err)
		}
	}
}

func (a *App) evalCheck(agentID string, c protocol.CheckObservation) {
	ag, err := a.Store.Agent(agentID)
	if err != nil {
		return
	}
	var cfg protocol.AgentConfig
	if ag.DesiredConfig != "" {
		if json.Unmarshal([]byte(ag.DesiredConfig), &cfg) != nil {
			return
		}
		if cfg.Paused {
			return
		}
		for _, d := range cfg.Checks {
			if d.ServiceID == c.ServiceID && (d.Paused || d.Ignored) {
				return
			}
		}
	}
	now := a.Clock.Now()
	if c.ObservedAt.After(now.Add(5*time.Second)) || now.Sub(c.ObservedAt) > protocol.CheckFreshness(c.IntervalSeconds) {
		return
	}
	latest, e := a.Store.LatestCheckObs(c.ServiceID)
	if e != nil && e != storage.ErrNotFound {
		a.Log.Error("latest check lookup failed", "error", e)
		return
	}
	if latest != nil && (latest.ObservedAt.After(c.ObservedAt) || latest.ConfigRev > c.ConfigRev) {
		return
	}
	maintenance, e := a.Store.InMaintenance("service", c.ServiceID, c.ObservedAt)
	if e != nil {
		a.Log.Error("maintenance lookup failed", "error", e)
		return
	}
	st, reason := "ok", ""
	known := c.Quality == protocol.QualityOK
	if !known {
		st, reason = "unknown", string(c.Quality)
	} else if c.Transport != "ok" {
		st, reason = "transport_fail", c.Transport
	} else if c.AppResult == "fail" {
		st, reason = "app_fail", c.AppReason
	} else if c.AppResult != "pass" && c.HTTPStatus != nil && *c.HTTPStatus >= 500 {
		st, reason = "http_error", "server error"
	}
	_ = a.Store.SetState("service", c.ServiceID, st, reason)
	if err := a.Store.ObserveIncident("service", c.ServiceID, "http", c.ObservedAt, known, known && st != "ok", "warning", reason, storage.IncidentPolicy{MaxGap: protocol.CheckFreshness(c.IntervalSeconds), Failures: 3, Successes: 2, Maintenance: maintenance}); err != nil {
		a.Log.Error("service incident evaluation failed", "error", err)
	}
}

func (a *App) handleReleaseImport(w http.ResponseWriter, r *http.Request, s *storage.Session) {
	if !a.requireRecent(w, s) {
		return
	}
	var body map[string]any
	if err := parseJSONLimit(r, &body, 1<<20); err != nil {
		a.writeErr(w, 400, "malformed", "invalid json")
		return
	}
	key := r.Header.Get("X-Idempotency-Key")
	if key == "" {
		key = idgen.New()
	}
	a.processSubmit(w, s, protocol.SubmitOperation{Action: "update.import", ClientRequestKey: key, Params: body})
}
