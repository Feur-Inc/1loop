# Building 1loop manually

Run these commands from the repository root in Bash on Linux x86-64.
The CI executes the same build commands in `.github/actions/build-and-verify/action.yml`.
There is no `ci.sh`, common build shell script, Docker build or compatibility sysroot.
Linux uses the build machine's system libraries; older distributions are not targeted.

## Requirements

On Debian 13, install the native dependencies (as root):

```sh
apt-get update
apt-get install -y --no-install-recommends build-essential cmake patch patchelf \
  pkg-config libgl-dev xorg-dev libwayland-dev libxkbcommon-dev binutils zip \
  unzip dpkg-dev python3 curl ca-certificates git gh jq openssl \
  gcc-mingw-w64-x86-64 p7zip-full
```

Also install Go 1.23+, Java 21 and the Android SDK. Android needs
`platforms;android-37.0`, `build-tools;37.0.0` and NDK `30.0.16248370`.
Set `ANDROID_HOME` to the SDK directory. Native helpers are compiled by the
existing Android build tooling. Linux packaging downloads only the pinned
AppImage packaging tool and its runtime; Windows packaging downloads pinned Mesa.

Set the Go parallelism for all commands in this shell:

```sh
export GOFLAGS="${GOFLAGS:+$GOFLAGS }-p=2"
```

