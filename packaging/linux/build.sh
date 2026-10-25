#!/usr/bin/env bash
# Builds the Linux desktop downloads (x86-64) from the repository:
#
#   packaging/linux/build.sh binaries   bin/linux/<format>/1loop and 1loop-wayland
#   packaging/linux/build.sh tarball    dist/1loop-VERSION-linux-amd64.tar.gz
#   packaging/linux/build.sh deb        dist/1loop-VERSION-amd64.deb
#   packaging/linux/build.sh appimage   dist/1loop-VERSION-x86_64.AppImage
#
# Several steps can be given at once, in that order. DIST sets the output
# folder (dist by default).
# The binaries are built in a Debian 12 container (Docker), against its
# glibc 2.36: built on a newer system they would not start on Debian 12 or
# Ubuntu 22.04. Go and the module cache come from the host, as in ci.sh.
# Each format gets its own link, naming it in internal/update.Packaged:
# packaged builds leave updates to the way they were installed.
#
# Every format ships packaging/linux/1loop.desktop and the icon, rendered by
# `1loop-release icons`; 1loop never installs them itself.
set -euo pipefail
cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.."

version=$(cat VERSION)
dist=${DIST:-dist}
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
  docker build -q --platform linux/amd64 -t 1loop-linux-build - >/dev/null <<'EOF'
FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends gcc libc6-dev pkg-config git ca-certificates \
      libgl1-mesa-dev libx11-dev libxrandr-dev libxxf86vm-dev libxi-dev libxcursor-dev libxinerama-dev \
      libwayland-dev libxkbcommon-dev && rm -rf /var/lib/apt/lists/*
EOF
  local cache="${XDG_CACHE_HOME:-$HOME/.cache}/1loop-linux-build" list=""
  mkdir -p "$cache/go" bin/linux
  for format in "${formats[@]}"; do list+="$format=$(packaged "$format") "; done
  docker run --rm --platform linux/amd64 -u "$(id -u):$(id -g)" \
    -v "$(go env GOROOT)":/usr/local/go:ro -v "$(go env GOMODCACHE)":/gomod \
    -v "$PWD":/src:ro -v "$PWD/bin/linux":/out -v "$cache":/cache -w /src \
    -e HOME=/tmp -e GOMODCACHE=/gomod -e GOCACHE=/cache/go -e GOFLAGS="${GOFLAGS:-}" -e GOTOOLCHAIN=local \
    -e CGO_ENABLED=1 -e FORMATS="$list" \
    1loop-linux-build bash -euo pipefail -c '
export PATH=/usr/local/go/bin:$PATH
for entry in $FORMATS; do
  format=${entry%%=*} packaged=${entry#*=}
  flags="-s -w -X 1loop/internal/update.Version=$(cat VERSION) -X 1loop/internal/update.Packaged=$packaged"
  mkdir -p /out/$format
  go build -trimpath -buildmode=pie -ldflags="$flags" -o /out/$format/1loop ./cmd/1loop
  # See WAYLAND_CFLAGS in the Makefile.
  CGO_CFLAGS="$(go env CGO_CFLAGS) -w" go build -trimpath -buildmode=pie -ldflags="$flags" -tags wayland -o /out/$format/1loop-wayland ./cmd/1loop
done'
  local glibc
  glibc=$(objdump -T bin/linux/tarball/1loop bin/linux/tarball/1loop-wayland | grep -o 'GLIBC_[0-9.]*' | sort -uV | tail -1)
  echo "Built bin/linux/{${formats[*]}}/ (needs $glibc)"
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
    { echo "Run packaging/linux/build.sh binaries first" >&2; exit 1; }
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
Depends: libc6 (>= 2.34), libgl1, libx11-6, libxrandr2, libxxf86vm1, libxi6, libxcursor1, libxinerama1, libwayland-client0, libwayland-cursor0, libwayland-egl1, libxkbcommon0
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

[ $# -gt 0 ] || set -- usage
for step; do
  case $step in
  binaries | tarball | deb | appimage) "$step" ;;
  *)
    echo "Usage: $0 binaries|tarball|deb|appimage..." >&2
    exit 2
    ;;
  esac
done
