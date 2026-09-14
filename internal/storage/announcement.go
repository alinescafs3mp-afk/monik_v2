package storage

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
	"github.com/alinescafs3mp-afk/monik_v2/internal/secure"
	"time"
)

type AgentCandidate struct {
	ID          string `json:"id"`
	Fingerprint string `json:"fingerprint"`
	Hostname    string `json:"hostname"`
	DisplayName string `json:"display_name"`
	OS          string `json:"os"`
	Arch        string `json:"arch"`
	Version     string `json:"version"`
	State       string `json:"state"`
	CreatedAt   string `json:"created_at"`
	LastSeenAt  string `json:"last_seen_at"`
	ExpiresAt   string `json:"expires_at"`
}

// All unauthenticated registration work is bounded: 100 live entries, 100 per
// source IP, fixed seven-day TTL. Polls do not extend retention or create events.
func (s *Store) Announce(req protocol.Announcement, ip string) (state string, created bool, err error) {
	verifier := secure.HashToken(req.Credential)
	err = s.WithTx(func(tx *sql.Tx) error {
		now := s.now().Format(dbTimeFormat)
		if _, e := tx.Exec(`DELETE FROM agent_candidates WHERE expires_at<=?`, now); e != nil {
			return e
		}
		var hash string
		var revoked int
		e := tx.QueryRow(`SELECT credential_hash,revoked FROM agents WHERE id=?`, req.AgentID).Scan(&hash, &revoked)
		if e == nil {
			if revoked != 0 || hash != verifier {
				return ErrConflict
			}
			state = "approved"
			_, e = tx.Exec(`DELETE FROM agent_candidates WHERE id=? AND state='approved'`, req.AgentID)
			return e
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		e = tx.QueryRow(`SELECT credential_hash,state FROM agent_candidates WHERE id=?`, req.AgentID).Scan(&hash, &state)
		if e == nil {
			if hash != verifier {
				return ErrConflict
			}
			if state == "approved" {
				return fmt.Errorf("registration requires local recovery")
			}
			if state == "pending" {
				_, e = tx.Exec(`UPDATE agent_candidates SET last_seen_at=? WHERE id=?`, now, req.AgentID)
			}
			return e
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		var count, peer int
		if e = tx.QueryRow(`SELECT COUNT(*),COALESCE(SUM(CASE WHEN source_ip=? THEN 1 ELSE 0 END),0) FROM agent_candidates`, ip).Scan(&count, &peer); e != nil {
			return e
		}
		if count >= 100 || peer >= 100 {
			return fmt.Errorf("pending registration capacity reached")
		}
		_, e = tx.Exec(`INSERT INTO agent_candidates(id,credential_hash,fingerprint,hostname,display_name,os,arch,worker_version,state,source_ip,created_at,last_seen_at,expires_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, req.AgentID, verifier, protocol.RegistrationFingerprint(req.AgentID, req.Credential), req.Hostname, req.DisplayName, req.OS, req.Arch, req.Version, "pending", ip, now, now, s.now().Add(7*24*time.Hour).Format(dbTimeFormat))
		state = "pending"
		created = e == nil
		return e
	})
	return
}
func (s *Store) AgentCandidates() ([]AgentCandidate, error) {
	rows, err := s.db().Query(`SELECT id,fingerprint,hostname,display_name,os,arch,worker_version,state,created_at,last_seen_at,expires_at FROM agent_candidates WHERE state='pending' AND expires_at>? ORDER BY created_at,id LIMIT 100`, s.now().Format(dbTimeFormat))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]AgentCandidate, 0)
	for rows.Next() {
		var c AgentCandidate
		if err = rows.Scan(&c.ID, &c.Fingerprint, &c.Hostname, &c.DisplayName, &c.OS, &c.Arch, &c.Version, &c.State, &c.CreatedAt, &c.LastSeenAt, &c.ExpiresAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Decision and creation of the enrolled identity/config journal are atomic.
// Identical retries are harmless; opposite decisions and stale fingerprints fail.
func (s *Store) DecideCandidate(id, fp, actor string, approve bool) error {
	return s.WithTx(func(tx *sql.Tx) error {
		var c AgentCandidate
		var hash string
		err := tx.QueryRow(`SELECT credential_hash,fingerprint,hostname,display_name,os,arch,state,expires_at,last_seen_at FROM agent_candidates WHERE id=?`, id).Scan(&hash, &c.Fingerprint, &c.Hostname, &c.DisplayName, &c.OS, &c.Arch, &c.State, &c.ExpiresAt, &c.LastSeenAt)
		if err != nil {
			return err
		}
		if c.Fingerprint != fp {
			return ErrConflict
		}
		want := "rejected"
		if approve {
			want = "approved"
		}
		if c.State == want {
			return nil
		}
		if c.State != "pending" || c.ExpiresAt <= s.now().Format(dbTimeFormat) {
			return ErrConflict
		}
		if approve {
			// Stale self-reported metadata is not enough to approve an absent device.
			if c.LastSeenAt < s.now().Add(-2*time.Minute).Format(dbTimeFormat) {
				return fmt.Errorf("candidate is offline; wait for a fresh announcement")
			}
			cfg := protocol.DefaultAgentConfig()
			cfg.DisplayName = c.DisplayName
			body, _ := json.Marshal(cfg)
			row := &AgentRow{ID: id, Hostname: c.Hostname, DisplayName: c.DisplayName, OS: c.OS, Arch: c.Arch, DesiredRevision: 1, DesiredHash: secure.SHA256Bytes(body), DesiredConfig: string(body)}
			ts := &Store{DB: s.DB, Clock: s.Clock, executor: tx}
			if err = ts.InsertAgent(row, hash); err != nil {
				return err
			}
			if _, err = tx.Exec(`INSERT INTO config_revisions(agent_id,revision,hash,body,created_at,actor) VALUES(?,?,?,?,?,?)`, id, 1, row.DesiredHash, row.DesiredConfig, s.now().Format(dbTimeFormat), actor); err != nil {
				return err
			}
		}
		_, err = tx.Exec(`UPDATE agent_candidates SET state=?,decided_by=?,decided_at=? WHERE id=?`, want, actor, s.now().Format(dbTimeFormat), id)
		return err
	})
}
