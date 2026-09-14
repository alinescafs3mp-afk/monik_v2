package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
	"github.com/alinescafs3mp-afk/monik_v2/internal/rules"
	"github.com/alinescafs3mp-afk/monik_v2/internal/storage"
)

func decodePolicyParams(params map[string]any, dst any) error {
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	if len(raw) > 32<<10 {
		return fmt.Errorf("policy request is too large")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	return dec.Decode(dst)
}
func (a *App) handleMonitoring(w http.ResponseWriter, r *http.Request, s *storage.Session) {
	rv, err := a.Store.HostRulesAt(a.Clock.Now())
	if err != nil {
		a.writeErr(w, 500, "rules", "could not read valid monitoring rules")
		return
	}
	windows, truncated, err := a.Store.MaintenanceWindows()
	if err != nil {
		a.writeErr(w, 500, "maintenance", "could not read maintenance windows")
		return
	}
	a.writeJSON(w, 200, map[string]any{"host_rules": rv, "maintenance": windows, "truncated": truncated, "server_time": a.Clock.Now()})
}
func (a *App) handleAdmissionPolicy(w http.ResponseWriter, r *http.Request, s *storage.Session) {
	if s.Role != "owner" {
		a.writeErr(w, 403, "forbidden", "owner only")
		return
	}
	policy, err := a.Store.AdmissionPolicy()
	if err != nil {
		a.writeErr(w, 500, "admission", "could not read admission policy")
		return
	}
	a.writeJSON(w, 200, policy)
}
func (a *App) executeMonitoring(op *protocol.Operation, req protocol.SubmitOperation, s *storage.Session) error {
	var evidence any
	switch req.Action {
	case "rule.save":
		var p struct {
			Base  *int64                `json:"base_revision"`
			Rules []rules.HostThreshold `json:"rules"`
		}
		if e := decodePolicyParams(req.Params, &p); e != nil {
			return e
		}
		if p.Base == nil {
			return fmt.Errorf("base_revision is required")
		}
		rv, e := a.Store.SaveHostRules(*p.Base, p.Rules, s.Username)
		if e != nil {
			return e
		}
		evidence = rv
	case "maintenance.set":
		var p struct {
			EntityType string     `json:"entity_type"`
			EntityID   string     `json:"entity_id"`
			Purpose    string     `json:"purpose"`
			Start      *time.Time `json:"start_at"`
			End        time.Time  `json:"end_at"`
		}
		if e := decodePolicyParams(req.Params, &p); e != nil {
			return e
		}
		start := a.Clock.Now().UTC()
		if p.Start != nil {
			start = *p.Start
		}
		m := storage.MaintenanceWindow{ID: op.ID, EntityType: p.EntityType, EntityID: p.EntityID, Purpose: p.Purpose, StartAt: start, EndAt: p.End, CreatedBy: s.Username}
		if e := a.Store.CreateMaintenance(m); e != nil {
			return e
		}
		evidence = map[string]any{"window_id": m.ID, "entity_type": m.EntityType, "entity_id": m.EntityID}
	case "maintenance.cancel":
		var p struct {
			ID string `json:"window_id"`
		}
		if e := decodePolicyParams(req.Params, &p); e != nil {
			return e
		}
		if p.ID == "" {
			return fmt.Errorf("window_id is required")
		}
		if e := a.Store.CancelMaintenance(p.ID, s.Username); e != nil {
			return e
		}
		evidence = map[string]any{"window_id": p.ID}
	case "enrollment.window.set":
		if s.Role != "owner" {
			return fmt.Errorf("owner only")
		}
		var p struct {
			Base    *int64 `json:"base_revision"`
			Minutes *int   `json:"minutes"`
		}
		if e := decodePolicyParams(req.Params, &p); e != nil {
			return e
		}
		if p.Base == nil || p.Minutes == nil {
			return fmt.Errorf("base_revision and minutes are required")
		}
		policy, e := a.Store.SetAdmission(*p.Base, *p.Minutes, s.Username)
		if e != nil {
			return e
		}
		evidence = policy
	default:
		return fmt.Errorf("unknown monitoring operation")
	}
	data, _ := json.Marshal(evidence)
	var fields map[string]any
	_ = json.Unmarshal(data, &fields)
	if err := a.Store.UpdateTarget(op.ID, "server", protocol.TargetSucceeded, "committed", "Policy committed; measurements and history are preserved", "", false, fields); err != nil {
		return &policyResultUnconfirmed{cause: err}
	}
	return nil
}
func (a *App) annotateIncidents(rows []map[string]any, at time.Time) error {
	for _, i := range rows {
		kind, _ := i["entity_type"].(string)
		id, _ := i["entity_id"].(string)
		active, err := a.Store.InMaintenance(kind, id, at)
		if err != nil {
			return err
		}
		i["maintenance_active"] = active
	}
	return nil
}

// Policy syntax is validated BEFORE journaling: unsupported fields and oversized
// payloads must not be persisted merely because the operation would later fail.
func validateMonitoringSyntax(req *protocol.SubmitOperation) error {
	switch req.Action {
	case "rule.save":
		var p struct {
			Base  *int64                `json:"base_revision"`
			Rules []rules.HostThreshold `json:"rules"`
		}
		if e := decodePolicyParams(req.Params, &p); e != nil {
			return e
		}
		if p.Base == nil || *p.Base < 0 {
			return fmt.Errorf("valid base_revision required")
		}
		return rules.ValidateThresholds(p.Rules)
	case "maintenance.set":
		var p struct {
			Kind    string     `json:"entity_type"`
			ID      string     `json:"entity_id"`
			Purpose string     `json:"purpose"`
			Start   *time.Time `json:"start_at"`
			End     time.Time  `json:"end_at"`
		}
		if e := decodePolicyParams(req.Params, &p); e != nil {
			return e
		}
		if (p.Kind != "fleet" && p.Kind != "agent" && p.Kind != "service") || len(p.ID) > 128 || len(p.Purpose) > 500 || p.Purpose == "" || p.End.IsZero() {
			return fmt.Errorf("maintenance scope, reason and end time required")
		}
	case "maintenance.cancel":
		var p struct {
			ID string `json:"window_id"`
		}
		if e := decodePolicyParams(req.Params, &p); e != nil {
			return e
		}
		if len(p.ID) == 0 || len(p.ID) > 128 {
			return fmt.Errorf("window_id required")
		}
	case "enrollment.window.set":
		var p struct {
			Base    *int64 `json:"base_revision"`
			Minutes *int   `json:"minutes"`
		}
		if e := decodePolicyParams(req.Params, &p); e != nil {
			return e
		}
		if p.Base == nil || *p.Base < 0 || p.Minutes == nil || *p.Minutes < 0 || *p.Minutes > 1440 {
			return fmt.Errorf("base revision and 0..1440 minutes required")
		}
	case "incident.unacknowledge":
		var p struct {
			ID string `json:"incident_id"`
		}
		if e := decodePolicyParams(req.Params, &p); e != nil {
			return e
		}
		if p.ID == "" || len(p.ID) > 128 {
			return fmt.Errorf("incident_id required")
		}
	}
	return nil
}

type policyResultUnconfirmed struct{ cause error }

func (e *policyResultUnconfirmed) Error() string {
	return "Policy was committed, but its operation result could not be recorded; read current policy before another action"
}
func (e *policyResultUnconfirmed) Unwrap() error { return e.cause }
