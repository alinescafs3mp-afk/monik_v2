#!/usr/bin/env bash
# Verification in a disposable checkout only. No installation, chmod of live
# state, controller actions, remote commands, or production network targets.
set -Eeuo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."
export GO=${GO:-go}
export VERSION=${VERSION:-0.1.0-audit18}
out=${MONIK_AUDIT_OUTPUT:-"$PWD/audit-output"}
mkdir -p "$out"
# Existing complete frontend/race/build/native worker/update/rollback battery.
bash scripts/verify-audit12.sh "${1:-}"
"$GO" test -race -count=5 -shuffle=on -run '^TestV18|^TestV17Console' ./internal/server ./internal/agentconsole ./internal/install 2>&1 | tee "$out/v18-security-repeat.log"
(cd web && node --test test/agentConsole.test.mjs test/overviewColumns.test.mjs) 2>&1 | tee "$out/v18-ui-controls.log"
python3 -m py_compile tests/browser/agent_console_scenarios.py
ARCHES='amd64 arm64' bash scripts/build-installer-templates.sh 2>&1 | tee "$out/v18-installers.log"
if command -v systemd-analyze >/dev/null; then
 MONIK_SYSTEMD_VERIFY=1 "$GO" test -count=1 -v -run '^TestV18SystemdUnitSyntax$' ./internal/install 2>&1 | tee "$out/v18-unit-syntax.log"
fi
# No hidden elevation. The UID-isolation process test needs a disposable root
# container/VM solely to DROP test subprocess UIDs. The normal suite must not
# be rerun as root on a production controller to satisfy this optional check.
if [[ $(uname -s) == Linux && $EUID -eq 0 && ${MONIK_V18_UID_FIXTURE:-0} == 1 ]]; then
 MONIK_NATIVE_TESTS=1 "$GO" test -race -count=1 -v -run '^TestV18NativeActivatedPeerUIDAndKeyIsolation$' ./internal/agentconsole 2>&1 | tee "$out/v18-uid-fixture.log"
else
 printf 'NOT RUN: isolated root fixture for peer UID/capability/key-isolation checks. Native systemd sandbox/boot remain separate.\n' | tee "$out/v18-uid-status.txt"
fi
printf 'V18 requested source checks completed. Do not call this native boot, physical-TV, large-fleet or independent penetration-test acceptance.\n'
