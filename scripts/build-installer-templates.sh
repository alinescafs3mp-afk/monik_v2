#!/usr/bin/env bash
# Build all components from the SAME source. Never bundle a downloaded mystery
# worker or controller CA/key into reusable templates.
set -euo pipefail
cd "$(dirname "$0")/.."
GO=${GO:-go}
COMMIT=${COMMIT:-$(git rev-parse HEAD)}
VERSION=${VERSION:-0.1.0}
OUT=${OUT:-dist/installer-templates}
ARCHES=${ARCHES:-amd64}
mkdir -p "$OUT"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
for arch in $ARCHES; do
 case "$arch" in amd64|arm64) ;; *) echo 'unsupported Linux architecture' >&2; exit 2;; esac
 flags="-s -w -X github.com/alinescafs3mp-afk/monik_v2/internal/version.Version=$VERSION -X github.com/alinescafs3mp-afk/monik_v2/internal/version.Commit=$COMMIT"
 CGO_ENABLED=0 GOOS=linux GOARCH="$arch" "$GO" build -trimpath -ldflags "$flags" -o "$tmp/stub" ./cmd/monik-installer
 CGO_ENABLED=0 GOOS=linux GOARCH="$arch" "$GO" build -trimpath -ldflags "$flags" -o "$tmp/worker" ./cmd/monik-agent
 CGO_ENABLED=0 GOOS=linux GOARCH="$arch" "$GO" build -trimpath -ldflags "$flags" -o "$tmp/host" ./cmd/monik-service-host
 "$GO" run ./cmd/monik-installer-pack --installer "$tmp/stub" --worker "$tmp/worker" --supervisor "$tmp/host" --arch "$arch" --build "$COMMIT" --out "$tmp/$arch.bin"
 # Publish the fully verified new template by rename. No live profile is here.
 install -m 0644 "$tmp/$arch.bin" "$OUT/.linux-$arch.bin.new"
 mv -f "$OUT/.linux-$arch.bin.new" "$OUT/linux-$arch.bin"
 if [ "$arch" = arm64 ]; then
  CGO_ENABLED=0 GOOS=linux GOARCH=arm64 "$GO" build -trimpath -ldflags "$flags" -o "$tmp/spark-worker" ./cmd/monik-spark-agent
  "$GO" run ./cmd/monik-installer-pack --agent-flavor spark-light --installer "$tmp/stub" --worker "$tmp/spark-worker" --arch arm64 --build "$COMMIT" --out "$tmp/spark-light.bin"
  install -m 0644 "$tmp/spark-light.bin" "$OUT/.linux-arm64-spark-light.bin.new"
  mv -f "$OUT/.linux-arm64-spark-light.bin.new" "$OUT/linux-arm64-spark-light.bin"
 fi
done
(cd "$OUT" && sha256sum linux-*.bin > SHA256SUMS)
printf 'Templates ready in %s. Deploy to <controller-data-dir>/installer-templates/ with the matching server build.\n' "$OUT"
