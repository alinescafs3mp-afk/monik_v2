package install

import (
	"fmt"
	"strconv"
	"strings"
)

// PlanSparkUnit deliberately has no model dependency or privileged capability.
// Stopping or upgrading monitoring cannot propagate a stop to model cgroups.
func PlanSparkUnit(worker, config, state, user string) string {
	quote := func(s string) string { return strconv.Quote(strings.NewReplacer("%", "%%", "$", "$$").Replace(s)) }
	return fmt.Sprintf(`[Unit]
Description=Monik Spark light collector
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=%s run --config %s
User=%s
Restart=on-failure
RestartSec=5
TimeoutStopSec=15
KillMode=control-group
UMask=0077
Nice=10
NoNewPrivileges=true
CapabilityBoundingSet=
AmbientCapabilities=
ProtectSystem=strict
ProtectHome=read-only
ReadWritePaths=%s
PrivateTmp=true
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectControlGroups=true
RestrictSUIDSGID=true
LockPersonality=true

[Install]
WantedBy=multi-user.target
`, quote(worker), quote(config), user, quote(state))
}
