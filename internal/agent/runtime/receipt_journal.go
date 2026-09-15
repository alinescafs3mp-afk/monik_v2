package runtime

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
	"io"
	"os"
	"path/filepath"
	"sort"
)

// Comparing the request's immutable snapshot prevents an acceptance ACK from
// deleting a completion produced concurrently while the HTTP request waited.
func sameReceipt(a, b protocol.JobReceipt) bool {
	x, e := json.Marshal(a)
	if e != nil {
		return false
	}
	y, e := json.Marshal(b)
	return e == nil && bytes.Equal(x, y)
}

func (a *Agent) loadJobs() error {
	path := filepath.Join(a.State.File.StateDir, "job-receipts.json")
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("job receipt journal: %w", err)
	}
	const limit = 8 << 20
	if !info.Mode().IsRegular() || info.Size() > limit {
		return fmt.Errorf("invalid job receipt journal size/type")
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return err
	}
	if len(raw) > limit {
		return fmt.Errorf("job receipt journal exceeds limit")
	}
	var jobs map[string]protocol.JobReceipt
	if err := json.Unmarshal(raw, &jobs); err != nil {
		return fmt.Errorf("job receipt journal corrupt: %w", err)
	}
	if jobs == nil || len(jobs) > 1000 {
		return fmt.Errorf("invalid job receipt journal object/count")
	}
	for id, rec := range jobs {
		if id == "" || rec.JobID != id || rec.Status == "" {
			return fmt.Errorf("invalid job receipt journal identity/status")
		}
		switch rec.Status {
		case protocol.TargetQueued, protocol.TargetWaitingOffline, protocol.TargetAccepted,
			protocol.TargetRunning, protocol.TargetAwaitingConfirmation, protocol.TargetSucceeded,
			protocol.TargetFailed, protocol.TargetRejected, protocol.TargetUnsupported,
			protocol.TargetExpired, protocol.TargetRolledBack, protocol.TargetUnknownResult,
			protocol.TargetCancelledBeforeExec:
		default:
			return fmt.Errorf("unknown job receipt journal status")
		}
		if rec.Stage == "trial_pending" {
			rec.Status = protocol.TargetFailed
			rec.Stage = "trial_interrupted"
			rec.Message = "worker restarted during trial; actual outcome unknown; no automatic repeat"
			rec.ErrorCode = "outcome_unknown"
			jobs[id] = rec
		}
	}
	a.jobs = jobs
	return nil
}

// The requesting process can be killed before the supervisor finishes probation.
// A new process must reserve that job before its first poll, or delivery retry
// will start the same update repeatedly before the first activation confirms.
func (a *Agent) restoreIntentReceipt() error {
	in, err := loadIntent(a.State.File.StateDir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("lifecycle intent: %w", err)
	}
	if in == nil {
		return fmt.Errorf("invalid lifecycle intent")
	}
	switch in.Kind {
	case "rebind_switch":
		if in.CandidateURL == "" || in.PlanID == "" || in.Generation <= 0 {
			return fmt.Errorf("invalid migration intent")
		}
		// Candidate confirmation is handled by the persisted migration state.
		return nil
	case "restart", "update", "rollback":
		if in.JobID == "" || in.OperationID == "" || in.PreviousSession == "" {
			return fmt.Errorf("incomplete lifecycle intent")
		}
	default:
		return fmt.Errorf("unknown lifecycle intent kind")
	}
	if _, exists := a.jobs[in.JobID]; !exists {
		a.jobs[in.JobID] = protocol.JobReceipt{JobID: in.JobID, OperationID: in.OperationID,
			Status: protocol.TargetAwaitingConfirmation, Stage: "lifecycle_pending",
			Message: "durable lifecycle request exists; awaiting supervisor evidence; not repeated"}
		return a.saveJobsLocked()
	}
	return nil
}

// receiptBatchLocked fairly rotates the bounded report window. A stable prefix
// of accepted jobs must not starve a later completed receipt indefinitely.
// The cursor is scheduling state only; all evidence remains in the durable map.
func (a *Agent) receiptBatchLocked(limit int) []protocol.JobReceipt {
	if limit <= 0 || len(a.jobs) == 0 {
		return nil
	}
	ids := make([]string, 0, len(a.jobs))
	for id := range a.jobs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	start := sort.Search(len(ids), func(i int) bool { return ids[i] > a.receiptCursor })
	out := make([]protocol.JobReceipt, 0, min(limit, len(ids)))
	for n := 0; n < limit && n < len(ids); n++ {
		id := ids[(start+n)%len(ids)]
		out = append(out, a.jobs[id])
		a.receiptCursor = id
	}
	return out
}
