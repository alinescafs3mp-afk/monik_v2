package rules

import (
	"fmt"
	"math"
	"time"
)

// HostThreshold is a deliberately small, complete rule contract. Percentages
// refer to CPU utilization, used RAM and the most-filled local filesystem.
// Per-host overrides and arbitrary expressions are not silently accepted.
type HostThreshold struct {
	Metric         string  `json:"metric"`
	Warning        float64 `json:"warning"`
	Critical       float64 `json:"critical"`
	Recovery       float64 `json:"recovery"`
	PersistSeconds int     `json:"persist_seconds"`
	RecoverSeconds int     `json:"recover_seconds"`
}

func DefaultThresholds() []HostThreshold {
	return []HostThreshold{
		{"cpu", 85, 95, 80, 60, 30}, {"ram", 85, 95, 80, 60, 30}, {"disk", 85, 95, 80, 60, 30},
	}
}
func ValidateThresholds(list []HostThreshold) error {
	if len(list) != 3 {
		return fmt.Errorf("exactly one cpu, ram and disk rule is required")
	}
	seen := map[string]bool{}
	for _, r := range list {
		if (r.Metric != "cpu" && r.Metric != "ram" && r.Metric != "disk") || seen[r.Metric] {
			return fmt.Errorf("unknown or duplicate metric")
		}
		seen[r.Metric] = true
		for _, v := range []float64{r.Warning, r.Critical, r.Recovery} {
			if math.IsNaN(v) || math.IsInf(v, 0) {
				return fmt.Errorf("finite thresholds required")
			}
		}
		if r.Recovery < 0 || r.Recovery >= r.Warning || r.Warning < 1 || r.Warning >= r.Critical || r.Critical > 100 {
			return fmt.Errorf("require 0 <= recovery < warning < critical <= 100")
		}
		if r.PersistSeconds < 5 || r.PersistSeconds > 3600 || r.RecoverSeconds < 5 || r.RecoverSeconds > 3600 {
			return fmt.Errorf("durations must be 5..3600 seconds")
		}
	}
	return nil
}
func ThresholdRules(list []HostThreshold) []Rule {
	out := make([]Rule, 0, len(list))
	for _, r := range list {
		w, c := r.Warning, r.Critical
		out = append(out, Rule{Name: r.Metric + "_high", Metric: r.Metric, Warning: &w, Critical: &c, PersistFor: time.Duration(r.PersistSeconds) * time.Second, RecoverFor: time.Duration(r.RecoverSeconds) * time.Second})
	}
	return out
}
