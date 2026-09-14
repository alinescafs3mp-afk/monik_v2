package runtime

import (
	"fmt"
	"github.com/alinescafs3mp-afk/monik_v2/internal/agent/configfile"
	"github.com/alinescafs3mp-afk/monik_v2/internal/netutil"
	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
	"github.com/alinescafs3mp-afk/monik_v2/internal/secure"
	"github.com/alinescafs3mp-afk/monik_v2/internal/tlsutil"
	"time"
)

// All helpers execute on the serialized control loop with the job mutex held.
func (a *Agent) validatePlan(p *protocol.MigrationPlan) error {
	if p == nil || p.PlanID == "" || p.ControllerID != a.State.File.ControllerID {
		return fmt.Errorf("plan does not identify the enrolled controller")
	}
	if _, err := netutil.ValidateControllerURL(p.CandidateURL); err != nil {
		return err
	}
	if p.Generation < 1 || p.Generation < a.State.File.EndpointGeneration {
		return fmt.Errorf("migration generation replay")
	}
	if !p.ExpiresAt.After(a.Clock.Now()) {
		return fmt.Errorf("migration plan expired")
	}
	if p.PayloadHash != secure.SHA256Bytes([]byte(p.CandidateURL+"|"+p.ControllerID)) {
		return fmt.Errorf("migration payload hash mismatch")
	}
	if p.TrustPEM != "" {
		if _, err := tlsutil.PoolFromPEM([]byte(p.TrustPEM)); err != nil {
			return err
		}
	}
	return nil
}

func (a *Agent) beginMigration(p *protocol.MigrationPlan, jobID, operationID string) error {
	if err := a.validatePlan(p); err != nil {
		return err
	}
	if in, err := loadIntent(a.State.File.StateDir); err == nil && in != nil {
		return fmt.Errorf("another local lifecycle intent is still active")
	}
	if err := a.verifyCandidate(p); err != nil {
		return err
	}
	in := &lifecycleIntent{Kind: "rebind_switch", JobID: jobID, OperationID: operationID, PlanID: p.PlanID, ControllerID: p.ControllerID, CandidateURL: p.CandidateURL, TrustPEM: p.TrustPEM, Generation: p.Generation, PreviousURL: a.State.File.ControllerURL, PreviousTrust: a.State.File.CACertPEM, SwitchedAt: a.Clock.Now().UTC()}
	if err := saveIntent(a.State.File.StateDir, in); err != nil {
		return err
	}
	if err := a.applySwitch(in); err != nil {
		clearIntent(a.State.File.StateDir)
		return err
	}
	return nil
}

func (a *Agent) recordMigrationContact(sentURL string) {
	in, err := loadIntent(a.State.File.StateDir)
	if err != nil || in.Kind != "rebind_switch" || sentURL != in.CandidateURL {
		return
	}
	in.Acknowledgements++
	now := a.Clock.Now().UTC()
	if in.Acknowledgements < 3 || now.Sub(in.SwitchedAt) < 15*time.Second {
		_ = saveIntent(a.State.File.StateDir, in)
		return
	}
	plan, err := configfile.LoadMigration(a.State.File.StateDir)
	if err != nil || plan.PlanID != in.PlanID {
		return
	}
	plan.ConfirmedAt = &now
	if err := configfile.SaveMigration(a.State.File.StateDir, plan); err != nil {
		return
	}
	if in.JobID != "" {
		a.jobs[in.JobID] = protocol.JobReceipt{JobID: in.JobID, OperationID: in.OperationID, Status: protocol.TargetSucceeded, Stage: "rebind.confirmed", Message: "candidate committed three fresh live exchanges over at least 15 seconds", AppliedAt: &now, Evidence: map[string]any{"plan_id": in.PlanID, "generation": in.Generation, "candidate_url": in.CandidateURL, "committed_exchanges": in.Acknowledgements}}
		if err := a.saveJobsLocked(); err != nil {
			return
		}
	}
	clearIntent(a.State.File.StateDir)
}

func (a *Agent) maintainMigration() {
	now := a.Clock.Now().UTC()
	if in, err := loadIntent(a.State.File.StateDir); err == nil && in != nil {
		if in.Kind != "rebind_switch" {
			return
		}
		// A crash before publishing the candidate configuration needs no URL rollback.
		if a.State.File.ControllerURL != in.CandidateURL && a.State.File.ControllerURL == in.PreviousURL {
			clearIntent(a.State.File.StateDir)
			return
		}
		if now.Sub(in.SwitchedAt) < 60*time.Second {
			return
		}
		fallback := &lifecycleIntent{CandidateURL: in.PreviousURL, TrustPEM: in.PreviousTrust, Generation: in.Generation}
		if err := a.applySwitch(fallback); err != nil {
			return
		}
		if in.JobID != "" {
			a.jobs[in.JobID] = protocol.JobReceipt{JobID: in.JobID, OperationID: in.OperationID, Status: protocol.TargetFailed, Stage: "rebind.fallback", Message: "candidate did not confirm live contact; retained the authorized previous endpoint", Retryable: true, Evidence: map[string]any{"plan_id": in.PlanID, "generation": in.Generation}}
			if err := a.saveJobsLocked(); err != nil {
				return
			}
		}
		clearIntent(a.State.File.StateDir)
		a.nextMigrationTrial = now.Add(time.Minute)
		return
	}
	plan, err := configfile.LoadMigration(a.State.File.StateDir)
	if err != nil || plan == nil || !plan.ArmFallback || plan.ConfirmedAt != nil || !plan.ExpiresAt.After(now) || now.Before(a.nextMigrationTrial) {
		return
	}
	loss := time.Duration(plan.PrimaryLossSeconds) * time.Second
	if loss < 15*time.Second {
		loss = 30 * time.Second
	}
	if now.Sub(a.lastContact) < loss {
		return
	}
	a.nextMigrationTrial = now.Add(time.Minute)
	// No cached command is fabricated. The already-authorized local fallback
	// trigger changes transport; its later report carries the active generation.
	_ = a.beginMigration(plan, "", "")
}

func (a *Agent) migrationStatus() *protocol.MigrationStatus {
	p, err := configfile.LoadMigration(a.State.File.StateDir)
	if err != nil {
		return nil
	}
	m := &protocol.MigrationStatus{PlanID: p.PlanID, Generation: p.Generation, Candidate: p.CandidateURL, State: "prepared", Reason: "plan persisted; connectivity not yet confirmed"}
	if p.ArmFallback {
		m.State = "armed"
		m.Reason = "primary-loss fallback is armed"
	}
	if !p.ExpiresAt.After(a.Clock.Now()) {
		m.State = "expired"
		m.Reason = "activation window expired"
	}
	if p.ConfirmedAt != nil {
		m.State = "confirmed"
		m.Reason = "fresh candidate exchanges confirmed"
	}
	if in, e := loadIntent(a.State.File.StateDir); e == nil && in.Kind == "rebind_switch" && in.PlanID == p.PlanID {
		m.State = "activating"
		m.Reason = "awaiting committed candidate exchanges"
	}
	return m
}
