package collectors

import (
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
	"github.com/shirou/gopsutil/v4/sensors"
)

func temperatures(now time.Time) ([]protocol.Temperature, protocol.Capability) {
	stats, err := sensors.SensorsTemperatures()
	if err != nil {
		return nil, protocol.Capability{Status: protocol.CapUnsupported, Reason: err.Error()}
	}
	if len(stats) == 0 {
		return nil, protocol.Capability{Status: protocol.CapUnsupported, Reason: "no sensors exposed"}
	}
	var out []protocol.Temperature
	for _, s := range stats {
		c := s.Temperature
		out = append(out, protocol.Temperature{
			Source: s.SensorKey, Label: s.SensorKey, Celsius: &c, Observed: now, Quality: protocol.QualityOK,
		})
	}
	t := now
	return out, protocol.Capability{Status: protocol.CapSupported, LastSuccess: &t}
}
