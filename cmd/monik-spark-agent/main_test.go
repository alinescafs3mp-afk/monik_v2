//go:build linux

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alinescafs3mp-afk/monik_v2/internal/agent/configfile"
)

func TestServiceStatusRequiresExactInstalledSparkIdentity(t *testing.T) {
	for _, state := range []configfile.File{
		{Managed: false, AgentFlavor: "spark-light", RuntimeMode: "direct"},
		{Managed: true, AgentFlavor: "general", RuntimeMode: "direct"},
		{Managed: true, AgentFlavor: "spark-light", RuntimeMode: "supervised"},
	} {
		path := filepath.Join(t.TempDir(), "agent.json")
		data, _ := json.Marshal(state)
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		if err := serviceCommand([]string{"status", "--config", path}); err == nil {
			t.Fatal("uninstalled/wrong-flavor status returned fake success")
		}
	}
	if err := serviceCommand(nil); err == nil || !strings.Contains(err.Error(), "rollback") {
		t.Fatal("service subcommand help omitted rollback")
	}
}
