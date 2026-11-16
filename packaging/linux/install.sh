#!/bin/sh
# SPDX-License-Identifier: MPL-2.0
# Adds 1loop, from the folder this script is in, to your applications menu:
# a launcher entry in ~/.local/share/applications and the icon in
# ~/.local/share/icons. Optional: 1loop runs without it, and never writes
# these files itself. Packages (.deb, AppImage, Flatpak, Nix) install their
# own entry instead.
#
# Usage: ./install.sh              add the entry (again after moving the folder)
#        ./install.sh --uninstall  remove it
set -eu

here=$(cd -- "$(dirname -- "$0")" && pwd)
data=${XDG_DATA_HOME:-$HOME/.local/share}
id=io.github.Feur_Inc.OneLoop
entry=$data/applications/$id.desktop

refresh() {
  command -v update-desktop-database >/dev/null 2>&1 && update-desktop-database -q "$data/applications" || true
  command -v gtk-update-icon-cache >/dev/null 2>&1 && gtk-update-icon-cache -q -t "$data/icons/hicolor" || true
}

# legacy removes what this script and 1loop itself wrote before #431, as
# 1loop.desktop and 1loop.png: an entry of that name, left behind when its
# folder moved or went, hid the packages' entry and its icon.
legacy() {
  old=$data/applications/1loop.desktop
  if [ -f "$old" ] && grep -qx 'Name=1loop' "$old" && grep -qx 'StartupWMClass=1loop' "$old"; then
    rm -f "$old"
  fi
  for icon in "$data"/icons/hicolor/*/apps/1loop.png; do
    [ -e "$icon" ] && rm -f "$icon"
  done
  return 0
}

case ${1:-} in
--uninstall)
  rm -f "$entry"
  for icon in "$data"/icons/hicolor/*/apps/$id.png; do
    [ -e "$icon" ] && rm -f "$icon"
  done
  legacy
  refresh
  echo "Removed 1loop from the applications menu."
  exit 0
  ;;
"") ;;
*)
  echo "Usage: $0 [--uninstall]" >&2
  exit 2
  ;;
esac

[ -x "$here/1loop" ] || { echo "No 1loop binary beside this script ($here)" >&2; exit 1; }

# Exec quoting from the desktop entry specification.
exec_path=$(printf '%s' "$here/1loop" | sed -e 's/[\\"`$]/\\&/g' -e 's/\\/\\\\/g')
mkdir -p "$data/applications"
sed "s|^Exec=.*|Exec=\"$(printf '%s' "$exec_path" | sed 's/[|&\\]/\\&/g')\"|" "$here/share/applications/$id.desktop" > "$entry.tmp"
mv "$entry.tmp" "$entry"
(cd "$here/share/icons" && find hicolor -name "$id.png") | while read -r icon; do
  mkdir -p "$data/icons/$(dirname "$icon")"
  cp "$here/share/icons/$icon" "$data/icons/$icon"
done
legacy
refresh
echo "Added 1loop to the applications menu ($entry)."
