#!/usr/bin/env bash
set -euo pipefail
cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.."

# Keep Go compilation and Gradle workers bounded on the development machine.
export GOFLAGS="${GOFLAGS:+$GOFLAGS }-p=1"

platform=${1:-all}
case $platform in
  all|linux|windows|android) ;;
  *) echo "Usage: $0 [all|linux|windows|android]" >&2; exit 2 ;;
esac
version=$(cat VERSION)
out=dist/ci
mkdir -p "$out"

if [[ $platform == all || $platform == linux ]]; then

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
mkdir -p bin
make build server

echo 'Packaging the Linux desktop (.tar.gz, .deb, AppImage) with the runner toolchain'
packaging/linux/build.sh binaries tarball deb appimage
dpkg-deb --info "dist/1loop-$(cat VERSION)-amd64.deb" >/dev/null

cp "dist/1loop-$version-linux-amd64.tar.gz" "dist/1loop-$version-amd64.deb" \
  "dist/1loop-$version-x86_64.AppImage" "$out/"
cp bin/1loop-server "$out/1loop-server-$version-linux-amd64"
fi

if [[ $platform == all || $platform == android ]]; then
echo 'Building Android APKs, running unit tests and lint'
./android/gradlew -p android --no-daemon --no-parallel --max-workers=1 \
  -Dorg.gradle.jvmargs=-Xmx1536m -Pkotlin.compiler.execution.strategy=in-process \
  :app:assembleDebug :app:assembleRelease :app:testDebugUnitTest :app:lintDebug \
  --warning-mode fail --console=plain

echo 'Packaging Android native corresponding sources'
python3 android/runtime/build.py --output android/app/build/local-runtime \
  --source-bundle android/app/build/local-runtime/native-sources.zip

# Without publication credentials both variants are deliberately unsigned.
if [[ -n ${ONELOOP_ANDROID_SIGNING_PROPERTIES:-} || -f android/signing/release.properties ]]; then
  apksigner="${ANDROID_HOME:-$HOME/Android/Sdk}/build-tools/37.0.0/apksigner"
  debug_certificate=$("$apksigner" verify --print-certs android/app/build/outputs/apk/debug/app-debug.apk |
    sed -n 's/^.*certificate SHA-256 digest: //p' | sort -u)
  release_certificate=$("$apksigner" verify --print-certs android/app/build/outputs/apk/release/app-release.apk |
    sed -n 's/^.*certificate SHA-256 digest: //p' | sort -u)
  test -n "$debug_certificate"
  test "$debug_certificate" = "$release_certificate"
  cp android/app/build/outputs/apk/release/app-release.apk "$out/1loop-$version.apk"
fi
cp android/app/build/local-runtime/native-sources.zip "$out/1loop-$version-android-native-sources.zip"
fi

if [[ $platform == all || $platform == windows ]]; then
echo 'Building Windows desktop and server directly with MinGW and the software renderer'
(
  export CGO_ENABLED=1 GOOS=windows GOARCH=amd64 CC=x86_64-w64-mingw32-gcc
  go vet ./...
  flags="-X 1loop/internal/update.Version=$version"
  mkdir -p bin/windows/software/licenses
  go build -trimpath -ldflags="-s -w -H=windowsgui -extldflags=-static $flags" \
    -o bin/windows/1loop.exe ./cmd/1loop
  go build -trimpath -ldflags="-s -w $flags" \
    -o bin/windows/1loop-server.exe ./cmd/1loop-server
)
# Mesa llvmpipe for PCs without a working OpenGL driver, as package-windows.ps1 does.
cache="${XDG_CACHE_HOME:-$HOME/.cache}/1loop-windows-build"
mkdir -p "$cache"
archive=$cache/mesa3d-26.2.4-release-msvc.7z
[[ -f $archive ]] || curl -fsSL -o "$archive" \
  https://github.com/pal1000/mesa-dist-win/releases/download/26.2.4/mesa3d-26.2.4-release-msvc.7z
printf '%s  %s\n' 351fc8c8b695878ffb3eaa044b3ead08672a48b1a045e3c3e3975811df0f6695 "$archive" | sha256sum -c --quiet
7z e "$archive" x64/opengl32.dll x64/libgallium_wgl.dll -obin/windows/software -y >/dev/null
cp bin/windows/1loop.exe bin/windows/software/
cp .github/windows-licenses/* bin/windows/software/licenses/
printf '@echo off\r\nsetlocal\r\nrem Use the portable CPU renderer on systems without a working OpenGL driver.\r\nset GALLIUM_DRIVER=llvmpipe\r\nset LP_NUM_THREADS=1\r\nstart "" "%%~dp0software\\1loop.exe" %%*\r\n' > bin/windows/1loop-software.cmd
# Windows tests need Windows itself: run go test ./... there (go vet runs above).

(cd bin/windows && zip -qr "../../$out/1loop-$version-windows-amd64.zip" .)
cp bin/windows/1loop-server.exe "$out/1loop-server-$version-windows-amd64.exe"
fi

printf '\nChecks passed for %s. Release files: %s/\n' "$platform" "$out"
