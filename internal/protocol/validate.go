package protocol

import (
	"fmt"
	"net"
	"strconv"
)

func ValidateAgentConfig(c AgentConfig) error {
	if len(c.DiscoveryDisabledTargets) > 256 {
		return fmt.Errorf("too many discovery exclusions")
	}
	for _, t := range c.DiscoveryDisabledTargets {
		h, p, e := net.SplitHostPort(t)
		n, _ := strconv.Atoi(p)
		if e != nil || net.ParseIP(h) == nil || n < 1 || n > 65535 {
			return fmt.Errorf("invalid numeric discovery exclusion")
		}
	}
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
	services := map[string]bool{}
	for _, d := range c.Checks {
		if d.ID == "" || d.ServiceID == "" || seen[d.ID] {
			return fmt.Errorf("check/service identity missing or duplicated")
		}
		seen[d.ID] = true
		if services[d.ServiceID] {
			return fmt.Errorf("only one primary check per service is supported")
		}
		services[d.ServiceID] = true
		if err := ValidateCheck(d); err != nil {
			return fmt.Errorf("check %s: %w", d.ID, err)
		}
	}
	return nil
}
