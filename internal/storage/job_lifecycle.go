package storage

import (
	"database/sql"
	"github.com/alinescafs3mp-afk/monik_v2/internal/actions"
	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
)

func (s *Store) expireAndBlockJobs() error {
	ops := map[string]bool{}
	err := s.WithTx(func(tx *sql.Tx) error {
		rows, err := tx.Query(`SELECT job_id,operation_id,agent_id,action,status,deadline FROM agent_jobs WHERE status IN ('queued','waiting_offline','delivered','accepted','running','awaiting_confirmation') ORDER BY deadline,job_id LIMIT 1000`)
		if err != nil {
			return err
		}
		type item struct{ id, op, agent, action, status, deadline string }
		items := []item{}
		for rows.Next() {
			var j item
			if err := rows.Scan(&j.id, &j.op, &j.agent, &j.action, &j.status, &j.deadline); err != nil {
				rows.Close()
				return err
			}
			items = append(items, j)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		now := s.now().Format(dbTimeFormat)
		for _, j := range items {
			status, reason := "", ""
			if why := actions.UnavailableReason(j.action); why != "" {
				status = string(protocol.TargetUnsupported)
				reason = why
			} else if j.deadline <= now {
				status = string(protocol.TargetExpired)
				reason = "job deadline expired; result was not confirmed"
			}
			if status == "" {
				continue
			}
			if _, err := tx.Exec(`UPDATE agent_jobs SET status=? WHERE job_id=?`, status, j.id); err != nil {
				return err
			}
			if _, err := tx.Exec(`UPDATE operation_targets SET status=?,stage='server_guard',message=?,error_code='blocked_or_expired',updated_at=? WHERE operation_id=? AND agent_id=?`, status, reason, now, j.op, j.agent); err != nil {
				return err
			}
			ops[j.op] = true
		}
		for id := range ops {
			if err := s.refreshOperationTx(tx, id); err != nil {
				return err
			}
		}
		return nil
	})
	return err
}
