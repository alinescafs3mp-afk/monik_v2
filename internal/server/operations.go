package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/actions"
	"github.com/alinescafs3mp-afk/monik_v2/internal/idgen"
	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
	"github.com/alinescafs3mp-afk/monik_v2/internal/secure"
	"github.com/alinescafs3mp-afk/monik_v2/internal/storage"
	"github.com/alinescafs3mp-afk/monik_v2/internal/tufutil"
)

func (a *App) handleSubmitOp(w http.ResponseWriter, r *http.Request, s *storage.Session) {
	var req protocol.SubmitOperation
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<20)).Decode(&req); err != nil {
		a.writeErr(w, 400, "malformed", "invalid json")
		return
	}
	a.processSubmit(w, s, req)
}

func (a *App) processSubmit(w http.ResponseWriter, s *storage.Session, req protocol.SubmitOperation) {
	a.controlMu.Lock()
	defer a.controlMu.Unlock()
	if reason := actions.UnavailableReason(req.Action); reason != "" {
		a.writeErr(w, 501, "not_implemented", reason)
		return
	}
	var secretPlain string
	if req.Action == "secret.replace" {
		if req.Params == nil {
			req.Params = map[string]any{}
		}
		if v, ok := req.Params["value"].(string); ok {
			secretPlain = v
			req.Params["value_sha256"] = secure.SHA256Bytes([]byte(v))
			delete(req.Params, "value")
			req.Params["redacted"] = true
		}
		if secretPlain == "" {
			a.writeErr(w, 400, "missing_secret", "secret value is required and is not stored in the operation journal")
			return
		}
	}
	if req.ClientRequestKey == "" {
		a.writeErr(w, 400, "missing_key", "client_request_key required")
		return
	}
	def, err := actions.Lookup(req.Action)
	if err != nil {
		a.writeErr(w, 400, "unknown_action", err.Error())
		return
	}
	if def.RecentAuthenticationRequired && !a.requireRecent(w, s) {
		return
	}
	if err := a.validateLifecycleParams(&req); err != nil {
		a.writeErr(w, 400, "bad_params", err.Error())
		return
	}
	if a.Cfg.RestoreMode && disruptive(def.Risk) {
		a.writeErr(w, 409, "restore_mode", "disruptive dispatch paused until restore reconciliation finishes")
		return
	}
	params, _ := json.Marshal(req.Params)
	if params == nil {
		params = []byte("{}")
	}
	h := storage.RequestHash(req.Action+"|"+req.TargetMode, req.TargetIDs, params)
	if existing, err := a.Store.LookupIdempotency(s.Username, req.ClientRequestKey, h); err == nil {
		status := 200
		if existing.Status == protocol.OpQueued || existing.Status == protocol.OpRunning {
			status = 202
		}
		a.writeJSON(w, status, existing)
		return
	} else if errors.Is(err, storage.ErrIdempotencyConflict) {
		a.writeErr(w, 409, "idempotency_conflict", "same key used with a different request")
		return
	}
	targets, err := a.expandTargets(req)
	if err != nil {
		a.writeErr(w, 400, "bad_targets", err.Error())
		return
	}
	if def.Scope == actions.ScopeAgents && len(targets) == 0 {
		a.writeErr(w, 400, "no_targets", "no agents in frozen target set")
		return
	}
	now := a.Clock.Now().UTC()
	op := &protocol.Operation{
		ID: idgen.New(), Action: req.Action, Status: protocol.OpQueued,
		Revision: 1, ClientRequestKey: req.ClientRequestKey, Actor: s.Username,
		CreatedAt: now, Params: req.Params, Summary: req.Action,
	}
	if def.Scope == actions.ScopeAgents || def.Scope == actions.ScopeServerJob {
		dl := now.Add(protocol.OneShotExpiry)
		if req.Action == "profile.apply" || req.Action == "check.apply" || req.Action == "service.pause" || req.Action == "service.ignore" {
			dl = now.Add(24 * time.Hour)
		}
		op.Deadline = &dl
	}
	for _, id := range targets {
		st := protocol.TargetQueued
		stage := "queued"
		msg := "queued"
		ag, err := a.Store.Agent(id)
		if err != nil {
			st, stage, msg = protocol.TargetRejected, "rejected", "unknown agent"
		} else if ag.Revoked {
			st, stage, msg = protocol.TargetRejected, "rejected", "credential revoked"
		} else if ag.LastLiveAt == nil || now.Sub(*ag.LastLiveAt) > protocol.UnreachableContact {
			st, stage, msg = protocol.TargetWaitingOffline, "waiting_offline", "agent offline"
		}
		op.Targets = append(op.Targets, protocol.TargetResult{AgentID: id, Status: st, Stage: stage, Message: msg, Retryable: st == protocol.TargetWaitingOffline})
	}
	if def.Scope == actions.ScopeServer || def.Scope == actions.ScopeServerJob {
		if req.Action != "credential.revoke" {
			op.Targets = nil
		}
		if len(op.Targets) == 0 {
			op.Targets = []protocol.TargetResult{{AgentID: "server", Status: protocol.TargetSucceeded, Stage: "commit", Message: "server-only"}}
		}
	}
	if actions.LifecycleConflict(req.Action) {
		for i, t := range op.Targets {
			if t.AgentID == "server" || t.Status == protocol.TargetRejected {
				continue
			}
			if other, ok := a.Store.HasActiveLifecycle(t.AgentID); ok {
				op.Targets[i].Status = protocol.TargetRejected
				op.Targets[i].Stage = "conflict"
				op.Targets[i].Message = "another lifecycle transaction is active: " + other
				op.Targets[i].Retryable = true
			}
		}
	}
	if err := a.Store.InsertOperation(op, h); err != nil {
		a.writeErr(w, 500, "persist", err.Error())
		return
	}
	a.Store.Audit(s.Username, req.Action, op.ID, "operation created")
	if err := a.executeServerSide(op, def, s, req, secretPlain); err != nil {
		for _, target := range op.Targets {
			_ = a.Store.UpdateTarget(op.ID, target.AgentID, protocol.TargetFailed, "failed", err.Error(), "exec", true, nil)
		}
		loaded, _ := a.Store.Operation(op.ID)
		a.writeJSON(w, 202, loaded)
		return
	}
	loaded, _ := a.Store.Operation(op.ID)
	code := 202
	if def.Scope == actions.ScopeServer && (loaded.Status == protocol.OpCompleted || loaded.Status == protocol.OpCompletedWithErrs) {
		code = 200
	}
	a.writeJSON(w, code, loaded)
}

