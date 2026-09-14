package checks

import (
	"encoding/json"
	"fmt"
	"net"
	"strings"

	"github.com/alinescafs3mp-afk/monik_v2/internal/netutil"
	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
)

// ParseTrial validates a proposed one-shot check. It never dials.
func ParseTrial(params map[string]any) (protocol.CheckDefinition, error) {
	var def protocol.CheckDefinition
	if params == nil {
		return def, fmt.Errorf("trial parameters required")
	}
	if _, ok := params["value"]; ok {
		return def, fmt.Errorf("trial must not include secret plaintext")
	}
	raw, _ := params["url"].(string)
	if raw == "" {
		return def, fmt.Errorf("url required")
	}
	u, err := netutil.ParseURL(raw)
	if err != nil {
		return def, err
	}
	method, _ := params["method"].(string)
	if method == "" {
		method = "GET"
	}
	method = strings.ToUpper(method)
	if method != "GET" && method != "HEAD" {
		return def, fmt.Errorf("only GET/HEAD health probes are allowed")
	}
	if ip := net.ParseIP(u.Hostname()); ip != nil && netutil.IsMetadata(ip) && !ip.IsLoopback() {
		return def, fmt.Errorf("metadata or link-local destination denied")
	}
	kind, _ := params["kind"].(string)
	if kind == "" {
		kind = "baseline_http"
	}
	id, _ := params["id"].(string)
	if id == "" {
		id = "trial"
	}
	def = protocol.CheckDefinition{
		ID: id, Kind: kind, URL: raw, Method: method, TimeoutSeconds: 2, IntervalSeconds: 5,
	}
	def.ServiceID, _ = params["service_id"].(string)
	def.DialTarget, _ = params["dial_target"].(string)
	def.HostHeader, _ = params["host_header"].(string)
	def.TLSServerName, _ = params["tls_server_name"].(string)
	def.ExpectText, _ = params["expect_text"].(string)
	def.ExpectJSONPath, _ = params["expect_json_path"].(string)
	def.ExpectJSONValue, _ = params["expect_json_value"].(string)
	def.SecretID, _ = params["secret_id"].(string)
	def.SecretHeader, _ = params["secret_header"].(string)
	def.Path, _ = params["path"].(string)
	if v, ok := params["timeout_seconds"]; ok && fmt.Sprint(v) != "2" {
		return def, fmt.Errorf("this worker supports a two-second probe timeout")
	}
	if v, ok := params["insecure_tls"].(bool); ok {
		def.InsecureTLS = v
	}
	if exp, exists := params["expected_status"]; exists {
		raw, err := json.Marshal(exp)
		if err != nil {
			return def, err
		}
		if err := json.Unmarshal(raw, &def.ExpectedStatus); err != nil {
			return def, fmt.Errorf("expected_status must be an array of integer HTTP codes")
		}
	}
	// Use the same contract as a saved check so preview cannot silently drop fields.
	validated := def
	if validated.ServiceID == "" {
		validated.ServiceID = "trial"
	}
	cfg := protocol.DefaultAgentConfig()
	cfg.Checks = []protocol.CheckDefinition{validated}
	if err := protocol.ValidateAgentConfig(cfg); err != nil {
		return def, err
	}

	return def, nil
}
