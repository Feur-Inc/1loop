#!/usr/bin/env bash
# Builds the Android app:
#
#   dist/1loop-VERSION.apk                         the release APK
#   dist/1loop-VERSION-android-native-sources.zip  sources of its native helpers
#
# DIST sets the output folder (dist by default). Needs JDK 21 and the Android
# SDK (ANDROID_HOME) with build-tools 37.0.0, and the NDK 30.0.16248370
# (ANDROID_NDK_HOME), cmake, make, patch and patchelf for the native helpers.
#
# Debug and release are signed with the publication key read from
# android/signing/release.properties (or ONELOOP_ANDROID_SIGNING_PROPERTIES):
# the release APK is copied only when both carry that same certificate.
# Without the key, debug uses the local debug key and release stays unsigned,
# so no APK lands in dist/.
set -euo pipefail
cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.."

version=$(cat VERSION)
dist=${DIST:-dist}
apks=android/app/build/outputs/apk
apksigner="${ANDROID_HOME:-$HOME/Android/Sdk}/build-tools/37.0.0/apksigner"

./android/gradlew -p android --console=plain :app:assembleDebug :app:assembleRelease
python3 android/runtime/build.py --output android/app/build/local-runtime \
  --source-bundle android/app/build/local-runtime/native-sources.zip

mkdir -p "$dist"
cp android/app/build/local-runtime/native-sources.zip "$dist/1loop-$version-android-native-sources.zip"
echo "$dist/1loop-$version-android-native-sources.zip"

# certificate APK: the SHA-256 of its signing certificate, empty if unsigned.
certificate() {
  { "$apksigner" verify --print-certs "$1" 2>/dev/null || true; } | sed -n 's/^.*certificate SHA-256 digest: //p' | sort -u
}
release=$(certificate "$apks/release/app-release.apk")
if [ -z "$release" ]; then
  echo "The release APK is unsigned (no publication key): not copied to $dist" >&2
  exit 0
fi
if [ "$release" != "$(certificate "$apks/debug/app-debug.apk")" ]; then
  echo "The debug and release APKs are not signed with the same certificate" >&2
  exit 1
fi
cp "$apks/release/app-release.apk" "$dist/1loop-$version.apk"
echo "$dist/1loop-$version.apk"
