package server

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
	"github.com/alinescafs3mp-afk/monik_v2/internal/storage"
)

func supportsSelective(ag *storage.AgentRow) bool {
	var caps map[string]protocol.Capability
	if json.Unmarshal([]byte(ag.Capabilities), &caps) != nil {
		return false
	}
	return caps["selective_monitor_v1"].Status == protocol.CapSupported
}

// Include the ORIGINAL discovered socket, even if a custom check dials an alias
// or an entirely different local port. These are exclusions, not permissions.
func (a *App) monitoringExclusions(ag *storage.AgentRow, cfg *protocol.AgentConfig) error {
	cfg.DiscoveryDisabledTargets = nil
	if !supportsSelective(ag) {
		return nil
	}
	seen := map[string]bool{}
	for _, d := range cfg.Checks {
		if !d.Paused && !d.Ignored {
			continue
		}
		sv, err := a.Store.Service(d.ServiceID)
		if err != nil || sv.AgentID != ag.ID {
			return fmt.Errorf("disabled service is not owned by this agent")
		}
		if sv.DialTarget != "" {
			seen[sv.DialTarget] = true
		}
	}
	for target := range seen {
		cfg.DiscoveryDisabledTargets = append(cfg.DiscoveryDisabledTargets, target)
	}
	sort.Strings(cfg.DiscoveryDisabledTargets)
	return nil
}
