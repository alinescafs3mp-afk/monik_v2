#!/usr/bin/env bash
# Disposable source verification only. Never installs a system service or deploys.
set -Eeuo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."
root=$PWD
out=${MONIK_AUDIT_OUTPUT:-"$root/audit-output"}
mkdir -p "$out"
out=$(cd "$out" && pwd)
GO=${GO:-go}
"$GO" version | tee "$out/toolchain.txt"
node --version | tee -a "$out/toolchain.txt"
# Depend on the repository lockfiles, not locally guessed dependency versions.
(cd web && npm ci --no-audit --no-fund && npm test && npx --no-install tsc --noEmit && npm run build) 2>&1 | tee "$out/frontend.log"
"$GO" test -race -count=1 -json ./... 2> "$out/go-stderr.log" | tee "$out/go-tests.jsonl"
"$GO" vet ./... 2>&1 | tee "$out/vet.log"
"$GO" mod verify 2>&1 | tee "$out/modules.log"
# UI has just been built from the same source; do not install dependencies twice.
make dist -o ui GO="$GO" VERSION="${VERSION:-0.1.0-audit14}" 2>&1 | tee "$out/build.log"
export MONIK_NATIVE_AGENT_BIN="$root/dist/linux-amd64/monik-agent"
export MONIK_NATIVE_SUPERVISOR_BIN="$root/dist/linux-amd64/monik-service-host"
if [[ $(uname -s) == Linux ]]; then
  "$GO" test -race -count=1 -v -run '^TestAudit8Native|^TestV12Native' ./internal/server 2>&1 | tee "$out/native.log"
else
  printf 'NOT RUN: compiled Linux process acceptance requires Linux.\n' | tee "$out/native.log"
fi
"$GO" test -race -shuffle=on -count=10 -run '^TestV14' ./cmd/monik-server ./internal/netutil ./internal/agent/discovery ./internal/storage ./internal/server ./internal/tlsutil 2>&1 | tee "$out/repeated-regressions.log"
"$GO" test -run '^$' -fuzz '^FuzzV14OwnerPIDTable$' -fuzztime=15s -parallel=2 ./internal/agent/discovery 2>&1 | tee "$out/listener-fuzz.log"
if [[ ${1:-} == --with-browser ]]; then
  "$GO" build -o "$out/audit-server" ./tests/fixtures/audit-server
  # Requires Python Playwright and its installed Chromium. No browser policy bypass.
  python3 tests/browser/audit.py --fixture "$out/audit-server" --output "$out/browser"
else
  printf 'NOT RUN: browser acceptance. Run with --with-browser in a permitted environment.\n' | tee "$out/browser-status.txt"
fi
printf 'Requested source checks completed. Native service boot, physical TV, power loss and complete restore remain separate gates.\n'
