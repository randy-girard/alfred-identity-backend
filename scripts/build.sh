#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
mkdir -p bin
for pair in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64; do
  OS=${pair%/*}
  ARCH=${pair#*/}
  EXT=""
  [[ "$OS" == windows ]] && EXT=".exe"
  echo "building $OS/$ARCH"
  CGO_ENABLED=0 GOOS="$OS" GOARCH="$ARCH" go build -o "bin/daemon-${OS}-${ARCH}${EXT}" ./cmd/daemon
done
