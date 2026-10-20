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

# TODO: restore native Windows vet, tests, desktop build and software-renderer packaging.

printf '\nLocal checks passed. Outputs:\n'
printf '%s\n' 'bin/1loop' 'bin/1loop-wayland' 'bin/1loop-server' \
  'android/app/build/outputs/apk/debug/app-debug.apk' \
  'android/app/build/outputs/apk/release/app-release.apk' \
  'android/app/build/local-runtime/native-sources.zip'
