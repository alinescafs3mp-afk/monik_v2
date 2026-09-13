package storage

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/idgen"
	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
	"github.com/alinescafs3mp-afk/monik_v2/internal/secure"
)

func (s *Store) LookupIdempotency(actor, key, reqHash string) (*protocol.Operation, error) {
	var id, action, status, params, created, summary, parent, storedHash string
	var rev int64
	var deadline sql.NullString
	err := s.DB.QueryRow(`SELECT id,action,status,revision,params,created_at,deadline,summary,parent_id,request_hash
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
	params, _ := json.Marshal(op.Params)
	var dl any
	if op.Deadline != nil {
		dl = op.Deadline.UTC().Format(time.RFC3339Nano)
	}
	return s.WithTx(func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO operations(id,action,status,revision,client_request_key,actor_id,params,created_at,deadline,summary,parent_id,request_hash)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, op.ID, op.Action, string(op.Status), op.Revision, op.ClientRequestKey, op.Actor,
			string(params), op.CreatedAt.UTC().Format(time.RFC3339Nano), dl, op.Summary, op.ParentID, reqHash)
		if err != nil {
			return err
		}
		for _, t := range op.Targets {
			ev, _ := json.Marshal(t.Evidence)
			jobID := idgen.New()
			_, err = tx.Exec(`INSERT INTO operation_targets(operation_id,agent_id,status,stage,message,error_code,retryable,evidence,job_id,updated_at)
				VALUES(?,?,?,?,?,?,?,?,?,?)`, op.ID, t.AgentID, string(t.Status), t.Stage, t.Message, t.ErrorCode, boolInt(t.Retryable), string(ev), jobID, s.now().Format(time.RFC3339Nano))
			if err != nil {
				return err
			}
			if t.Status == protocol.TargetQueued || t.Status == protocol.TargetWaitingOffline {
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
					op.CreatedAt.UTC().Format(time.RFC3339Nano), env.Deadline.UTC().Format(time.RFC3339Nano))
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
	err := s.DB.QueryRow(`SELECT action,status,revision,client_request_key,actor_id,params,created_at,deadline,summary,parent_id FROM operations WHERE id=?`, id).
		Scan(&action, &status, &rev, &key, &actor, &params, &created, &deadline, &summary, &parent)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	op := loadOp(id, action, status, key, actor, params, created, deadline.String, summary, parent, rev)
	op.Targets, _ = s.Targets(id)
	return op, nil
}

func (s *Store) Operations(limit int) ([]*protocol.Operation, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.DB.Query(`SELECT id,action,status,revision,client_request_key,actor_id,params,created_at,deadline,summary,parent_id FROM operations ORDER BY created_at DESC LIMIT ?`, limit)
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
		out = append(out, op)
	}
	return out, rows.Err()
}

func (s *Store) Targets(opID string) ([]protocol.TargetResult, error) {
	rows, err := s.DB.Query(`SELECT agent_id,status,stage,message,error_code,retryable,evidence FROM operation_targets WHERE operation_id=?`, opID)
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
	_, err := s.DB.Exec(`UPDATE operation_targets SET status=?, stage=?, message=?, error_code=?, retryable=?, evidence=?, updated_at=?
		WHERE operation_id=? AND agent_id=?`, string(st), stage, msg, code, boolInt(retry), string(ev), s.now().Format(time.RFC3339Nano), opID, agentID)
	if err != nil {
		return err
	}
	return s.refreshOperation(opID)
}

func (s *Store) refreshOperation(opID string) error {
	rows, err := s.DB.Query(`SELECT status FROM operation_targets WHERE operation_id=?`, opID)
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
		running, fail, wait, attn := 0, 0, 0, 0
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
			}
		}
		switch {
		case running > 0:
			agg = protocol.OpRunning
		case wait > 0 && fail == 0:
			agg = protocol.OpAttentionRequired
		case fail > 0 && running == 0:
			agg = protocol.OpCompletedWithErrs
		case attn > 0:
			agg = protocol.OpAttentionRequired
		default:
			agg = protocol.OpCompleted
		}
	}
	_, err = s.DB.Exec(`UPDATE operations SET status=?, revision=revision+1 WHERE id=?`, string(agg), opID)
	_ = s.AppendEvent("operation", "operation", opID, 0, map[string]any{"status": agg})
	return err
}

func (s *Store) PendingJobs(agentID string, limit int) ([]protocol.JobEnvelope, error) {
	if limit <= 0 {
		limit = 8
	}
	now := s.now().Format(time.RFC3339Nano)
	rows, err := s.DB.Query(`SELECT envelope FROM agent_jobs WHERE agent_id=? AND status IN ('queued','waiting_offline') AND deadline>? ORDER BY created_at LIMIT ?`,
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
	_, err := s.DB.Exec(`UPDATE agent_jobs SET status='delivered', delivered_at=? WHERE job_id=? AND status IN ('queued','waiting_offline')`,
		s.now().Format(time.RFC3339Nano), jobID)
	return err
}

func (s *Store) ApplyReceipt(agentID string, rec protocol.JobReceipt) error {
	b, _ := json.Marshal(rec)
	_, _ = s.DB.Exec(`UPDATE agent_jobs SET status=?, result=? WHERE job_id=?`, string(rec.Status), string(b), rec.JobID)
	var opID string
	_ = s.DB.QueryRow(`SELECT operation_id FROM agent_jobs WHERE job_id=?`, rec.JobID).Scan(&opID)
	if opID == "" {
		opID = rec.OperationID
	}
	if opID == "" {
		return nil
	}
	return s.UpdateTarget(opID, agentID, rec.Status, rec.Stage, rec.Message, rec.ErrorCode, rec.Retryable, rec.Evidence)
}

func (s *Store) CancelPending(opID string) error {
	now := s.now().Format(time.RFC3339Nano)
	_, _ = s.DB.Exec(`UPDATE agent_jobs SET status='cancelled_before_execution' WHERE operation_id=? AND status IN ('queued','waiting_offline')`, opID)
	_, _ = s.DB.Exec(`UPDATE operation_targets SET status='cancelled_before_execution', stage='cancelled', message='cancelled before execution', updated_at=?
		WHERE operation_id=? AND status IN ('queued','waiting_offline')`, now, opID)
	_, _ = s.DB.Exec(`UPDATE operations SET status='cancelled', revision=revision+1 WHERE id=?`, opID)
	return s.AppendEvent("operation", "operation", opID, 0, map[string]any{"status": "cancelled"})
}

func (s *Store) InsertIncident(inc map[string]any) error {
	_, err := s.DB.Exec(`INSERT INTO incidents(id,entity_type,entity_id,metric,severity,status,opened_at,reason)
		VALUES(?,?,?,?,?,?,?,?)`, inc["id"], inc["entity_type"], inc["entity_id"], inc["metric"], inc["severity"], inc["status"],
		s.now().Format(time.RFC3339Nano), inc["reason"])
	return err
}

func (s *Store) OpenIncidents() ([]map[string]any, error) {
	rows, err := s.DB.Query(`SELECT id,entity_type,entity_id,metric,severity,status,opened_at,reason,acked_at FROM incidents WHERE status IN ('pending','confirmed') ORDER BY opened_at DESC`)
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
	_, err := s.DB.Exec(`UPDATE incidents SET acked_at=?, acked_by=? WHERE id=?`, s.now().Format(time.RFC3339Nano), actor, id)
	return err
}

func (s *Store) FindOpenIncident(entityID, metric string) (string, error) {
	var id string
	err := s.DB.QueryRow(`SELECT id FROM incidents WHERE entity_id=? AND IFNULL(metric,'')=IFNULL(?, '') AND status IN ('pending','confirmed') ORDER BY opened_at DESC LIMIT 1`, entityID, metric).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return id, err
}

func (s *Store) ConfirmIncident(id string) error {
	_, err := s.DB.Exec(`UPDATE incidents SET status='confirmed', confirmed_at=? WHERE id=? AND status='pending'`, s.now().Format(time.RFC3339Nano), id)
	return err
}

func (s *Store) ResolveIncident(id string) error {
	_, err := s.DB.Exec(`UPDATE incidents SET status='resolved', resolved_at=? WHERE id=? AND status IN ('pending','confirmed')`, s.now().Format(time.RFC3339Nano), id)
	return err
}

func (s *Store) InsertRelease(id, version, digest, notes, metadata, platforms string, trust bool) error {
	_, err := s.DB.Exec(`INSERT INTO releases(id,version,digest,notes,metadata,imported_at,trust_ok,platforms) VALUES(?,?,?,?,?,?,?,?)`,
		id, version, digest, notes, metadata, s.now().Format(time.RFC3339Nano), boolInt(trust), platforms)
	return err
}

func (s *Store) ReleaseByDigest(digest string) (string, error) {
	var id string
	err := s.DB.QueryRow(`SELECT id FROM releases WHERE digest=?`, digest).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return id, err
}

func (s *Store) Releases() ([]map[string]any, error) {
	rows, err := s.DB.Query(`SELECT id,version,digest,notes,imported_at,trust_ok,platforms FROM releases ORDER BY imported_at DESC`)
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
	_, err := s.DB.Exec(`INSERT INTO release_artifacts(release_id,os,arch,name,sha256,length,path) VALUES(?,?,?,?,?,?,?)`,
		relID, osn, arch, name, sha, length, path)
	return err
}

func (s *Store) Artifact(osn, arch, name string) (path, sha string, length int64, err error) {
	err = s.DB.QueryRow(`SELECT path,sha256,length FROM release_artifacts WHERE os=? AND arch=? AND name=? ORDER BY rowid DESC LIMIT 1`, osn, arch, name).
		Scan(&path, &sha, &length)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return
}

func (s *Store) SaveSecret(id, name, header, agentID, checkID string, version int, nonce, ct []byte) error {
	_, err := s.DB.Exec(`INSERT INTO check_secrets(id,name,header_name,version,ciphertext,nonce,agent_id,check_id,created_at)
		VALUES(?,?,?,?,?,?,?,?,?)`, id, name, header, version, ct, nonce, agentID, checkID, s.now().Format(time.RFC3339Nano))
	return err
}

func (s *Store) SecretMeta(id string) (name, header, agentID, checkID string, version int, err error) {
	err = s.DB.QueryRow(`SELECT name,header_name,agent_id,IFNULL(check_id,''),version FROM check_secrets WHERE id=?`, id).
		Scan(&name, &header, &agentID, &checkID, &version)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return
}

func (s *Store) SecretBlob(id string) (nonce, ct []byte, err error) {
	err = s.DB.QueryRow(`SELECT nonce,ciphertext FROM check_secrets WHERE id=?`, id).Scan(&nonce, &ct)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return
}

func (s *Store) ListSecrets() ([]map[string]any, error) {
	rows, err := s.DB.Query(`SELECT id,name,header_name,version,agent_id,IFNULL(check_id,''),created_at FROM check_secrets ORDER BY created_at DESC`)
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
	return secure.SHA256Bytes(append(append([]byte(action+"|"), []byte(join(targets))...), params...))
}

func join(s []string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
