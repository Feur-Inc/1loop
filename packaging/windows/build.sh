#!/usr/bin/env bash
# SPDX-License-Identifier: MPL-2.0
# Builds the Windows desktop download (x86-64) from Linux, with MinGW-w64:
#
#   dist/1loop-VERSION-windows-amd64.zip
#
# holding bin/windows/: 1loop.exe, 1loop-server.exe and licenses/. 1loop.exe
# carries Mesa's CPU renderer (internal/softgl, -tags mesa) for PCs without
# a working OpenGL driver: it puts it beside itself when it needs it.
#
# DIST sets the output folder (dist by default). Needs gcc-mingw-w64-x86-64,
# curl, 7z (p7zip-full), gzip and zip. go vet runs for Windows here; the
# tests need Windows itself (go test ./... there).
set -euo pipefail
cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.."

version=$(cat VERSION)
dist=${DIST:-dist}
out=bin/windows
cache="${XDG_CACHE_HOME:-$HOME/.cache}/1loop-windows-build"
mkdir -p "$cache" "$out/licenses"

# Mesa, gzipped into the package that embeds it (internal/softgl).
archive=$cache/mesa3d-26.2.4-release-msvc.7z
[ -f "$archive" ] || curl -fsSL -o "$archive" https://github.com/pal1000/mesa-dist-win/releases/download/26.2.4/mesa3d-26.2.4-release-msvc.7z
echo "351fc8c8b695878ffb3eaa044b3ead08672a48b1a045e3c3e3975811df0f6695  $archive" | sha256sum -c --quiet
mesa=$cache/mesa3d-26.2.4
mkdir -p "$mesa"
7z e "$archive" x64/opengl32.dll x64/libgallium_wgl.dll -o"$mesa" -y >/dev/null
for dll in opengl32.dll libgallium_wgl.dll; do
  gzip -9 -n -c "$mesa/$dll" > "internal/softgl/$dll.gz"
done

# The desktop needs cgo for OpenGL.
export CGO_ENABLED=1 GOOS=windows GOARCH=amd64 CC="${CC:-x86_64-w64-mingw32-gcc}"
go vet -tags mesa ./...
flag="-X 1loop/internal/update.Version=$version"
go build -trimpath -tags mesa -ldflags="-s -w -H=windowsgui -extldflags=-static $flag" -o "$out/1loop.exe" ./cmd/1loop
go build -trimpath -ldflags="-s -w $flag" -o "$out/1loop-server.exe" ./cmd/1loop-server
cp .github/windows-licenses/* "$out/licenses/"

mkdir -p "$dist"
zip="$PWD/$dist/1loop-$version-windows-amd64.zip"
case $dist in /*) zip="$dist/1loop-$version-windows-amd64.zip" ;; esac
rm -f -- "$zip"
(cd "$out" && zip -qr "$zip" .)
echo "$dist/1loop-$version-windows-amd64.zip"
