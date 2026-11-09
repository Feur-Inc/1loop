#!/usr/bin/env bash
# SPDX-License-Identifier: MPL-2.0
# Prepares the end-of-sprint release (docs/BUILD.md#the-end-of-a-sprint):
#   1. lists the pull requests merged into dev since the last stable tag;
#   2. picks the next version: minor when one of them is labelled
#      enhancement (something new), patch otherwise; or the one given;
#   3. writes it in VERSION and drafts docs/releases/VERSION.md, grouped by
#      label, with the previous release's Install section.
# The draft is a start: reword it for users before release.sh publishes it.
#
# Usage: bash release-prepare.sh [VERSION]
set -euo pipefail
cd -- "$(dirname -- "${BASH_SOURCE[0]}")"

repo=Feur-Inc/1loop
previous=$(cat VERSION)
last=$(git tag --list 'v[0-9]*' --sort=-version:refname | grep -vE -- '-' | head -1)
[ -n "$last" ] || { echo "No stable tag (vX.Y.Z) to start from: fetch the tags first (git fetch --tags)"; exit 1; }
since=$(git log -1 --format=%cI "$last")

prs=$(gh pr list --repo "$repo" --base dev --state merged --limit 500 \
  --search "merged:>$since" --json number,title,labels \
  --jq 'sort_by(.number) | .[] | [.number, ([.labels[].name] | join(",")), .title] | @tsv')
[ -n "$prs" ] || { echo "Nothing was merged into dev since $last"; exit 1; }

if [ $# -ge 1 ]; then
  version=$1
else
  IFS=. read -r major minor patch <<< "${last#v}"
  if grep -qE $'^[0-9]+\t([^\t]*,)?enhancement(,|\t)' <<< "$prs"; then
    version=$major.$((minor + 1)).0
  else
    version=$major.$minor.$((patch + 1))
  fi
fi
notes=docs/releases/$version.md
[ ! -e "$notes" ] || { echo "$notes already exists: edit it, or remove it to draft it again"; exit 1; }

# The pull requests with a label, a line each: "- **Title** (#N)".
section() {
  local title=$1 label=$2 lines
  lines=$(awk -F'\t' -v label="$label" '{ n = split($2, l, ","); for (i = 1; i <= n; i++) if (l[i] == label) { printf "- **%s** (#%s)\n", $3, $1; break } }' <<< "$prs")
  [ -z "$lines" ] || printf '**%s %s**\n\n%s\n\n' "$title" "$version" "$lines"
}
other=$(awk -F'\t' '$2 !~ /(^|,)(enhancement|bug|documentation)(,|$)/ { printf "- **%s** (#%s)\n", $3, $1 }' <<< "$prs")

{
  section "New in" enhancement
  section "Fixed in" bug
  section "Documentation in" documentation
  [ -z "$other" ] || printf '**Also in %s** (no type label: move each where it belongs)\n\n%s\n\n' "$version" "$other"
  echo '<!-- end of the in-app notes -->'
  # The Install section of the last notes, for the new version.
  if [ -f "docs/releases/$previous.md" ]; then
    sed -n '/<!-- end of the in-app notes -->/,$p' "docs/releases/$previous.md" | tail -n +2 | sed "s/$previous/$version/g"
  fi
} > "$notes"
echo "$version" > VERSION

count=$(wc -l <<< "$prs")
echo "$count pull requests since $last: $previous → $version"
echo "Drafted $notes. Reword it for users (what changes for them, not how), then commit"
echo "VERSION and the notes, merge dev into main, and publish with release.sh."
