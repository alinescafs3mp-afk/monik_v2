package storage

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/actions"
	"github.com/alinescafs3mp-afk/monik_v2/internal/idgen"
	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
	"github.com/alinescafs3mp-afk/monik_v2/internal/secure"
)

func (s *Store) LookupIdempotency(actor, key, reqHash string) (*protocol.Operation, error) {
	var id, action, status, params, created, summary, parent, storedHash string
	var rev int64
	var deadline sql.NullString
	err := s.db().QueryRow(`SELECT id,action,status,revision,params,created_at,deadline,summary,parent_id,request_hash
		FROM operations WHERE actor_id=? AND client_request_key=?`, actor, key).
		Scan(&id, &action, &status, &rev, &params, &created, &deadline, &summary, &parent, &storedHash)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if storedHash != reqHash {
		return nil, ErrIdempotencyConflict
	}
	op := loadOp(id, action, status, key, actor, params, created, deadline.String, summary, parent, rev)
	op.Targets, _ = s.Targets(id)
	if op.Action == "secret.replace" {
		op.Params = map[string]any{"redacted": true}
		for i := range op.Targets {
			op.Targets[i].Evidence = nil
		}
	}
	return op, nil
}

func loadOp(id, action, status, key, actor, params, created, deadline, summary, parent string, rev int64) *protocol.Operation {
	op := &protocol.Operation{
		ID: id, Action: action, Status: protocol.OperationStatus(status),
		Revision: rev, ClientRequestKey: key, Actor: actor, Summary: summary, ParentID: parent,
	}
	_ = json.Unmarshal([]byte(params), &op.Params)
	op.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	if deadline != "" {
		t, _ := time.Parse(time.RFC3339Nano, deadline)
		op.Deadline = &t
	}
	return op
}

