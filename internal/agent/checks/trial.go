package checks

import (
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
		method = "HEAD"
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
		kind = "http_health"
	}
	id, _ := params["id"].(string)
	if id == "" {
		id = "trial"
	}
	def = protocol.CheckDefinition{
		ID: id, Kind: kind, URL: raw, Method: method, TimeoutSeconds: 2,
	}
	def.ServiceID, _ = params["service_id"].(string)
	def.DialTarget, _ = params["dial_target"].(string)
	def.HostHeader, _ = params["host_header"].(string)
	def.TLSServerName, _ = params["tls_server_name"].(string)
	def.ExpectText, _ = params["expect_text"].(string)
	def.ExpectJSONPath, _ = params["expect_json_path"].(string)
	def.ExpectJSONValue, _ = params["expect_json_value"].(string)
	def.SecretID, _ = params["secret_id"].(string)
	if v, ok := params["timeout_seconds"].(float64); ok && v > 0 && v <= 5 {
		def.TimeoutSeconds = int(v)
	}
	if v, ok := params["insecure_tls"].(bool); ok {
		def.InsecureTLS = v
	}
	switch exp := params["expected_status"].(type) {
	case []any:
		for _, item := range exp {
			if n, ok := item.(float64); ok {
				def.ExpectedStatus = append(def.ExpectedStatus, int(n))
			}
		}
	case []float64:
		for _, n := range exp {
			def.ExpectedStatus = append(def.ExpectedStatus, int(n))
		}
	}
	return def, nil
}
