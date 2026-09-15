#!/usr/bin/env bash
# Local disposable-source acceptance. Does not change services, caps or sysctls.
set -Eeuo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."
export GO=${GO:-go}
export VERSION=${VERSION:-0.1.0-audit16}
# Includes full frontend, race tests, vet, locked modules, builds and native
# process/update/rollback fixtures. --with-browser uses the existing permitted
# loopback fixture and now includes scroll/focus/column-layout assertions.
bash scripts/verify-audit15.sh "${1:-}"
out=${MONIK_AUDIT_OUTPUT:-"$PWD/audit-output"}
"$GO" test -race -count=10 -shuffle=on -run '^TestV16|^TestRolloutFreshnessAndIdentityGate' ./internal/server ./internal/agent/collectors ./internal/servicehost 2>&1 | tee "$out/v16-repeat.log"
bash -n scripts/repair-icmp-linux.sh
python3 -m py_compile tests/browser/seamless_scenarios.py
printf 'ICMP kernel echo and actual systemd capability inheritance are NOT proved by unit generation. Follow V16 instructions on one Linux pilot. No production settings have been changed.\n'
