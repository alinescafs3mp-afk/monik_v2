package rules

import (
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
)

type Rule struct {
	Name       string
	Metric     string
	Warning    *float64
	Critical   *float64
	PersistFor time.Duration
	RecoverFor time.Duration
	MinCover   float64
}

func DefaultRules() []Rule {
	wCPU, cCPU := 85.0, 95.0
	wRAM, cRAM := 85.0, 95.0
	wDisk, cDisk := 85.0, 95.0
	return []Rule{
		{Name: "cpu_high", Metric: "cpu", Warning: &wCPU, Critical: &cCPU, PersistFor: 60 * time.Second, RecoverFor: 30 * time.Second},
		{Name: "ram_high", Metric: "ram", Warning: &wRAM, Critical: &cRAM, PersistFor: 60 * time.Second, RecoverFor: 30 * time.Second},
		{Name: "disk_high", Metric: "disk", Warning: &wDisk, Critical: &cDisk, PersistFor: 60 * time.Second, RecoverFor: 30 * time.Second},
	}
}

type Breach struct {
	Metric   string
	Severity string
	Value    float64
	Reason   string
}

func EvaluateHost(h *protocol.HostMetrics, rules []Rule) []Breach {
	var out []Breach
	for _, r := range rules {
		var v *float64
		switch r.Metric {
		case "cpu":
			v = h.CPUPercent
		case "ram":
			if h.RAMTotal > 0 {
				p := float64(h.RAMUsed) / float64(h.RAMTotal) * 100
				v = &p
			}
		case "disk":
			var max float64
			for _, d := range h.Disks {
				if d.UsedPct > max {
					max = d.UsedPct
				}
			}
			if len(h.Disks) > 0 {
				v = &max
			}
		}
		if v == nil {
			continue
		}
		if r.Critical != nil && *v >= *r.Critical {
			out = append(out, Breach{Metric: r.Metric, Severity: "critical", Value: *v, Reason: r.Name})
		} else if r.Warning != nil && *v >= *r.Warning {
			out = append(out, Breach{Metric: r.Metric, Severity: "warning", Value: *v, Reason: r.Name})
		}
	}
	return out
}

func AgentFreshness(lastLive *time.Time, now time.Time) (state, reason string) {
	if lastLive == nil {
		return "unknown", "no live report yet"
	}
	age := now.Sub(*lastLive)
	if age > protocol.UnreachableContact {
		return "unreachable", "no live report for 30s"
	}
	if age > protocol.StaleContact {
		return "stale", "no live report for 15s"
	}
	return "ok", ""
}
