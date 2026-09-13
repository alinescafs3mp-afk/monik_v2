package protocol

import (
	"fmt"
	"github.com/alinescafs3mp-afk/monik_v2/internal/netutil"
	"strings"
)

func ValidateAgentConfig(c AgentConfig) error {
	if c.Intervals.CollectSeconds != 5 || c.Intervals.ReportSeconds != 5 || c.Intervals.CheckSeconds != 5 {
		return fmt.Errorf("this worker supports five-second host/report/check scheduling only")
	}
	if c.Intervals.DiscoverySeconds < 5 || c.Intervals.DiscoverySeconds > 3600 {
		return fmt.Errorf("discovery interval outside 5..3600 seconds")
	}
	if c.Ping.Enabled && c.Ping.Target != "8.8.8.8" {
		return fmt.Errorf("ICMP target is outside the locally authorized policy")
	}
	if len(c.Checks) > 128 {
		return fmt.Errorf("at most 128 check definitions per agent")
	}
	seen := map[string]bool{}
	for _, d := range c.Checks {
		if d.ID == "" || d.ServiceID == "" || seen[d.ID] {
			return fmt.Errorf("check/service identity missing or duplicated")
		}
		seen[d.ID] = true
		if _, err := netutil.ParseURL(d.URL); err != nil {
			return err
		}
		if d.Method != "" && d.Method != "GET" && d.Method != "HEAD" {
			return fmt.Errorf("only GET/HEAD health checks are supported")
		}
		if strings.ContainsAny(d.HostHeader+d.TLSServerName, "\r\n") {
			return fmt.Errorf("invalid host header/server name")
		}
		if d.TimeoutSeconds != 2 || d.IntervalSeconds != 5 {
			return fmt.Errorf("this worker supports a two-second check timeout and five-second check interval only")
		}
		if d.SecretID != "" || d.SecretHeader != "" {
			return fmt.Errorf("secret delivery is not implemented")
		}
		if d.Kind != "" && d.Kind != "baseline_http" && len(d.ExpectedStatus) == 0 {
			return fmt.Errorf("application checks require expected HTTP status codes")
		}
		if d.Kind != "" && d.Kind != "baseline_http" && d.Kind != "http_health" && d.Kind != "configured_http" {
			return fmt.Errorf("unsupported check kind")
		}
	}
	return nil
}
