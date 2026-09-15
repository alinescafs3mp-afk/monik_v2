//go:build linux

package install

import (
	"strings"
	"testing"
)

func TestV18ConsoleUnitsHaveNoRootNetworkProcess(t *testing.T) {
	socket, service, e := ConsoleUnits("/usr/lib/monik/monik-service-host", "/var/lib/monik-agent", 1001)
	if e != nil {
		t.Fatal(e)
	}
	for _, part := range []string{"ListenStream=/run/monik-console/broker.sock", "SocketMode=0600", "SocketUser=monik", "DirectoryMode=0755", "Accept=yes", "MaxConnections=2", "RemoveOnStop=yes"} {
		if !strings.Contains(socket, part) {
			t.Fatal(part)
		}
	}
	for _, part := range []string{"User=monik-console", "Group=monik-console", "--agent-uid 1001", "StandardInput=socket", "NoNewPrivileges=yes", "CapabilityBoundingSet=\n", "AmbientCapabilities=\n", "ProtectHome=yes", "ProtectSystem=strict", "InaccessiblePaths=-/var/lib/monik-agent", "ProtectProc=invisible", "KillMode=control-group", "RuntimeMaxSec=3600", "TasksMax=64", "MemoryMax=256M", "BindsTo=monik-console.socket"} {
		if !strings.Contains(service, part) {
			t.Fatal(part)
		}
	}
	for _, bad := range []string{"User=root", "sudo", "setuid", "TCPListen", "CAP_SYS_ADMIN", "0.0.0.0", "::"} {
		if strings.Contains(service+socket, bad) {
			t.Fatal("unsafe service plan", bad)
		}
	}
}
func TestV18ConsolePlanCannotBeAnArbitraryPrivilegedCommand(t *testing.T) {
	for _, v := range []struct {
		p, s string
		uid  int
	}{{"/tmp/anything", "/var/lib/monik-agent", 1001}, {"/usr/lib/monik/monik-service-host", "/tmp/state", 1001}, {"/usr/lib/monik/monik-service-host", "/var/lib/monik-agent", 0}} {
		if _, _, e := ConsoleUnits(v.p, v.s, v.uid); e == nil {
			t.Fatal("invalid plan accepted")
		}
	}
}
