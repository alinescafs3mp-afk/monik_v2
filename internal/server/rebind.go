package server

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/idgen"
	"github.com/alinescafs3mp-afk/monik_v2/internal/netutil"
	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
	"github.com/alinescafs3mp-afk/monik_v2/internal/secure"
	"github.com/alinescafs3mp-afk/monik_v2/internal/storage"
)

func (a *App) handleRebind(op *protocol.Operation, req protocol.SubmitOperation, s *storage.Session) error {
	switch req.Action {
	case "rebind.prepare":
		candidate, _ := req.Params["candidate_url"].(string)
		if _, err := netutil.ValidateControllerURL(candidate); err != nil {
			return err
		}
		if candidate == a.Cfg.AdvertisedURL {
			return fmt.Errorf("candidate_url must differ from the current advertised URL")
		}
		mode, _ := req.Params["mode"].(string)
		if mode == "" {
			mode = "prepare"
		}
		planID := idgen.New()
		hash := secure.SHA256Bytes([]byte(candidate + "|" + a.ControllerID()))
		plan := &protocol.MigrationPlan{
			PlanID: planID, ControllerID: a.ControllerID(), CurrentURL: a.Cfg.AdvertisedURL,
			CandidateURL: candidate, Generation: a.Store.NextMigrationGeneration(), Mode: mode, PayloadHash: hash,
			TrustPEM: string(a.CACertPEM()), ExpiresAt: a.Clock.Now().Add(24 * time.Hour).UTC(),
			PrimaryLossSeconds: 300,
		}
		if err := a.Store.InsertMigration(plan, op.ID); err != nil {
			return err
		}
		for _, t := range op.Targets {
			if t.AgentID == "server" || t.Status == protocol.TargetRejected {
				continue
			}
			_ = a.Store.SetMigrationTarget(planID, t.AgentID, "prepared", "")
			extra := map[string]any{
				"plan_id": planID, "candidate_url": candidate, "payload_hash": hash,
				"generation": plan.Generation, "controller_id": plan.ControllerID,
				"current_url": plan.CurrentURL, "trust_pem": plan.TrustPEM,
				"expires_at": plan.ExpiresAt.Format(time.RFC3339),
			}
			if jobID, err := a.Store.JobIDFor(op.ID, t.AgentID); err == nil {
				_ = a.Store.PatchJobParams(jobID, extra)
			}
			stage := "rebind.prepared"
			st := protocol.TargetQueued
			if t.Status == protocol.TargetWaitingOffline {
				st = protocol.TargetWaitingOffline
			}
			_ = a.Store.UpdateTarget(op.ID, t.AgentID, st, stage, "migration plan persisted; controller URL is unchanged", "", true, extra)
		}
		_ = a.Store.SetSetting("active_migration_id", planID)
		return nil
	case "rebind.arm":
		id, err := a.planID(req)
		if err != nil {
			return err
		}
		plan, err := a.Store.Migration(id)
		if err != nil {
			return err
		}
		if err := a.Store.ArmMigration(id, true); err != nil {
			return err
		}
		for _, t := range op.Targets {
			if t.AgentID == "server" || t.Status == protocol.TargetRejected {
				continue
			}
			extra := map[string]any{"plan_id": id, "generation": plan.Generation, "candidate_url": plan.CandidateURL, "arm_fallback": true}
			if jobID, err := a.Store.JobIDFor(op.ID, t.AgentID); err == nil {
				_ = a.Store.PatchJobParams(jobID, extra)
			}
			_ = a.Store.SetMigrationTarget(id, t.AgentID, "armed", "")
			_ = a.Store.UpdateTarget(op.ID, t.AgentID, protocol.TargetQueued, "rebind.armed", "fallback trigger persisted", "", true, extra)
		}
		return nil
	case "rebind.activate":
		id, err := a.planID(req)
		if err != nil {
			return err
		}
		plan, err := a.Store.Migration(id)
		if err != nil {
			return fmt.Errorf("no prepared migration plan")
		}
		if plan.Mode == "retired" || !plan.ExpiresAt.After(a.Clock.Now()) {
			return fmt.Errorf("migration plan is retired or expired")
		}
		_ = a.Store.SetMigrationMode(id, "activate")
		for _, t := range op.Targets {
			if t.AgentID == "server" || t.Status == protocol.TargetRejected {
				continue
			}
			var n int
			_ = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM migration_targets WHERE plan_id=? AND agent_id=?`, id, t.AgentID).Scan(&n)
			if n != 1 {
				_ = a.Store.MarkTargetAndJob(op.ID, t.AgentID, protocol.TargetRejected, "rebind.unprepared", "agent has no prepared plan for this candidate; offline discovery of an unknown address is refused", false, map[string]any{"plan_id": id})
				continue
			}
			extra := map[string]any{
				"plan_id": plan.PlanID, "candidate_url": plan.CandidateURL, "generation": plan.Generation,
				"controller_id": plan.ControllerID, "trust_pem": plan.TrustPEM, "payload_hash": plan.PayloadHash,
				"current_url": plan.CurrentURL,
			}
			if jobID, err := a.Store.JobIDFor(op.ID, t.AgentID); err == nil {
				_ = a.Store.PatchJobParams(jobID, extra)
			}
			_ = a.Store.SetMigrationTarget(id, t.AgentID, "activating", "")
			_ = a.Store.UpdateTarget(op.ID, t.AgentID, protocol.TargetQueued, "rebind.activating", "waiting for candidate identity verification and committed generation", "", true, extra)
		}
		return nil
	case "rebind.retire":
		id, err := a.planID(req)
		if err != nil {
			return err
		}
		_ = a.Store.SetMigrationMode(id, "retired")
		for _, t := range op.Targets {
			if t.AgentID == "server" || t.Status == protocol.TargetRejected {
				continue
			}
			extra := map[string]any{"plan_id": id}
			if jobID, err := a.Store.JobIDFor(op.ID, t.AgentID); err == nil {
				_ = a.Store.PatchJobParams(jobID, extra)
			}
			_ = a.Store.SetMigrationTarget(id, t.AgentID, "retired", "")
			_ = a.Store.UpdateTarget(op.ID, t.AgentID, protocol.TargetQueued, "rebind.retire_queued", "waiting for endpoint retirement receipt", "", true, extra)
		}
		return nil
	}
	return fmt.Errorf("unknown rebind action")
}

func (a *App) planID(req protocol.SubmitOperation) (string, error) {
	id, _ := req.Params["plan_id"].(string)
	if id == "" {
		id, _ = a.Store.Setting("active_migration_id")
	}
	if id == "" {
		return "", fmt.Errorf("plan_id required")
	}
	return id, nil
}

func (a *App) handleRollout(op *protocol.Operation, req protocol.SubmitOperation) error {
	if req.Action == "update.resume" {
		for _, t := range op.Targets {
			if t.AgentID == "server" || t.Status == protocol.TargetRejected {
				continue
			}
			_ = a.Store.UpdateTarget(op.ID, t.AgentID, protocol.TargetQueued, "update.resumed", "rollout revalidated", "", true, nil)
		}
		return nil
	}
	releaseID, _ := req.Params["release_id"].(string)
	if releaseID == "" && req.Action != "update.rollback" {
		return fmt.Errorf("release_id required")
	}
	var arts []map[string]any
	if releaseID != "" {
		var err error
		arts, err = a.Store.ArtifactsByRelease(releaseID)
		if err != nil {
			return err
		}
	}
	for _, t := range op.Targets {
		if t.AgentID == "server" || t.Status == protocol.TargetRejected {
			continue
		}
		ag, err := a.Store.Agent(t.AgentID)
		if err != nil {
			continue
		}
		if !ag.ManagedReady {
			_ = a.Store.MarkTargetAndJob(op.ID, t.AgentID, protocol.TargetUnsupported, "unmanaged", "unmanaged worker cannot replace or restart itself; install the service host first", false, map[string]any{"release_id": releaseID})
			continue
		}
		if req.Action == "update.rollback" {
			extra := map[string]any{"release_id": releaseID, "os": ag.OS, "arch": ag.Arch, "component": "worker"}
			if jobID, err := a.Store.JobIDFor(op.ID, t.AgentID); err == nil {
				_ = a.Store.PatchJobParams(jobID, extra)
			}
			_ = a.Store.UpdateTarget(op.ID, t.AgentID, t.Status, "update.rollback_queued", "eligible prior build requested", "", true, extra)
			continue
		}
		art := matchArtifact(arts, ag.OS, ag.Arch)
		if art == nil {
			_ = a.Store.MarkTargetAndJob(op.ID, t.AgentID, protocol.TargetUnsupported, "no_artifact", "no signed artifact for this os/arch", false, map[string]any{"release_id": releaseID, "os": ag.OS, "arch": ag.Arch})
			continue
		}
		extra := map[string]any{
			"release_id": releaseID, "os": ag.OS, "arch": ag.Arch,
			"name": art["name"], "sha256": art["sha256"], "length": art["length"],
			"component": "worker",
		}
		if jobID, err := a.Store.JobIDFor(op.ID, t.AgentID); err == nil {
			_ = a.Store.PatchJobParams(jobID, extra)
		}
		_ = a.Store.UpdateTarget(op.ID, t.AgentID, t.Status, "update.queued", "signed artifact authorized for this target", "", true, extra)
	}
	return nil
}

func (a *App) handleRestart(op *protocol.Operation) error {
	for _, t := range op.Targets {
		if t.AgentID == "server" || t.Status == protocol.TargetRejected {
			continue
		}
		ag, err := a.Store.Agent(t.AgentID)
		if err != nil {
			continue
		}
		if !ag.ManagedReady {
			_ = a.Store.MarkTargetAndJob(op.ID, t.AgentID, protocol.TargetUnsupported, "unmanaged", "unmanaged worker cannot restart itself; install the service host first", false, nil)
			continue
		}
		_ = a.Store.UpdateTarget(op.ID, t.AgentID, t.Status, "restart.queued", "managed restart authorized", "", true, nil)
	}
	return nil
}

func matchArtifact(arts []map[string]any, osn, arch string) map[string]any {
	osn, arch = strings.ToLower(osn), strings.ToLower(arch)
	for _, art := range arts {
		if strings.EqualFold(fmt.Sprint(art["os"]), osn) && strings.EqualFold(fmt.Sprint(art["arch"]), arch) {
			return art
		}
	}
	return nil
}

func (a *App) retrySelected(op *protocol.Operation, req protocol.SubmitOperation, s *storage.Session) error {
	parent, _ := req.Params["operation_id"].(string)
	if parent == "" {
		return fmt.Errorf("operation_id required")
	}
	orig, err := a.Store.Operation(parent)
	if err != nil {
		return err
	}
	_ = a.Store.UpdateTarget(op.ID, "server", protocol.TargetSucceeded, "linked_attempt", "retry linked to parent", "", false, map[string]any{
		"parent_id": orig.ID, "parent_action": orig.Action,
	})
	return nil
}

func pendingMigration(st *storage.Store, agentID string) *protocol.MigrationPlan {
	id, err := st.Setting("active_migration_id")
	if err != nil || id == "" {
		return nil
	}
	plan, err := st.Migration(id)
	if err != nil {
		return nil
	}
	var n int
	if err := st.DB.QueryRow(`SELECT COUNT(*) FROM migration_targets WHERE plan_id=? AND agent_id=?`, id, agentID).Scan(&n); err != nil || n != 1 {
		return nil
	}
	if plan.Mode == "retired" || !plan.ExpiresAt.After(st.Clock.Now()) {
		return nil
	}
	return plan
}

func encodePlan(p *protocol.MigrationPlan) string {
	b, _ := json.Marshal(p)
	return string(b)
}
