package actions

// UnavailableReason is the remaining fail-closed boundary. Do not remove an
// entry unless the action is implemented and covered by tests.
func UnavailableReason(action string) string {
	return ""
}

func LifecycleConflict(action string) bool {
	switch action {
	case "update.rollout", "update.rollback", "rebind.activate", "agent.restart", "credential.rotate", "trust.retire":
		return true
	}
	return false
}
