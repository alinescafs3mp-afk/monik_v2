package storage

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/netutil"
	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
)

// A missing listener is confirmed by two distinct complete scans at least one
// minute apart. Offline agents, failed scans and HTTP errors are not evidence.
const serviceAbsenceGrace = time.Minute
const inventoryMaxAge = 5 * time.Minute

func validateInventory(d *protocol.DiscoveryDelta, observed, now time.Time) error {
	if d == nil || d.InventoryVersion == 0 {
		return nil
	} // additive old-agent support
	if d.InventoryVersion != 1 || d.Kind != "snapshot" {
		return fmt.Errorf("unsupported listener inventory version/kind")
	}
	if len(d.ListenerTargets) > 4096 || d.ListenerCount < len(d.ListenerTargets) {
		return fmt.Errorf("listener inventory exceeds budget")
	}
	if d.StartedAt.IsZero() || d.EndedAt.Before(d.StartedAt) || d.EndedAt.After(now.Add(5*time.Second)) || d.EndedAt.After(observed.Add(5*time.Second)) {
		return fmt.Errorf("invalid listener inventory timestamps")
	}
	seen := make(map[string]bool, len(d.ListenerTargets))
	for _, target := range d.ListenerTargets {
		canonical, err := netutil.CanonicalListenerTarget(target)
		if err != nil || canonical != target || seen[target] {
			return fmt.Errorf("invalid or duplicate listener inventory target")
		}
		seen[target] = true
	}
	for _, ep := range d.Confirmed {
		if !seen[ep.DialTarget] {
			return fmt.Errorf("identified service is not in OS inventory")
		}
	}
	// Only require the full subset when no inventory truncation occurred.
	if d.ListenerCoverageComplete {
		for _, ep := range d.Unresolved {
			if !seen[ep.DialTarget] {
				return fmt.Errorf("unresolved endpoint is not in OS inventory")
			}
		}
	}
	return nil
}

// inventoryIsNew checks observation time, not report arrival time. Replayed or
// delayed scans must not resurrect retired endpoints or move the watermark back.
func (s *Store) inventoryIsNew(agentID string, d *protocol.DiscoveryDelta) (bool, error) {
	if d == nil {
		return false, nil
	}
	if d.InventoryVersion == 0 {
		return true, nil
	}
	if s.now().Sub(d.EndedAt) > inventoryMaxAge {
		return false, nil
	}
	var last string
	err := s.db().QueryRow(`SELECT started_at FROM discovery_checkpoints WHERE agent_id=?`, agentID).Scan(&last)
	if err == sql.ErrNoRows {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	at, err := time.Parse(time.RFC3339Nano, last)
	if err != nil {
		return false, fmt.Errorf("corrupt inventory checkpoint: %w", err)
	}
	return d.StartedAt.After(at), nil
}

// reconcileInventory is part of the telemetry transaction. Never remove source
// rows, check definitions, pins, owner names or history. Presentation can hide
// missing, unmonitored and unpinned discoveries independently from health.
func (s *Store) reconcileInventory(agentID string, d *protocol.DiscoveryDelta) error {
	if d.InventoryVersion != 1 {
		return nil
	}
	present := make(map[string]bool, len(d.ListenerTargets))
	for _, target := range d.ListenerTargets {
		present[target] = true
	}
	rows, err := s.db().Query(`SELECT v.id,v.dial_target,v.last_discovered_at,
 COALESCE(p.last_seen_at,''),COALESCE(p.missing_since,''),COALESCE(p.absent_snapshots,0)
 FROM services v LEFT JOIN service_presence p ON p.service_id=v.id
 WHERE v.agent_id=? AND v.source LIKE 'listener%'`, agentID)
	if err != nil {
		return err
	}
	type record struct {
		id, target, discovered, last, missing string
		count                                 int
	}
	var records []record
	for rows.Next() {
		var r record
		var discovered sql.NullString
		if err = rows.Scan(&r.id, &r.target, &discovered, &r.last, &r.missing, &r.count); err != nil {
			rows.Close()
			return err
		}
		r.discovered = discovered.String
		records = append(records, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	stamp := d.EndedAt.UTC().Format(dbTimeFormat)
	for _, r := range records {
		target, e := netutil.CanonicalListenerTarget(r.target)
		if e != nil {
			continue
		} // legacy invalid/unknown targets are never auto-retired
		if present[target] {
			if _, err = s.db().Exec(`INSERT INTO service_presence(service_id,state,last_seen_at,missing_since,absent_snapshots) VALUES(?,'present',?,'',0)
    ON CONFLICT(service_id) DO UPDATE SET state='present',last_seen_at=excluded.last_seen_at,missing_since='',absent_snapshots=0`, r.id, stamp); err != nil {
				return err
			}
			if _, err = s.db().Exec(`UPDATE services SET last_discovered_at=? WHERE id=?`, stamp, r.id); err != nil {
				return err
			}
			continue
		}
		if !d.ListenerCoverageComplete {
			continue
		}
		state := "unconfirmed"
		if r.missing == "" {
			r.missing = stamp
		}
		since, e := time.Parse(time.RFC3339Nano, r.missing)
		if e != nil {
			return fmt.Errorf("invalid inventory absence time: %w", e)
		}
		if r.count < 2 {
			r.count++
		}
		if r.count >= 2 && d.EndedAt.Sub(since) >= serviceAbsenceGrace {
			state = "missing"
		}
		if r.last == "" {
			r.last = r.discovered
		}
		if _, err = s.db().Exec(`INSERT INTO service_presence(service_id,state,last_seen_at,missing_since,absent_snapshots) VALUES(?,?,?,?,?)
   ON CONFLICT(service_id) DO UPDATE SET state=excluded.state,missing_since=excluded.missing_since,absent_snapshots=excluded.absent_snapshots`, r.id, state, r.last, r.missing, r.count); err != nil {
			return err
		}
	}
	_, err = s.db().Exec(`INSERT INTO discovery_checkpoints(agent_id,started_at,ended_at,complete,listener_count) VALUES(?,?,?,?,?)
 ON CONFLICT(agent_id) DO UPDATE SET started_at=excluded.started_at,ended_at=excluded.ended_at,complete=excluded.complete,listener_count=excluded.listener_count`, agentID, d.StartedAt.UTC().Format(dbTimeFormat), stamp, boolInt(d.ListenerCoverageComplete), len(d.ListenerTargets))
	return err
}

func IsListenerService(s *ServiceRow) bool { return strings.HasPrefix(s.Source, "listener") }
