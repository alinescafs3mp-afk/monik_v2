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

	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
)

var ErrInvalidOperationQuery = errors.New("invalid operation query")

const operationColumns = `o.id,o.action,o.status,o.revision,o.client_request_key,o.actor_id,o.params,o.created_at,o.deadline,COALESCE(o.summary,''),COALESCE(o.parent_id,''),COALESCE(n.generation,1),COALESCE(n.required,0),COALESCE(n.acknowledged_generation,0),n.acked_at,n.acked_by`
const operationJoin = `operations o LEFT JOIN operation_attention n ON n.operation_id=o.id`

type rowScanner interface{ Scan(...any) error }

func scanOperation(row rowScanner) (*protocol.Operation, error) {
	op := &protocol.Operation{}
	var params, created string
	var deadline, ackedAt, ackedBy sql.NullString
	var required int
	var ackRevision int64
	if e := row.Scan(&op.ID, &op.Action, &op.Status, &op.Revision, &op.ClientRequestKey, &op.Actor, &params, &created, &deadline, &op.Summary, &op.ParentID, &op.AttentionRevision, &required, &ackRevision, &ackedAt, &ackedBy); e != nil {
		return nil, e
	}
	if e := json.Unmarshal([]byte(params), &op.Params); e != nil {
		return nil, fmt.Errorf("corrupt operation parameters: %w", e)
	}
	var e error
	op.CreatedAt, e = time.Parse(time.RFC3339Nano, created)
	if e != nil {
		return nil, fmt.Errorf("corrupt operation creation time: %w", e)
	}
	if deadline.Valid && deadline.String != "" {
		v, e := time.Parse(time.RFC3339Nano, deadline.String)
		if e != nil {
			return nil, fmt.Errorf("corrupt operation deadline: %w", e)
		}
		op.Deadline = &v
	}
	op.AttentionRequired = required == 1
	op.Read = ackRevision == op.AttentionRevision
	op.NeedsAttention = op.AttentionRequired && !op.Read
	if op.Read && ackedAt.Valid {
		v, e := time.Parse(time.RFC3339Nano, ackedAt.String)
		if e != nil {
			return nil, fmt.Errorf("corrupt operation read time: %w", e)
		}
		op.ReadAt = &v
		op.ReadBy = ackedBy.String
	}
	return op, nil
}
func targetsWith(q sqlExecutor, id string) ([]protocol.TargetResult, error) {
	rows, e := q.Query(`SELECT agent_id,status,stage,COALESCE(message,''),COALESCE(error_code,''),retryable,COALESCE(evidence,'null') FROM operation_targets WHERE operation_id=? ORDER BY agent_id`, id)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := make([]protocol.TargetResult, 0)
	for rows.Next() {
		var t protocol.TargetResult
		var retry int
		var raw string
		if e = rows.Scan(&t.AgentID, &t.Status, &t.Stage, &t.Message, &t.ErrorCode, &retry, &raw); e != nil {
			return nil, e
		}
		t.Retryable = retry == 1
		if e = json.Unmarshal([]byte(raw), &t.Evidence); e != nil {
			return nil, fmt.Errorf("corrupt target evidence: %w", e)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
func attachTargets(q sqlExecutor, op *protocol.Operation) error {
	var e error
	op.Targets, e = targetsWith(q, op.ID)
	if e != nil {
		return e
	}
	if op.Action == "secret.replace" {
		op.Params = map[string]any{"redacted": true}
		for i := range op.Targets {
			op.Targets[i].Evidence = nil
		}
	}
	return nil
}

// A single snapshot keeps status, target evidence and acknowledgement coherent.
// Rows are closed before dependent queries, including with a one-connection pool.
func (s *Store) readOperations(fn func(sqlExecutor) error) error {
	if s.executor != nil {
		return fn(s.executor)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tx, e := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = fn(tx); e != nil {
		return e
	}
	return tx.Commit()
}
func (s *Store) Operation(id string) (out *protocol.Operation, e error) {
	e = s.readOperations(func(q sqlExecutor) error {
		var e error
		out, e = scanOperation(q.QueryRow(`SELECT `+operationColumns+` FROM `+operationJoin+` WHERE o.id=?`, id))
		if errors.Is(e, sql.ErrNoRows) {
			return ErrNotFound
		}
		if e != nil {
			return e
		}
		return attachTargets(q, out)
	})
	return
}
func (s *Store) Targets(id string) ([]protocol.TargetResult, error) { return targetsWith(s.db(), id) }
func (s *Store) LookupIdempotency(actor, key, reqHash string) (*protocol.Operation, error) {
	var op *protocol.Operation
	e := s.readOperations(func(q sqlExecutor) error {
		var id, hash string
		if e := q.QueryRow(`SELECT id,request_hash FROM operations WHERE actor_id=? AND client_request_key=?`, actor, key).Scan(&id, &hash); e != nil {
			if errors.Is(e, sql.ErrNoRows) {
				return ErrNotFound
			}
			return e
		}
		if hash != reqHash {
			return ErrIdempotencyConflict
		}
		var e error
		op, e = scanOperation(q.QueryRow(`SELECT `+operationColumns+` FROM `+operationJoin+` WHERE o.id=?`, id))
		if e != nil {
			return e
		}
		return attachTargets(q, op)
	})
	return op, e
}

type OperationCounts struct {
	Total           int `json:"total"`
	Running         int `json:"running"`
	Attention       int `json:"attention"`
	UnreadAttention int `json:"unread_attention"`
	Read            int `json:"read"`
}

func operationCounts(q sqlExecutor) (c OperationCounts, e error) {
	e = q.QueryRow(`SELECT COUNT(*),COALESCE(SUM(o.status IN ('queued','running')),0),COALESCE(SUM(n.required=1),0),COALESCE(SUM(n.required=1 AND n.acknowledged_generation<>n.generation),0),COALESCE(SUM(n.acknowledged_generation=n.generation),0) FROM `+operationJoin).Scan(&c.Total, &c.Running, &c.Attention, &c.UnreadAttention, &c.Read)
	return
}
func (s *Store) OperationCounts() (c OperationCounts, e error) {
	if e = s.expireAndBlockJobs(); e != nil {
		return
	}
	e = s.readOperations(func(q sqlExecutor) error { var e error; c, e = operationCounts(q); return e })
	return
}

type OperationQuery struct {
	Filter, Read, Search, Before string
	Limit                        int
}
type operationCursor struct {
	At, ID, Filter, Read, Search string
	Anchor                       int64
}
type OperationPage struct {
	Operations []*protocol.Operation `json:"operations"`
	Next       string                `json:"next_cursor,omitempty"`
	Counts     OperationCounts       `json:"counts"`
}

func (s *Store) QueryOperations(q OperationQuery) (out OperationPage, e error) {
	if q.Limit == 0 {
		q.Limit = 50
	}
	if q.Limit < 1 || q.Limit > 200 || len(q.Search) > 120 || len(q.Before) > 2048 {
		return out, ErrInvalidOperationQuery
	}
	if q.Filter == "" {
		q.Filter = "all"
	}
	if q.Read == "" {
		q.Read = "all"
	}
	where := "1=1"
	args := []any{}
	switch q.Filter {
	case "all":
	case "running":
		where += ` AND o.status IN ('queued','running')`
	case "attention":
		where += ` AND n.required=1 AND n.acknowledged_generation<>n.generation`
	case "errors":
		where += ` AND n.required=1`
	case "completed":
		where += ` AND o.status IN ('completed','completed_with_errors','cancelled')`
	default:
		return out, ErrInvalidOperationQuery
	}
	switch q.Read {
	case "all":
	case "read":
		where += ` AND n.acknowledged_generation=n.generation`
	case "unread":
		where += ` AND n.acknowledged_generation<>n.generation`
	default:
		return out, ErrInvalidOperationQuery
	}
	if q.Search != "" {
		pattern := "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(q.Search) + "%"
		where += ` AND (o.action LIKE ? ESCAPE '\' OR o.actor_id LIKE ? ESCAPE '\' OR o.id LIKE ? ESCAPE '\')`
		args = append(args, pattern, pattern, pattern)
	}
	cursor := operationCursor{Filter: q.Filter, Read: q.Read, Search: q.Search}
	if q.Before != "" {
		b, err := base64.RawURLEncoding.DecodeString(q.Before)
		if err != nil {
			return out, ErrInvalidOperationQuery
		}
		if err = json.Unmarshal(b, &cursor); err != nil || cursor.ID == "" || len(cursor.ID) > 128 || cursor.Anchor < 1 || cursor.Filter != q.Filter || cursor.Read != q.Read || cursor.Search != q.Search {
			return out, ErrInvalidOperationQuery
		}
		t, err := time.Parse(time.RFC3339Nano, cursor.At)
		if err != nil {
			return out, ErrInvalidOperationQuery
		}
		cursor.At = t.UTC().Format(dbTimeFormat)
		where += ` AND (o.created_at<? OR (o.created_at=? AND o.id<?))`
		args = append(args, cursor.At, cursor.At, cursor.ID)
	}
	if e = s.expireAndBlockJobs(); e != nil {
		return out, e
	}
	e = s.readOperations(func(ex sqlExecutor) error {
		if cursor.Anchor == 0 {
			if e := ex.QueryRow(`SELECT COALESCE(MAX(rowid),0) FROM operations`).Scan(&cursor.Anchor); e != nil {
				return e
			}
		}
		rows, e := ex.Query(`SELECT `+operationColumns+` FROM `+operationJoin+` WHERE `+where+` AND o.rowid<=? ORDER BY o.created_at DESC,o.id DESC LIMIT ?`, append(args, cursor.Anchor, q.Limit+1)...)
		if e != nil {
			return e
		}
		out.Operations = make([]*protocol.Operation, 0)
		for rows.Next() {
			op, e := scanOperation(rows)
			if e != nil {
				rows.Close()
				return e
			}
			out.Operations = append(out.Operations, op)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		if len(out.Operations) > q.Limit {
			out.Operations = out.Operations[:q.Limit]
			last := out.Operations[len(out.Operations)-1]
			cursor.At = last.CreatedAt.UTC().Format(dbTimeFormat)
			cursor.ID = last.ID
			b, _ := json.Marshal(cursor)
			out.Next = base64.RawURLEncoding.EncodeToString(b)
		}
		for _, op := range out.Operations {
			if e = attachTargets(ex, op); e != nil {
				return e
			}
		}
		out.Counts, e = operationCounts(ex)
		return e
	})
	return
}
func (s *Store) Operations(limit int) ([]*protocol.Operation, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 200 {
		limit = 200
	}
	page, e := s.QueryOperations(OperationQuery{Limit: limit})
	return page.Operations, e
}
