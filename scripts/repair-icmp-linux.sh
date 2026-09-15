#!/usr/bin/env bash
# Explicit operator repair for existing V15 systemd installations. No global
# sysctl change, no root worker, no re-enrollment and no arbitrary unit argument.
set -Eeuo pipefail
export PATH=/usr/sbin:/usr/bin:/sbin:/bin
[[ $(id -u) == 0 ]] || { echo 'Run with sudo on the monitored Linux host.' >&2; exit 1; }
unit=monik-agent.service
[[ -d /run/systemd/system ]] || { echo 'System systemd manager is not running.' >&2; exit 1; }
user=$(systemctl show "$unit" --property=User --value)
[[ "$user" == monik ]] || { echo 'Expected dedicated monik service account; inspect custom installation manually.' >&2; exit 1; }
dir=/etc/systemd/system/monik-agent.service.d
[[ ! -L "$dir" ]] || { echo 'Refusing linked service directory.' >&2; exit 1; }
install -d -o root -g root -m 0755 "$dir"
tmp=$(mktemp "$dir/.icmp.XXXXXXXX")
trap 'rm -f "$tmp"' EXIT
cat > "$tmp" <<'UNIT'
[Service]
NoNewPrivileges=true
CapabilityBoundingSet=
CapabilityBoundingSet=CAP_NET_RAW
AmbientCapabilities=
AmbientCapabilities=CAP_NET_RAW
UNIT
chown root:root "$tmp"; chmod 0644 "$tmp"
mv -T "$tmp" "$dir/30-monik-icmp.conf"
systemctl daemon-reload
systemctl restart "$unit"
systemctl is-active --quiet "$unit"
systemctl show "$unit" --property=User --property=AmbientCapabilities --property=CapabilityBoundingSet
printf 'Monik service restarted with ICMP capability. Verify fresh ping status in the UI; network filtering can still prevent replies.\n'