func (s *Store) InsertOperation(op *protocol.Operation, reqHash string) error {
	if op.Action == "secret.replace" && op.Params != nil {
		delete(op.Params, "value")
		op.Params["redacted"] = true
	}
	params, _ := json.Marshal(op.Params)
	var dl any
	if op.Deadline != nil {
		dl = op.Deadline.UTC().Format(dbTimeFormat)
	}
	return s.WithTx(func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO operations(id,action,status,revision,client_request_key,actor_id,params,created_at,deadline,summary,parent_id,request_hash)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, op.ID, op.Action, string(op.Status), op.Revision, op.ClientRequestKey, op.Actor,
			string(params), op.CreatedAt.UTC().Format(dbTimeFormat), dl, op.Summary, op.ParentID, reqHash)
		if err != nil {
			return err
		}
		for _, t := range op.Targets {
			ev, _ := json.Marshal(t.Evidence)
			jobID := idgen.New()
			_, err = tx.Exec(`INSERT INTO operation_targets(operation_id,agent_id,status,stage,message,error_code,retryable,evidence,job_id,updated_at)
				VALUES(?,?,?,?,?,?,?,?,?,?)`, op.ID, t.AgentID, string(t.Status), t.Stage, t.Message, t.ErrorCode, boolInt(t.Retryable), string(ev), jobID, s.now().UTC().Format(dbTimeFormat))
			if err != nil {
				return err
			}
			def, _ := actions.Lookup(op.Action)
			if def.Scope == actions.ScopeAgents && (t.Status == protocol.TargetQueued || t.Status == protocol.TargetWaitingOffline) {
				env := protocol.JobEnvelope{
					JobID: jobID, OperationID: op.ID, IdempotencyKey: op.ClientRequestKey,
					Action: op.Action, SchemaVersion: protocol.SchemaVersion, ActorID: op.Actor,
					Params: op.Params, CreatedAt: op.CreatedAt, NotBefore: op.CreatedAt,
					Risk: "config",
				}
				if op.Deadline != nil {
					env.Deadline = *op.Deadline
				} else {
					env.Deadline = op.CreatedAt.Add(protocol.OneShotExpiry)
				}
				b, _ := json.Marshal(env)
				_, err = tx.Exec(`INSERT INTO agent_jobs(job_id,operation_id,agent_id,action,envelope,status,created_at,deadline)
					VALUES(?,?,?,?,?,?,?,?)`, jobID, op.ID, t.AgentID, op.Action, string(b), string(t.Status),
					op.CreatedAt.UTC().Format(dbTimeFormat), env.Deadline.UTC().Format(dbTimeFormat))
				if err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func (s *Store) Operation(id string) (*protocol.Operation, error) {
	var action, status, key, actor, params, created, summary, parent string
	var rev int64
	var deadline sql.NullString
	err := s.db().QueryRow(`SELECT action,status,revision,client_request_key,actor_id,params,created_at,deadline,summary,parent_id FROM operations WHERE id=?`, id).
		Scan(&action, &status, &rev, &key, &actor, &params, &created, &deadline, &summary, &parent)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	op := loadOp(id, action, status, key, actor, params, created, deadline.String, summary, parent, rev)
	op.Targets, _ = s.Targets(id)
	if op.Action == "secret.replace" {
		op.Params = map[string]any{"redacted": true}
		for i := range op.Targets {
			op.Targets[i].Evidence = nil
		}
	}
	return op, nil
}

func (s *Store) Operations(limit int) ([]*protocol.Operation, error) {
	if err := s.expireAndBlockJobs(); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db().Query(`SELECT id,action,status,revision,client_request_key,actor_id,params,created_at,deadline,summary,parent_id FROM operations ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*protocol.Operation
	for rows.Next() {
		var id, action, status, key, actor, params, created, summary, parent string
		var rev int64
		var deadline sql.NullString
		if err := rows.Scan(&id, &action, &status, &rev, &key, &actor, &params, &created, &deadline, &summary, &parent); err != nil {
			return nil, err
		}
		op := loadOp(id, action, status, key, actor, params, created, deadline.String, summary, parent, rev)
		op.Targets, _ = s.Targets(id)
		if op.Action == "secret.replace" {
			op.Params = map[string]any{"redacted": true}
			for i := range op.Targets {
				op.Targets[i].Evidence = nil
			}
		}
		out = append(out, op)
	}
	return out, rows.Err()
}

func (s *Store) Targets(opID string) ([]protocol.TargetResult, error) {
	rows, err := s.db().Query(`SELECT agent_id,status,stage,message,error_code,retryable,evidence FROM operation_targets WHERE operation_id=?`, opID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []protocol.TargetResult
	for rows.Next() {
		var t protocol.TargetResult
		var retry int
		var ev string
		if err := rows.Scan(&t.AgentID, &t.Status, &t.Stage, &t.Message, &t.ErrorCode, &retry, &ev); err != nil {
			return nil, err
		}
		t.Retryable = retry == 1
		_ = json.Unmarshal([]byte(ev), &t.Evidence)
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) UpdateTarget(opID, agentID string, st protocol.TargetStatus, stage, msg, code string, retry bool, evidence map[string]any) error {
	ev, _ := json.Marshal(evidence)
	_, err := s.db().Exec(`UPDATE operation_targets SET status=?, stage=?, message=?, error_code=?, retryable=?, evidence=?, updated_at=?
		WHERE operation_id=? AND agent_id=?`, string(st), stage, msg, code, boolInt(retry), string(ev), s.now().UTC().Format(dbTimeFormat), opID, agentID)
	if err != nil {
		return err
	}
	return s.refreshOperation(opID)
}

func (s *Store) refreshOperation(opID string) error {
	rows, err := s.db().Query(`SELECT status FROM operation_targets WHERE operation_id=?`, opID)
	if err != nil {
		return err
	}
	defer rows.Close()
	var statuses []string
	for rows.Next() {
		var st string
		_ = rows.Scan(&st)
		statuses = append(statuses, st)
	}
	agg := protocol.OpCompleted
	if len(statuses) == 0 {
		agg = protocol.OpCompleted
	} else {
		running, fail, wait, attn, cancelled := 0, 0, 0, 0, 0
		for _, st := range statuses {
			switch protocol.TargetStatus(st) {
			case protocol.TargetQueued, protocol.TargetAccepted, protocol.TargetRunning, protocol.TargetAwaitingConfirmation:
				running++
			case protocol.TargetWaitingOffline:
				wait++
			case protocol.TargetFailed, protocol.TargetRejected, protocol.TargetExpired, protocol.TargetUnknownResult, protocol.TargetRolledBack:
				fail++
			case protocol.TargetUnsupported:
				attn++
			case protocol.TargetCancelledBeforeExec:
				cancelled++
			}
		}
		switch {
		case running > 0:
			agg = protocol.OpRunning
		case wait > 0:
			agg = protocol.OpAttentionRequired
		case fail > 0 && running == 0:
			agg = protocol.OpCompletedWithErrs
		case cancelled == len(statuses):
			agg = protocol.OpCancelled
		case attn > 0 || cancelled > 0:
			agg = protocol.OpAttentionRequired
		default:
			agg = protocol.OpCompleted
		}
	}
	_, err = s.db().Exec(`UPDATE operations SET status=?, revision=revision+1 WHERE id=?`, string(agg), opID)
	_ = s.AppendEvent("operation", "operation", opID, 0, map[string]any{"status": agg})
	return err
}

func (s *Store) PendingJobs(agentID string, limit int) ([]protocol.JobEnvelope, error) {
	if err := s.expireAndBlockJobs(); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 8
	}
	now := s.now().UTC().Format(dbTimeFormat)
	rows, err := s.db().Query(`SELECT envelope FROM agent_jobs WHERE agent_id=? AND status IN ('queued','waiting_offline','delivered') AND deadline>? ORDER BY created_at LIMIT ?`,
		agentID, now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []protocol.JobEnvelope
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var env protocol.JobEnvelope
		_ = json.Unmarshal([]byte(raw), &env)
		out = append(out, env)
	}
	return out, rows.Err()
}

func (s *Store) MarkJobDelivered(jobID string) error {
	_, err := s.db().Exec(`UPDATE agent_jobs SET status='delivered', delivered_at=? WHERE job_id=? AND status IN ('queued','waiting_offline')`,
		s.now().UTC().Format(dbTimeFormat), jobID)
	return err
}

func (s *Store) ApplyReceipt(agentID string, rec protocol.JobReceipt) error {
	switch rec.Status {
	case protocol.TargetAccepted, protocol.TargetRunning, protocol.TargetAwaitingConfirmation, protocol.TargetSucceeded,
		protocol.TargetFailed, protocol.TargetRejected, protocol.TargetUnsupported, protocol.TargetExpired,
		protocol.TargetUnknownResult, protocol.TargetRolledBack: // Only worker-reported states.
	default:
		return fmt.Errorf("invalid receipt status")
	}
	b, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	changed := false
	err = s.WithTx(func(tx *sql.Tx) error {
		var opID, status, previous, action, created, envelope string
		if err := tx.QueryRow(`SELECT operation_id,status,COALESCE(result,''),action,created_at,envelope FROM agent_jobs WHERE job_id=? AND agent_id=?`, rec.JobID, agentID).Scan(&opID, &status, &previous, &action, &created, &envelope); err != nil {
			return ErrNotFound
		}
		if rec.OperationID != "" && rec.OperationID != opID {
			return ErrConflict
		}
		if previous == string(b) {
			return nil
		}
		// Terminal evidence cannot be overwritten by delayed delivery or replay.
		switch protocol.TargetStatus(status) {
		case protocol.TargetSucceeded, protocol.TargetFailed, protocol.TargetRejected, protocol.TargetUnsupported,
			protocol.TargetExpired, protocol.TargetCancelledBeforeExec, protocol.TargetRolledBack:
			return ErrConflict
		}
		if rec.Status == protocol.TargetSucceeded {
			var job protocol.JobEnvelope
			if err := json.Unmarshal([]byte(envelope), &job); err != nil {
				return err
			}
			proven := false
			switch action {
			case "agent.collect_now", "agent.discover_now":
				session, _ := rec.Evidence["session_id"].(string)
				var observed, received string
				var host, discovery int
				err := tx.QueryRow(`SELECT observed_at,received_at,has_host,has_discovery FROM ingest_receipts WHERE agent_id=? AND session_id=? AND seq=?`, agentID, session, rec.Evidence["sequence"]).Scan(&observed, &received, &host, &discovery)
				if err == nil && rec.AcceptedAt != nil {
					at, e := time.Parse(time.RFC3339Nano, observed)
					proven = e == nil && !at.Before(*rec.AcceptedAt) && received >= created && (action == "agent.collect_now" && host == 1 || action == "agent.discover_now" && discovery == 1)
				}
			case "profile.apply", "check.apply", "service.pause", "service.ignore":
				var expected string
				if err := tx.QueryRow(`SELECT evidence FROM operation_targets WHERE operation_id=? AND agent_id=?`, opID, agentID).Scan(&expected); err == nil {
					var wanted map[string]any
					if json.Unmarshal([]byte(expected), &wanted) == nil {
						var rev int64
						var hash string
						err := tx.QueryRow(`SELECT applied_revision,applied_hash FROM agents WHERE id=?`, agentID).Scan(&rev, &hash)
						proven = err == nil && fmt.Sprint(wanted["revision"]) == fmt.Sprint(rev) && wanted["hash"] == hash && fmt.Sprint(rec.Evidence["revision"]) == fmt.Sprint(rev) && rec.Evidence["hash"] == hash
					}
				}
			case "agent.diagnostics":
				proven = rec.Stage == "diagnostics" && rec.Evidence["os"] != nil
			case "secret.replace":
				id, _ := job.Params["secret_id"].(string)
				var version int
				err := tx.QueryRow(`SELECT version FROM check_secrets WHERE id=? AND agent_id=?`, id, agentID).Scan(&version)
				proven = err == nil && id != "" && rec.Evidence["secret_id"] == id && fmt.Sprint(rec.Evidence["version"]) == fmt.Sprint(version) && rec.Evidence["value"] == nil
			case "check.trial":
				proven = rec.Stage == "redacted_trial_result" && rec.Evidence["trial"] == true && rec.Evidence["vantage"] == "agent/local" && rec.Evidence["quality"] != nil && rec.Evidence["transport"] != nil
			case "credential.rotate":
				var current string
				err := tx.QueryRow(`SELECT credential_hash FROM agents WHERE id=?`, agentID).Scan(&current)
				proven = err == nil && current != "" && rec.Evidence["verifier_sha256"] == current && rec.Stage == "new_credential_authentication_receipt"
			case "trust.stage", "trust.retire":
				fingerprint, _ := job.Params["fingerprint"].(string)
				proven = fingerprint != "" && rec.Evidence["fingerprint"] == fingerprint && rec.Evidence["roots"] != nil && (action == "trust.stage" && rec.Stage == "persisted_scoped_trust" || action == "trust.retire" && rec.Stage == "acknowledged_trust_removal")
			case "agent.restart":
				session, _ := rec.Evidence["session_id"].(string)
				var current string
				_ = tx.QueryRow(`SELECT session_id FROM agents WHERE id=?`, agentID).Scan(&current)
				previousSession, _ := job.Params["previous_session"].(string)
				proven = session != "" && session == current && previousSession != "" && session != previousSession
			case "update.rollout", "update.rollback":
				digest, _ := rec.Evidence["worker_digest"].(string)
				session, _ := rec.Evidence["session_id"].(string)
				var gotDigest, gotSession string
				_ = tx.QueryRow(`SELECT IFNULL(worker_digest,''), IFNULL(session_id,'') FROM agents WHERE id=?`, agentID).Scan(&gotDigest, &gotSession)
				expectedDigest, _ := job.Params["sha256"].(string)
				previousSession, _ := job.Params["previous_session"].(string)
				if action == "update.rollback" && expectedDigest == "" && rec.Evidence["rollback_tx_id"] != nil && rec.Evidence["previous_good_digest"] == digest {
					expectedDigest = digest
				}
				proven = digest != "" && digest == gotDigest && expectedDigest != "" && digest == expectedDigest && session != "" && session == gotSession && previousSession != "" && session != previousSession
			case "rebind.prepare":
				proven = rec.Stage == "rebind.prepared" && job.Params["plan_id"] != nil && rec.Evidence["plan_id"] == job.Params["plan_id"] && fmt.Sprint(rec.Evidence["generation"]) == fmt.Sprint(job.Params["generation"])
			case "rebind.arm":
				proven = rec.Stage == "rebind.armed" && job.Params["plan_id"] != nil && rec.Evidence["plan_id"] == job.Params["plan_id"]
			case "rebind.activate":
				gen := fmt.Sprint(rec.Evidence["generation"])
				var got int64
				_ = tx.QueryRow(`SELECT endpoint_generation FROM agents WHERE id=?`, agentID).Scan(&got)
				proven = rec.Stage == "rebind.confirmed" && job.Params["plan_id"] != nil && rec.Evidence["plan_id"] == job.Params["plan_id"] && gen == fmt.Sprint(job.Params["generation"]) && fmt.Sprint(got) == gen && rec.Evidence["candidate_url"] == job.Params["candidate_url"]
			case "rebind.retire":
				proven = rec.Stage == "rebind.retired" && job.Params["plan_id"] != nil && rec.Evidence["plan_id"] == job.Params["plan_id"]
			}
			if !proven {
				rec.Status = protocol.TargetRejected
				rec.Stage = "unverified_result"
				rec.ErrorCode = "missing_evidence"
				rec.Message = "Agent reported success without matching committed observation/configuration evidence"
				// Keep the received receipt bytes as the deduplication token;
				// the target stores our independently validated outcome.
			}
		}
		if _, err := tx.Exec(`UPDATE agent_jobs SET status=?,result=? WHERE job_id=? AND agent_id=?`, rec.Status, string(b), rec.JobID, agentID); err != nil {
			return err
		}
		ev, err := json.Marshal(rec.Evidence)
		if err != nil {
			return err
		}
		_, err = tx.Exec(`UPDATE operation_targets SET status=?,stage=?,message=?,error_code=?,retryable=?,evidence=?,updated_at=? WHERE operation_id=? AND agent_id=?`, rec.Status, rec.Stage, rec.Message, rec.ErrorCode, boolInt(rec.Retryable), string(ev), s.now().UTC().Format(dbTimeFormat), opID, agentID)
		if err == nil && strings.HasPrefix(action, "rebind.") && rec.Status == protocol.TargetSucceeded {
			var job protocol.JobEnvelope
			if json.Unmarshal([]byte(envelope), &job) == nil {
				phase := map[string]string{"rebind.prepare": "prepared", "rebind.arm": "armed", "rebind.activate": "confirmed", "rebind.retire": "retired"}[action]
				if phase != "" {
					_, err = tx.Exec(`UPDATE migration_targets SET state=?,reason=?,updated_at=? WHERE plan_id=? AND agent_id=?`, phase, rec.Message, s.now().UTC().Format(dbTimeFormat), job.Params["plan_id"], agentID)
				}
			}
		}
		changed = err == nil
		return err
	})
	if err != nil {
		return err
	}
	if !changed {
		return nil
	}
	var opID string
	if err := s.db().QueryRow(`SELECT operation_id FROM agent_jobs WHERE job_id=? AND agent_id=?`, rec.JobID, agentID).Scan(&opID); err != nil {
		return err
	}
	return s.refreshOperation(opID)
}

func (s *Store) CancelPending(opID string) error {
	if _, err := s.Operation(opID); err != nil {
		return err
	}
	err := s.WithTx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`UPDATE operation_targets SET status='cancelled_before_execution',stage='cancelled',message='cancelled before dispatch',updated_at=?
  WHERE operation_id=? AND status IN ('queued','waiting_offline') AND NOT EXISTS(SELECT 1 FROM agent_jobs j WHERE j.operation_id=operation_targets.operation_id AND j.agent_id=operation_targets.agent_id AND j.status NOT IN ('queued','waiting_offline'))`, s.now().UTC().Format(dbTimeFormat), opID); err != nil {
			return err
		}
		_, err := tx.Exec(`UPDATE agent_jobs SET status='cancelled_before_execution' WHERE operation_id=? AND status IN ('queued','waiting_offline')`, opID)
		return err
	})
	if err != nil {
		return err
	}
	return s.refreshOperation(opID)
}

func (s *Store) InsertIncident(inc map[string]any) error {
	_, err := s.db().Exec(`INSERT INTO incidents(id,entity_type,entity_id,metric,severity,status,opened_at,reason)
		VALUES(?,?,?,?,?,?,?,?)`, inc["id"], inc["entity_type"], inc["entity_id"], inc["metric"], inc["severity"], inc["status"],
		s.now().UTC().Format(dbTimeFormat), inc["reason"])
	return err
}

func (s *Store) OpenIncidents() ([]map[string]any, error) {
	rows, err := s.db().Query(`SELECT id,entity_type,entity_id,metric,severity,status,opened_at,reason,acked_at FROM incidents WHERE status IN ('pending','confirmed') ORDER BY opened_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var id, et, eid, metric, sev, st, opened, reason string
		var acked sql.NullString
		if err := rows.Scan(&id, &et, &eid, &metric, &sev, &st, &opened, &reason, &acked); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"id": id, "entity_type": et, "entity_id": eid, "metric": metric, "severity": sev, "status": st, "opened_at": opened, "reason": reason, "acked_at": acked.String})
	}
	return out, rows.Err()
}

func (s *Store) AckIncident(id, actor string) error {
	result, err := s.db().Exec(`UPDATE incidents SET acked_at=?, acked_by=? WHERE id=?`, s.now().UTC().Format(dbTimeFormat), actor, id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) FindOpenIncident(entityID, metric string) (string, error) {
	var id string
	err := s.db().QueryRow(`SELECT id FROM incidents WHERE entity_id=? AND IFNULL(metric,'')=IFNULL(?, '') AND status IN ('pending','confirmed') ORDER BY opened_at DESC LIMIT 1`, entityID, metric).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return id, err
}

func (s *Store) ConfirmIncident(id string) error {
	_, err := s.db().Exec(`UPDATE incidents SET status='confirmed', confirmed_at=? WHERE id=? AND status='pending'`, s.now().UTC().Format(dbTimeFormat), id)
	return err
}

func (s *Store) ResolveIncident(id string) error {
	_, err := s.db().Exec(`UPDATE incidents SET status='resolved', resolved_at=? WHERE id=? AND status IN ('pending','confirmed')`, s.now().UTC().Format(dbTimeFormat), id)
	return err
}

func (s *Store) InsertRelease(id, version, digest, notes, metadata, platforms string, trust bool) error {
	_, err := s.db().Exec(`INSERT INTO releases(id,version,digest,notes,metadata,imported_at,trust_ok,platforms) VALUES(?,?,?,?,?,?,?,?)`,
		id, version, digest, notes, metadata, s.now().UTC().Format(dbTimeFormat), boolInt(trust), platforms)
	return err
}

func (s *Store) ReleaseByDigest(digest string) (string, error) {
	var id string
	err := s.db().QueryRow(`SELECT id FROM releases WHERE digest=?`, digest).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return id, err
}

func (s *Store) Releases() ([]map[string]any, error) {
	rows, err := s.db().Query(`SELECT id,version,digest,notes,imported_at,trust_ok,platforms FROM releases ORDER BY imported_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var id, ver, dig, notes, at, plat string
		var trust int
		if err := rows.Scan(&id, &ver, &dig, &notes, &at, &trust, &plat); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"id": id, "version": ver, "digest": dig, "notes": notes, "imported_at": at, "trust_ok": trust == 1, "platforms": json.RawMessage(plat)})
	}
	return out, rows.Err()
}

func (s *Store) InsertArtifact(relID, osn, arch, name, sha string, length int64, path string) error {
	_, err := s.db().Exec(`INSERT INTO release_artifacts(release_id,os,arch,name,sha256,length,path) VALUES(?,?,?,?,?,?,?)`,
		relID, osn, arch, name, sha, length, path)
	return err
}

func (s *Store) Artifact(osn, arch, name string) (path, sha string, length int64, err error) {
	err = s.db().QueryRow(`SELECT path,sha256,length FROM release_artifacts WHERE os=? AND arch=? AND name=? ORDER BY rowid DESC LIMIT 1`, osn, arch, name).
		Scan(&path, &sha, &length)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return
}

func (s *Store) SaveSecret(id, name, header, agentID, checkID string, version int, nonce, ct []byte) error {
	_, err := s.db().Exec(`INSERT INTO check_secrets(id,name,header_name,version,ciphertext,nonce,agent_id,check_id,created_at)
		VALUES(?,?,?,?,?,?,?,?,?)`, id, name, header, version, ct, nonce, agentID, checkID, s.now().UTC().Format(dbTimeFormat))
	return err
}

func (s *Store) SecretMeta(id string) (name, header, agentID, checkID string, version int, err error) {
	err = s.db().QueryRow(`SELECT name,header_name,agent_id,IFNULL(check_id,''),version FROM check_secrets WHERE id=?`, id).
		Scan(&name, &header, &agentID, &checkID, &version)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return
}

func (s *Store) SecretBlob(id string) (nonce, ct []byte, err error) {
	err = s.db().QueryRow(`SELECT nonce,ciphertext FROM check_secrets WHERE id=?`, id).Scan(&nonce, &ct)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return
}

func (s *Store) ListSecrets() ([]map[string]any, error) {
	rows, err := s.db().Query(`SELECT id,name,header_name,version,agent_id,IFNULL(check_id,''),created_at FROM check_secrets ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var id, name, header, agentID, checkID, created string
		var version int
		if err := rows.Scan(&id, &name, &header, &version, &agentID, &checkID, &created); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"id": id, "name": name, "header": header, "version": version, "agent_id": agentID, "check_id": checkID, "created_at": created})
	}
	return out, rows.Err()
}

func RequestHash(action string, targets []string, params []byte) string {
	ids := append([]string{}, targets...)
	sort.Strings(ids)
	var value any
	if len(params) == 0 {
		params = []byte("{}")
	}
	if err := json.Unmarshal(params, &value); err != nil {
		return secure.SHA256Bytes(params)
	}
	canonical, _ := json.Marshal(struct {
		Action  string
		Targets []string
		Params  any
	}{action, ids, value})
	return secure.SHA256Bytes(canonical)
}

func join(s []string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// A read-only recovery path scoped to the authenticated actor. It never creates
// or retries an operation and needs no copy of secret request parameters.
func (s *Store) OperationByRequestKey(actor, key string) (*protocol.Operation, error) {
	var id string
	if err := s.db().QueryRow(`SELECT id FROM operations WHERE actor_id=? AND client_request_key=?`, actor, key).Scan(&id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return s.Operation(id)
}
