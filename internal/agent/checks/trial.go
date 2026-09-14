package checks

import (
	"encoding/json"
	"fmt"
	"github.com/alinescafs3mp-afk/monik_v2/internal/netutil"
	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
	"net"
	"strings"
)

// ParseTrial shares the exact schema with scheduled checks; it never dials.
func ParseTrial(params map[string]any) (protocol.CheckDefinition, error) {
	var def protocol.CheckDefinition
	if params == nil {
		return def, fmt.Errorf("trial parameters required")
	}
	clean := map[string]any{}
	for k, v := range params {
		switch k {
		case "trial", "base_revision":
			continue
		default:
			if strings.HasPrefix(k, "_") {
				continue
			}
			clean[k] = v
		}
	}
	data, err := json.Marshal(clean)
	if err != nil {
		return def, err
	}
	def, err = protocol.DecodeCheck(data)
	if err != nil {
		return def, err
	}
	if def.ID == "" {
		def.ID = "trial"
	}
	if def.Method == "" {
		def.Method = "GET"
	}
	if def.Kind == "" {
		def.Kind = "baseline_http"
	}
	if _, ok := params["timeout_seconds"]; !ok {
		def.TimeoutSeconds = 2
	}
	if _, ok := params["interval_seconds"]; !ok {
		def.IntervalSeconds = 5
	}
	u, err := netutil.ParseURL(def.URL)
	if err != nil {
		return def, err
	}
	if ip := net.ParseIP(u.Hostname()); ip != nil && netutil.IsMetadata(ip) && !ip.IsLoopback() {
		return def, fmt.Errorf("metadata/link-local destination denied")
	}
	if err := protocol.ValidateCheck(def); err != nil {
		return def, err
	}
	// A trial is an explicit one-shot diagnostic, not a change to scheduled pause/ignore.
	def.Paused, def.Ignored = false, false
	return def, nil
}
