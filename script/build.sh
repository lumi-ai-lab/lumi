#!/bin/bash

# 构建嵌入式单文件二进制
# 用法: ./script/build.sh

set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

echo "Building frontend..."
cd "$REPO_DIR/web"
npm run build

echo "Building backend binary..."
cd "$REPO_DIR/backend"
go build -buildvcs=false -o lumi ./cmd/lumi

echo "Building Lumi CLI..."
cd "$REPO_DIR/cli"
go build -buildvcs=false -o lumi-cli .

echo "Build complete:"
echo "  $REPO_DIR/backend/lumi"
echo "  $REPO_DIR/cli/lumi-cli"
