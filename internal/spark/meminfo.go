package spark

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Memory fields are Linux physical-memory observations, not a GPU allocation guarantee.
// UsedPressure is MemTotal-MemAvailable, NOT the old gopsutil Used definition.
type Memory struct {
	TotalBytes        *uint64 `json:"total_bytes"`
	AvailableBytes    *uint64 `json:"available_bytes"`
	UsedPressureBytes *uint64 `json:"used_pressure_bytes"`
	SwapTotalBytes    *uint64 `json:"swap_total_bytes"`
	SwapFreeBytes     *uint64 `json:"swap_free_bytes"`
}

func ParseMeminfo(r io.Reader) (Memory, error) {
	var out Memory
	const maxInput = 128 * 1024
	b, err := io.ReadAll(io.LimitReader(r, maxInput+1))
	if err != nil {
		return out, err
	}
	if len(b) > maxInput {
		return out, fmt.Errorf("meminfo too large")
	}
	wanted := map[string]**uint64{"MemTotal": &out.TotalBytes, "MemAvailable": &out.AvailableBytes, "SwapTotal": &out.SwapTotalBytes, "SwapFree": &out.SwapFreeBytes}
	seen := map[string]bool{}
	s := bufio.NewScanner(strings.NewReader(string(b)))
	for s.Scan() {
		key, rest, found := strings.Cut(s.Text(), ":")
		if !found {
			continue
		}
		dst, ok := wanted[key]
		if !ok {
			continue
		}
		if seen[key] {
			return Memory{}, fmt.Errorf("duplicate %s", key)
		}
		seen[key] = true
		fields := strings.Fields(rest)
		if len(fields) != 2 || fields[1] != "kB" {
			return Memory{}, fmt.Errorf("invalid unit/field %s", key)
		}
		n, e := strconv.ParseUint(fields[0], 10, 64)
		if e != nil || n > ^uint64(0)/1024 {
			return Memory{}, fmt.Errorf("invalid or overflowing %s", key)
		}
		n *= 1024
		*dst = &n
	}
	if err := s.Err(); err != nil {
		return Memory{}, err
	}
	if out.TotalBytes != nil && out.AvailableBytes != nil {
		if *out.AvailableBytes > *out.TotalBytes {
			return Memory{}, fmt.Errorf("available exceeds total")
		}
		used := *out.TotalBytes - *out.AvailableBytes
		out.UsedPressureBytes = &used
	}
	if out.SwapTotalBytes != nil && out.SwapFreeBytes != nil && *out.SwapFreeBytes > *out.SwapTotalBytes {
		return Memory{}, fmt.Errorf("swap free exceeds total")
	}
	return out, nil
}
