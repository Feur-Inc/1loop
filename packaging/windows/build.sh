#!/usr/bin/env bash
# Builds the Windows downloads (x86-64) from Linux, with MinGW-w64, in
# bin/windows/: 1loop.exe, 1loop-server.exe, and software/ with
# 1loop-software.cmd, the same desktop with the Mesa llvmpipe renderer for
# PCs without a working OpenGL driver (as .github/package-windows.ps1 does).
#
# Needs gcc-mingw-w64-x86-64, curl and 7z (p7zip-full). go vet runs for
# Windows here; the tests need Windows itself (go test ./... there).
set -euo pipefail
cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.."

out=bin/windows
cache="${XDG_CACHE_HOME:-$HOME/.cache}/1loop-windows-build"
mkdir -p "$cache" "$out/software/licenses"

# The desktop needs cgo for OpenGL.
export CGO_ENABLED=1 GOOS=windows GOARCH=amd64 CC="${CC:-x86_64-w64-mingw32-gcc}"
go vet ./...
version="-X 1loop/internal/update.Version=$(cat VERSION)"
go build -trimpath -ldflags="-s -w -H=windowsgui -extldflags=-static $version" -o "$out/1loop.exe" ./cmd/1loop
go build -trimpath -ldflags="-s -w $version" -o "$out/1loop-server.exe" ./cmd/1loop-server

archive=$cache/mesa3d-26.2.4-release-msvc.7z
[ -f "$archive" ] || curl -fsSL -o "$archive" https://github.com/pal1000/mesa-dist-win/releases/download/26.2.4/mesa3d-26.2.4-release-msvc.7z
echo "351fc8c8b695878ffb3eaa044b3ead08672a48b1a045e3c3e3975811df0f6695  $archive" | sha256sum -c --quiet
7z e "$archive" x64/opengl32.dll x64/libgallium_wgl.dll -o"$out/software" -y >/dev/null
cp "$out/1loop.exe" "$out/software/"
cp .github/windows-licenses/* "$out/software/licenses/"
printf "@echo off\r\nsetlocal\r\nrem Use the portable CPU renderer on systems without a working OpenGL driver.\r\nset GALLIUM_DRIVER=llvmpipe\r\nset LP_NUM_THREADS=1\r\nstart \"\" \"%%~dp0software\\\\1loop.exe\" %%*\r\n" > "$out/1loop-software.cmd"
echo "Built $out/"
