#!/usr/bin/env bash
# Builds the headless server downloads (x86-64), static and without cgo:
#
#   dist/1loop-server-VERSION-linux-amd64
#   dist/1loop-server-VERSION-windows-amd64.exe
#
# DIST sets the output folder (dist by default). `make server` builds the
# development binary in bin/ instead.
set -euo pipefail
cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.."

version=$(cat VERSION)
dist=${DIST:-dist}
flags="-s -w -X 1loop/internal/update.Version=$version"
mkdir -p "$dist"

CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="$flags" \
  -o "$dist/1loop-server-$version-linux-amd64" ./cmd/1loop-server
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags="$flags" \
  -o "$dist/1loop-server-$version-windows-amd64.exe" ./cmd/1loop-server
echo "$dist/1loop-server-$version-linux-amd64"
echo "$dist/1loop-server-$version-windows-amd64.exe"
