package actions

import "fmt"

type Scope string

const (
	ScopeServer    Scope = "server"
	ScopeAgents    Scope = "agents"
	ScopeServerJob Scope = "server_job"
)

type Def struct {
	ID                           string
	Scope                        Scope
	Risk                         string
	RequiresCapability           string
	DurableOperation             bool
	IdempotencyRequired          bool
	RecentAuthenticationRequired bool
	AuthoritativeCompletion      string
}

var Registry = map[string]Def{
	"session.revoke_others":  {ID: "session.revoke_others", Scope: ScopeServer, Risk: "sensitive", IdempotencyRequired: true, RecentAuthenticationRequired: true, AuthoritativeCompletion: "other_sessions_revoked"},
	"maintenance.cancel":     {ID: "maintenance.cancel", Scope: ScopeServer, Risk: "config", IdempotencyRequired: true, AuthoritativeCompletion: "committed_cancellation_interval"},
	"enrollment.window.set":  {ID: "enrollment.window.set", Scope: ScopeServer, Risk: "sensitive", IdempotencyRequired: true, RecentAuthenticationRequired: true, AuthoritativeCompletion: "committed_admission_window"},
	"incident.unacknowledge": {ID: "incident.unacknowledge", Scope: ScopeServer, Risk: "reversible", IdempotencyRequired: true, AuthoritativeCompletion: "commit_unread"},

	"enrollment.approve":       {ID: "enrollment.approve", Scope: ScopeServer, Risk: "sensitive", IdempotencyRequired: true, RecentAuthenticationRequired: true, AuthoritativeCompletion: "committed_owner_approval"},
	"enrollment.reject":        {ID: "enrollment.reject", Scope: ScopeServer, Risk: "sensitive", IdempotencyRequired: true, RecentAuthenticationRequired: true, AuthoritativeCompletion: "committed_owner_rejection"},
	"preference.save":          {ID: "preference.save", Scope: ScopeServer, Risk: "reversible", DurableOperation: false, IdempotencyRequired: true, AuthoritativeCompletion: "commit"},
	"profile.apply":            {ID: "profile.apply", Scope: ScopeAgents, Risk: "config", RequiresCapability: "monitoring_configuration", DurableOperation: true, IdempotencyRequired: true, AuthoritativeCompletion: "applied_revision_hash"},
	"rule.save":                {ID: "rule.save", Scope: ScopeServer, Risk: "config", DurableOperation: false, IdempotencyRequired: true, AuthoritativeCompletion: "commit_effective_rule"},
	"maintenance.set":          {ID: "maintenance.set", Scope: ScopeServer, Risk: "reversible", DurableOperation: false, IdempotencyRequired: true, AuthoritativeCompletion: "commit_interval"},
	"incident.acknowledge":     {ID: "incident.acknowledge", Scope: ScopeServer, Risk: "reversible", DurableOperation: false, IdempotencyRequired: true, AuthoritativeCompletion: "commit_ack"},
	"agent.collect_now":        {ID: "agent.collect_now", Scope: ScopeAgents, Risk: "read_only", RequiresCapability: "monitoring_configuration", DurableOperation: true, IdempotencyRequired: true, AuthoritativeCompletion: "fresh_job_result"},
	"agent.discover_now":       {ID: "agent.discover_now", Scope: ScopeAgents, Risk: "read_only", RequiresCapability: "monitoring_configuration", DurableOperation: true, IdempotencyRequired: true, AuthoritativeCompletion: "discovery_receipt"},
	"agent.diagnostics":        {ID: "agent.diagnostics", Scope: ScopeAgents, Risk: "read_only", RequiresCapability: "bounded_diagnostics", DurableOperation: true, IdempotencyRequired: true, AuthoritativeCompletion: "bounded_redacted_receipt"},
	"agent.restart":            {ID: "agent.restart", Scope: ScopeAgents, Risk: "disruptive", RequiresCapability: "restart_installed_agent", DurableOperation: true, IdempotencyRequired: true, RecentAuthenticationRequired: true, AuthoritativeCompletion: "new_worker_session_and_control"},
	"check.apply":              {ID: "check.apply", Scope: ScopeAgents, Risk: "config", RequiresCapability: "monitoring_configuration", DurableOperation: true, IdempotencyRequired: true, AuthoritativeCompletion: "applied_revision_hash"},
	"check.trial":              {ID: "check.trial", Scope: ScopeAgents, Risk: "read_only", RequiresCapability: "monitoring_configuration", DurableOperation: true, IdempotencyRequired: true, AuthoritativeCompletion: "redacted_trial_result"},
	"secret.replace":           {ID: "secret.replace", Scope: ScopeAgents, Risk: "sensitive", RequiresCapability: "scoped_check_secret_provisioning", DurableOperation: true, IdempotencyRequired: true, RecentAuthenticationRequired: true, AuthoritativeCompletion: "secret_version_applied"},
	"update.import":            {ID: "update.import", Scope: ScopeServerJob, Risk: "sensitive", DurableOperation: true, IdempotencyRequired: true, RecentAuthenticationRequired: true, AuthoritativeCompletion: "verified_catalog_commit"},
	"update.rollout":           {ID: "update.rollout", Scope: ScopeAgents, Risk: "disruptive", RequiresCapability: "immutable_release_v1", DurableOperation: true, IdempotencyRequired: true, RecentAuthenticationRequired: true, AuthoritativeCompletion: "all_frozen_waves_digest_session_and_fresh_observation"},
	"update.rollback":          {ID: "update.rollback", Scope: ScopeAgents, Risk: "disruptive", RequiresCapability: "binary_update", DurableOperation: true, IdempotencyRequired: true, RecentAuthenticationRequired: true, AuthoritativeCompletion: "eligible_prior_build_confirmed"},
	"update.pause":             {ID: "update.pause", Scope: ScopeServerJob, Risk: "config", DurableOperation: true, IdempotencyRequired: true, AuthoritativeCompletion: "same_rollout_revision_paused_durably"},
	"update.resume":            {ID: "update.resume", Scope: ScopeServerJob, Risk: "disruptive", DurableOperation: true, IdempotencyRequired: true, RecentAuthenticationRequired: true, AuthoritativeCompletion: "same_frozen_rollout_revision_resumed_durably"},
	"operation.cancel_pending": {ID: "operation.cancel_pending", Scope: ScopeServerJob, Risk: "config", DurableOperation: true, IdempotencyRequired: true, AuthoritativeCompletion: "cancellable_targets_receipts"},
	"operation.retry_selected": {ID: "operation.retry_selected", Scope: ScopeAgents, Risk: "config", DurableOperation: true, IdempotencyRequired: true, AuthoritativeCompletion: "action_specific_linked_attempt"},
	"rebind.prepare":           {ID: "rebind.prepare", Scope: ScopeAgents, Risk: "config", RequiresCapability: "controller_migration_within_enrolled_identity", DurableOperation: true, IdempotencyRequired: true, AuthoritativeCompletion: "persisted_migration_plan"},
	"rebind.arm":               {ID: "rebind.arm", Scope: ScopeAgents, Risk: "disruptive", RequiresCapability: "controller_migration_within_enrolled_identity", DurableOperation: true, IdempotencyRequired: true, RecentAuthenticationRequired: true, AuthoritativeCompletion: "persisted_trigger_receipt"},
	"rebind.activate":          {ID: "rebind.activate", Scope: ScopeAgents, Risk: "disruptive", RequiresCapability: "controller_migration_within_enrolled_identity", DurableOperation: true, IdempotencyRequired: true, RecentAuthenticationRequired: true, AuthoritativeCompletion: "candidate_committed_exchanges"},
	"rebind.retire":            {ID: "rebind.retire", Scope: ScopeAgents, Risk: "disruptive", RequiresCapability: "controller_migration_within_enrolled_identity", DurableOperation: true, IdempotencyRequired: true, RecentAuthenticationRequired: true, AuthoritativeCompletion: "endpoint_retirement_receipts"},
	"credential.rotate":        {ID: "credential.rotate", Scope: ScopeAgents, Risk: "sensitive", RequiresCapability: "monitoring_configuration", DurableOperation: true, IdempotencyRequired: true, RecentAuthenticationRequired: true, AuthoritativeCompletion: "new_credential_authentication_receipt"},
	"credential.revoke":        {ID: "credential.revoke", Scope: ScopeServer, Risk: "disruptive", DurableOperation: false, IdempotencyRequired: true, RecentAuthenticationRequired: true, AuthoritativeCompletion: "committed_auth_denial"},
	"trust.stage":              {ID: "trust.stage", Scope: ScopeAgents, Risk: "sensitive", RequiresCapability: "controller_migration_within_enrolled_identity", DurableOperation: true, IdempotencyRequired: true, RecentAuthenticationRequired: true, AuthoritativeCompletion: "persisted_scoped_trust"},
	"trust.retire":             {ID: "trust.retire", Scope: ScopeAgents, Risk: "disruptive", RequiresCapability: "controller_migration_within_enrolled_identity", DurableOperation: true, IdempotencyRequired: true, RecentAuthenticationRequired: true, AuthoritativeCompletion: "acknowledged_trust_removal"},
	"backup.create":            {ID: "backup.create", Scope: ScopeServerJob, Risk: "sensitive", DurableOperation: true, IdempotencyRequired: true, RecentAuthenticationRequired: true, AuthoritativeCompletion: "verified_complete_artifact"},
	"history.export":           {ID: "history.export", Scope: ScopeServerJob, Risk: "read_only", DurableOperation: true, IdempotencyRequired: true, AuthoritativeCompletion: "bounded_artifact_ready"},
	"agent.archive":            {ID: "agent.archive", Scope: ScopeServer, Risk: "config", DurableOperation: false, IdempotencyRequired: true, AuthoritativeCompletion: "explicit_lifecycle_policy_commit"},
	"enrollment.create":        {ID: "enrollment.create", Scope: ScopeServer, Risk: "sensitive", DurableOperation: false, IdempotencyRequired: true, AuthoritativeCompletion: "commit"},
	"service.pin":              {ID: "service.pin", Scope: ScopeServer, Risk: "reversible", DurableOperation: false, IdempotencyRequired: true, AuthoritativeCompletion: "commit"},
	"service.hide":             {ID: "service.hide", Scope: ScopeServer, Risk: "reversible", DurableOperation: false, IdempotencyRequired: true, AuthoritativeCompletion: "commit"},
	"service.pause":            {ID: "service.pause", Scope: ScopeAgents, Risk: "config", RequiresCapability: "monitoring_configuration", DurableOperation: true, IdempotencyRequired: true, AuthoritativeCompletion: "applied_revision_hash"},
	"service.ignore":           {ID: "service.ignore", Scope: ScopeAgents, Risk: "config", RequiresCapability: "monitoring_configuration", DurableOperation: true, IdempotencyRequired: true, AuthoritativeCompletion: "applied_revision_hash"},
	"service.rename":           {ID: "service.rename", Scope: ScopeServer, Risk: "reversible", IdempotencyRequired: true, AuthoritativeCompletion: "commit"},
	"agent.rename":             {ID: "agent.rename", Scope: ScopeServer, Risk: "reversible", DurableOperation: false, IdempotencyRequired: true, AuthoritativeCompletion: "commit"},
	"agent.pin":                {ID: "agent.pin", Scope: ScopeServer, Risk: "reversible", DurableOperation: false, IdempotencyRequired: true, AuthoritativeCompletion: "commit"},
}

func Lookup(id string) (Def, error) {
	d, ok := Registry[id]
	if !ok {
		return Def{}, fmt.Errorf("unknown action %q", id)
	}
	return d, nil
}

func IDs() []string {
	out := make([]string, 0, len(Registry))
	for id := range Registry {
		out = append(out, id)
	}
	return out
}