func disruptive(risk string) bool {
	return risk == "disruptive" || risk == "sensitive"
}

func (a *App) expandTargets(req protocol.SubmitOperation) ([]string, error) {
	if req.TargetMode == "all" {
		ags, err := a.Store.Agents()
		if err != nil {
			return nil, err
		}
		var ids []string
		for _, ag := range ags {
			if !ag.Archived && !ag.Revoked {
				ids = append(ids, ag.ID)
			}
		}
		return ids, nil
	}
	seen := map[string]bool{}
	var out []string
	for _, id := range req.TargetIDs {
		if id != "" && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out, nil
}

func (a *App) executeServerSide(op *protocol.Operation, def actions.Def, s *storage.Session, req protocol.SubmitOperation, secretPlain string) error {
	switch req.Action {
	case "enrollment.create":
		code, exp, err := a.Store.CreateEnrollmentCode(s.Username, protocol.EnrollmentTTL)
		if err != nil {
			return err
		}
		_ = a.Store.UpdateTarget(op.ID, "server", protocol.TargetSucceeded, "commit", "enrollment code created", "", false, map[string]any{
			"code": code, "expires_at": exp.Format(time.RFC3339), "advertised_url": a.Cfg.AdvertisedURL,
			"ca_cert_pem": string(a.CACertPEM()),
		})
	case "preference.save", "agent.rename", "agent.pin", "service.pin", "service.hide", "agent.archive":
		if err := a.applyPreference(req); err != nil {
			return err
		}
		_ = a.Store.UpdateTarget(op.ID, "server", protocol.TargetSucceeded, "commit", "saved", "", false, nil)
	case "incident.acknowledge":
		id, _ := req.Params["incident_id"].(string)
		if err := a.Store.AckIncident(id, s.Username); err != nil {
			return err
		}
		_ = a.Store.UpdateTarget(op.ID, "server", protocol.TargetSucceeded, "commit_ack", "acknowledged", "", false, nil)
	case "credential.revoke":
		for _, t := range op.Targets {
			if t.AgentID == "server" {
				continue
			}
			_ = a.Store.RevokeAgent(t.AgentID)
			_ = a.Store.UpdateTarget(op.ID, t.AgentID, protocol.TargetSucceeded, "revoked", "credential rejected immediately", "", false, nil)
		}
	case "backup.create":
		return a.runBackup(op)
	case "update.import":
		return a.importRelease(op, req)
	case "rule.save":
		name, _ := req.Params["name"].(string)
		if name == "" {
			name = "default"
		}
		body, _ := json.Marshal(req.Params)
		if err := a.Store.SaveRule(name, string(body)); err != nil {
			return err
		}
		_ = a.Store.UpdateTarget(op.ID, "server", protocol.TargetSucceeded, "commit_effective_rule", "rule version stored", "", false, map[string]any{"name": name})
	case "maintenance.set":
		purpose, _ := req.Params["purpose"].(string)
		start, _ := req.Params["start_at"].(string)
		end, _ := req.Params["end_at"].(string)
		if start == "" || end == "" {
			return fmt.Errorf("start_at and end_at required")
		}
		if err := a.Store.SaveMaintenance(idgen.New(), "fleet", "", purpose, start, end, s.Username); err != nil {
			return err
		}
		_ = a.Store.UpdateTarget(op.ID, "server", protocol.TargetSucceeded, "commit_interval", "maintenance window stored", "", false, nil)
	case "operation.cancel_pending":
		id, _ := req.Params["operation_id"].(string)
		if id == "" {
			id = op.ID
		}
		if err := a.Store.CancelPending(id); err != nil {
			return err
		}
		_ = a.Store.UpdateTarget(op.ID, "server", protocol.TargetSucceeded, "commit", "cancelled only targets not yet dispatched; delivered targets remain unresolved", "", false, nil)
	case "history.export":
		dir := filepath.Join(a.Cfg.DataDir, "exports")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		path := filepath.Join(dir, op.ID+".json")
		ops, err := a.Store.OpenIncidents()
		if err != nil {
			return err
		}
		b, err := json.MarshalIndent(map[string]any{"exported_at": a.Clock.Now().UTC(), "open_incidents": ops, "note": "raw 48h series remains on the history API"}, "", "  ")
		if err != nil {
			return err
		}
		if err := os.WriteFile(path, b, 0o600); err != nil {
			return err
		}
		_ = a.Store.UpdateTarget(op.ID, "server", protocol.TargetSucceeded, "bounded_artifact_ready", "export written", "", false, map[string]any{"path": path})
	case "profile.apply", "check.apply", "service.pause", "service.ignore":
		return a.bumpDesired(op, req)
	case "secret.replace":
		return a.replaceSecret(op, req, secretPlain)
	case "rebind.prepare", "rebind.arm", "rebind.activate", "rebind.retire":
		return a.handleRebind(op, req, s)
	case "update.rollout", "update.rollback", "update.resume":
		return a.handleRollout(op, req)
	case "agent.restart":
		return a.handleRestart(op)
	case "check.trial":
		return a.handleCheckTrial(op, req)
	case "credential.rotate":
		return a.handleCredentialRotate(op)
	case "trust.stage", "trust.retire":
		return a.handleTrust(op, req)
	case "operation.retry_selected":
		return a.retrySelected(op, req, s)
	}
	_ = a.Store.AppendEvent("operation", "operation", op.ID, 1, map[string]any{"action": op.Action, "status": "updated"})
	return nil
}

func (a *App) applyPreference(req protocol.SubmitOperation) error {
	switch req.Action {
	case "preference.save":
		b, err := json.Marshal(req.Params)
		if err != nil {
			return err
		}
		_, err = a.Store.DB.Exec(`INSERT INTO dashboard_preferences(id,body) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET body=excluded.body,revision=revision+1`, "owner", string(b))
		return err
	case "agent.rename":
		id, _ := req.Params["agent_id"].(string)
		name, _ := req.Params["display_name"].(string)
		return a.Store.UpdateAgentFlags(id, map[string]any{"display_name": name})
	case "agent.pin":
		id, _ := req.Params["agent_id"].(string)
		pinned, _ := req.Params["pinned"].(bool)
		return a.Store.UpdateAgentFlags(id, map[string]any{"pinned": boolInt(pinned)})
	case "agent.archive":
		id, _ := req.Params["agent_id"].(string)
		return a.Store.UpdateAgentFlags(id, map[string]any{"archived": 1})
	case "service.pin":
		id, _ := req.Params["service_id"].(string)
		pinned, _ := req.Params["pinned"].(bool)
		return a.Store.UpdateServiceFlags(id, map[string]any{"pinned": boolInt(pinned)})
	case "service.hide":
		id, _ := req.Params["service_id"].(string)
		hidden, _ := req.Params["hidden"].(bool)
		return a.Store.UpdateServiceFlags(id, map[string]any{"hidden": boolInt(hidden)})
	}
	return nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (a *App) bumpDesired(op *protocol.Operation, req protocol.SubmitOperation) error {
	for _, t := range op.Targets {
		if t.AgentID == "server" || t.Status == protocol.TargetRejected {
			continue
		}
		ag, err := a.Store.Agent(t.AgentID)
		if err != nil {
			continue
		}
		var cfg protocol.AgentConfig
		if ag.DesiredConfig != "" {
			_ = json.Unmarshal([]byte(ag.DesiredConfig), &cfg)
		} else {
			cfg = protocol.DefaultAgentConfig()
		}
		applyConfigPatch(&cfg, req)
		if err := protocol.ValidateAgentConfig(cfg); err != nil {
			return err
		}
		for _, check := range cfg.Checks {
			sv, err := a.Store.Service(check.ServiceID)
			if err != nil || sv.AgentID != ag.ID {
				return fmt.Errorf("check service does not belong to selected agent")
			}
		}
		body, _ := json.Marshal(cfg)
		hash := secure.SHA256Bytes(body)
		rev := ag.DesiredRevision + 1
		if err := a.Store.SetDesired(t.AgentID, rev, hash, string(body)); err != nil {
			return err
		}
		stage := "desired_committed"
		if t.Status == protocol.TargetWaitingOffline {
			_ = a.Store.UpdateTarget(op.ID, t.AgentID, protocol.TargetWaitingOffline, stage, "desired revision stored; waiting for agent", "", true, map[string]any{"revision": rev, "hash": hash})
		} else {
			_ = a.Store.UpdateTarget(op.ID, t.AgentID, protocol.TargetQueued, stage, "desired revision stored", "", true, map[string]any{"revision": rev, "hash": hash})
		}
	}
	return nil
}

func applyConfigPatch(cfg *protocol.AgentConfig, req protocol.SubmitOperation) {
	if v, ok := req.Params["paused"].(bool); ok && req.Action == "profile.apply" {
		cfg.Paused = v
	}
	if v, ok := req.Params["display_name"].(string); ok {
		cfg.DisplayName = v
	}
	if v, ok := req.Params["ping_target"].(string); ok && v != "" {
		cfg.Ping.Target = v
	}
	if v, ok := req.Params["collect_seconds"].(float64); ok && v >= 5 {
		cfg.Intervals.CollectSeconds = int(v)
	}
	if chk, ok := req.Params["check"].(map[string]any); ok {
		b, _ := json.Marshal(chk)
		var d protocol.CheckDefinition
		if json.Unmarshal(b, &d) == nil && d.ID != "" {
			found := false
			for i := range cfg.Checks {
				if cfg.Checks[i].ID == d.ID {
					cfg.Checks[i] = d
					found = true
				}
			}
			if !found {
				cfg.Checks = append(cfg.Checks, d)
			}
		}
	}
	if sid, ok := req.Params["service_id"].(string); ok && req.Action == "service.ignore" {
		ign, _ := req.Params["ignored"].(bool)
		for i := range cfg.Checks {
			if cfg.Checks[i].ServiceID == sid {
				cfg.Checks[i].Ignored = ign
			}
		}
	}
	if sid, ok := req.Params["service_id"].(string); ok && req.Action == "service.pause" {
		p, _ := req.Params["paused"].(bool)
		for i := range cfg.Checks {
			if cfg.Checks[i].ServiceID == sid {
				cfg.Checks[i].Paused = p
			}
		}
	}
}

func (a *App) replaceSecret(op *protocol.Operation, req protocol.SubmitOperation, secretPlain string) error {
	name, _ := req.Params["name"].(string)
	header, _ := req.Params["header"].(string)
	agentID, _ := req.Params["agent_id"].(string)
	checkID, _ := req.Params["check_id"].(string)
	if agentID == "" {
		for _, t := range op.Targets {
			if t.AgentID != "" && t.AgentID != "server" && t.Status != protocol.TargetRejected {
				agentID = t.AgentID
				break
			}
		}
	}
	if name == "" || header == "" || secretPlain == "" || agentID == "" {
		return fmt.Errorf("name, header, value, agent_id required")
	}
	if strings.ContainsAny(header, "\r\n:") || strings.ContainsAny(secretPlain, "\r\n") {
		return fmt.Errorf("invalid header")
	}
	hl := strings.ToLower(header)
	if hl == "host" || hl == "content-length" {
		return fmt.Errorf("header not allowed")
	}
	nonce, ct, err := secure.Seal(a.Master, []byte(secretPlain))
	if err != nil {
		return err
	}
	if _, err := a.Store.Agent(agentID); err != nil {
		return fmt.Errorf("unknown agent")
	}
	id := idgen.New()
	if err := a.Store.SaveSecret(id, name, header, agentID, checkID, 1, nonce, ct); err != nil {
		return err
	}
	extra := map[string]any{"secret_id": id, "version": 1, "header": header, "name": name, "check_id": checkID}
	if jobID, err := a.Store.JobIDFor(op.ID, agentID); err == nil {
		_ = a.Store.PatchJobParams(jobID, extra)
	}
	_ = a.Store.UpdateTarget(op.ID, agentID, protocol.TargetQueued, "secret_version_committed", "secret stored; waiting for agent apply", "", true, extra)
	return nil
}

func (a *App) runBackup(op *protocol.Operation) error {
	dir := filepath.Join(a.Cfg.DataDir, "backups")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	id := idgen.New()
	dst := filepath.Join(dir, id+".db")
	if err := a.Store.BackupTo(dst); err != nil {
		return err
	}
	sum, size, err := secure.SHA256File(dst)
	if err != nil {
		return err
	}
	if err := a.Store.InsertBackup(id, dst, sum, size); err != nil {
		return err
	}
	_ = a.Store.UpdateTarget(op.ID, "server", protocol.TargetSucceeded, "database_snapshot_written", "database snapshot written; complete controller restore has not been verified", "", false, map[string]any{"path": dst, "sha256": sum, "size": size, "restore_verified": false, "scope": "database_only"})
	return nil
}

func (a *App) importRelease(op *protocol.Operation, req protocol.SubmitOperation) error {
	path, _ := req.Params["bundle_path"].(string)
	if path == "" {
		return fmt.Errorf("bundle_path required")
	}
	enroll, _ := req.Params["enroll_root"].(bool)
	dir := filepath.Join(a.Cfg.DataDir, "tuf")
	res, err := tufutil.ImportTrusted(path, dir, tufutil.ImportOpts{Enroll: enroll, Now: a.Clock.Now().UTC()})
	if err != nil {
		return err
	}
	if _, err := a.Store.ReleaseByDigest(res.Digest); err == nil {
		_ = a.Store.UpdateTarget(op.ID, "server", protocol.TargetSucceeded, "verified_catalog_commit", "already imported", "", false, map[string]any{"digest": res.Digest, "already_imported": true, "enrolled_root": true})
		return nil
	}
	plats, _ := json.Marshal(res.Platforms)
	if err := a.Store.InsertRelease(res.ID, res.Version, res.Digest, res.Notes, res.MetadataJSON, string(plats), true); err != nil {
		return err
	}
	for _, art := range res.Artifacts {
		_ = a.Store.InsertArtifact(res.ID, art.OS, art.Arch, art.Name, art.SHA256, art.Length, art.Path)
	}
	_ = a.Store.UpdateTarget(op.ID, "server", protocol.TargetSucceeded, "verified_catalog_commit", "release imported against enrolled TUF root", "", false, map[string]any{"digest": res.Digest, "version": res.Version, "enrolled_root": true})
	return nil
}
