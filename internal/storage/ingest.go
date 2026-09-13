package storage

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/alinescafs3mp-afk/monik_v2/internal/idgen"
	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
	"github.com/alinescafs3mp-afk/monik_v2/internal/secure"
	"time"
)

type AcceptedReport struct {
	Duplicate bool
	Live      bool
	Endpoints []protocol.DiscoveredEndpoint
}

// AcceptReport commits telemetry and its deduplication receipt together. A replay
// can repair history, but cannot refresh live health, versions or configuration.
func (s *Store) AcceptReport(rep protocol.AgentReport) (AcceptedReport, error) {
	out := AcceptedReport{}
	if rep.SchemaVersion != protocol.SchemaVersion || rep.AgentID == "" || rep.SessionID == "" || rep.Sequence < 1 || rep.ObservedAt.IsZero() {
		return out, fmt.Errorf("invalid report identity/schema/time")
	}
	if len(rep.Checks) > 1000 || len(rep.JobReceipts) > 128 {
		return out, fmt.Errorf("report exceeds item budget")
	}
	if rep.Discovery != nil && len(rep.Discovery.Confirmed) > 256 {
		return out, fmt.Errorf("discovery exceeds item budget")
	}
	if rep.ObservedAt.Before(s.now().Add(-protocol.RawRetention)) || rep.ObservedAt.After(s.now().Add(5*time.Minute)) {
		return out, fmt.Errorf("observation outside accepted clock/retention window")
	}
	// IsLive and ReportedAt change when a buffered envelope is replayed.
	normalized := rep
	normalized.IsLive = false
	normalized.ReportedAt = time.Time{}
	normalized.JobReceipts = nil
	raw, err := json.Marshal(normalized)
	if err != nil {
		return out, err
	}
	hash := secure.SHA256Bytes(raw)
	err = s.WithTx(func(tx *sql.Tx) error {
		ts := &Store{DB: s.DB, Clock: s.Clock, executor: tx}
		ag, err := ts.Agent(rep.AgentID)
		if err != nil {
			return err
		}
		if ag.Revoked {
			return fmt.Errorf("agent revoked")
		}
		var prior string
		err = tx.QueryRow(`SELECT payload_hash FROM ingest_receipts WHERE agent_id=? AND session_id=? AND seq=?`, rep.AgentID, rep.SessionID, rep.Sequence).Scan(&prior)
		if err == nil {
			if prior != hash {
				return ErrConflict
			}
			out.Duplicate = true
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		// Validate ownership before any inserts. Never trust IDs supplied by a worker.
		for _, c := range rep.Checks {
			var owner string
			if err := tx.QueryRow(`SELECT agent_id FROM services WHERE id=?`, c.ServiceID).Scan(&owner); err != nil || owner != rep.AgentID {
				return fmt.Errorf("service does not belong to reporting agent")
			}
			if c.ObservedAt.IsZero() || c.ObservedAt.Before(s.now().Add(-protocol.RawRetention)) || c.ObservedAt.After(s.now().Add(5*time.Minute)) {
				return fmt.Errorf("invalid check observation time")
			}
			if c.Vantage != "agent/local" && c.Vantage != "agent" && c.Vantage != "local" {
				return fmt.Errorf("invalid agent vantage")
			}
		}
		for _, r := range rep.JobReceipts {
			var opID string
			if err := tx.QueryRow(`SELECT operation_id FROM agent_jobs WHERE job_id=? AND agent_id=?`, r.JobID, rep.AgentID).Scan(&opID); err != nil {
				return fmt.Errorf("job does not belong to reporting agent")
			}
			if r.OperationID != "" && r.OperationID != opID {
				return ErrConflict
			}
		}
		if rep.Host != nil {
			if err := ts.InsertHostSample(rep.AgentID, rep.Sequence, rep.SessionID, rep.ObservedAt, rep.Host); err != nil {
				return err
			}
		}
		for _, c := range rep.Checks {
			if err := ts.InsertCheckObs(c, rep.AgentID); err != nil {
				return err
			}
		}
		out.Live = rep.IsLive && (ag.SessionID != rep.SessionID || rep.Sequence > ag.LastSeq)
		if out.Live && ag.SessionID != "" {
			var previousObserved string
			if err := tx.QueryRow(`SELECT observed_at FROM ingest_receipts WHERE agent_id=? AND session_id=? AND seq=?`, rep.AgentID, ag.SessionID, ag.LastSeq).Scan(&previousObserved); err == nil {
				if t, err := time.Parse(time.RFC3339Nano, previousObserved); err == nil && rep.ObservedAt.Before(t) {
					out.Live = false
				}
			} else if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
		}
		if out.Live {
			if rep.Discovery != nil {
				for _, ep := range rep.Discovery.Confirmed {
					// The logical endpoint retains its server ID across rediscovery/PID changes.
					var existing string
					err := tx.QueryRow(`SELECT id FROM services WHERE agent_id=? AND dial_target=? AND host_header=?`, rep.AgentID, ep.DialTarget, ep.HostHeader).Scan(&existing)
					if err == nil {
						ep.ServiceID = existing
					} else if errors.Is(err, sql.ErrNoRows) {
						ep.ServiceID = idgen.New()
					} else {
						return err
					}
					if err := ts.UpsertService(ep, rep.AgentID); err != nil {
						return err
					}
					out.Endpoints = append(out.Endpoints, ep)
				}
			}
			versions := map[string]string{"worker": rep.WorkerVersion, "worker_digest": rep.WorkerDigest, "service_host": rep.ServiceHostVersion, "service_host_digest": rep.ServiceHostDigest}
			if rep.ManagedReady {
				versions["managed"] = "1"
			}
			if err := ts.TouchAgent(rep.AgentID, rep.SessionID, rep.Sequence, true, rep.Host, rep.Capabilities, versions); err != nil {
				return err
			}
			if rep.EndpointGeneration > 0 {
				if err := ts.SetEndpointGeneration(rep.AgentID, rep.EndpointGeneration); err != nil {
					return err
				}
			}
			// A mismatched config receipt is visible as desired/applied lag, not false convergence.
			if rep.ConfigRevision > 0 && rep.ConfigHash != "" {
				if err := ts.SetApplied(rep.AgentID, rep.ConfigRevision, rep.ConfigHash); err != nil && !errors.Is(err, ErrConflict) {
					return err
				}
			}
		}
		_, err = tx.Exec(`INSERT INTO ingest_receipts(agent_id,session_id,seq,observed_at,received_at,payload_hash,has_host,has_discovery) VALUES(?,?,?,?,?,?,?,?)`, rep.AgentID, rep.SessionID, rep.Sequence, rep.ObservedAt.UTC().Format(dbTimeFormat), s.now().UTC().Format(dbTimeFormat), hash, boolInt(rep.Host != nil), boolInt(rep.Discovery != nil))
		return err
	})
	return out, err
}
