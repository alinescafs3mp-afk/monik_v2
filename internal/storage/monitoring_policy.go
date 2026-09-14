package storage

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/alinescafs3mp-afk/monik_v2/internal/rules"
)

const hostThresholdName = "monik_host_thresholds_v1"

type HostRuleVersion struct {
	Revision      int64                 `json:"revision"`
	EffectiveFrom *time.Time            `json:"effective_from"`
	Rules         []rules.HostThreshold `json:"rules"`
}

func (s *Store) HostRulesAt(at time.Time) (HostRuleVersion, error) {
	out := HostRuleVersion{Rules: rules.DefaultThresholds()}
	var raw, stamp string
	err := s.db().QueryRow(`SELECT id,body,effective_from FROM rule_versions WHERE name=? AND effective_from<=? AND (effective_to IS NULL OR effective_to>?) ORDER BY effective_from DESC,id DESC LIMIT 1`, hostThresholdName, at.UTC().Format(dbTimeFormat), at.UTC().Format(dbTimeFormat)).Scan(&out.Revision, &raw, &stamp)
	if errors.Is(err, sql.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	if err = json.Unmarshal([]byte(raw), &out.Rules); err != nil {
		return out, fmt.Errorf("stored host rules are corrupt: %w", err)
	}
	if err = rules.ValidateThresholds(out.Rules); err != nil {
		return out, err
	}
	t, err := time.Parse(time.RFC3339Nano, stamp)
	if err != nil {
		return out, err
	}
	out.EffectiveFrom = &t
	return out, nil
}
func (s *Store) SaveHostRules(base int64, list []rules.HostThreshold, actor string) (HostRuleVersion, error) {
	if err := rules.ValidateThresholds(list); err != nil {
		return HostRuleVersion{}, err
	}
	if base < 0 {
		return HostRuleVersion{}, ErrConflict
	}
	raw, _ := json.Marshal(list)
	var out HostRuleVersion
	err := s.WithTx(func(tx *sql.Tx) error {
		var current int64
		if e := tx.QueryRow(`SELECT COALESCE(MAX(id),0) FROM rule_versions WHERE name=?`, hostThresholdName).Scan(&current); e != nil {
			return e
		}
		if current != base {
			return fmt.Errorf("%w: host rules changed; reload and review", ErrConflict)
		}
		now := s.now()
		stamp := now.Format(dbTimeFormat)
		if _, e := tx.Exec(`UPDATE rule_versions SET effective_to=? WHERE name=? AND effective_to IS NULL`, stamp, hostThresholdName); e != nil {
			return e
		}
		result, e := tx.Exec(`INSERT INTO rule_versions(name,body,effective_from) VALUES(?,?,?)`, hostThresholdName, string(raw), stamp)
		if e != nil {
			return e
		}
		rev, e := result.LastInsertId()
		if e != nil {
			return e
		}
		// A policy change is NOT measured recovery. Preserve the old rule anchor.
		if _, e = tx.Exec(`UPDATE incidents SET status='policy_changed',resolved_at=?,reason=COALESCE(reason,'') || '; rule policy changed, not observed recovery' WHERE entity_type='agent' AND metric IN ('cpu','ram','disk') AND status IN ('pending','confirmed')`, stamp); e != nil {
			return e
		}
		if _, e = tx.Exec(`DELETE FROM incident_streaks WHERE entity_type='agent' AND metric IN ('cpu','ram','disk')`); e != nil {
			return e
		}
		if _, e = tx.Exec(`INSERT INTO audit_events(at,actor,action,entity,detail) VALUES(?,?,'rule.save',?,?)`, stamp, actor, hostThresholdName, fmt.Sprintf("rule revision %d -> %d", base, rev)); e != nil {
			return e
		}
		out = HostRuleVersion{Revision: rev, EffectiveFrom: &now, Rules: list}
		return nil
	})
	return out, err
}

type MaintenanceWindow struct {
	ID          string     `json:"id"`
	EntityType  string     `json:"entity_type"`
	EntityID    string     `json:"entity_id"`
	Purpose     string     `json:"purpose"`
	StartAt     time.Time  `json:"start_at"`
	EndAt       time.Time  `json:"end_at"`
	CreatedBy   string     `json:"created_by"`
	CancelledAt *time.Time `json:"cancelled_at,omitempty"`
	CancelledBy string     `json:"cancelled_by,omitempty"`
}

func (m MaintenanceWindow) ActiveAt(at time.Time) bool {
	return !at.Before(m.StartAt) && at.Before(m.EndAt) && (m.CancelledAt == nil || at.Before(*m.CancelledAt))
}
func (s *Store) CreateMaintenance(m MaintenanceWindow) error {
	m.Purpose = strings.TrimSpace(m.Purpose)
	if m.ID == "" || len(m.ID) > 128 || !utf8.ValidString(m.Purpose) || m.Purpose == "" || len(m.Purpose) > 500 || strings.IndexFunc(m.Purpose, unicode.IsControl) >= 0 {
		return fmt.Errorf("a bounded purpose and identity are required")
	}
	now := s.now()
	if m.StartAt.IsZero() || !m.EndAt.After(m.StartAt) || m.EndAt.Sub(m.StartAt) > 7*24*time.Hour || m.StartAt.Before(now.Add(-time.Minute)) || m.StartAt.After(now.Add(30*24*time.Hour)) || !m.EndAt.After(now) {
		return fmt.Errorf("maintenance must start now or within 30 days and last at most seven days")
	}
	if m.EntityType != "fleet" && m.EntityType != "agent" && m.EntityType != "service" {
		return fmt.Errorf("invalid maintenance scope")
	}
	if len(m.EntityID) > 128 || (m.EntityType == "fleet") != (m.EntityID == "") {
		return fmt.Errorf("choose either fleet scope or one explicit machine/service")
	}
	if m.StartAt.Before(now) {
		m.StartAt = now
	} // Never retroactively suppress an observed interval.
	return s.WithTx(func(tx *sql.Tx) error {
		if m.EntityType != "fleet" {
			table := "agents"
			if m.EntityType == "service" {
				table = "services"
			}
			var id string
			if e := tx.QueryRow(`SELECT id FROM `+table+` WHERE id=?`, m.EntityID).Scan(&id); e != nil {
				return e
			}
		}
		var n int
		if e := tx.QueryRow(`SELECT COUNT(*) FROM maintenance_windows m LEFT JOIN maintenance_cancellations c ON c.window_id=m.id WHERE m.end_at>? AND c.window_id IS NULL`, now.Format(dbTimeFormat)).Scan(&n); e != nil {
			return e
		}
		if n >= 200 {
			return fmt.Errorf("too many active or scheduled maintenance windows")
		}
		_, e := tx.Exec(`INSERT INTO maintenance_windows(id,entity_type,entity_id,purpose,start_at,end_at,created_by) VALUES(?,?,?,?,?,?,?)`, m.ID, m.EntityType, m.EntityID, m.Purpose, m.StartAt.UTC().Format(dbTimeFormat), m.EndAt.UTC().Format(dbTimeFormat), m.CreatedBy)
		return e
	})
}

// Cancellation has its own durable timestamp; a historical interval is never
// deleted or retrospectively suppressed before the action actually occurred.
func (s *Store) CancelMaintenance(id, actor string) error {
	return s.WithTx(func(tx *sql.Tx) error {
		var end string
		if e := tx.QueryRow(`SELECT end_at FROM maintenance_windows WHERE id=?`, id).Scan(&end); e != nil {
			return e
		}
		var already int
		if e := tx.QueryRow(`SELECT COUNT(*) FROM maintenance_cancellations WHERE window_id=?`, id).Scan(&already); e != nil {
			return e
		}
		if already > 0 {
			return nil
		}
		if end <= s.now().Format(dbTimeFormat) {
			return fmt.Errorf("maintenance has already ended")
		}
		_, e := tx.Exec(`INSERT INTO maintenance_cancellations(window_id,cancelled_at,cancelled_by) VALUES(?,?,?)`, id, s.now().Format(dbTimeFormat), actor)
		return e
	})
}
func (s *Store) MaintenanceWindows() ([]MaintenanceWindow, bool, error) {
	rows, err := s.db().Query(`SELECT m.id,m.entity_type,COALESCE(m.entity_id,''),COALESCE(m.purpose,''),m.start_at,m.end_at,m.created_by,c.cancelled_at,COALESCE(c.cancelled_by,'') FROM maintenance_windows m LEFT JOIN maintenance_cancellations c ON c.window_id=m.id WHERE m.end_at>? ORDER BY CASE WHEN m.end_at>? AND c.window_id IS NULL THEN 0 ELSE 1 END,m.start_at DESC,m.id DESC LIMIT 201`, s.now().Add(-180*24*time.Hour).Format(dbTimeFormat), s.now().Format(dbTimeFormat))
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	out := []MaintenanceWindow{}
	for rows.Next() {
		var m MaintenanceWindow
		var start, end string
		var cancel sql.NullString
		if err = rows.Scan(&m.ID, &m.EntityType, &m.EntityID, &m.Purpose, &start, &end, &m.CreatedBy, &cancel, &m.CancelledBy); err != nil {
			return nil, false, err
		}
		if m.StartAt, err = time.Parse(time.RFC3339Nano, start); err != nil {
			return nil, false, err
		}
		if m.EndAt, err = time.Parse(time.RFC3339Nano, end); err != nil {
			return nil, false, err
		}
		if cancel.Valid {
			t, e := time.Parse(time.RFC3339Nano, cancel.String)
			if e != nil {
				return nil, false, e
			}
			m.CancelledAt = &t
		}
		out = append(out, m)
	}
	if err = rows.Err(); err != nil {
		return nil, false, err
	}
	truncated := len(out) > 200
	if truncated {
		out = out[:200]
	}
	return out, truncated, nil
}
func (s *Store) InMaintenance(kind, id string, at time.Time) (bool, error) {
	agentID := ""
	if kind == "agent" {
		agentID = id
	}
	if kind == "service" {
		e := s.db().QueryRow(`SELECT agent_id FROM services WHERE id=?`, id).Scan(&agentID)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return false, e
		}
	}
	stamp := at.UTC().Format(dbTimeFormat)
	var n int
	err := s.db().QueryRow(`SELECT EXISTS(SELECT 1 FROM maintenance_windows m LEFT JOIN maintenance_cancellations c ON c.window_id=m.id WHERE m.start_at<=? AND m.end_at>? AND (c.cancelled_at IS NULL OR c.cancelled_at>?) AND (m.entity_type='fleet' OR (m.entity_type=? AND m.entity_id=?) OR (m.entity_type='agent' AND m.entity_id=?)))`, stamp, stamp, stamp, kind, id, agentID).Scan(&n)
	return n != 0, err
}

func (s *Store) UnackIncident(id, actor string) error {
	return s.WithTx(func(tx *sql.Tx) error {
		var at, by sql.NullString
		if e := tx.QueryRow(`SELECT acked_at,acked_by FROM incidents WHERE id=?`, id).Scan(&at, &by); e != nil {
			return e
		}
		if !at.Valid {
			return nil
		}
		if _, e := tx.Exec(`UPDATE incidents SET acked_at=NULL,acked_by=NULL WHERE id=?`, id); e != nil {
			return e
		}
		detail, _ := json.Marshal(map[string]string{"previous_ack_at": at.String, "previous_ack_by": by.String})
		_, e := tx.Exec(`INSERT INTO audit_events(at,actor,action,entity,detail) VALUES(?,?,'incident.unacknowledge',?,?)`, s.now().Format(dbTimeFormat), actor, id, string(detail))
		return e
	})
}