Commands write generated files to `bin/`, `dist/ci/` and `android/app/build/`.
Keep private signing material in the ignored `android/signing/` directory;
see [Android signing](../README.md#android-signing). A local debug key is used
when publication credentials are absent; release remains unsigned in that case.
With publication credentials present, both variants use the same certificate.

## Go checks

```sh
go vet ./...
go test ./...
```

## Linux executables

```sh
set -euo pipefail
version=$(cat VERSION)
out=dist/ci
mkdir -p "$out"
formats=(tarball deb appimage)

# packaged is the internal/update.Packaged value of each format.
packaged() {
  case $1 in
  tarball) echo "" ;;
  deb) echo "deb" ;;
  appimage) echo "AppImage" ;;
  esac
}

binaries() {
  local format flags
  for format in "${formats[@]}"; do
    flags="-s -w -X 1loop/internal/update.Version=$version -X 1loop/internal/update.Packaged=$(packaged "$format")"
    mkdir -p "bin/linux/$format"
    CGO_ENABLED=1 go build -trimpath -buildmode=pie -ldflags="$flags" \
      -o "bin/linux/$format/1loop" ./cmd/1loop
    # See WAYLAND_CFLAGS in the Makefile.
    CGO_ENABLED=1 CGO_CFLAGS="$(go env CGO_CFLAGS) -w" go build -trimpath -buildmode=pie \
      -ldflags="$flags" -tags wayland -o "bin/linux/$format/1loop-wayland" ./cmd/1loop
  done
  echo "Built bin/linux/{${formats[*]}}/ (needs glibc $(required_glibc bin/linux/tarball/1loop bin/linux/tarball/1loop-wayland))"
}

required_glibc() {
  objdump -T "$@" | grep -o 'GLIBC_[0-9.]*' | sort -uV | tail -1 | sed 's/^GLIBC_//'
}

binaries
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X 1loop/internal/update.Version=$version" \
  -o "$out/1loop-server-$version-linux-amd64" ./cmd/1loop-server
```

## Linux packages

```sh
set -euo pipefail
version=$(cat VERSION)
out=dist/ci
mkdir -p "$out"
dist=$out
required_glibc() {
  objdump -T "$@" | grep -o 'GLIBC_[0-9.]*' | sort -uV | tail -1 | sed 's/^GLIBC_//'
}

# share STAGE: the desktop entry and the icons in STAGE/applications and
# STAGE/icons, as under /usr/share.
share() {
  mkdir -p "$1/applications" "$1/icons"
  cp packaging/linux/1loop.desktop "$1/applications/"
  go run ./cmd/1loop-release icons "$1/icons"
}

need() {
  [ -x "bin/linux/$1/1loop" ] && [ -x "bin/linux/$1/1loop-wayland" ] ||
    { echo "Build the Linux executables first; see docs/BUILD.md" >&2; exit 1; }
}

tarball() {
  need tarball
  local stage
  stage=$(mktemp -d)
  trap 'rm -rf "$stage"' RETURN
  chmod 755 "$stage" # the archive's root: the folder it is extracted in
  cp bin/linux/tarball/1loop bin/linux/tarball/1loop-wayland packaging/linux/install.sh "$stage/"
  share "$stage/share"
  mkdir -p "$dist"
  # Files at the archive's root: the updater replaces them in place.
  tar -C "$stage" --owner=0 --group=0 -czf "$dist/1loop-$version-linux-amd64.tar.gz" .
  echo "$dist/1loop-$version-linux-amd64.tar.gz"
}

deb() {
  need deb
  local stage root maintainer
  # DEB_MAINTAINER, or whoever builds the release, as for its commits.
  maintainer=${DEB_MAINTAINER:-"$(git config user.name) <$(git config user.email)>"}
  stage=$(mktemp -d)
  trap 'rm -rf "$stage"' RETURN
  root=$stage/1loop
  install -Dm755 bin/linux/deb/1loop "$root/usr/lib/1loop/1loop"
  install -Dm755 bin/linux/deb/1loop-wayland "$root/usr/lib/1loop/1loop-wayland"
  mkdir -p "$root/usr/bin"
  # 1loop finds 1loop-wayland beside its real path, links resolved.
  ln -s ../lib/1loop/1loop "$root/usr/bin/1loop"
  share "$root/usr/share"
  mkdir -p "$root/usr/share/doc/1loop"
  printf '1loop (%s) stable; urgency=medium\n\n  * See https://github.com/Feur-Inc/1loop/releases/tag/v%s\n\n -- %s  %s\n' \
    "$version" "$version" "$maintainer" "$(date -R)" | gzip -9n > "$root/usr/share/doc/1loop/changelog.gz"
  printf 'Format: https://www.debian.org/doc/packaging-manuals/copyright-format/1.0/\nUpstream-Name: 1loop\nSource: https://github.com/Feur-Inc/1loop\n' > "$root/usr/share/doc/1loop/copyright"
  mkdir -p "$root/DEBIAN"
  # Installed-Size is in KiB.
  cat > "$root/DEBIAN/control" <<EOF
Package: 1loop
Version: $version
Architecture: amd64
Maintainer: $maintainer
Installed-Size: $(du -sk --exclude=DEBIAN "$root" | cut -f1)
Section: devel
Priority: optional
Homepage: https://github.com/Feur-Inc/1loop
Depends: libc6 (>= $(required_glibc bin/linux/deb/1loop bin/linux/deb/1loop-wayland)), libgl1, libx11-6, libxrandr2, libxxf86vm1, libxi6, libxcursor1, libxinerama1, libwayland-client0, libwayland-cursor0, libwayland-egl1, libxkbcommon0
Recommends: git, chromium | google-chrome-stable | chromium-browser
Suggests: python3, python3-gi, python3-dbus, python3-pil, gir1.2-gtk-3.0, gir1.2-gstreamer-1.0, gir1.2-gst-plugins-base-1.0, gstreamer1.0-pipewire, texlive-latex-base, dvipng
Description: Coding agent for your project, with your model
 1loop runs an AI agent on your project with the model you choose. It reads
 and edits files, runs commands in a terminal, drives a browser (Chromium or
 Chrome) and, on desktops whose portal allows it, the desktop itself.
 .
 Starts natively on Wayland and on X11 (including XWayland).
EOF
  mkdir -p "$dist"
  dpkg-deb --root-owner-group -Zxz --build "$root" "$dist/1loop-$version-amd64.deb" >/dev/null
  echo "$dist/1loop-$version-amd64.deb"
}

# appimage_tools downloads appimagetool and the AppImage runtime it packs,
# pinned and checked, into the build cache: the runtime is given to
# appimagetool so it fetches nothing itself. It runs extracted, without FUSE.
appimage_tools() {
  local cache="${XDG_CACHE_HOME:-$HOME/.cache}/1loop-linux-build/appimage" name url sum
  mkdir -p "$cache"
  # Only the hash is read: not every sha256sum has -c.
  checked() { [ -f "$1" ] && [ "$(sha256sum "$1" | cut -d' ' -f1)" = "$2" ]; }
  while read -r name url sum; do
    if ! checked "$cache/$name" "$sum"; then
      curl -fsSL -o "$cache/$name" "$url"
      checked "$cache/$name" "$sum" || { echo "$name does not match its checksum" >&2; exit 1; }
    fi
  done <<'TOOLS'
appimagetool-1.9.1 https://github.com/AppImage/appimagetool/releases/download/1.9.1/appimagetool-x86_64.AppImage ed4ce84f0d9caff66f50bcca6ff6f35aae54ce8135408b3fa33abfc3cb384eb0
runtime-20251108 https://github.com/AppImage/type2-runtime/releases/download/20251108/runtime-x86_64 2fca8b443c92510f1483a883f60061ad09b46b978b2631c807cd873a47ec260d
TOOLS
  chmod +x "$cache/appimagetool-1.9.1"
  appimagetool=("$cache/appimagetool-1.9.1" --appimage-extract-and-run)
  runtime=$cache/runtime-20251108
}

appimage() {
  need appimage
  local appimagetool runtime
  appimage_tools
  local stage app
  stage=$(mktemp -d)
  trap 'rm -rf "$stage"' RETURN
  app=$stage/1loop.AppDir
  install -Dm755 bin/linux/appimage/1loop "$app/usr/lib/1loop/1loop"
  install -Dm755 bin/linux/appimage/1loop-wayland "$app/usr/lib/1loop/1loop-wayland"
  share "$app/usr/share"
  cp packaging/linux/1loop.desktop "$app/"
  cp "$app/usr/share/icons/hicolor/256x256/apps/1loop.png" "$app/1loop.png"
  ln -s 1loop.png "$app/.DirIcon"
  cat > "$app/AppRun" <<'EOF'
#!/bin/sh
# Nothing is bundled but 1loop itself: OpenGL, X11, Wayland, Git, the
# browser and Python come from the system, as for the other formats.
here=$(dirname -- "$(readlink -f -- "$0")")
exec "$here/usr/lib/1loop/1loop" "$@"
EOF
  chmod 755 "$app/AppRun"
  mkdir -p "$dist"
  ARCH=x86_64 "${appimagetool[@]}" --no-appstream --runtime-file "$runtime" \
    "$app" "$dist/1loop-$version-x86_64.AppImage" >/dev/null
  echo "$dist/1loop-$version-x86_64.AppImage"
}

tarball
deb
appimage
dpkg-deb --info "$dist/1loop-$version-amd64.deb"
```

## Windows executables and archive

```sh
set -euo pipefail
version=$(cat VERSION)
out=dist/ci
mkdir -p "$out"
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
```

## Android APKs and checks

```sh
set -euo pipefail
version=$(cat VERSION)
out=dist/ci
mkdir -p "$out"
echo 'Building Android APKs, running unit tests and lint'
./android/gradlew -p android --no-daemon --no-parallel --max-workers=1 \
  -Dorg.gradle.jvmargs=-Xmx1536m -Pkotlin.compiler.execution.strategy=in-process \
  :app:assembleDebug :app:assembleRelease :app:testDebugUnitTest :app:lintDebug \
  --warning-mode fail --console=plain

echo 'Packaging Android native corresponding sources'
python3 android/runtime/build.py --output android/app/build/local-runtime \
  --source-bundle android/app/build/local-runtime/native-sources.zip

# Publication credentials sign both variants; contributors use a local debug key.
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
```

## Other Linux packaging formats

Flatpak and Nix use their existing package definitions, with no build wrapper:

```sh
flatpak-builder --repo=bin/flatpak-repo --state-dir=bin/flatpak-state \
  bin/flatpak-build packaging/flatpak/io.github.Feur_Inc.OneLoop.yml
mkdir -p dist/ci
flatpak build-bundle bin/flatpak-repo "dist/ci/1loop-$(cat VERSION).flatpak" io.github.Feur_Inc.OneLoop
nix build .#default
```

These require the Flatpak SDK/platform and Nix described in [INSTALL.md](INSTALL.md).
The alpha CI publishes the tarball, Debian package, AppImage, Windows archive,
headless servers and signed APK; it does not build the optional Flatpak/Nix targets.
Windows is cross-compiled: testing its runtime needs a Windows machine.

## Stable publication

Once the checks above have passed for the clean source commit and generated downloads,
`SKIP_CI=1 bash release.sh` publishes the prebuilt downloads. This publisher does not
compile the application. Keep the signing keys and review the release version/notes
as explained in [Publishing an update](../README.md#publishing-an-update).
