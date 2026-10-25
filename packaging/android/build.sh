#!/usr/bin/env bash
# Builds the Android app, with the sources of its native helpers:
#
#   packaging/android/build.sh debug     dist/1loop-VERSION-debug.apk
#   packaging/android/build.sh release   dist/1loop-VERSION.apk
#
# both with dist/1loop-VERSION-android-native-sources.zip. DIST sets the
# output folder (dist by default). Needs JDK 21 and the Android SDK
# (ANDROID_HOME) with build-tools 37.0.0, and the NDK 30.0.16248370
# (ANDROID_NDK_HOME), cmake, make, patch and patchelf for the native helpers.
#
# debug is "1loop debug" (io.oneloop.remote.debug), signed with the shared
# key committed in android/debug.keystore: this is what the CI builds.
# release is signed with the private release key of
# android/signing/release.properties (or ONELOOP_ANDROID_SIGNING_PROPERTIES)
# and is only built on the machine that publishes.
set -euo pipefail
cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.."

variant=${1:-}
case $variant in
debug) task=:app:assembleDebug apk=android/app/build/outputs/apk/debug/app-debug.apk suffix=-debug ;;
release) task=:app:assembleRelease apk=android/app/build/outputs/apk/release/app-release.apk suffix= ;;
*)
  echo "Usage: $0 debug|release" >&2
  exit 2
  ;;
esac
version=$(cat VERSION)
dist=${DIST:-dist}
apksigner="${ANDROID_HOME:-$HOME/Android/Sdk}/build-tools/37.0.0/apksigner"

./android/gradlew -p android --console=plain "$task"
python3 android/runtime/build.py --output android/app/build/local-runtime \
  --source-bundle android/app/build/local-runtime/native-sources.zip

# An unsigned release (no release key here) is not a download.
if ! "$apksigner" verify "$apk" >/dev/null 2>&1; then
  echo "$apk is not signed: set up android/signing/release.properties (docs/BUILD.md)" >&2
  exit 1
fi
mkdir -p "$dist"
cp "$apk" "$dist/1loop-$version$suffix.apk"
cp android/app/build/local-runtime/native-sources.zip "$dist/1loop-$version-android-native-sources.zip"
echo "$dist/1loop-$version$suffix.apk"
echo "$dist/1loop-$version-android-native-sources.zip"
