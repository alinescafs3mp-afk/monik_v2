package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/alinescafs3mp-afk/monik_v2/internal/agent/checks"
	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
	"github.com/alinescafs3mp-afk/monik_v2/internal/secure"
	"github.com/alinescafs3mp-afk/monik_v2/internal/storage"
	"github.com/alinescafs3mp-afk/monik_v2/internal/tlsutil"
)

func (a *App) validateLifecycleParams(req *protocol.SubmitOperation) error {
	switch req.Action {
	case "update.import":
		path, ok := req.Params["bundle_path"].(string)
		if !ok || path == "" || len(path) > 4096 {
			return fmt.Errorf("bundle_path required")
		}
		for k, v := range req.Params {
			if k == "enroll_root" {
				if _, ok := v.(bool); !ok {
					return fmt.Errorf("enroll_root must be boolean")
				}
			} else if k != "bundle_path" {
				return fmt.Errorf("unknown release import parameter")
			}
		}
		return nil
	case "update.rollout":
		id, ok := req.Params["release_id"].(string)
		if !ok || id == "" || len(id) > 128 {
			return fmt.Errorf("release_id required")
		}
		for k, v := range req.Params {
			switch k {
			case "release_id":
			case "batch_size", "observe_seconds":
				n, ok := v.(float64)
				if !ok || n != float64(int(n)) {
					return fmt.Errorf("%s must be an integer", k)
				}
				if k == "batch_size" && (n < 1 || n > 10) || k == "observe_seconds" && (n < 15 || n > 300) {
					return fmt.Errorf("%s outside supported range", k)
				}
			default:
				return fmt.Errorf("unknown rollout option; artifact locations are server-controlled")
			}
		}
		if len(req.TargetIDs) > 500 {
			return fmt.Errorf("at most 500 frozen targets")
		}
		return nil
	case "update.pause", "update.resume":
		id, ok := req.Params["operation_id"].(string)
		rev, valid := req.Params["rollout_revision"].(float64)
		if !ok || id == "" || len(id) > 128 || !valid || rev < 1 || rev != float64(int64(rev)) || len(req.Params) != 2 {
			return fmt.Errorf("operation_id and integer rollout_revision required")
		}
		if len(req.TargetIDs) != 0 || req.TargetMode == "all" {
			return fmt.Errorf("rollout control uses its existing frozen targets")
		}
		return nil

	case "session.revoke_others":
		if len(req.Params) != 0 {
			return fmt.Errorf("no parameters accepted")
		}
		return nil
	case "profile.apply":
		for k, v := range req.Params {
			switch k {
			case "auto_monitor_new", "pause_all_services", "paused":
				if _, ok := v.(bool); !ok {
					return fmt.Errorf("%s must be a boolean", k)
				}
			case "collect_seconds", "ping_target", "display_name", "base_revision":
			default:
				return fmt.Errorf("unknown profile parameter: %s", k)
			}
		}
		return nil
	case "service.pin", "service.hide":
		id, ok := req.Params["service_id"].(string)
		if !ok || id == "" || len(id) > 128 {
			return fmt.Errorf("service_id is required")
		}
		field := "pinned"
		if req.Action == "service.hide" {
			field = "hidden"
		}
		if _, ok := req.Params[field].(bool); !ok {
			return fmt.Errorf("%s must be a boolean", field)
		}
		for key, value := range req.Params {
			if key == "expected_pinned" && req.Action == "service.pin" {
				if _, ok := value.(bool); !ok {
					return fmt.Errorf("expected_pinned must be a boolean")
				}
				continue
			}
			if key != "service_id" && key != field {
				return fmt.Errorf("unknown service preference parameter")
			}
		}
		return nil
	case "rule.save", "maintenance.set", "maintenance.cancel", "enrollment.window.set", "incident.unacknowledge":
		return validateMonitoringSyntax(req)
	case "service.rename":
		id, ok := req.Params["service_id"].(string)
		name, nameOK := req.Params["display_name"].(string)
		old, oldOK := req.Params["expected_name"].(string)
		if !ok || id == "" || len(id) > 128 || !nameOK || !oldOK || len(old) > 2048 || len(strings.TrimSpace(name)) == 0 || len(name) > 255 {
			return fmt.Errorf("service_id, expected_name and a name up to 255 UTF-8 bytes required")
		}
		for _, c := range name {
			if c < 32 || c == 127 {
				return fmt.Errorf("name cannot contain control characters")
			}
		}
		for k := range req.Params {
			if k != "service_id" && k != "display_name" && k != "expected_name" {
				return fmt.Errorf("unknown rename parameter")
			}
		}
		req.Params["display_name"] = strings.TrimSpace(name)
		return nil
	case "agent.rename":
		id, idOK := req.Params["agent_id"].(string)
		name, nameOK := req.Params["display_name"].(string)
		if !idOK || id == "" || !nameOK || len(strings.TrimSpace(name)) == 0 || len(name) > 255 {
			return fmt.Errorf("agent_id and a nonempty display_name up to 255 bytes required")
		}
		for _, ch := range name {
			if ch < 32 || ch == 127 {
				return fmt.Errorf("machine name cannot contain control characters")
			}
		}
		if old, exists := req.Params["expected_name"]; exists {
			if _, ok := old.(string); !ok {
				return fmt.Errorf("expected_name must be a string")
			}
		}
		req.Params["display_name"] = strings.TrimSpace(name)
		return nil
	case "enrollment.approve", "enrollment.reject":
		id, _ := req.Params["agent_id"].(string)
		fp, _ := req.Params["fingerprint"].(string)
		if id == "" || len(id) > 128 || len(fp) != 64 {
			return fmt.Errorf("agent_id and full registration fingerprint required")
		}
		return nil
	case "check.apply":
		for key := range req.Params {
			if key != "check" && key != "base_revision" {
				return fmt.Errorf("unknown check operation parameter")
			}
		}
		raw, err := json.Marshal(req.Params["check"])
		if err != nil {
			return err
		}
		d, err := protocol.DecodeCheck(raw)
		if err != nil {
			return err
		}
		if d.ID == "" || d.ServiceID == "" {
			return fmt.Errorf("check and service identity required")
		}
		return protocol.ValidateCheck(d)
	case "check.trial":
		for key := range req.Params {
			if strings.HasPrefix(key, "_") {
				return fmt.Errorf("reserved trial parameter")
			}
		}
		_, err := checks.ParseTrial(req.Params)
		return err
	case "trust.stage":
		pem, _ := req.Params["trust_pem"].(string)
		fp, err := tlsutil.CertFingerprint([]byte(pem))
		if err != nil {
			return fmt.Errorf("trust_pem must be a PEM certificate")
		}
		if req.Params == nil {
			req.Params = map[string]any{}
		}
		req.Params["fingerprint"] = fp
		return nil
	case "trust.retire":
		fp, _ := req.Params["fingerprint"].(string)
		if strings.TrimSpace(fp) == "" {
			return fmt.Errorf("fingerprint required")
		}
		return nil
	}
	return nil
}

