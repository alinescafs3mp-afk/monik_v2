package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/actions"
	"github.com/alinescafs3mp-afk/monik_v2/internal/idgen"
	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
	"github.com/alinescafs3mp-afk/monik_v2/internal/storage"
)

func (a *App) sparkOnline(ag *storage.AgentRow, v *storage.SparkSnapshot) bool {
	if ag.Revoked || ag.Archived || ag.Conflict || ag.LastLiveAt == nil || v.AgentSession != ag.SessionID {
		return false
	}
	age := a.Clock.Now().Sub(*ag.LastLiveAt)
	observed := a.Clock.Now().Sub(v.ObservedAt)
	return age >= 0 && age <= protocol.SparkControlFreshness && observed >= -5*time.Second && observed <= protocol.SparkControlFreshness
}
func (a *App) handleSparks(w http.ResponseWriter, r *http.Request, s *storage.Session) {
	ags, err := a.Store.Agents()
	if err != nil {
		a.writeErr(w, 500, "db", "could not read Spark inventory")
		return
	}
	rows := []map[string]any{}
	for _, ag := range ags {
		v, err := a.Store.SparkSnapshot(ag.ID)
		if errors.Is(err, storage.ErrNotFound) {
			continue
		}
		if err != nil {
			a.writeErr(w, 500, "db", "could not read Spark state")
			return
		}
		if v.Classification != "spark" || ag.Archived {
			continue
		}
		profiles := []map[string]any{}
		if v.Spark.Inventory != nil {
			for _, p := range v.Spark.Inventory.Profiles {
				row := map[string]any{"profile_id": p.ProfileID, "display_name": p.DisplayName, "engine": p.Engine, "supervisor": p.Supervisor, "state": "unknown", "readiness": "unknown", "quality": "unknown"}
				for _, st := range v.Spark.ModelStates {
					if st.ProfileID == p.ProfileID {
						row["state"] = st.SupervisorState
						row["readiness"] = st.Readiness
						row["quality"] = st.Quality
						row["generation"] = st.Generation
						row["observed_at"] = st.ObservedAt
						row["reason"] = st.Reason
					}
				}
				profiles = append(profiles, row)
			}
		}
		host, hostAt, e := a.Store.LatestHost(ag.ID)
		if e != nil && !errors.Is(e, storage.ErrNotFound) {
			a.writeErr(w, 500, "db", "host state unavailable")
			return
		}
		rows = append(rows, map[string]any{"host": host, "host_observed_at": hostAt, "agent_id": ag.ID, "display_name": ag.DisplayName, "hostname": ag.Hostname, "online": a.sparkOnline(ag, v), "agent_session": ag.SessionID, "received_at": v.ReceivedAt, "observed_at": v.ObservedAt, "memory_topology": v.Spark.MemoryTopology, "metrics": v.Spark.Metrics, "profiles": profiles, "classification": v.Classification})
	}
	w.Header().Set("Cache-Control", "no-store")
	a.writeJSON(w, 200, map[string]any{"sparks": rows, "features": protocol.SparkFeatures})
}
func (a *App) handleSpark(w http.ResponseWriter, r *http.Request, s *storage.Session) {
	s = a.currentOwner(w, s, false)
	if s == nil {
		return
	}
	id := r.PathValue("id")
	ag, err := a.Store.Agent(id)
	if err != nil {
		a.writeErr(w, 404, "not_found", "agent not found")
		return
	}
	v, err := a.Store.SparkSnapshot(id)
	if err != nil {
		a.writeErr(w, 404, "not_found", "Spark state unavailable")
		return
	}
	ops, err := a.Store.SparkOperations(id, 30)
	if err != nil {
		a.writeErr(w, 500, "db", "could not read model operations")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	a.writeJSON(w, 200, map[string]any{"agent_id": id, "display_name": ag.DisplayName, "hostname": ag.Hostname, "agent_session": ag.SessionID, "online": a.sparkOnline(ag, v), "received_at": v.ReceivedAt, "observed_at": v.ObservedAt, "classification": v.Classification, "spark": v.Spark, "operations": ops})
}
func (a *App) handleSparkClassification(w http.ResponseWriter, r *http.Request, s *storage.Session) {
	var body struct {
		Classification string `json:"classification"`
	}
	if err := parseJSONStrictLimit(r, &body, 1024); err != nil {
		a.writeErr(w, 400, "bad_params", "classification required")
		return
	}
	s = a.currentOwner(w, s, false)
	if s == nil {
		return
	}
	if err := a.Store.SetSparkClassification(r.PathValue("id"), body.Classification); err != nil {
		a.writeErr(w, 400, "bad_params", err.Error())
		return
	}
	a.Store.Audit(s.Username, "spark.classify", r.PathValue("id"), body.Classification)
	a.writeJSON(w, 200, map[string]any{"classification": body.Classification})
}
func (a *App) handleSparkHistory(w http.ResponseWriter, r *http.Request, s *storage.Session) {
	s = a.currentOwner(w, s, false)
	if s == nil {
		return
	}
	to := a.Clock.Now()
	from := to.Add(-time.Hour)
	if raw := r.URL.Query().Get("from"); raw != "" {
		t, e := time.Parse(time.RFC3339Nano, raw)
		if e != nil {
			a.writeErr(w, 400, "bad_range", "invalid from")
			return
		}
		from = t
	}
	if raw := r.URL.Query().Get("to"); raw != "" {
		t, e := time.Parse(time.RFC3339Nano, raw)
		if e != nil {
			a.writeErr(w, 400, "bad_range", "invalid to")
			return
		}
		to = t
	}
	if to.Before(from) || to.Sub(from) > protocol.RawRetention {
		a.writeErr(w, 400, "bad_range", "range exceeds Spark raw retention")
		return
	}
	rows, err := a.Store.SparkHistory(r.PathValue("id"), from, to, 500)
	if err != nil {
		a.writeErr(w, 500, "db", "Spark history unavailable")
		return
	}
	a.writeJSON(w, 200, map[string]any{"samples": rows, "limit": 500, "truncated": len(rows) == 500})
}
func (a *App) processSparkSubmit(w http.ResponseWriter, s *storage.Session, req protocol.SubmitOperation) {
	if s == nil || s.Role != "owner" || s.ID == "" {
		a.writeErr(w, 403, "forbidden", "owner session required")
		return
	}
	if !a.requireRecent(w, s) {
		return
	}
	if _, err := actions.Lookup(req.Action); err != nil {
		a.writeErr(w, 400, "unknown_action", "unknown model action")
		return
	}
	if req.ClientRequestKey == "" || len(req.ClientRequestKey) > 256 {
		a.writeErr(w, 400, "missing_key", "bounded client_request_key required")
		return
	}
	p, err := protocol.DecodeModelActionParams(req.Params)
	if err != nil {
		a.writeErr(w, 400, "bad_params", err.Error())
		return
	}
	if len(req.TargetIDs) != 1 || req.TargetIDs[0] == "" || req.TargetMode != "" && req.TargetMode != "selected" || req.BaseRevision != nil {
		a.writeErr(w, 400, "bad_targets", "model action requires one explicit agent target")
		return
	}
	b, err := json.Marshal(req.Params)
	if err != nil {
		a.writeErr(w, 400, "bad_params", "invalid model params")
		return
	}
	h := storage.RequestHash(req.Action+"|"+req.TargetMode, req.TargetIDs, b)
	if existing, e := a.Store.LookupIdempotency(s.Username, req.ClientRequestKey, h); e == nil {
		code := 200
		if existing.Status == protocol.OpQueued || existing.Status == protocol.OpRunning {
			code = 202
		}
		a.writeJSON(w, code, existing)
		return
	} else if errors.Is(e, storage.ErrIdempotencyConflict) {
		a.writeErr(w, 409, "idempotency_conflict", "same key used with different request")
		return
	} else if !errors.Is(e, storage.ErrNotFound) {
		a.writeErr(w, 503, "idempotency_lookup", "original operation unavailable; no new dispatch")
		return
	}
	if a.Cfg.RestoreMode {
		a.writeErr(w, 409, "restore_mode", "model dispatch paused during restore reconciliation")
		return
	}
	id := req.TargetIDs[0]
	if _, _, err = a.Store.ValidateSparkControl(id, p, req.Action); err != nil {
		a.writeErr(w, 409, err.Error(), "model authority or live state changed; refresh")
		return
	}
	if conflict, e := a.Store.HasActiveSparkProfile(id, p.ProfileID, req.Action); e != nil {
		a.writeErr(w, 503, "db", "could not verify model reservation")
		return
	} else if conflict {
		a.writeErr(w, 409, "model_operation_in_progress", "another model effect is unresolved")
		return
	}
	now := a.Clock.Now().UTC()
	deadline := now.Add(protocol.SparkDispatchLease)
	op := &protocol.Operation{ID: idgen.New(), Action: req.Action, Status: protocol.OpQueued, Revision: 1, ClientRequestKey: req.ClientRequestKey, Actor: s.Username, CreatedAt: now, Deadline: &deadline, Params: req.Params, Summary: req.Action + " " + p.ProfileID, Targets: []protocol.TargetResult{{AgentID: id, Status: protocol.TargetQueued, Stage: "queued", Message: "fresh online-only model dispatch"}}}
	if err = a.Store.InsertSparkOperation(op, h, s); err != nil {
		a.writeErr(w, 409, "model_dispatch_conflict", "could not atomically confirm live authority and dispatch")
		return
	}
	a.Store.Audit(s.Username, req.Action, op.ID, "one locally approved profile; dispatch expires in 30 seconds")
	a.writeJSON(w, 202, op)
}
func (a *App) handleSparkStage(w http.ResponseWriter, r *http.Request) {
	if a.Cfg.RestoreMode {
		a.writeErr(w, 409, "restore_mode", "model stages paused")
		return
	}
	var body struct {
		OperationID string `json:"operation_id"`
		JobID       string `json:"job_id"`
	}
	if err := parseJSONStrictLimit(r, &body, 2048); err != nil || !protocol.SparkValidID(body.OperationID) || !protocol.SparkValidID(body.JobID) {
		a.writeErr(w, 400, "bad_params", "operation_id and job_id required")
		return
	}
	a.controlMu.Lock()
	defer a.controlMu.Unlock()
	ag, _, ok := a.agentFromCurrent(r)
	if !ok {
		a.writeErr(w, 401, "unauthenticated", "current agent credential required")
		return
	}
	auth, err := a.Store.AuthorizeSparkStartStage(ag.ID, body.OperationID, body.JobID)
	if err != nil {
		a.writeErr(w, 409, "stage_authorization_denied", err.Error())
		return
	}
	// Authenticate response time separately from immutable stage issuance;
	// a replay must not recreate the original dispatch budget.
	w.Header().Set("X-Monik-Server-Time", a.Clock.Now().UTC().Format(time.RFC3339Nano))
	a.writeJSON(w, 200, auth)
}
