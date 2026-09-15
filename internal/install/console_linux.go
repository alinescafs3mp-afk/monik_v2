//go:build linux

package install

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/alinescafs3mp-afk/monik_v2/internal/secure"
)

const ConsoleSocketUnit = "/etc/systemd/system/monik-console.socket"
const ConsoleServiceUnit = "/etc/systemd/system/monik-console@.service"

// ConsoleUnits uses socket activation to keep privilege switching outside Monik
// network parsers. Both network agent and terminal process remain non-root.
func ConsoleUnits(host, state string, agentUID int) (string, string, error) {
	if agentUID <= 0 || host != "/usr/lib/monik/monik-service-host" || state != "/var/lib/monik-agent" {
		return "", "", fmt.Errorf("console currently supports the standard managed Linux installation only")
	}
	socket := `[Unit]
Description=Monik local console socket (no TCP listener)

[Socket]
ListenStream=/run/monik-console/broker.sock
SocketUser=monik
SocketGroup=monik
SocketMode=0600
Backlog=8
TriggerLimitIntervalSec=60
TriggerLimitBurst=30
DirectoryMode=0755
Accept=yes
MaxConnections=2
RemoveOnStop=yes

[Install]
WantedBy=sockets.target
`
	service := fmt.Sprintf(`[Unit]
Description=Monik isolated interactive console
BindsTo=monik-console.socket
After=monik-console.socket

[Service]
Type=exec
User=monik-console
Group=monik-console
SupplementaryGroups=
ExecStart=%s console-session --agent-uid %d
StandardInput=socket
StandardOutput=journal
StandardError=journal
WorkingDirectory=/var/lib/monik-console
Environment=HOME=/var/lib/monik-console
Environment=PATH=/usr/local/bin:/usr/bin:/bin
UMask=0077
NoNewPrivileges=yes
CapabilityBoundingSet=
AmbientCapabilities=
PrivateTmp=yes
PrivateDevices=yes
ProtectSystem=strict
ProtectHome=yes
ReadWritePaths=/var/lib/monik-console
InaccessiblePaths=-%s
ProtectProc=invisible
ProtectKernelTunables=yes
ProtectKernelModules=yes
ProtectKernelLogs=yes
ProtectControlGroups=yes
RestrictNamespaces=yes
RestrictSUIDSGID=yes
LockPersonality=yes
RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6
KillMode=control-group
TimeoutStopSec=3
RuntimeMaxSec=3600
TasksMax=64
MemoryMax=256M
LimitNOFILE=256
`, host, agentUID, state)
	return socket, service, nil
}

// EnableConsole is a LOCAL elevated opt-in, never a controller command. It does
// not upgrade service-host; caller must first deploy the audited matching pair.
func EnableConsole() error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("local root approval required")
	}
	unlock, e := lockNativeInstall()
	if e != nil {
		return e
	}
	defer unlock()
	ag, e := user.Lookup("monik")
	if e != nil {
		return fmt.Errorf("install managed agent first")
	}
	uid, e := strconv.Atoi(ag.Uid)
	if e != nil || uid <= 0 {
		return fmt.Errorf("invalid agent UID")
	}
	host := "/usr/lib/monik/monik-service-host"
	for _, p := range []string{host, ConsoleSocketUnit, ConsoleServiceUnit, "/var/lib/monik-console", "/run/monik-console"} {
		if e = checkRootAncestors(p, p == "/var/lib/monik-console"); e != nil {
			return e
		}
	}
	info, e := os.Lstat(host)
	if e != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 {
		return fmt.Errorf("protected service-host missing")
	}
	if st, ok := info.Sys().(*syscall.Stat_t); !ok || st.Uid != 0 || st.Nlink != 1 {
		return fmt.Errorf("protected service-host ownership required")
	}
	// A dedicated unprivileged account is mandatory in this first implementation.
	cu, e := ensureServiceUser("monik-console")
	if e != nil {
		return e
	}
	cUID, e := strconv.Atoi(cu.Uid)
	if e != nil || cUID <= 0 || cUID == uid {
		return fmt.Errorf("terminal UID must be distinct and non-root")
	}
	cGID, e := strconv.Atoi(cu.Gid)
	if e != nil || cGID <= 0 {
		return fmt.Errorf("terminal GID invalid")
	}
	group, e := user.LookupGroupId(cu.Gid)
	if e != nil || group.Name != "monik-console" {
		return fmt.Errorf("dedicated console primary group required")
	}
	// Reject preexisting accounts with extra group privileges (docker, sudo, etc.).
	groups, e := cu.GroupIds()
	if e != nil {
		return e
	}
	for _, g := range groups {
		if g != cu.Gid {
			return fmt.Errorf("monik-console must have no supplementary groups")
		}
	}
	home := "/var/lib/monik-console"
	if e = os.MkdirAll(home, 0700); e != nil {
		return e
	}
	hi, e := os.Lstat(home)
	if e != nil || !hi.IsDir() {
		return fmt.Errorf("console home must not be a link")
	}
	if st, ok := hi.Sys().(*syscall.Stat_t); !ok || (st.Uid != 0 && int(st.Uid) != cUID) {
		return fmt.Errorf("console home belongs to another user")
	}
	if e = os.Chown(home, cUID, cGID); e != nil {
		return e
	}
	if e = os.Chmod(home, 0700); e != nil {
		return e
	}
	socket, service, e := ConsoleUnits(host, "/var/lib/monik-agent", uid)
	if e != nil {
		return e
	}
	ctl, e := systemTool("systemctl")
	if e != nil {
		return e
	}
	// Close old socket and its bound sessions BEFORE changing local policy.
	if _, e = os.Stat(ConsoleSocketUnit); e == nil {
		if e = runSystemTool(ctl, "stop", "monik-console.socket"); e != nil {
			return e
		}
	}
	for _, x := range []struct{ p, b string }{{ConsoleServiceUnit, service}, {ConsoleSocketUnit, socket}} {
		if e = os.MkdirAll(filepath.Dir(x.p), 0755); e != nil {
			return e
		}
		if e = secure.AtomicWrite(x.p, []byte(x.b), 0644); e != nil {
			return e
		}
	}
	for _, args := range [][]string{{"daemon-reload"}, {"enable", "--now", "monik-console.socket"}, {"is-active", "--quiet", "monik-console.socket"}} {
		if e = runSystemTool(ctl, args...); e != nil {
			return fmt.Errorf("console not confirmed: %s: %w", strings.Join(args, " "), e)
		}
	}
	return nil
}
func DisableConsole() error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("local root approval required")
	}
	unlock, e := lockNativeInstall()
	if e != nil {
		return e
	}
	defer unlock()
	ctl, e := systemTool("systemctl")
	if e != nil {
		return e
	}
	// BindsTo closes live template instances as well. Do not delete identities.
	return runSystemTool(ctl, "disable", "--now", "monik-console.socket")
}
