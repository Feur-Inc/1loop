#!/usr/bin/env bash
# Publishes the version written in VERSION:
#   1. builds the downloads in dist/VERSION/ with packaging/*/build.sh
#      (docs/BUILD.md);
#   2. checks that the APK is signed with the publication key;
#   3. signs the update manifest with release-key.pem (never committed);
#   4. publishes the files and the manifest on the release page, GitHub Pages
#      served from the gh-pages branch: https://tpm28.github.io/1loop/;
#   5. creates the GitHub release with the same files.
# Installed apps find the new version through that manifest.
#
# Usage: bash release.sh
set -euo pipefail
cd -- "$(dirname -- "${BASH_SOURCE[0]}")"

version=$(cat VERSION)
key=${RELEASE_KEY:-release-key.pem}
repo=tpm28/1loop
site=https://tpm28.github.io/1loop
notes=docs/releases/$version.md
out=dist/$version
pages=dist/gh-pages

token=${GH_TOKEN:-$(cat TOKEN 2>/dev/null || true)}
[ -n "$token" ] || { echo "Set GH_TOKEN or put a GitHub token in TOKEN"; exit 1; }
export GH_TOKEN=$token
auth="Authorization: Basic $(printf 'x-access-token:%s' "$token" | base64 -w0)"

[ -f "$key" ] || { echo "No release key at $key: the updates must be signed"; exit 1; }
[ -f "$notes" ] || { echo "Write the release notes in $notes first"; exit 1; }
[ -z "$(git status --porcelain --untracked-files=no)" ] || { echo "Commit your changes first: the release is built from HEAD"; exit 1; }
[ ! -e "$out" ] || { echo "$out already exists"; exit 1; }
if gh release view "v$version" --repo "$repo" >/dev/null 2>&1; then
  echo "v$version is already released: raise VERSION"
  exit 1
fi

echo "Building $version in $out"
mkdir -p "$out"
DIST=$out packaging/linux/build.sh binaries tarball deb appimage
DIST=$out packaging/server/build.sh
DIST=$out packaging/windows/build.sh
DIST=$out packaging/android/build.sh
[ -f "$out/1loop-$version.apk" ] || { echo "No signed APK: set up android/signing/release.properties (docs/BUILD.md)"; exit 1; }

echo "Signing the update manifest"
go run ./cmd/1loop-release manifest -key "$key" -version "$version" \
  -base "$site/updates/$version" -page "$site/" -notes "$notes" \
  "desktop-linux-amd64=$out/1loop-$version-linux-amd64.tar.gz" \
  "server-linux-amd64=$out/1loop-server-$version-linux-amd64" \
  "desktop-windows-amd64=$out/1loop-$version-windows-amd64.zip" \
  "server-windows-amd64=$out/1loop-server-$version-windows-amd64.exe" \
  "android=$out/1loop-$version.apk" > "$out/latest.json"

echo "Publishing on the release page ($site)"
if [ ! -d "$pages" ]; then
  if git -c http.extraHeader="$auth" ls-remote --exit-code --heads origin gh-pages >/dev/null 2>&1; then
    git -c http.extraHeader="$auth" fetch -q origin gh-pages
    git worktree add -q -B gh-pages "$pages" origin/gh-pages
  else
    git worktree add -q --orphan -b gh-pages "$pages"
  fi
fi
mkdir -p "$pages/updates/$version"
cp "$out"/1loop-* "$pages/updates/$version/"
cp "$out/latest.json" "$pages/updates/latest.json"
touch "$pages/.nojekyll"
sed -e "s|{{VERSION}}|$version|g" -e "s|{{DATE}}|$(date -u +%Y-%m-%d)|g" docs/site/index.html > "$pages/index.html"
cp assets/1loop-logo.png assets/1loop-ring-on-black.png "$pages/"
git -C "$pages" add -A
git -C "$pages" -c user.name="$(git config user.name)" -c user.email="$(git config user.email)" commit -q -m "Release $version"
git -C "$pages" -c http.extraHeader="$auth" push -q origin gh-pages

echo "Creating the GitHub release v$version"
gh release create "v$version" --repo "$repo" --target "$(git rev-parse HEAD)" \
  --title "1loop $version" --notes-file "$notes" "$out"/1loop-*

printf '\nReleased %s:\n  %s/\n  https://github.com/%s/releases/tag/v%s\n' "$version" "$site" "$repo" "$version"
