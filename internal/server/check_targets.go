package server

import (
	"encoding/json"
	"fmt"
	"github.com/alinescafs3mp-afk/monik_v2/internal/agent/checks"
	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
	"github.com/alinescafs3mp-afk/monik_v2/internal/storage"
)

func supportsCustom(a *storage.AgentRow) bool {
	var caps map[string]protocol.Capability
	_ = json.Unmarshal([]byte(a.Capabilities), &caps)
	return caps["http_custom_v1"].Status == "supported"
}
func (a *App) validateCheckTargets(req protocol.SubmitOperation, targets []string) error {
	if req.Action != "check.apply" && req.Action != "check.trial" {
		return nil
	}
	var d protocol.CheckDefinition
	var err error
	if req.Action == "check.apply" {
		b, _ := json.Marshal(req.Params["check"])
		d, err = protocol.DecodeCheck(b)
	} else {
		d, err = checks.ParseTrial(req.Params)
	}
	if err != nil {
		return err
	}
	for _, id := range targets {
		ag, err := a.Store.Agent(id)
		if err != nil {
			return fmt.Errorf("unknown agent")
		}
		if protocol.RequiresCustomRequest(d) && !supportsCustom(ag) {
			return fmt.Errorf("agent %s has not advertised http_custom_v1; update it and wait for a fresh capability report before saving custom checks", id)
		}
		if d.ServiceID != "" {
			sv, e := a.Store.Service(d.ServiceID)
			if e != nil || sv.AgentID != id {
				return fmt.Errorf("service is not owned by selected agent")
			}
		}
		for _, secret := range []string{d.SecretID, d.BodySecretID} {
			if secret == "" {
				continue
			}
			_, header, owner, check, _, e := a.Store.SecretMeta(secret)
			if e != nil || owner != id || check != "" && check != d.ID {
				return fmt.Errorf("secret is not authorized for this agent/check")
			}
			if secret == d.SecretID && header != d.SecretHeader {
				return fmt.Errorf("secret header does not match its protected definition")
			}
		}
	}
	return nil
}
