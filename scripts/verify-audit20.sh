#!/usr/bin/env bash
# Source and loopback verification only. Does not install services or deploy.
set -Eeuo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."
export GO=${GO:-go}
export VERSION=${VERSION:-0.1.0-audit20}
export MONIK_AUDIT_OUTPUT=${MONIK_AUDIT_OUTPUT:-"$PWD/audit-output"}
mkdir -p "$MONIK_AUDIT_OUTPUT"
bash scripts/verify-audit12.sh "${1:-}"
"$GO" test -race -count=5 -shuffle=on -run '^TestV20' ./internal/server ./internal/storage 2>&1 | tee "$MONIK_AUDIT_OUTPUT/v20-repeat.log"
"$GO" test -run '^$' -fuzz '^FuzzV20BrowserOrigin$' -fuzztime=15s -parallel=2 ./internal/server 2>&1 | tee "$MONIK_AUDIT_OUTPUT/v20-origin-fuzz.log"
(cd web && node --test test/networkSecurity.test.mjs test/tvProfile.test.mjs test/tvProfileShell.test.mjs) 2>&1 | tee "$MONIK_AUDIT_OUTPUT/v20-network-ui.log"
python3 -m py_compile tests/browser/audit.py tests/browser/tv_profile_scenarios.py
ARCHES='amd64 arm64' bash scripts/build-installer-templates.sh 2>&1 | tee "$MONIK_AUDIT_OUTPUT/v20-installers.log"
printf 'V20 requested checks finished. Physical TV/Yandex, native service boot, external pentest, dependency vulnerability audit, 24h soak and complete restore remain separate gates.\n'
