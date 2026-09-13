package collectors

import (
	"net"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
	"github.com/shirou/gopsutil/v4/disk"
)

func listIPsImpl() ([]string, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	var out []string
	for _, iface := range ifaces {
		addrs, _ := iface.Addrs()
		for _, a := range addrs {
			var ip net.IP
			switch v := a.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip == nil || ip.IsMulticast() {
				continue
			}
			out = append(out, ip.String())
		}
	}
	return out, nil
}

func disks(now time.Time) ([]protocol.Disk, protocol.Capability) {
	parts, err := disk.Partitions(false)
	if err != nil {
		return nil, protocol.Capability{Status: protocol.CapError, Reason: err.Error()}
	}
	var out []protocol.Disk
	skipFS := map[string]bool{
		"tmpfs": true, "devtmpfs": true, "proc": true, "sysfs": true, "cgroup": true, "cgroup2": true,
		"overlay": true, "squashfs": true, "nsfs": true, "devpts": true, "securityfs": true,
		"pstore": true, "bpf": true, "tracefs": true, "debugfs": true, "fusectl": true,
		"configfs": true, "hugetlbfs": true, "mqueue": true, "autofs": true, "rpc_pipefs": true,
		"nfs": true, "nfs4": true, "cifs": true, "smb": true, "fuse.gvfsd-fuse": true,
	}
	for _, p := range parts {
		if skipFS[p.Fstype] {
			continue
		}
		u, err := disk.Usage(p.Mountpoint)
		if err != nil {
			continue
		}
		d := protocol.Disk{
			Mount: p.Mountpoint, FS: p.Fstype, Total: int64(u.Total), Used: int64(u.Used),
			Available: int64(u.Free), UsedPct: u.UsedPercent,
		}
		if u.InodesTotal > 0 {
			t, used := int64(u.InodesTotal), int64(u.InodesUsed)
			d.InodesTot, d.InodesUsed = &t, &used
		}
		out = append(out, d)
	}
	t := now
	return out, protocol.Capability{Status: protocol.CapSupported, LastSuccess: &t}
}
