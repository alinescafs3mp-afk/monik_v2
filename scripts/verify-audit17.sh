#!/usr/bin/env bash
# Disposable-source verification. No production URLs, service installation,
# runtime capability changes or Git publication.
set -Eeuo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."
export GO=${GO:-go}
export VERSION=${VERSION:-0.1.0-audit17}
bash scripts/verify-audit16.sh "${1:-}"
out=${MONIK_AUDIT_OUTPUT:-"$PWD/audit-output"}
(cd web && node --test test/overviewColumns.test.mjs test/audit17-regressions.test.mjs) 2>&1 | tee "$out/v17-interactions.log"
python3 -m py_compile tests/browser/column_scenarios.py
printf 'V17 source checks passed. Browser drag/geometry require --with-browser in a permitted environment; a TV or native boot is not simulated by these unit tests.\n'
