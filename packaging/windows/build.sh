#!/usr/bin/env bash
# SPDX-License-Identifier: MPL-2.0
# Builds the Windows desktop download (x86-64) from Linux, with MinGW-w64:
#
#   dist/1loop-VERSION-windows-amd64.exe
#
# One exe, nothing beside it: the server is packaging/server/build.sh's.
# It carries the 1loop icon and its version (`1loop-release winres`).
#
# DIST sets the output folder (dist by default). Needs gcc-mingw-w64-x86-64.
# go vet runs for Windows here; the tests need Windows itself (go test ./...
# there).
set -euo pipefail
cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.."

version=$(cat VERSION)
dist=${DIST:-dist}
mkdir -p "$dist"
exe=$dist/1loop-$version-windows-amd64.exe

# The icon and version, linked from cmd/1loop while the exe is built.
syso=cmd/1loop/rsrc_windows_amd64.syso
trap 'rm -f "$syso"' EXIT
go run ./cmd/1loop-release winres -version "$version" -file 1loop.exe -o "$syso"

# The desktop needs cgo for OpenGL.
export CGO_ENABLED=1 GOOS=windows GOARCH=amd64 CC="${CC:-x86_64-w64-mingw32-gcc}"
go vet ./...
flag="-X 1loop/internal/update.Version=$version"
go build -trimpath -ldflags="-s -w -H=windowsgui -extldflags=-static $flag" -o "$exe" ./cmd/1loop
echo "$exe"
