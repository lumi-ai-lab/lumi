#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
ARCH="${1:-$(go env GOHOSTARCH)}"
OUT="${2:-$ROOT_DIR/docker/sandbox/bin/device-executor}"

case "$ARCH" in
  amd64|arm64) ;;
  *)
    echo "unsupported GOARCH: $ARCH" >&2
    echo "usage: docker/sandbox/build-device-executor.sh [amd64|arm64] [output]" >&2
    exit 1
    ;;
esac

mkdir -p "$(dirname "$OUT")"

cd "$ROOT_DIR/backend"
CGO_ENABLED=0 GOOS=linux GOARCH="$ARCH" \
  go build -buildvcs=false -trimpath -ldflags="-s -w" \
  -o "$OUT" ./cmd/device-executor

chmod 0755 "$OUT"
echo "built $OUT for linux/$ARCH"
