#!/bin/bash

# 构建嵌入式单文件二进制
# 用法: ./build.sh

set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

echo "Building frontend..."
cd "$SCRIPT_DIR/web"
npm run build

echo "Building backend binary..."
cd "$SCRIPT_DIR/backend"
go build -buildvcs=false -o lumi ./cmd/lumi

echo "Build complete: $SCRIPT_DIR/backend/lumi"
