package storage

import (
	"database/sql"
	"errors"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/idgen"
)

// IncidentPolicy evaluates real, chronologically ordered observations. It is
// intentionally separate from today's parameter highlighting in the overview.
type IncidentPolicy struct {
	PersistFor, RecoverFor, MaxGap time.Duration
	Failures, Successes            int
	RuleVersion                    int64
	HoldRecovery                   bool // A confirmed breach must cross the recovery threshold.
	Maintenance                    bool
}

type incidentStreak struct {
	Last, BadSince, GoodSince time.Time
	Bad, Good                 int
}

// ObserveIncident persists the streak and incident transition in one transaction.
// Unknown evidence resets continuity but can never resolve a confirmed problem.
func (s *Store) ObserveIncident(entityType, entityID, metric string, at time.Time, known, failed bool, severity, reason string, policy IncidentPolicy) error {
	if at.IsZero() {
		return ErrConflict
	}
	return s.WithTx(func(tx *sql.Tx) error {
		var streak incidentStreak
		var last, bad, good string
		err := tx.QueryRow(`SELECT last_at,bad_since,good_since,bad_count,good_count FROM incident_streaks WHERE entity_type=? AND entity_id=? AND metric=?`, entityType, entityID, metric).Scan(&last, &bad, &good, &streak.Bad, &streak.Good)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		streak.Last, _ = time.Parse(time.RFC3339Nano, last)
		streak.BadSince, _ = time.Parse(time.RFC3339Nano, bad)
		streak.GoodSince, _ = time.Parse(time.RFC3339Nano, good)
		if !streak.Last.IsZero() && !at.After(streak.Last) {
			return nil
		}
		var incidentID, status, previousSeverity string
		err = tx.QueryRow(`SELECT id,status,severity FROM incidents WHERE entity_type=? AND entity_id=? AND metric=? AND status IN ('pending','confirmed') ORDER BY opened_at DESC LIMIT 1`, entityType, entityID, metric).Scan(&incidentID, &status, &previousSeverity)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		gap := !streak.Last.IsZero() && at.Sub(streak.Last) > policy.MaxGap
		if !known || gap {
			streak.Bad, streak.Good = 0, 0
			streak.BadSince, streak.GoodSince = time.Time{}, time.Time{}
			if status == "pending" {
				if _, err = tx.Exec(`UPDATE incidents SET status='interrupted',resolved_at=?,reason=reason || '; observation continuity interrupted' WHERE id=?`, at.UTC().Format(dbTimeFormat), incidentID); err != nil {
					return err
				}
				status, incidentID, previousSeverity = "", "", ""
			}
		}
		if known && failed {
			streak.Good, streak.GoodSince = 0, time.Time{}
			if streak.Bad == 0 {
				streak.BadSince = at
			}
			streak.Bad++
			if incidentID == "" {
				incidentID = idgen.New()
				status = "pending"
				if _, err = tx.Exec(`INSERT INTO incidents(id,entity_type,entity_id,metric,severity,status,opened_at,reason,rule_version,maintenance) VALUES(?,?,?,?,?,'pending',?,?,?,?)`, incidentID, entityType, entityID, metric, severity, at.UTC().Format(dbTimeFormat), reason, policy.RuleVersion, policy.Maintenance); err != nil {
					return err
				}
			}
			if previousSeverity == "warning" && severity == "critical" {
				if _, err = tx.Exec(`UPDATE incidents SET acked_at=NULL,acked_by=NULL WHERE id=?`, incidentID); err != nil {
					return err
				}
				if _, err = tx.Exec(`INSERT INTO audit_events(at,actor,action,entity,detail) VALUES(?,'system','incident.escalated',?,'critical escalation requires acknowledgement')`, at.UTC().Format(dbTimeFormat), incidentID); err != nil {
					return err
				}
			}
			// Retain peak severity until recovery, not alternating read/unread on every sample.
			if previousSeverity == "critical" {
				severity = "critical"
			}
			if _, err = tx.Exec(`UPDATE incidents SET severity=?,reason=?,maintenance=MAX(maintenance,?) WHERE id=?`, severity, reason, policy.Maintenance, incidentID); err != nil {
				return err
			}
			if status == "pending" && streak.Bad >= policy.Failures && at.Sub(streak.BadSince) >= policy.PersistFor {
				if _, err = tx.Exec(`UPDATE incidents SET status='confirmed',confirmed_at=? WHERE id=?`, at.UTC().Format(dbTimeFormat), incidentID); err != nil {
					return err
				}
			}
		} else if known && status == "confirmed" && policy.HoldRecovery {
			streak.Good, streak.GoodSince = 0, time.Time{}
		} else if known {
			streak.Bad, streak.BadSince = 0, time.Time{}
			if streak.Good == 0 {
				streak.GoodSince = at
			}
			streak.Good++
			if status == "pending" || (status == "confirmed" && streak.Good >= policy.Successes && at.Sub(streak.GoodSince) >= policy.RecoverFor) {
				if _, err = tx.Exec(`UPDATE incidents SET status='resolved',resolved_at=? WHERE id=?`, at.UTC().Format(dbTimeFormat), incidentID); err != nil {
					return err
				}
			}
		}
		_, err = tx.Exec(`INSERT INTO incident_streaks(entity_type,entity_id,metric,last_at,bad_since,good_since,bad_count,good_count) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(entity_type,entity_id,metric) DO UPDATE SET last_at=excluded.last_at,bad_since=excluded.bad_since,good_since=excluded.good_since,bad_count=excluded.bad_count,good_count=excluded.good_count`, entityType, entityID, metric, at.UTC().Format(dbTimeFormat), streak.BadSince.UTC().Format(dbTimeFormat), streak.GoodSince.UTC().Format(dbTimeFormat), streak.Bad, streak.Good)
		return err
	})
}
