package storage

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

var ErrInvalidHistoryQuery = errors.New("invalid history query")

type IncidentQuery struct {
	From, To                                                   time.Time
	State, Severity, Metric, EntityID, Before, Acknowledgement string
	Limit                                                      int
}
type incidentCursor struct {
	At string `json:"at"`
	ID string `json:"id"`
}

// SearchIncidents returns intervals that overlap [From, To), not only incidents
// that started there. Tuple pagination is stable even for equal timestamps.
func (s *Store) SearchIncidents(ctx context.Context, q IncidentQuery) ([]map[string]any, string, error) {
	if !q.To.After(q.From) || q.To.Sub(q.From) > 180*24*time.Hour {
		return nil, "", fmt.Errorf("%w: invalid incident range", ErrInvalidHistoryQuery)
	}
	if q.Limit <= 0 {
		q.Limit = 100
	}
	if q.Limit > 200 {
		return nil, "", fmt.Errorf("%w: page limit must be <=200", ErrInvalidHistoryQuery)
	}
	where := ` opened_at<? AND (resolved_at IS NULL OR resolved_at>?)`
	args := []any{q.To.UTC().Format(dbTimeFormat), q.From.UTC().Format(dbTimeFormat)}
	switch q.State {
	case "", "all":
	case "open":
		where += ` AND status IN ('pending','confirmed')`
	case "pending", "confirmed", "resolved", "interrupted", "policy_changed":
		where += ` AND status=?`
		args = append(args, q.State)
	default:
		return nil, "", fmt.Errorf("%w: invalid incident state", ErrInvalidHistoryQuery)
	}
	switch q.Acknowledgement {
	case "", "all":
	case "unread":
		where += ` AND acked_at IS NULL`
	case "read":
		where += ` AND acked_at IS NOT NULL`
	default:
		return nil, "", fmt.Errorf("%w: invalid acknowledgement filter", ErrInvalidHistoryQuery)
	}
	if q.Severity != "" {
		if q.Severity != "warning" && q.Severity != "critical" {
			return nil, "", fmt.Errorf("%w: invalid severity", ErrInvalidHistoryQuery)
		}
		where += ` AND severity=?`
		args = append(args, q.Severity)
	}
	if q.Metric != "" {
		where += ` AND metric=?`
		args = append(args, q.Metric)
	}
	if q.EntityID != "" {
		where += ` AND entity_id=?`
		args = append(args, q.EntityID)
	}
	if q.Before != "" {
		if len(q.Before) > 1024 {
			return nil, "", fmt.Errorf("%w: invalid cursor", ErrInvalidHistoryQuery)
		}
		raw, err := base64.RawURLEncoding.DecodeString(q.Before)
		var c incidentCursor
		if err != nil || json.Unmarshal(raw, &c) != nil || c.ID == "" {
			return nil, "", fmt.Errorf("%w: invalid cursor", ErrInvalidHistoryQuery)
		}
		at, err := time.Parse(time.RFC3339Nano, c.At)
		if err != nil {
			return nil, "", fmt.Errorf("%w: invalid cursor", ErrInvalidHistoryQuery)
		}
		when := at.UTC().Format(dbTimeFormat)
		where += ` AND (opened_at<? OR (opened_at=? AND id<?))`
		args = append(args, when, when, c.ID)
	}
	args = append(args, q.Limit+1)
	rows, err := s.DB.QueryContext(ctx, `SELECT id,entity_type,entity_id,metric,severity,status,opened_at,confirmed_at,resolved_at,reason,acked_at,acked_by,rule_version,maintenance,CASE WHEN entity_type='agent' THEN COALESCE((SELECT display_name FROM agents WHERE agents.id=incidents.entity_id),entity_id) ELSE COALESCE((SELECT display_name FROM services WHERE services.id=incidents.entity_id),entity_id) END,CASE WHEN entity_type='agent' THEN entity_id ELSE COALESCE((SELECT agent_id FROM services WHERE services.id=incidents.entity_id),'') END FROM incidents WHERE `+where+` ORDER BY opened_at DESC,id DESC LIMIT ?`, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	out := make([]map[string]any, 0)
	for rows.Next() {
		var id, et, eid, sev, state, opened string
		var metric sql.NullString
		var rule sql.NullInt64
		var maintenance int
		var displayName, agentID string
		var confirmed, resolved, reason, acked, actor sql.NullString
		if err = rows.Scan(&id, &et, &eid, &metric, &sev, &state, &opened, &confirmed, &resolved, &reason, &acked, &actor, &rule, &maintenance, &displayName, &agentID); err != nil {
			return nil, "", err
		}
		out = append(out, map[string]any{"id": id, "entity_type": et, "entity_id": eid, "metric": metric.String, "severity": sev, "status": state, "opened_at": opened, "confirmed_at": confirmed.String, "resolved_at": resolved.String, "reason": reason.String, "acked_at": acked.String, "acked_by": actor.String, "rule_version": rule.Int64, "maintenance_observed": maintenance != 0, "display_name": displayName, "agent_id": agentID, "name_semantics": "current_inventory"})
	}
	if err = rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(out) > q.Limit {
		out = out[:q.Limit]
		last := out[len(out)-1]
		b, _ := json.Marshal(incidentCursor{At: last["opened_at"].(string), ID: last["id"].(string)})
		next = base64.RawURLEncoding.EncodeToString(b)
	}
	return out, next, nil
}

type ExportRow struct {
	ObservedAt string          `json:"observed_at"`
	ReceivedAt string          `json:"received_at"`
	Payload    json.RawMessage `json:"payload"`
}

func (s *Store) ExportSamples(ctx context.Context, kind, id string, from, to time.Time) ([]ExportRow, error) {
	if !to.After(from) || to.Sub(from) > 24*time.Hour || id == "" {
		return nil, fmt.Errorf("choose one entity and a range up to 24 hours")
	}
	table, column := "host_samples", "agent_id"
	if kind == "service" {
		table, column = "service_observations", "service_id"
	} else if kind != "agent" {
		return nil, fmt.Errorf("unknown entity kind")
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT observed_at,received_at,payload FROM `+table+` WHERE `+column+`=? AND observed_at>=? AND observed_at<? ORDER BY observed_at,id LIMIT 20001`, id, from.UTC().Format(dbTimeFormat), to.UTC().Format(dbTimeFormat))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]ExportRow, 0)
	size := 0
	for rows.Next() {
		var v ExportRow
		var raw string
		if err = rows.Scan(&v.ObservedAt, &v.ReceivedAt, &raw); err != nil {
			return nil, err
		}
		size += len(raw) + len(v.ObservedAt) + len(v.ReceivedAt) + 100
		if len(out) >= 20000 || size > 12<<20 {
			return nil, fmt.Errorf("export limit reached; choose a shorter range (no partial file created)")
		}
		if !json.Valid([]byte(raw)) || strings.TrimSpace(raw) == "" {
			return nil, fmt.Errorf("stored observation is invalid")
		}
		v.Payload = json.RawMessage(raw)
		out = append(out, v)
	}
	return out, rows.Err()
}
