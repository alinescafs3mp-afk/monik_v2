//go:build linux

package discovery

import (
	"bufio"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func Listeners() ([]Listener, error) {
	var out []Listener
	for _, f := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		ls, err := parseProcNet(f)
		if err != nil {
			if os.IsNotExist(err) && f == "/proc/net/tcp6" {
				continue
			}
			return nil, err
		}
		out = append(out, ls...)
	}
	inodePID := mapFDs()
	for i := range out {
		if pid, ok := inodePID[out[i].Inode]; ok {
			out[i].PID = pid
			out[i].Process = procName(pid)
		}
	}
	return out, nil
}

func parseProcNet(path string) ([]Listener, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	if !sc.Scan() {
		if err := sc.Err(); err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("missing TCP table header")
	}
	header := strings.Fields(sc.Text())
	if len(header) < 4 || header[1] != "local_address" || header[3] != "st" {
		return nil, fmt.Errorf("invalid TCP table header")
	}
	var out []Listener
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 10 {
			return nil, fmt.Errorf("malformed TCP table row")
		}
		if fields[3] != "0A" {
			continue
		}
		ip, port, err := parseAddr(fields[1])
		if err != nil {
			return nil, fmt.Errorf("malformed listening address: %w", err)
		}
		out = append(out, Listener{IP: ip, Port: port, Inode: fields[9]})
	}
	return out, sc.Err()
}

func parseAddr(s string) (net.IP, int, error) {
	parts := strings.Split(s, ":")
	if len(parts) != 2 {
		return nil, 0, fmt.Errorf("bad addr")
	}
	port64, err := strconv.ParseInt(parts[1], 16, 32)
	if err != nil {
		return nil, 0, err
	}
	b, err := hex.DecodeString(parts[0])
	if err != nil {
		return nil, 0, err
	}
	if len(b) == 4 {
		return net.IPv4(b[3], b[2], b[1], b[0]), int(port64), nil
	}
	if len(b) == 16 {
		var nb [16]byte
		for i := 0; i < 16; i += 4 {
			v := binary.LittleEndian.Uint32(b[i : i+4])
			binary.BigEndian.PutUint32(nb[i:i+4], v)
		}
		return net.IP(nb[:]), int(port64), nil
	}
	return nil, 0, fmt.Errorf("bad ip len")
}

func mapFDs() map[string]int {
	out := map[string]int{}
	procs, _ := os.ReadDir("/proc")
	for _, p := range procs {
		if !p.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(p.Name())
		if err != nil {
			continue
		}
		fds, err := os.ReadDir(filepath.Join("/proc", p.Name(), "fd"))
		if err != nil {
			continue
		}
		for _, fd := range fds {
			link, err := os.Readlink(filepath.Join("/proc", p.Name(), "fd", fd.Name()))
			if err != nil {
				continue
			}
			if strings.HasPrefix(link, "socket:[") {
				ino := strings.TrimSuffix(strings.TrimPrefix(link, "socket:["), "]")
				out[ino] = pid
			}
		}
	}
	return out
}

func procName(pid int) string {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/comm", pid))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}
