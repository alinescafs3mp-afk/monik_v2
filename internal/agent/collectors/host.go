package collectors

import (
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/mem"
)

type Host struct {
	mu       sync.Mutex
	lastCPU  []float64
	lastTime time.Time
	started  time.Time
}

func NewHost() *Host { return &Host{started: time.Now()} }

func (h *Host) Snapshot(now time.Time) (*protocol.HostMetrics, map[string]protocol.Capability) {
	caps := map[string]protocol.Capability{}
	hi, _ := host.Info()
	hn, _ := os.Hostname()
	out := &protocol.HostMetrics{
		Hostname: hn, OS: runtime.GOOS, Arch: runtime.GOARCH,
		CPULogical: runtime.NumCPU(), AgentUptime: now.Sub(h.started).Seconds(),
	}
	if hi != nil {
		out.OSVersion = strings.TrimSpace(hi.Platform + " " + hi.PlatformVersion)
		out.SystemUptime = float64(hi.Uptime)
	}
	if infos, err := cpu.Info(); err == nil && len(infos) > 0 {
		out.CPUModel = infos[0].ModelName
	}
	addrs, _ := localAddrs()
	out.Addresses = addrs

	pcts, err := cpu.Percent(0, false)
	if err != nil {
		caps["cpu"] = protocol.Capability{Status: protocol.CapError, Reason: err.Error()}
	} else if len(pcts) == 0 || (h.lastTime.IsZero()) {
		out.CPUPercent = nil
		caps["cpu"] = protocol.Capability{Status: protocol.CapSupported, Reason: "first sample has no baseline"}
		h.lastTime = now
	} else {
		v := pcts[0]
		out.CPUPercent = &v
		t := now
		caps["cpu"] = protocol.Capability{Status: protocol.CapSupported, LastSuccess: &t}
	}

	vm, err := mem.VirtualMemory()
	if err != nil {
		caps["memory"] = protocol.Capability{Status: protocol.CapError, Reason: err.Error()}
	} else {
		out.RAMTotal = int64(vm.Total)
		out.RAMAvailable = int64(vm.Available)
		out.RAMUsed = int64(vm.Used)
		t := now
		caps["memory"] = protocol.Capability{Status: protocol.CapSupported, LastSuccess: &t}
	}

	temps, capT := temperatures(now)
	out.Temperatures = temps
	caps["temperature"] = capT

	disks, capD := disks(now)
	out.Disks = disks
	caps["disk"] = capD
	return out, caps
}

func localAddrs() ([]string, error) {
	return listIPs()
}

func listIPs() ([]string, error) {
	// filled in net.go via net.Interfaces to avoid pulling extra deps
	return listIPsImpl()
}
