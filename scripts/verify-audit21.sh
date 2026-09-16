#!/usr/bin/env bash
# Source and disposable loopback fixtures only. Never deploy or install a service.
set -Eeuo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."
export GO=${GO:-go}
export VERSION=${VERSION:-0.1.0-audit21}
export MONIK_AUDIT_OUTPUT=${MONIK_AUDIT_OUTPUT:-"$PWD/audit-output"}
mkdir -p "$MONIK_AUDIT_OUTPUT"
bash scripts/verify-audit12.sh "${1:-}"
"$GO" test -race -count=10 -shuffle=on -run '^TestV21' ./internal/server ./internal/storage 2>&1 | tee "$MONIK_AUDIT_OUTPUT/v21-repeat.log"
for i in $(seq 1 10); do
 (cd web && node --test test/tvScreen.test.mjs test/tvProfile.test.mjs test/tvProfileShell.test.mjs test/overviewColumns.test.mjs) > "$MONIK_AUDIT_OUTPUT/v21-ui-repeat-$i.log" 2>&1
done
python3 -m py_compile tests/browser/audit.py tests/browser/tv_screen_scenarios.py
ARCHES='amd64 arm64' bash scripts/build-installer-templates.sh 2>&1 | tee "$MONIK_AUDIT_OUTPUT/v21-installers.log"
printf 'V21 source checks finished. Hiding controls is not a viewer role. Physical TV/Yandex, service boot, external perimeter and long soak remain separate gates.\n'
