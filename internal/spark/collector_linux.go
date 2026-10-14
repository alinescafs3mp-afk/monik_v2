//go:build linux

package spark

import (
	"bufio"
	"context"
	"encoding/csv"
	"fmt"
	"math"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
	"github.com/shirou/gopsutil/v4/mem"
	"golang.org/x/sys/unix"
)

type Collector struct {
	started                       time.Time
	hostname, osVersion, cpuModel string
	lastTotal, lastIdle           uint64
	cpuBaseline                   bool
	lastCounters                  map[string]uint64
	lastRates                     time.Time
	lastProcCPU                   float64
	lastProcAt                    time.Time
	lastDisk                      time.Time
	disks                         []protocol.Disk
	diskQuality                   protocol.Capability
	addresses                     []string
	lastAddresses                 time.Time
	mu                            sync.Mutex
	gpu                           []protocol.SparkMetric
	gpuAt                         time.Time
	gpuReason                     string
	topology                      string
}

func NewCollector() *Collector {
	hostname, _ := os.Hostname()
	c := &Collector{started: time.Now(), hostname: hostname, lastCounters: map[string]uint64{}, topology: "unknown", gpuReason: "gpu_probe_not_yet_observed"}
	if b, e := readBoundedFile("/etc/os-release", 64<<10); e == nil {
		for _, line := range strings.Split(string(b), "\n") {
			if v, ok := strings.CutPrefix(line, "PRETTY_NAME="); ok {
				c.osVersion = strings.Trim(v, "\"")
			}
		}
	}
	if b, e := readBoundedFile("/proc/cpuinfo", 1<<20); e == nil {
		for _, line := range strings.Split(string(b), "\n") {
			key, value, ok := strings.Cut(line, ":")
			if ok && member(strings.TrimSpace(key), "model name", "Hardware", "Processor") {
				c.cpuModel = strings.TrimSpace(value)
				break
			}
		}
	}
	if b, e := os.ReadFile("/sys/firmware/devicetree/base/model"); e == nil && strings.Contains(strings.ToLower(string(b)), "dgx spark") {
		c.topology = "unified"
	}
	return c
}
func observation(value *float64, unit, source string, at time.Time, quality, reason string) protocol.SparkObservation {
	return protocol.SparkObservation{Value: value, Unit: unit, Source: source, ObservedAt: at, Quality: quality, Reason: reason}
}
func metric(id, unit, source string, at time.Time, value *float64, reason string) protocol.SparkMetric {
	q := "ok"
	if value == nil {
		q = "unknown"
		if reason == "" {
			reason = "source_unavailable"
		}
	} else {
		// The reason supplied to this helper explains an unavailable sample.
		// A later valid counter or meminfo value has recovered from that cause.
		reason = ""
	}
	return protocol.SparkMetric{MetricID: id, Observation: observation(value, unit, source, at, q, reason)}
}
func number(v float64) *float64 { return &v }
func u64number(v *uint64) *float64 {
	if v == nil {
		return nil
	}
	return number(float64(*v))
}
func (c *Collector) Snapshot(now time.Time) (*protocol.HostMetrics, map[string]protocol.Capability, *protocol.SparkExtensionV1) {
	h := &protocol.HostMetrics{Hostname: c.hostname, OS: runtime.GOOS, OSVersion: c.osVersion, Arch: runtime.GOARCH, CPUModel: c.cpuModel, CPULogical: runtime.NumCPU(), AgentUptime: time.Since(c.started).Seconds()}
	caps := map[string]protocol.Capability{}
	s := &protocol.SparkExtensionV1{SchemaVersion: 1, MemoryTopology: "unknown", Metrics: []protocol.SparkMetric{}}
	if b, e := readBoundedFile("/proc/uptime", 4096); e == nil {
		fields := strings.Fields(string(b))
		if len(fields) > 0 {
			h.SystemUptime, _ = strconv.ParseFloat(fields[0], 64)
		}
	}
	if b, e := readBoundedFile("/proc/stat", 256<<10); e == nil {
		sc := bufio.NewScanner(strings.NewReader(string(b)))
		if sc.Scan() {
			fields := strings.Fields(sc.Text())
			if len(fields) >= 9 && fields[0] == "cpu" {
				var total, idle uint64
				valid := true
				for i := 1; i <= 8; i++ {
					v, e := strconv.ParseUint(fields[i], 10, 64)
					if e != nil {
						valid = false
						break
					}
					total += v
					if i == 4 || i == 5 {
						idle += v
					}
				}
				if valid {
					if c.cpuBaseline && total > c.lastTotal && idle >= c.lastIdle && total-c.lastTotal >= idle-c.lastIdle {
						h.CPUPercent = number(100 * float64((total-c.lastTotal)-(idle-c.lastIdle)) / float64(total-c.lastTotal))
					}
					c.lastTotal = total
					c.lastIdle = idle
					c.cpuBaseline = true
				}
			}
		}
	}
	s.Metrics = append(s.Metrics, metric("cpu.usage_percent", "percent", "proc.stat", now, h.CPUPercent, "first_sample_or_counter_reset"))
	caps["cpu"] = protocol.Capability{Status: protocol.CapSupported}
	if b, e := readBoundedFile("/proc/loadavg", 4096); e == nil {
		f := strings.Fields(string(b))
		if len(f) > 0 {
			v, e := strconv.ParseFloat(f[0], 64)
			if e == nil {
				s.Metrics = append(s.Metrics, metric("cpu.load1", "load", "proc.loadavg", now, number(v), ""))
			}
		}
	}
	memory := Memory{}
	if file, e := os.Open("/proc/meminfo"); e == nil {
		memory, _ = ParseMeminfo(file)
		file.Close()
	}
	for _, v := range []struct {
		id string
		n  *uint64
	}{{"memory.total_bytes", memory.TotalBytes}, {"memory.available_bytes", memory.AvailableBytes}, {"memory.pressure_used_bytes", memory.UsedPressureBytes}, {"memory.swap_total_bytes", memory.SwapTotalBytes}, {"memory.swap_free_bytes", memory.SwapFreeBytes}} {
		s.Metrics = append(s.Metrics, metric(v.id, "bytes", "proc.meminfo", now, u64number(v.n), "meminfo_field_unavailable"))
	}
	// Keep the legacy host history's gopsutil Used definition. The separate
	// pressure-used metric explicitly means MemTotal-MemAvailable.
	if vm, e := mem.VirtualMemory(); e == nil {
		h.RAMTotal = int64(vm.Total)
		h.RAMUsed = int64(vm.Used)
		h.RAMAvailable = int64(vm.Available)
		caps["memory"] = protocol.Capability{Status: protocol.CapSupported}
	} else {
		caps["memory"] = protocol.Capability{Status: protocol.CapError, Reason: "proc_memory_unavailable"}
	}
	if b, e := readBoundedFile("/proc/pressure/memory", 8192); e == nil {
		for _, line := range strings.Split(string(b), "\n") {
			f := strings.Fields(line)
			if len(f) < 2 {
				continue
			}
			for _, cell := range f[1:] {
				key, raw, ok := strings.Cut(cell, "=")
				if ok && key == "avg10" {
					if v, e := strconv.ParseFloat(raw, 64); e == nil {
						s.Metrics = append(s.Metrics, metric("memory.psi."+f[0]+".avg10", "percent", "proc.pressure.memory", now, number(v), ""))
					}
				}
			}
		}
	}
	h.Temperatures = c.temperatures(now)
	caps["temperature"] = protocol.Capability{Status: protocol.CapSupported}
	if len(h.Temperatures) == 0 {
		caps["temperature"] = protocol.Capability{Status: protocol.CapPartial, Reason: "no_readable_hwmon_sensors"}
	}
	if c.lastDisk.IsZero() || now.Sub(c.lastDisk) >= 30*time.Second {
		c.collectDisks(now)
		c.lastDisk = now
	}
	h.Disks = append([]protocol.Disk(nil), c.disks...)
	caps["disk"] = c.diskQuality
	if c.lastAddresses.IsZero() || now.Sub(c.lastAddresses) >= time.Minute {
		c.addresses = nil
		if addrs, e := net.InterfaceAddrs(); e == nil {
			for _, a := range addrs {
				ip, _, e := net.ParseCIDR(a.String())
				if e == nil && !ip.IsLoopback() && len(c.addresses) < 32 {
					c.addresses = append(c.addresses, ip.String())
				}
			}
		}
		c.lastAddresses = now
	}
	h.Addresses = append([]string(nil), c.addresses...)
	s.Metrics = append(s.Metrics, c.rates(now)...)
	s.Metrics = append(s.Metrics, c.footprint(now)...)
	c.mu.Lock()
	s.MemoryTopology = c.topology
	gpu := append([]protocol.SparkMetric(nil), c.gpu...)
	gpuAt := c.gpuAt
	gpuReason := c.gpuReason
	c.mu.Unlock()
	if len(gpu) == 0 {
		s.Metrics = append(s.Metrics, protocol.SparkMetric{MetricID: "gpu.utilization_percent", Observation: observation(nil, "percent", "nvidia-smi", now, "unknown", gpuReason)})
	} else {
		for _, m := range gpu {
			if now.Sub(gpuAt) > 15*time.Second && m.Observation.Value != nil {
				m.Observation.Quality = "stale"
				m.Observation.Reason = "gpu_probe_stale"
			}
			s.Metrics = append(s.Metrics, m)
		}
	}
	return h, caps, s
}
func (c *Collector) temperatures(now time.Time) []protocol.Temperature {
	out := []protocol.Temperature{}
	paths, _ := filepath.Glob("/sys/class/hwmon/hwmon*/temp*_input")
	for _, p := range paths {
		if len(out) >= 64 {
			break
		}
		b, e := os.ReadFile(p)
		if e != nil {
			continue
		}
		raw, e := strconv.ParseFloat(strings.TrimSpace(string(b)), 64)
		if e != nil || math.IsNaN(raw) || math.IsInf(raw, 0) {
			continue
		}
		name, _ := os.ReadFile(filepath.Join(filepath.Dir(p), "name"))
		label, _ := os.ReadFile(strings.TrimSuffix(p, "_input") + "_label")
		id := filepath.Base(filepath.Dir(p)) + "." + strings.TrimSuffix(filepath.Base(p), "_input")
		text := strings.TrimSpace(string(name)) + " " + strings.TrimSpace(string(label))
		if len(text) > 128 {
			text = text[:128]
		}
		v := raw / 1000
		out = append(out, protocol.Temperature{Source: "sys.hwmon:" + id, Label: strings.TrimSpace(text), Celsius: &v, Observed: now, Quality: protocol.QualityOK})
	}
	return out
}
func (c *Collector) collectDisks(now time.Time) {
	c.disks = nil
	b, e := readBoundedFile("/proc/self/mountinfo", 256<<10)
	if e != nil {
		c.diskQuality = protocol.Capability{Status: protocol.CapError, Reason: "mount_inventory_unavailable"}
		return
	}
	devices := map[string]bool{}
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) < 8 {
			continue
		}
		separator := 0
		for i, v := range f {
			if v == "-" {
				separator = i
				break
			}
		}
		if separator == 0 || separator+2 >= len(f) {
			continue
		}
		fs := f[separator+1]
		if !member(fs, "ext4", "xfs", "btrfs", "zfs", "f2fs", "vfat", "ntfs3") {
			continue
		}
		if devices[f[2]] {
			continue
		}
		mount := strings.NewReplacer("\\040", " ", "\\011", "\t", "\\134", "\\").Replace(f[4])
		var st unix.Statfs_t
		if e = unix.Statfs(mount, &st); e != nil {
			continue
		}
		devices[f[2]] = true
		total := int64(st.Blocks) * st.Bsize
		available := int64(st.Bavail) * st.Bsize
		used := int64(st.Blocks-st.Bfree) * st.Bsize
		pct := 0.0
		if total > 0 {
			pct = 100 * float64(used) / float64(total)
		}
		c.disks = append(c.disks, protocol.Disk{Mount: mount, FS: fs, Total: total, Available: available, Used: used, UsedPct: pct})
		if len(c.disks) >= 32 {
			break
		}
	}
	c.diskQuality = protocol.Capability{Status: protocol.CapSupported, LastSuccess: &now}
	if len(c.disks) == 0 {
		c.diskQuality = protocol.Capability{Status: protocol.CapPartial, Reason: "no_readable_supported_filesystems"}
	}
}
func (c *Collector) rates(now time.Time) []protocol.SparkMetric {
	current := map[string]uint64{}
	if b, e := readBoundedFile("/proc/net/dev", 128<<10); e == nil {
		for _, line := range strings.Split(string(b), "\n") {
			name, v, ok := strings.Cut(line, ":")
			f := strings.Fields(v)
			if ok && strings.TrimSpace(name) != "lo" && len(f) >= 9 {
				rx, _ := strconv.ParseUint(f[0], 10, 64)
				tx, _ := strconv.ParseUint(f[8], 10, 64)
				current["network.rx_bytes_per_second"] += rx
				current["network.tx_bytes_per_second"] += tx
			}
		}
	}
	if b, e := readBoundedFile("/proc/diskstats", 128<<10); e == nil {
		for _, line := range strings.Split(string(b), "\n") {
			f := strings.Fields(line)
			if len(f) < 14 || (!strings.HasPrefix(f[2], "nvme") && !strings.HasPrefix(f[2], "sd") && !strings.HasPrefix(f[2], "vd")) {
				continue
			}
			device := f[2]
			if strings.Contains(device, "p") && strings.HasPrefix(device, "nvme") {
				continue
			}
			r, _ := strconv.ParseUint(f[5], 10, 64)
			w, _ := strconv.ParseUint(f[9], 10, 64)
			current["disk."+device+".read_bytes_per_second"] = r * 512
			current["disk."+device+".write_bytes_per_second"] = w * 512
			if len(current) > 66 {
				break
			}
		}
	}
	out := []protocol.SparkMetric{}
	for id, value := range current {
		var rate *float64
		if prior, ok := c.lastCounters[id]; ok && !c.lastRates.IsZero() && now.After(c.lastRates) && value >= prior {
			rate = number(float64(value-prior) / now.Sub(c.lastRates).Seconds())
		}
		out = append(out, metric(id, "bytes/second", "proc.counters", now, rate, "first_sample_or_counter_reset"))
	}
	c.lastRates = now
	c.lastCounters = current
	return out
}
func (c *Collector) footprint(now time.Time) []protocol.SparkMetric {
	out := []protocol.SparkMetric{metric("agent.goroutines", "count", "go.runtime", now, number(float64(runtime.NumGoroutine())), "")}
	if f, e := os.ReadDir("/proc/self/fd"); e == nil {
		out = append(out, metric("agent.fd_count", "count", "proc.self.fd", now, number(float64(len(f))), ""))
	}
	if b, e := readBoundedFile("/proc/self/smaps_rollup", 64<<10); e == nil {
		for _, line := range strings.Split(string(b), "\n") {
			f := strings.Fields(line)
			if len(f) >= 2 && member(f[0], "Rss:", "Pss:") {
				n, e := strconv.ParseFloat(f[1], 64)
				if e == nil {
					out = append(out, metric("agent."+strings.ToLower(strings.TrimSuffix(f[0], ":"))+"_bytes", "bytes", "proc.self.smaps_rollup", now, number(n*1024), ""))
				}
			}
		}
	}
	var self, children unix.Rusage
	if unix.Getrusage(unix.RUSAGE_SELF, &self) == nil && unix.Getrusage(unix.RUSAGE_CHILDREN, &children) == nil {
		usage := float64(self.Utime.Sec+self.Stime.Sec+children.Utime.Sec+children.Stime.Sec) + float64(self.Utime.Usec+self.Stime.Usec+children.Utime.Usec+children.Stime.Usec)/1e6
		at := time.Now()
		var percent *float64
		if !c.lastProcAt.IsZero() && at.After(c.lastProcAt) && usage >= c.lastProcCPU {
			percent = number(100 * (usage - c.lastProcCPU) / at.Sub(c.lastProcAt).Seconds())
		}
		cpu := metric("agent.cpu_core_percent", "percent_one_core", "getrusage.self_and_reaped_children", now, percent, "first_sample_or_counter_reset")
		if percent != nil {
			// This source includes reaped children, but not a still-running helper.
			cpu.Observation.Reason = "active_helper_cpu_not_yet_reaped"
		}
		out = append(out, cpu)
		c.lastProcAt = at
		c.lastProcCPU = usage
	}
	return out
}
func (c *Collector) RunGPU(ctx context.Context) {
	delay := time.NewTimer(0)
	defer delay.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-delay.C:
		}
		probe, end := context.WithTimeout(ctx, 2*time.Second)
		metrics, unified, e := queryGPU(probe)
		end()
		c.mu.Lock()
		if e != nil {
			c.gpuReason = "gpu_probe_unavailable"
		} else {
			c.gpu = metrics
			c.gpuAt = time.Now().UTC()
			c.gpuReason = ""
			if unified {
				c.topology = "unified"
			}
		}
		c.mu.Unlock()
		period := 5 * time.Second
		if e != nil {
			period = time.Minute
		}
		delay.Reset(period)
	}
}
func queryGPU(ctx context.Context) ([]protocol.SparkMetric, bool, error) {
	fields := []string{"index", "name", "utilization.gpu", "utilization.memory", "temperature.gpu", "power.draw", "clocks.sm", "clocks.mem", "memory.total", "memory.used"}
	b, e := fixedCommand(ctx, "/usr/bin/nvidia-smi", []string{"--query-gpu=" + strings.Join(fields, ","), "--format=csv,noheader,nounits"}, 0, 32<<10)
	if e != nil {
		return nil, false, e
	}
	rows, e := csv.NewReader(strings.NewReader(string(b))).ReadAll()
	if e != nil || len(rows) > 8 {
		return nil, false, fmt.Errorf("invalid bounded NVIDIA query")
	}
	out := []protocol.SparkMetric{}
	unified := false
	at := time.Now().UTC()
	names := []string{"utilization_percent", "memory_interface_percent", "temperature_celsius", "power_watts", "clock_sm_mhz", "clock_memory_mhz", "framebuffer_total_bytes", "framebuffer_used_bytes"}
	units := []string{"percent", "percent", "celsius", "watts", "MHz", "MHz", "bytes", "bytes"}
	for _, row := range rows {
		if len(row) != len(fields) {
			return nil, false, fmt.Errorf("unexpected NVIDIA fields")
		}
		index := strings.TrimSpace(row[0])
		if _, e = strconv.ParseUint(index, 10, 32); e != nil {
			return nil, false, fmt.Errorf("GPU index invalid")
		}
		gb10 := strings.Contains(strings.ToLower(row[1]), "gb10")
		unified = unified || gb10
		for i, name := range names {
			cell := strings.Trim(strings.ToLower(strings.TrimSpace(row[i+2])), "[]")
			obs := observation(nil, units[i], "nvidia-smi:"+fields[i+2], at, "unknown", "driver_unavailable")
			if gb10 && i >= 6 {
				obs.Quality = "unsupported"
				obs.Reason = "gb10_unified_memory_no_dedicated_framebuffer"
			} else if cell == "not supported" {
				obs.Quality = "unsupported"
				obs.Reason = "driver_unsupported"
			} else if v, e := strconv.ParseFloat(cell, 64); e == nil && !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0 {
				if i == 0 || i == 1 {
					if v > 100 {
						out = append(out, protocol.SparkMetric{MetricID: "gpu." + index + "." + name, Observation: obs})
						continue
					}
				}
				if i >= 6 {
					v *= 1024 * 1024
				}
				obs.Value = &v
				obs.Quality = "ok"
				obs.Reason = ""
			}
			out = append(out, protocol.SparkMetric{MetricID: "gpu." + index + "." + name, Observation: obs})
		}
	}
	return out, unified, nil
}