func (a *App) handleCheckTrial(op *protocol.Operation, req protocol.SubmitOperation) error {
	def, err := checks.ParseTrial(req.Params)
	if err != nil {
		return err
	}
	for _, t := range op.Targets {
		if t.AgentID == "server" || t.Status == protocol.TargetRejected {
			continue
		}
		extra := map[string]any{
			"url": def.URL, "method": def.Method, "kind": def.Kind,
			"service_id": def.ServiceID, "timeout_seconds": def.TimeoutSeconds,
		}
		if jobID, err := a.Store.JobIDFor(op.ID, t.AgentID); err == nil {
			jobExtra := map[string]any{"trial": true}
			for k, v := range extra {
				jobExtra[k] = v
			}
			_ = a.Store.PatchJobParams(jobID, jobExtra)
		}
		// Queued evidence must not look like a completed agent trial.
		_ = a.Store.UpdateTarget(op.ID, t.AgentID, t.Status, "trial.queued", "one-shot trial queued on the agent; definition is not saved", "", true, extra)
	}
	return nil
}

func (a *App) handleTrust(op *protocol.Operation, req protocol.SubmitOperation) error {
	for _, t := range op.Targets {
		if t.AgentID == "server" || t.Status == protocol.TargetRejected {
			continue
		}
		extra := map[string]any{}
		for _, k := range []string{"trust_pem", "fingerprint"} {
			if v, ok := req.Params[k]; ok {
				extra[k] = v
			}
		}
		if jobID, err := a.Store.JobIDFor(op.ID, t.AgentID); err == nil {
			_ = a.Store.PatchJobParams(jobID, extra)
		}
		stage := "trust.staged"
		msg := "additional controller trust queued for the agent"
		if req.Action == "trust.retire" {
			stage = "trust.retire_queued"
			msg = "trust retirement queued; last remaining root cannot be removed"
		}
		_ = a.Store.UpdateTarget(op.ID, t.AgentID, t.Status, stage, msg, "", true, map[string]any{"fingerprint": extra["fingerprint"]})
	}
	return nil
}

