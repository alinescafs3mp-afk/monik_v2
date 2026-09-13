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
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(&rep); err != nil {
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
		if rep.Host != nil && a.Clock.Now().Sub(rep.ObservedAt) <= protocol.StaleContact {
			_ = a.Store.SetState("agent", ag.ID, "ok", "")
			a.evalHostIncidents(ag.ID, rep.Host, rep.ObservedAt)
		}
		_ = a.Store.AppendEvent("metrics", "agent", ag.ID, rep.Sequence, map[string]any{"seq": rep.Sequence, "live": true})
		for _, c := range rep.Checks {
			if a.Clock.Now().Sub(c.ObservedAt) <= protocol.StaleContact {
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
	a.writeJSON(w, 200, protocol.ControlResponse{Ack: &protocol.IngestAck{UpToSequence: rep.Sequence, Committed: true}, DesiredConfig: desired, Jobs: jobs, ControllerID: a.ControllerID(), ServerTime: a.Clock.Now().UTC(), ReceiptAcks: receiptAcks /* controller migration is fail-closed until candidate verification exists */})
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

func (a *App) evalHostIncidents(agentID string, h *protocol.HostMetrics, at time.Time) {
	breaches := map[string]rules.Breach{}
	for _, b := range rules.EvaluateHost(h, rules.DefaultRules()) {
		breaches[b.Metric] = b
	}
	for _, rule := range rules.DefaultRules() {
		known := false
		switch rule.Metric {
		case "cpu":
			known = h.CPUPercent != nil
		case "ram":
			known = h.RAMTotal > 0
		case "disk":
			known = len(h.Disks) > 0
		}
		b, failed := breaches[rule.Metric]
		if err := a.Store.ObserveIncident("agent", agentID, rule.Metric, at, known, failed, b.Severity, b.Reason, storage.IncidentPolicy{PersistFor: rule.PersistFor, RecoverFor: rule.RecoverFor, MaxGap: protocol.StaleContact, Failures: 1, Successes: 1}); err != nil {
			a.Log.Error("host incident evaluation failed", "error", err)
		}
	}
}

func (a *App) evalCheck(agentID string, c protocol.CheckObservation) {
	st, reason := "ok", ""
	known := c.Quality == protocol.QualityOK
	if !known {
		st, reason = "unknown", string(c.Quality)
	} else if c.Transport != "ok" {
		st, reason = "transport_fail", c.Transport
	} else if c.AppResult == "fail" {
		st, reason = "app_fail", c.AppReason
	} else if c.HTTPStatus != nil && *c.HTTPStatus >= 500 {
		st, reason = "http_error", "server error"
	}
	_ = a.Store.SetState("service", c.ServiceID, st, reason)
	if err := a.Store.ObserveIncident("service", c.ServiceID, "http", c.ObservedAt, known, known && st != "ok", "warning", reason, storage.IncidentPolicy{MaxGap: protocol.StaleContact, Failures: 3, Successes: 2}); err != nil {
		a.Log.Error("service incident evaluation failed", "error", err)
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
