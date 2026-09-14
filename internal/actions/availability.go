package actions

// UnavailableReason is the remaining fail-closed boundary. Do not remove an
// entry unless the action is implemented and covered by tests.
func UnavailableReason(action string) string {
	switch action {
	case "operation.retry_selected":
		return "Retry orchestration is not implemented; explicitly review and submit a new eligible operation."
	case "update.resume":
		return "There is no persisted rollout batching/resume state machine yet."
	case "rule.save":
		return "Custom rule storage is not connected to the active evaluator; changing it would not change evaluation."
	case "maintenance.set":
		return "Maintenance storage is not connected to incident evaluation/presentation yet."
	}
	return ""
}

func LifecycleConflict(action string) bool {
	switch action {
	case "update.rollout", "update.rollback", "rebind.activate", "agent.restart", "credential.rotate", "trust.retire":
		return true
	}
	return false
}
