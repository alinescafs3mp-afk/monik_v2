//go:build linux

package spark

import (
	"strings"
	"testing"
)

func TestMonitorServiceStatusBindsObservedEntrypointToSavedInstall(t *testing.T) {
	fixture := "LoadState=loaded\nActiveState=active\nSubState=running\nMainPID=23\nExecStart={ path=/var/lib/monik-agent/bin/monik-agent ; argv[]=/var/lib/monik-agent/bin/monik-agent run --config /var/lib/monik-agent/agent.json ; ignore_errors=no ; pid=23 }\n"
	got, err := monitorServiceState("/var/lib/monik-agent/agent.json", "/var/lib/monik-agent", []byte(fixture))
	if err != nil || got.State != "running" || got.PID != 23 {
		t.Fatalf("actual saved install fixture unavailable: %+v %v", got, err)
	}
	for _, changed := range []string{
		strings.Replace(fixture, "run --config /var/lib/monik-agent/agent.json", "run --config /another/identity.json", 1),
		strings.ReplaceAll(fixture, "/var/lib/monik-agent/bin/monik-agent", "/usr/bin/unrelated"),
		fixture + "MainPID=44\n",
		strings.Replace(fixture, "MainPID=23", "MainPID=unknown", 1),
		strings.Replace(fixture, "LoadState=loaded", "LoadState=masked", 1),
	} {
		got, err := monitorServiceState("/var/lib/monik-agent/agent.json", "/var/lib/monik-agent", []byte(changed))
		if err == nil || got.State != "unknown" || got.Reason == "" {
			t.Fatalf("unverifiable monitor claimed an observed state: %+v %v", got, err)
		}
	}
	got, err = monitorServiceState("/var/lib/monik-agent/agent.json", "/var/lib/monik-agent", []byte("LoadState=not-found\nActiveState=inactive\n"))
	if err != nil || got.State != "not_installed" {
		t.Fatalf("absent monitor not classified: %+v %v", got, err)
	}
}
