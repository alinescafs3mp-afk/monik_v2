#!/usr/bin/env bash
# Run on a disposable checkout, never against the production controller.
# This performs source/loopback verification only; no service installation.
set -Eeuo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."
export GO=${GO:-go}
export VERSION=${VERSION:-0.1.0-audit19}
export MONIK_AUDIT_OUTPUT=${MONIK_AUDIT_OUTPUT:-"$PWD/audit-output"}
mkdir -p "$MONIK_AUDIT_OUTPUT"
# Full suite, source build, existing native update/recovery fixtures, optional browser.
bash scripts/verify-audit12.sh "${1:-}"
"$GO" test -race -count=5 -shuffle=on -run '^TestV19' ./internal/server ./internal/storage 2>&1 | tee "$MONIK_AUDIT_OUTPUT/v19-repeat.log"
for n in $(seq 1 10); do
 (cd web && node --test test/tvProfile.test.mjs test/tvProfileShell.test.mjs test/overviewColumns.test.mjs) 2>&1 | tee "$MONIK_AUDIT_OUTPUT/v19-ui-repeat-$n.log"
done
python3 -m py_compile tests/browser/tv_profile_scenarios.py tests/browser/audit.py tests/browser/column_scenarios.py tests/browser/agent_console_scenarios.py
ARCHES='amd64 arm64' bash scripts/build-installer-templates.sh 2>&1 | tee "$MONIK_AUDIT_OUTPUT/v19-installers.log"
printf 'V19 requested checks completed. Physical TV/Yandex, native service boot, 24h soak and complete restore remain separate gates.\n'
