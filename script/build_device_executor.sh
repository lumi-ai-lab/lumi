#!/bin/bash

# Build the device-executor binary for Intel Mac.
# Usage: ./script/build_device_executor.sh [output_path]

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
BACKEND_DIR="$REPO_DIR/backend"
OUTPUT="${1:-$BACKEND_DIR/bin/device-executor-darwin-amd64}"

case "$OUTPUT" in
  /*) ;;
  *) OUTPUT="$REPO_DIR/$OUTPUT" ;;
esac

mkdir -p "$(dirname "$OUTPUT")"

echo "Building device-executor for Intel Mac (darwin/amd64)..."
(
  cd "$BACKEND_DIR"
  GOOS=darwin GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o "$OUTPUT" ./cmd/device-executor
)

chmod +x "$OUTPUT"

echo "Build complete: $OUTPUT"

if command -v file >/dev/null 2>&1; then
  file "$OUTPUT"
fi

if command -v shasum >/dev/null 2>&1; then
  shasum -a 256 "$OUTPUT"
elif command -v sha256sum >/dev/null 2>&1; then
  sha256sum "$OUTPUT"
fi
