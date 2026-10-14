package storage

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
)

// ReconcileUnverifiedModelReceipts is called after committing a live report. It
// rechecks original claimed successes that an older controller may already have
// ACKed, using ApplyReceipt's existing committed-proof rules. It never dispatches
// an effect, changes an envelope, or creates a new receipt or execution lease.
func (s *Store) ReconcileUnverifiedModelReceipts(agentID string) error {
	rows, err := s.db().Query(`SELECT j.job_id,j.operation_id,j.result FROM agent_jobs j JOIN operation_targets t ON t.operation_id=j.operation_id AND t.agent_id=j.agent_id AND t.job_id=j.job_id WHERE j.agent_id=? AND j.action LIKE 'model.%' AND j.status='unknown_result' AND t.status='unknown_result' AND t.stage='unverified_result' AND t.error_code='missing_evidence' AND CASE WHEN json_valid(j.result) THEN json_extract(j.result,'$.status') END='succeeded' ORDER BY t.updated_at,j.job_id LIMIT 16`, agentID)
	if err != nil {
		return err
	}
	var receipts []protocol.JobReceipt
	for rows.Next() {
		var jobID, opID, raw string
		if err = rows.Scan(&jobID, &opID, &raw); err != nil {
			rows.Close()
			return err
		}
		var rec protocol.JobReceipt
		if err = json.Unmarshal([]byte(raw), &rec); err != nil {
			rows.Close()
			return fmt.Errorf("decode stored model receipt: %w", err)
		}
		if rec.JobID != jobID || rec.OperationID != "" && rec.OperationID != opID {
			rows.Close()
			return fmt.Errorf("stored model receipt identity mismatch")
		}
		receipts = append(receipts, rec)
	}
	err = rows.Err()
	closeErr := rows.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	// Release the read cursor before ApplyReceipt starts its write transaction,
	// including on a store configured with a single connection.
	// Unresolved targets get a new updated_at, rotating them behind candidates
	// not yet checked when more than 16 need reconciliation.
	for _, rec := range receipts {
		if err = s.ApplyReceipt(agentID, rec); err != nil && !errors.Is(err, ErrConflict) && !errors.Is(err, ErrNotFound) {
			return err
		}
	}
	return nil
}
