#!/usr/bin/env bash
# SPDX-License-Identifier: MPL-2.0
# Builds the Windows desktop downloads (x86-64) from Linux, with MinGW-w64:
#
#   dist/1loop-VERSION-windows-amd64-setup.exe
#   dist/1loop-VERSION-windows-amd64.exe
#
# The setup (cmd/1loop-setup) installs the second file, which also runs on its own
# and is what the updater downloads. Nothing beside them: the server is packaging/server/build.sh's.
# It carries the 1loop icon and its version (`1loop-release winres`), and
# Mesa's CPU renderer and its licences (internal/softgl) for PCs without a
# working OpenGL driver: it extracts it in %LOCALAPPDATA%\1loop when needed.
#
# DIST sets the output folder (dist by default). Needs gcc-mingw-w64-x86-64,
# 7z (p7zip-full) and xz (otherwise Go compresses, less). go vet runs for
# Windows here; the tests need Windows itself (go test ./... there).
set -euo pipefail
cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.."

version=$(cat VERSION)
dist=${DIST:-dist}
mkdir -p "$dist"
exe=$dist/1loop-$version-windows-amd64.exe
setup=$dist/1loop-$version-windows-amd64-setup.exe

# The icon and version, linked from cmd/1loop while the exe is built.
syso=cmd/1loop/rsrc_windows_amd64.syso
setupsyso=cmd/1loop-setup/rsrc_windows_amd64.syso
work=$(mktemp -d)
trap 'rm -f "$syso" "$setupsyso"; rm -rf "$work"' EXIT
go run ./cmd/1loop-release winres -version "$version" -file 1loop.exe -o "$syso"

# Mesa, xz-compressed into the package that embeds it (internal/softgl),
# from the pinned archive kept in the user's cache. Before GOOS is set:
# the fetcher runs here.
go generate ./internal/softgl

# The desktop needs cgo for OpenGL.
export CGO_ENABLED=1 GOOS=windows GOARCH=amd64 CC="${CC:-x86_64-w64-mingw32-gcc}"
go vet ./...
flag="-X 1loop/internal/update.Version=$version"
go build -trimpath -ldflags="-s -w -H=windowsgui -extldflags=-static $flag" -o "$exe" ./cmd/1loop

# The setup: its own program, without cgo, then the exe above at its end.
GOOS= GOARCH= CGO_ENABLED=0 go run ./cmd/1loop-release winres -version "$version" -description "1loop setup" -file "${setup##*/}" -o "$setupsyso"
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -H=windowsgui -X main.version=$version" -o "$work/setup.exe" ./cmd/1loop-setup
GOOS= GOARCH= CGO_ENABLED=0 go run ./cmd/1loop-release setup -program "$work/setup.exe" -app "$exe" -o "$setup"
echo "$setup"
echo "$exe"
