package storage

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
)

// Attention is orthogonal to execution. Acknowledging a notice never changes
// the job status, target evidence or incident health.
func targetNeedsAttention(status protocol.TargetStatus) bool {
	switch status {
	case protocol.TargetWaitingOffline, protocol.TargetFailed, protocol.TargetRejected,
		protocol.TargetExpired, protocol.TargetUnknownResult, protocol.TargetRolledBack, protocol.TargetUnsupported:
		return true
	}
	return false
}

type attentionReason struct {
	Agent                string
	Status               protocol.TargetStatus
	Stage, Code, Message string
}
type attentionSnapshot struct {
	Reasons  []attentionReason
	Fallback string
}

func attentionFingerprint(status protocol.OperationStatus, targets []protocol.TargetResult) (string, bool) {
	snapshot := attentionSnapshot{Reasons: make([]attentionReason, 0)}
	for _, t := range targets {
		if targetNeedsAttention(t.Status) {
			snapshot.Reasons = append(snapshot.Reasons, attentionReason{t.AgentID, t.Status, t.Stage, t.ErrorCode, t.Message})
		}
	}
	sort.Slice(snapshot.Reasons, func(i, j int) bool { return snapshot.Reasons[i].Agent < snapshot.Reasons[j].Agent })
	if len(snapshot.Reasons) == 0 && (status == protocol.OpAttentionRequired || status == protocol.OpCompletedWithErrs) {
		snapshot.Fallback = string(status)
	}
	if len(snapshot.Reasons) == 0 && snapshot.Fallback == "" {
		return "", false
	}
	// This is a structural signature, not a security token. Retaining its bounded
	// reasons lets recovery remove an old problem without resurfacing another.
	b, _ := json.Marshal(snapshot)
	return string(b), true
}

func introducesAttention(previous, current string) bool {
	if current == "" {
		return false
	}
	var old, next attentionSnapshot
	if json.Unmarshal([]byte(current), &next) != nil {
		return true
	}
	if previous == "" || json.Unmarshal([]byte(previous), &old) != nil {
		return true
	}
	if next.Fallback != "" && next.Fallback != old.Fallback {
		return true
	}
	seen := map[attentionReason]bool{}
	for _, reason := range old.Reasons {
		seen[reason] = true
	}
	for _, reason := range next.Reasons {
		if !seen[reason] {
			return true
		}
	}
	return false
}

func (s *Store) syncOperationAttentionTx(tx *sql.Tx, id string, status protocol.OperationStatus, targets []protocol.TargetResult) error {
	fingerprint, required := attentionFingerprint(status, targets)
	// A new rollout block is new evidence even if a manual pause was already read.
	var rolloutState, rolloutReason string
	if e := tx.QueryRow(`SELECT state,reason FROM update_rollouts WHERE operation_id=?`, id).Scan(&rolloutState, &rolloutReason); e != nil && !errors.Is(e, sql.ErrNoRows) {
		return e
	}
	if rolloutState == "blocked" {
		var snapshot attentionSnapshot
		if fingerprint != "" {
			if e := json.Unmarshal([]byte(fingerprint), &snapshot); e != nil {
				return e
			}
		}
		snapshot.Fallback = "rollout.blocked:" + rolloutReason
		raw, e := json.Marshal(snapshot)
		if e != nil {
			return e
		}
		fingerprint = string(raw)
		required = true
	}
	var previous string
	e := tx.QueryRow(`SELECT fingerprint FROM operation_attention WHERE operation_id=?`, id).Scan(&previous)
	if errors.Is(e, sql.ErrNoRows) {
		_, e = tx.Exec(`INSERT INTO operation_attention(operation_id,generation,fingerprint,required) VALUES(?,1,?,?)`, id, fingerprint, boolInt(required))
		return e
	}
	if e != nil {
		return e
	}
	if fingerprint == previous {
		return nil
	}
	_, e = tx.Exec(`UPDATE operation_attention SET generation=generation+?,fingerprint=?,required=? WHERE operation_id=?`, boolInt(introducesAttention(previous, fingerprint)), fingerprint, boolInt(required), id)
	return e
}

