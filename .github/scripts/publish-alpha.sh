#!/usr/bin/env bash
# Called only after all three platform jobs uploaded their checked downloads.
set -euo pipefail
cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.."

: "${GH_REPO:?}" "${RELEASE_TAG:?}" "${RELEASE_KEY_PEM:?}"
version=$(cat VERSION)
work=$(mktemp -d "$RUNNER_TEMP/1loop-alpha.XXXXXX")
key=$work/release-key.pem
trap 'rm -f -- "$key"' EXIT
umask 077
printf '%s\n' "$RELEASE_KEY_PEM" > "$key"
openssl pkey -in "$key" -pubout -out "$work/release-key.pub"
cmp .github/release-key.pub "$work/release-key.pub"

assets=(
  "1loop-$version-linux-amd64.tar.gz"
  "1loop-$version-amd64.deb"
  "1loop-$version-x86_64.AppImage"
  "1loop-server-$version-linux-amd64"
  "1loop-$version-windows-amd64.zip"
  "1loop-server-$version-windows-amd64.exe"
  "1loop-$version.apk"
  "1loop-$version-android-native-sources.zip"
)
release=$(gh release view "$RELEASE_TAG" --json isDraft,targetCommitish)
test "$(jq -r .isDraft <<< "$release")" = true
test "$(jq -r .targetCommitish <<< "$release")" = "$GITHUB_SHA"
gh release download "$RELEASE_TAG" --dir "$work"
for asset in "${assets[@]}"; do test -s "$work/$asset"; done
(
  cd "$work"
  sha256sum "${assets[@]}" > SHA256SUMS
)
openssl dgst -sha256 -sign "$key" -out "$work/SHA256SUMS.sig" "$work/SHA256SUMS"
openssl dgst -sha256 -verify .github/release-key.pub \
  -signature "$work/SHA256SUMS.sig" "$work/SHA256SUMS"
gh release upload "$RELEASE_TAG" "$work/SHA256SUMS" "$work/SHA256SUMS.sig" \
  "$work/release-key.pub" --clobber
cat > "$work/notes.md" <<NOTES
Test build of 1loop $version from commit $GITHUB_SHA.

Includes the Linux tarball, Debian package and AppImage (built against Debian 12),
Windows desktop with software renderer, both headless servers, and the Android APK
plus its native corresponding sources. All platform checks passed before publication.
Windows is cross-compiled on Linux; Windows runtime tests are not executed here.

Verify the downloads with the public key from this source commit:
https://github.com/$GH_REPO/blob/$GITHUB_SHA/.github/release-key.pub

\`\`\`sh
openssl dgst -sha256 -verify release-key.pub -signature SHA256SUMS.sig SHA256SUMS
sha256sum --check SHA256SUMS
\`\`\`
NOTES
gh release edit "$RELEASE_TAG" --draft=false --prerelease --latest=false --notes-file "$work/notes.md"

# Match only our published alpha-N prereleases; stable and other releases stay intact.
releases=$(gh api --paginate --slurp "repos/$GH_REPO/releases?per_page=100")
stale=$(jq -r '[.[][] | select(.draft == false and .prerelease == true
  and (.tag_name | test("^alpha-[0-9]+$")))]
  | sort_by(.published_at, .id) | reverse | .[3:][] | .tag_name' <<< "$releases")
while IFS= read -r tag; do
  [[ -n $tag ]] || continue
  gh release delete "$tag" --cleanup-tag --yes
done <<< "$stale"
