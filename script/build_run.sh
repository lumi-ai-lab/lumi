#!/bin/bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

echo "Running build..."
bash "$SCRIPT_DIR/build.sh"

echo "Starting backend..."
exec "$REPO_DIR/backend/lumi" "$@"
