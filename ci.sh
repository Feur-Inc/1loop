#!/usr/bin/env bash
set -euo pipefail
cd -- "$(dirname -- "${BASH_SOURCE[0]}")"

# Keep Go compilation and Gradle workers bounded on the development machine.
export GOFLAGS="${GOFLAGS:+$GOFLAGS }-p=1"

echo 'Checking Go formatting'
unformatted=$(git ls-files --cached --others --exclude-standard -z -- '*.go' | xargs -0 -r gofmt -l)
if [ -n "$unformatted" ]; then
  printf 'Not formatted:\n%s\n' "$unformatted"
  exit 1
fi

echo 'Running go vet'
go vet ./...

echo 'Running Go tests'
go test ./...

echo 'Building Linux desktop (X11 and Wayland) and headless server'
make build server

echo 'Building Android APKs, running unit tests and lint'
./android/gradlew -p android --no-daemon --no-parallel --max-workers=1 \
  -Dorg.gradle.jvmargs=-Xmx1536m -Pkotlin.compiler.execution.strategy=in-process \
  :app:assembleDebug :app:assembleRelease :app:testDebugUnitTest :app:lintDebug \
  --warning-mode fail --console=plain

echo 'Packaging Android native corresponding sources'
python3 android/runtime/build.py --output android/app/build/local-runtime \
  --source-bundle android/app/build/local-runtime/native-sources.zip

echo 'Building Windows desktop and server (MinGW in Docker) with the software renderer'
# The desktop needs cgo for OpenGL: a throwaway amd64 image carries MinGW, so
# nothing is installed on the host. Go, the module cache and a build cache
# come from the host; the container runs as the current user.
docker build -q --platform linux/amd64 -t 1loop-windows-build - >/dev/null <<'EOF'
FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends gcc-mingw-w64-x86-64 git curl ca-certificates p7zip-full && rm -rf /var/lib/apt/lists/*
EOF
cache="${XDG_CACHE_HOME:-$HOME/.cache}/1loop-windows-build"
mkdir -p "$cache/go" bin/windows
docker run --rm --platform linux/amd64 -u "$(id -u):$(id -g)" \
  -v "$(go env GOROOT)":/usr/local/go:ro -v "$(go env GOMODCACHE)":/gomod \
  -v "$PWD":/src:ro -v "$PWD/bin/windows":/out -v "$cache":/cache -w /src \
  -e HOME=/tmp -e GOMODCACHE=/gomod -e GOCACHE=/cache/go -e GOFLAGS="$GOFLAGS" -e GOTOOLCHAIN=local \
  -e CGO_ENABLED=1 -e GOOS=windows -e GOARCH=amd64 -e CC=x86_64-w64-mingw32-gcc \
  1loop-windows-build bash -euo pipefail -c '
export PATH=/usr/local/go/bin:$PATH
go vet ./...
version="-X 1loop/internal/update.Version=$(cat VERSION)"
go build -trimpath -ldflags="-s -w -H=windowsgui -extldflags=-static $version" -o /out/1loop.exe ./cmd/1loop
go build -trimpath -ldflags="-s -w $version" -o /out/1loop-server.exe ./cmd/1loop-server
# Mesa llvmpipe for PCs without a working OpenGL driver, as package-windows.ps1 does.
archive=/cache/mesa3d-26.2.4-release-msvc.7z
[ -f "$archive" ] || curl -fsSL -o "$archive" https://github.com/pal1000/mesa-dist-win/releases/download/26.2.4/mesa3d-26.2.4-release-msvc.7z
echo "351fc8c8b695878ffb3eaa044b3ead08672a48b1a045e3c3e3975811df0f6695  $archive" | sha256sum -c --quiet
mkdir -p /out/software/licenses
7z e "$archive" x64/opengl32.dll x64/libgallium_wgl.dll -o/out/software -y >/dev/null
cp /out/1loop.exe /out/software/
cp .github/windows-licenses/* /out/software/licenses/
printf "@echo off\r\nsetlocal\r\nrem Use the portable CPU renderer on systems without a working OpenGL driver.\r\nset GALLIUM_DRIVER=llvmpipe\r\nset LP_NUM_THREADS=1\r\nstart \"\" \"%%~dp0software\\\\1loop.exe\" %%*\r\n" > /out/1loop-software.cmd'
# Windows tests need Windows itself: run go test ./... there (go vet runs above).

printf '\nLocal checks passed. Outputs:\n'
printf '%s\n' 'bin/1loop' 'bin/1loop-wayland' 'bin/1loop-server' \
  'bin/windows/1loop.exe' 'bin/windows/1loop-server.exe' 'bin/windows/1loop-software.cmd' \
  'android/app/build/outputs/apk/debug/app-debug.apk' \
  'android/app/build/outputs/apk/release/app-release.apk' \
  'android/app/build/local-runtime/native-sources.zip'
