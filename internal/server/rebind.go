package server

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/idgen"
	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
	"github.com/alinescafs3mp-afk/monik_v2/internal/secure"
	"github.com/alinescafs3mp-afk/monik_v2/internal/storage"
)

func (a *App) handleRebind(op *protocol.Operation, req protocol.SubmitOperation, s *storage.Session) error {
	switch req.Action {
	case "rebind.prepare":
		candidate, _ := req.Params["candidate_url"].(string)
		if !strings.HasPrefix(candidate, "https://") {
			return fmt.Errorf("candidate_url must be https")
		}
		mode, _ := req.Params["mode"].(string)
		if mode == "" {
			mode = "prepare"
		}
		planID := idgen.New()
		hash := secure.SHA256Bytes([]byte(candidate + a.ControllerID()))
		plan := &protocol.MigrationPlan{
			PlanID: planID, ControllerID: a.ControllerID(), CurrentURL: a.Cfg.AdvertisedURL,
			CandidateURL: candidate, Generation: 1, Mode: mode, PayloadHash: hash,
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
			stage := "rebind.prepared"
			st := protocol.TargetQueued
			if t.Status == protocol.TargetWaitingOffline {
				st = protocol.TargetWaitingOffline
			}
			_ = a.Store.UpdateTarget(op.ID, t.AgentID, st, stage, "migration plan persisted", "", true, map[string]any{
				"plan_id": planID, "candidate_url": candidate, "payload_hash": hash,
			})
		}
		_ = a.Store.SetSetting("active_migration_id", planID)
		return nil
	case "rebind.arm":
		id, _ := req.Params["plan_id"].(string)
		if id == "" {
			id, _ = a.Store.Setting("active_migration_id")
		}
		if err := a.Store.ArmMigration(id, true); err != nil {
			return err
		}
		for _, t := range op.Targets {
			if t.AgentID == "server" {
				continue
			}
			_ = a.Store.UpdateTarget(op.ID, t.AgentID, protocol.TargetQueued, "rebind.armed", "fallback trigger persisted", "", true, map[string]any{"plan_id": id})
		}
		return nil
	case "rebind.activate":
		id, _ := req.Params["plan_id"].(string)
		if id == "" {
			id, _ = a.Store.Setting("active_migration_id")
		}
		plan, err := a.Store.Migration(id)
		if err != nil {
			return err
		}
		plan.Mode = "activate"
		_ = a.Store.SetMigrationMode(id, "activate")
		for _, t := range op.Targets {
			if t.AgentID == "server" {
				continue
			}
			_ = a.Store.SetMigrationTarget(id, t.AgentID, "activating", "")
			_ = a.Store.UpdateTarget(op.ID, t.AgentID, protocol.TargetQueued, "rebind.activating", "waiting for candidate committed exchanges", "", true, map[string]any{
				"plan_id": plan.PlanID, "candidate_url": plan.CandidateURL,
			})
		}
		return nil
	case "rebind.retire":
		id, _ := req.Params["plan_id"].(string)
		if id == "" {
			id, _ = a.Store.Setting("active_migration_id")
		}
		_ = a.Store.SetMigrationMode(id, "retired")
		for _, t := range op.Targets {
			if t.AgentID == "server" {
				continue
			}
			_ = a.Store.UpdateTarget(op.ID, t.AgentID, protocol.TargetSucceeded, "rebind.retired", "endpoint retirement recorded", "", false, nil)
		}
		return nil
	}
	return fmt.Errorf("unknown rebind action")
}

func (a *App) handleRollout(op *protocol.Operation, req protocol.SubmitOperation) error {
	releaseID, _ := req.Params["release_id"].(string)
	if req.Action == "update.resume" {
		for _, t := range op.Targets {
			if t.AgentID == "server" {
				continue
			}
			_ = a.Store.UpdateTarget(op.ID, t.AgentID, protocol.TargetQueued, "update.resumed", "rollout revalidated", "", true, nil)
		}
		return nil
	}
	if releaseID == "" && req.Action != "update.rollback" {
		return fmt.Errorf("release_id required")
	}
	arts, _ := a.Store.ArtifactsByRelease(releaseID)
	for _, t := range op.Targets {
		if t.AgentID == "server" || t.Status == protocol.TargetRejected {
			continue
		}
		ag, err := a.Store.Agent(t.AgentID)
		if err != nil {
			continue
		}
		stage := "update.queued"
		msg := "signed artifact authorized for this target"
		if req.Action == "update.rollback" {
			stage = "update.rollback_queued"
			msg = "eligible prior build requested"
		}
		_ = a.Store.UpdateTarget(op.ID, t.AgentID, t.Status, stage, msg, "", true, map[string]any{
			"release_id": releaseID, "os": ag.OS, "arch": ag.Arch, "artifacts": arts,
		})
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
	if plan.Mode == "retired" {
		return nil
	}
	return plan
}

func encodePlan(p *protocol.MigrationPlan) string {
	b, _ := json.Marshal(p)
	return string(b)
}
