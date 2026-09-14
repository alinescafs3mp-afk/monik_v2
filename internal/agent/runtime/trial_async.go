package runtime

import (
	"context"
	"github.com/alinescafs3mp-afk/monik_v2/internal/agent/checks"
	"github.com/alinescafs3mp-afk/monik_v2/internal/netutil"
	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
	"net"
	"time"
)

// Caller holds a.mu. Persist before dispatch, never block heartbeat on a long
// trial. A restarted pending trial becomes unknown/interrupted, not replayed.
func (a *Agent) startTrialLocked(job protocol.JobEnvelope, rec protocol.JobReceipt) bool {
	if a.trialRunning.Load() >= 2 {
		return false
	}
	d, err := checks.ParseTrial(job.Params)
	if err != nil {
		rec.Status = protocol.TargetRejected
		rec.Stage = "rejected"
		rec.Message = err.Error()
		rec.ErrorCode = "bad_check"
		a.jobs[job.JobID] = rec
		_ = a.saveJobsLocked()
		return true
	}
	rec.Status = protocol.TargetAccepted
	rec.Stage = "trial_pending"
	rec.Message = "bounded trial running on agent"
	rec.ErrorCode = ""
	a.jobs[job.JobID] = rec
	if a.saveJobsLocked() != nil {
		delete(a.jobs, job.JobID)
		return false
	}
	a.trialRunning.Add(1)
	go a.finishTrial(job, d, rec)
	return true
}
func (a *Agent) finishTrial(job protocol.JobEnvelope, d protocol.CheckDefinition, rec protocol.JobReceipt) {
	defer a.trialRunning.Add(-1)
	c, cancel := context.WithTimeout(context.Background(), time.Duration(d.TimeoutSeconds)*time.Second)
	defer cancel()
	var locals []net.IP
	locals, _ = netutil.LocalInterfaceIPs()
	hdr, val := a.requestSecret(c, d.SecretID)
	body := ""
	if d.BodySecretID != "" {
		_, body = a.requestSecret(c, d.BodySecretID)
	}
	obs := checks.RunRequest(c, d, locals, hdr, val, body)
	rec.Status = protocol.TargetSucceeded
	rec.Stage = "redacted_trial_result"
	rec.ErrorCode = ""
	rec.Message = "trial completed; definition not saved"
	rec.AppliedAt = &obs.ObservedAt
	rec.Evidence = map[string]any{"trial": true, "vantage": "agent/local", "transport": obs.Transport, "http_status": obs.HTTPStatus, "latency_ms": obs.LatencyMS, "method": d.Method, "failure_layer": obs.FailureLayer, "purpose": obs.Purpose, "app_result": obs.AppResult, "app_reason": obs.AppReason, "quality": obs.Quality, "feedback": obs.Feedback}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.jobs[job.JobID] = rec
	_ = a.saveJobsLocked()
}
