#!/usr/bin/env bash
# SPDX-License-Identifier: MPL-2.0
# Builds the Windows desktop download (x86-64) from Linux, with MinGW-w64:
#
#   dist/1loop-VERSION-windows-amd64.zip
#
# holding bin/windows/: 1loop.exe and 1loop-server.exe. 1loop.exe carries
# Mesa's CPU renderer and its licences (internal/softgl) for PCs without a
# working OpenGL driver: it extracts it in %LOCALAPPDATA%\1loop when needed.
#
# DIST sets the output folder (dist by default). Needs gcc-mingw-w64-x86-64,
# 7z (p7zip-full), xz (otherwise Go compresses, less) and zip. go vet runs
# for Windows here; the tests need Windows itself (go test ./... there).
set -euo pipefail
cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.."

version=$(cat VERSION)
dist=${DIST:-dist}
out=bin/windows
mkdir -p "$out"

# Mesa, xz-compressed into the package that embeds it (internal/softgl),
# from the pinned archive kept in the user's cache. Before GOOS is set:
# the fetcher runs here.
go generate ./internal/softgl

# The desktop needs cgo for OpenGL.
export CGO_ENABLED=1 GOOS=windows GOARCH=amd64 CC="${CC:-x86_64-w64-mingw32-gcc}"
go vet ./...
flag="-X 1loop/internal/update.Version=$version"
go build -trimpath -ldflags="-s -w -H=windowsgui -extldflags=-static $flag" -o "$out/1loop.exe" ./cmd/1loop
go build -trimpath -ldflags="-s -w $flag" -o "$out/1loop-server.exe" ./cmd/1loop-server

mkdir -p "$dist"
zip="$PWD/$dist/1loop-$version-windows-amd64.zip"
case $dist in /*) zip="$dist/1loop-$version-windows-amd64.zip" ;; esac
rm -f -- "$zip"
(cd "$out" && zip -qr "$zip" .)
echo "$dist/1loop-$version-windows-amd64.zip"