func (a *App) handleCredentialRotate(op *protocol.Operation) error {
	for _, t := range op.Targets {
		if t.AgentID == "server" || t.Status == protocol.TargetRejected {
			continue
		}
		_ = a.Store.UpdateTarget(op.ID, t.AgentID, t.Status, "credential.rotate_queued", "agent will generate the next credential, register its verifier, then prove the new token", "", true, nil)
	}
	return nil
}

func (a *App) handleCredentialNext(w http.ResponseWriter, r *http.Request) {
	ag, _, ok := a.agentFromCurrent(r)
	if !ok {
		a.writeErr(w, 401, "unauthenticated", "agent credential required")
		return
	}
	var body struct {
		JobID    string `json:"job_id"`
		Verifier string `json:"verifier"`
	}
	if err := parseJSONLimit(r, &body, 1<<16); err != nil || body.Verifier == "" {
		a.writeErr(w, 400, "malformed", "job_id and verifier required")
		return
	}
	if len(body.Verifier) != 64 {
		a.writeErr(w, 400, "malformed", "verifier must be a sha256 hex digest")
		return
	}
	if secure.EqualHash(ag.CredentialHash, body.Verifier) {
		a.writeJSON(w, 200, map[string]any{"ok": true, "already_current": true})
		return
	}
	if pending, _, err := a.Store.PendingCredential(ag.ID); err == nil && pending != "" && !secure.EqualHash(pending, body.Verifier) {
		a.writeErr(w, 409, "overlap", "a different pending credential is already registered")
		return
	}
	until := a.Clock.Now().UTC().Add(protocol.OneShotExpiry)
	if err := a.Store.SetPendingCredential(ag.ID, body.Verifier, body.JobID, until); err != nil {
		a.writeErr(w, 500, "persist", "could not store pending verifier")
		return
	}
	a.writeJSON(w, 200, map[string]any{"ok": true, "overlap_until": until})
}

func (a *App) agentFromCurrent(r *http.Request) (*storage.AgentRow, string, bool) {
	return a.lookupAgent(r, false)
}

func (a *App) lookupAgent(r *http.Request, promotePending bool) (*storage.AgentRow, string, bool) {
	agentID, cred, ok := bearer(r)
	if !ok {
		return nil, "", false
	}
	ag, err := a.Store.Agent(agentID)
	if err != nil || ag.Revoked {
		return nil, "", false
	}
	got := secure.HashToken(cred)
	if secure.EqualHash(ag.CredentialHash, got) {
		return ag, cred, true
	}
	if !promotePending {
		return nil, "", false
	}
	pending, _, err := a.Store.PendingCredential(ag.ID)
	if err != nil || pending == "" || !secure.EqualHash(pending, got) {
		return nil, "", false
	}
	if err := a.Store.PromoteCredential(ag.ID, got); err != nil {
		return nil, "", false
	}
	fresh, err := a.Store.Agent(ag.ID)
	if err != nil {
		return nil, "", false
	}
	return fresh, cred, true
}
