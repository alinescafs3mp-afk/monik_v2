package server

import (
	"encoding/json"
	"fmt"
	"io"
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
	case "check.trial":
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
			"url": def.URL, "method": def.Method, "kind": def.Kind, "trial": true,
			"service_id": def.ServiceID, "timeout_seconds": def.TimeoutSeconds,
		}
		if jobID, err := a.Store.JobIDFor(op.ID, t.AgentID); err == nil {
			_ = a.Store.PatchJobParams(jobID, extra)
		}
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
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&body); err != nil || body.Verifier == "" {
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
