package actions

import "testing"

func TestRequiredActionsPresent(t *testing.T) {
	need := []string{
		"enrollment.create", "profile.apply", "agent.restart", "update.import", "update.rollout",
		"rebind.prepare", "rebind.activate", "backup.create", "credential.revoke", "operation.cancel_pending",
	}
	for _, id := range need {
		if _, err := Lookup(id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Lookup("shell.exec"); err == nil {
		t.Fatal("arbitrary exec must not exist")
	}
}