// Reconcile in bounded keyset batches at startup, including writes made by an
// older controller during a rollback. Unchanged read state is preserved; only
// genuinely new attention invalidates its acknowledgement.
func (s *Store) initializeOperationAttention() error {
	lastID := ""
	for {
		count := 0
		e := s.WithTx(func(tx *sql.Tx) error {
			rows, e := tx.Query(`SELECT id,status FROM operations WHERE id>? ORDER BY id LIMIT 500`, lastID)
			if e != nil {
				return e
			}
			type item struct {
				id     string
				status protocol.OperationStatus
			}
			items := []item{}
			for rows.Next() {
				var i item
				if e = rows.Scan(&i.id, &i.status); e != nil {
					rows.Close()
					return e
				}
				items = append(items, i)
			}
			e = rows.Err()
			rows.Close()
			if e != nil {
				return e
			}
			for _, i := range items {
				targets, e := targetsWith(tx, i.id)
				if e != nil {
					return e
				}
				if e = s.syncOperationAttentionTx(tx, i.id, i.status, targets); e != nil {
					return e
				}
			}
			count = len(items)
			if count > 0 {
				lastID = items[count-1].id
			}
			return nil
		})
		if e != nil {
			return fmt.Errorf("operation attention migration: %w", e)
		}
		if count < 500 {
			return nil
		}
	}
}

type OperationReadTarget struct {
	ID                string `json:"operation_id"`
	AttentionRevision int64  `json:"attention_revision"`
}

// MarkOperationsRead is an atomic, idempotent acknowledgement of a frozen
// versioned selection, not a fleet command and not a new operation itself.
func (s *Store) MarkOperationsRead(actor string, read bool, selection []OperationReadTarget) error {
	if actor == "" || len(selection) == 0 || len(selection) > 100 {
		return ErrInvalidOperationQuery
	}
	seen := map[string]bool{}
	for _, v := range selection {
		if v.ID == "" || len(v.ID) > 128 || v.AttentionRevision < 1 || seen[v.ID] {
			return ErrInvalidOperationQuery
		}
		seen[v.ID] = true
	}
	return s.WithTx(func(tx *sql.Tx) error {
		type notice struct {
			id              string
			generation, ack int64
		}
		notices := []notice{}
		for _, v := range selection {
			var n notice
			n.id = v.ID
			if e := tx.QueryRow(`SELECT generation,acknowledged_generation FROM operation_attention WHERE operation_id=?`, v.ID).Scan(&n.generation, &n.ack); e != nil {
				if errors.Is(e, sql.ErrNoRows) {
					return ErrNotFound
				}
				return e
			}
			if n.generation != v.AttentionRevision {
				return ErrConflict
			}
			notices = append(notices, n)
		}
		for _, n := range notices {
			alreadyRead := n.ack == n.generation
			if read == alreadyRead {
				continue
			} // Preserve first acknowledgement time/actor on retry.
			var ack int64
			var at, by any
			if read {
				ack = n.generation
				at = s.now().Format(dbTimeFormat)
				by = actor
			}
			if _, e := tx.Exec(`UPDATE operation_attention SET acknowledged_generation=?,acked_at=?,acked_by=? WHERE operation_id=?`, ack, at, by, n.id); e != nil {
				return e
			}
			action := "operation.unread"
			if read {
				action = "operation.read"
			}
			if _, e := tx.Exec(`INSERT INTO audit_events(at,actor,action,entity,detail) VALUES(?,?,?,?,?)`, s.now().Format(dbTimeFormat), actor, action, n.id, fmt.Sprintf("attention_revision=%d; execution unchanged", n.generation)); e != nil {
				return e
			}
			body, _ := json.Marshal(map[string]any{"read": read, "attention_revision": n.generation})
			if _, e := tx.Exec(`INSERT INTO event_log(ts,type,entity,entity_id,revision,payload) VALUES(?,'operation','operation',?,?,?)`, s.now().Format(dbTimeFormat), n.id, n.generation, string(body)); e != nil {
				return e
			}
		}
		return nil
	})
}
