package server

import (
	"encoding/json"
	"fmt"
	"github.com/alinescafs3mp-afk/monik_v2/internal/tufutil"
	"path/filepath"
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
			PrimaryLossSeconds: 30,
		}
		if err := a.Store.InsertMigration(plan, op.ID); err != nil {
			return err
		}
		for _, t := range op.Targets {
			if t.AgentID == "server" || t.Status == protocol.TargetRejected {
				continue
			}
			_ = a.Store.SetMigrationTarget(planID, t.AgentID, "queued", "awaiting agent persistence")
			extra := map[string]any{
				"plan_id": planID, "candidate_url": candidate, "payload_hash": hash,
				"generation": plan.Generation, "controller_id": plan.ControllerID,
				"current_url": plan.CurrentURL, "trust_pem": plan.TrustPEM,
				"expires_at": plan.ExpiresAt.Format(time.RFC3339), "primary_loss_seconds": plan.PrimaryLossSeconds,
			}
			if jobID, err := a.Store.JobIDFor(op.ID, t.AgentID); err == nil {
				_ = a.Store.PatchJobParams(jobID, extra)
			}
			stage := "rebind.prepared"
			st := protocol.TargetQueued
			if t.Status == protocol.TargetWaitingOffline {
				st = protocol.TargetWaitingOffline
			}
			_ = a.Store.UpdateTarget(op.ID, t.AgentID, st, stage, "plan saved on controller; awaiting agent persistence; URL unchanged", "", true, extra)
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
			_ = a.Store.SetMigrationTarget(id, t.AgentID, "arm_queued", "awaiting agent persistence")
			_ = a.Store.UpdateTarget(op.ID, t.AgentID, protocol.TargetQueued, "rebind.armed", "fallback trigger queued; awaiting agent persistence", "", true, extra)
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
			_ = a.Store.SetMigrationTarget(id, t.AgentID, "retire_queued", "awaiting agent confirmation")
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
	eligible := false
	for _, target := range op.Targets {
		if target.AgentID != "server" && target.Status != protocol.TargetRejected {
			eligible = true
			break
		}
	}
	if !eligible {
		return a.Store.PublishUpdatePlan(op.ID, nil)
	}
	releaseID, _ := req.Params["release_id"].(string)
	var arts []map[string]any
	var digest string
	if req.Action != "update.rollback" {
		var e error
		var files []tufutil.PublicationFile
		digest, files, e = a.Store.Publication(releaseID)
		if e != nil {
			return fmt.Errorf("release is not immutable: re-import its signed bundle before creating new rollouts")
		}
		dir, e := tufutil.PublicationDir(filepath.Join(a.Cfg.DataDir, "tuf"), digest)
		if e != nil {
			return e
		}
		if e = tufutil.VerifyPublication(dir, digest, files); e != nil {
			return e
		}
		root, e := a.updateRoot()
		if e != nil {
			return e
		}
		// Validity is checked again before queueing. An agent still applies its own
		// high-water protection; selecting an older release cannot bypass that.
		if _, e = tufutil.VerifyRepo(root, dir, tufutil.HighWater{}, a.Clock.Now()); e != nil {
			return e
		}
		arts, e = a.Store.ArtifactsByRelease(releaseID)
		if e != nil {
			return e
		}
	}
	plan := []storage.PreparedUpdateTarget{}
	for _, t := range op.Targets {
		if t.AgentID == "server" || t.Status == protocol.TargetRejected {
			continue
		}
		ag, e := a.Store.Agent(t.AgentID)
		if e != nil {
			return e
		}
		p := storage.PreparedUpdateTarget{AgentID: t.AgentID, Status: t.Status, Stage: "update.queued", Message: "verified release pinned; waiting for agent execution"}
		if !ag.ManagedReady {
			p.Status = protocol.TargetUnsupported
			p.Stage = "unmanaged"
			p.Message = "install a managed service before updating"
			plan = append(plan, p)
			continue
		}
		if req.Action == "update.rollback" {
			p.Stage = "update.rollback_queued"
			p.Evidence = map[string]any{"component": "worker", "previous_session": ag.SessionID}
			plan = append(plan, p)
			continue
		}
		var caps map[string]protocol.Capability
		_ = json.Unmarshal([]byte(ag.Capabilities), &caps)
		if caps["immutable_release_v1"].Status != "supported" {
			p.Status = protocol.TargetUnsupported
			p.Stage = "upgrade_required"
			p.Message = "agent must advertise immutable_release_v1; upgrade this older agent locally once"
			plan = append(plan, p)
			continue
		}
		art := matchArtifact(arts, ag.OS, ag.Arch)
		if art == nil {
			p.Status = protocol.TargetUnsupported
			p.Stage = "no_artifact"
			p.Message = "release has no signed worker for this platform"
			plan = append(plan, p)
			continue
		}
		p.Evidence = map[string]any{"release_id": releaseID, "release_digest": digest, "os": ag.OS, "arch": ag.Arch, "name": art["name"], "sha256": art["sha256"], "length": art["length"], "component": "worker", "previous_session": ag.SessionID}
		plan = append(plan, p)
	}
	if req.Action == "update.rollout" {
		invalid := len(plan) != len(op.Targets)
		for _, p := range plan {
			if p.Status != protocol.TargetQueued && p.Status != protocol.TargetWaitingOffline {
				invalid = true
			}
		}
		if invalid {
			// No partial best-effort rollout after a failed frozen-scope preflight.
			for i := range plan {
				if plan[i].Status == protocol.TargetQueued || plan[i].Status == protocol.TargetWaitingOffline {
					plan[i].Status = protocol.TargetRejected
					plan[i].Stage = "rollout.preflight_blocked"
					plan[i].Message = "Another frozen target failed preflight; review an entirely eligible selection"
				}
			}
			return a.Store.PublishUpdatePlan(op.ID, plan)
		}
		opts := storage.RolloutOptions{BatchSize: 2, ObserveSeconds: 30}
		if n, ok := req.Params["batch_size"].(float64); ok {
			opts.BatchSize = int(n)
		}
		if n, ok := req.Params["observe_seconds"].(float64); ok {
			opts.ObserveSeconds = int(n)
		}
		return a.Store.PublishBatchedUpdatePlan(op.ID, releaseID, plan, opts)
	}
	return a.Store.PublishUpdatePlan(op.ID, plan)
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
		if jobID, err := a.Store.JobIDFor(op.ID, t.AgentID); err == nil {
			if err := a.Store.PatchJobParams(jobID, map[string]any{"previous_session": ag.SessionID}); err != nil {
				return err
			}
		}
		_ = a.Store.UpdateTarget(op.ID, t.AgentID, t.Status, "restart.queued", "managed restart authorized", "", true, nil)
	}
	return nil
}

func matchArtifact(arts []map[string]any, osn, arch string) map[string]any {
	osn, arch = strings.ToLower(osn), strings.ToLower(arch)
	for _, art := range arts {
		want := osn + "-" + arch + "/monik-agent"
		if osn == "windows" {
			want += ".exe"
		}
		if fmt.Sprint(art["name"]) == want && strings.EqualFold(fmt.Sprint(art["os"]), osn) && strings.EqualFold(fmt.Sprint(art["arch"]), arch) {
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
