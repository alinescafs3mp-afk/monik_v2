#!/usr/bin/env bash
# SOURCE verification only. Does not install an OS service or contact production.
set -Eeuo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."
GO=${GO:-go}
export GO
export VERSION=${VERSION:-0.1.0-audit15}
# Repository go.mod/lockfiles are authoritative. Never downgrade them to fit an
# auditor's toolchain. A complete run needs the supported Go toolchain and deps.
bash scripts/verify-audit12.sh "${1:-}"
out=${MONIK_AUDIT_OUTPUT:-"$PWD/audit-output"}
make installer-templates GO="$GO" VERSION="$VERSION" 2>&1 | tee "$out/installer-templates.log"
"$GO" test -race -count=10 -shuffle=on -run 'TestV15' ./internal/server ./internal/storage ./internal/install ./internal/agent/setup ./internal/installerbundle ./cmd/monik-installer ./tests/fixtures/audit-server 2>&1 | tee "$out/audit15-repeat.log"
printf 'Source checks requested above passed. NOT a native clean-VM install/boot acceptance. Test a prepared file on a disposable Linux systemd VM before publishing it.\n'
