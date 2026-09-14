package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAudit8InspectionDoesNotWaitForRegistration(t *testing.T) {
	p := filepath.Join(t.TempDir(), "agent.json")
	os.WriteFile(p, []byte(`{"agent_id":"pending","pending_registration":true,"controller_url":"https://127.0.0.1:1"}`), 0600)
	start := time.Now()
	if cmdDoctor([]string{"--config", p}) != 0 {
		t.Fatal("doctor")
	}
	if cmdController([]string{"show", "--config", p}) != 0 {
		t.Fatal("show")
	}
	if time.Since(start) > time.Second {
		t.Fatal("read-only inspection performed a network wait")
	}
}
func TestAudit8ShellQuoting(t *testing.T) {
	if got := shellQuote("/tmp/O'Brien $(touch x)"); got != "'/tmp/O'\"'\"'Brien $(touch x)'" {
		t.Fatal(got)
	}
}
