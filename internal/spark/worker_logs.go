package spark

import (
	"context"
	"fmt"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/jsonutil"
	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
)

// Polling an authenticated lease channel is separate from heartbeat transport.
// Revoked/missing leases immediately cancel the local shared subscription.
func (w *Worker) pollLogs() {
	requestStarted := time.Now()
	ctx, end := context.WithTimeout(w.ctx, 2*time.Second)
	defer end()
	response, e := w.authenticatedRequest(ctx, "GET", "/api/v1/agent/spark/logs", nil)
	if e != nil {
		return
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		w.closeLogSessions()
		return
	}
	var answer struct {
		Leases     []protocol.SparkLogLease `json:"leases"`
		ServerTime time.Time                `json:"server_time"`
	}
	if jsonutil.ReadObject(response.Body, 128<<10, &answer) != nil || len(answer.Leases) > 16 {
		return
	}
	active := map[string]bool{}
	for _, lease := range answer.Leases {
		if !protocol.SparkValidID(lease.StreamID) || lease.Request.ExpectedAgentSession != w.session {
			continue
		}
		remaining := lease.Deadline.Sub(answer.ServerTime) - time.Since(requestStarted)
		if remaining <= 0 || remaining > 30*time.Second {
			continue
		}
		lease.Deadline = time.Now().Add(remaining)
		active[lease.StreamID] = true
		// Renewal changes only the short lease, never the source or authority.
		if e = w.broker.RenewLogs(ctx, lease); e != nil {
			continue
		}
		w.mu.Lock()
		_, exists := w.logSessions[lease.StreamID]
		if !exists {
			streamCtx, cancel := context.WithCancel(w.ctx)
			w.logSessions[lease.StreamID] = cancel
			w.wg.Add(1)
			go w.streamLogs(streamCtx, lease)
		}
		w.mu.Unlock()
	}
	w.mu.Lock()
	for id, cancel := range w.logSessions {
		if !active[id] {
			cancel()
			delete(w.logSessions, id)
		}
	}
	w.mu.Unlock()
}
func (w *Worker) closeLogSessions() {
	w.mu.Lock()
	defer w.mu.Unlock()
	for id, cancel := range w.logSessions {
		cancel()
		delete(w.logSessions, id)
	}
}
func (w *Worker) streamLogs(ctx context.Context, lease protocol.SparkLogLease) {
	defer w.wg.Done()
	defer func() { w.mu.Lock(); delete(w.logSessions, lease.StreamID); w.mu.Unlock() }()
	records := make(chan protocol.SparkLogRecord, 64)
	errors := make(chan error, 1)
	streamCtx, end := context.WithCancel(ctx)
	defer end()
	go func() {
		errors <- w.broker.Logs(streamCtx, lease, func(r protocol.SparkLogRecord) error {
			select {
			case records <- r:
				return nil
			case <-streamCtx.Done():
				return streamCtx.Err()
			default:
				return fmt.Errorf("bounded_log_queue_full")
			}
		})
	}()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	batch := []protocol.SparkLogRecord{}
	bytes := 0
	flush := func() bool {
		if len(batch) == 0 {
			return true
		}
		postCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		response, e := w.authenticatedRequest(postCtx, "POST", "/api/v1/agent/spark/logs", protocol.SparkLogBatch{StreamID: lease.StreamID, AgentSession: w.session, Records: batch})
		if e != nil {
			return false
		}
		response.Body.Close()
		batch = nil
		bytes = 0
		return response.StatusCode == 200
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-errors:
			_ = flush()
			return
		case <-ticker.C:
			if !flush() {
				return
			}
		case r := <-records:
			if len(r.Text) > LogMaxLine {
				continue
			}
			if len(batch) >= 200 || bytes+len(r.Text)+1 > 128<<10 {
				if !flush() {
					return
				}
			}
			batch = append(batch, r)
			bytes += len(r.Text) + 1
		}
	}
}
