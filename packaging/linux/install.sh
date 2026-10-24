#!/bin/sh
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
entry=$data/applications/1loop.desktop

refresh() {
  command -v update-desktop-database >/dev/null 2>&1 && update-desktop-database -q "$data/applications" || true
  command -v gtk-update-icon-cache >/dev/null 2>&1 && gtk-update-icon-cache -q -t "$data/icons/hicolor" || true
}

case ${1:-} in
--uninstall)
  rm -f "$entry"
  for icon in "$data"/icons/hicolor/*/apps/1loop.png; do
    [ -e "$icon" ] && rm -f "$icon"
  done
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
sed "s|^Exec=.*|Exec=\"$(printf '%s' "$exec_path" | sed 's/[|&\\]/\\&/g')\"|" "$here/share/applications/1loop.desktop" > "$entry.tmp"
mv "$entry.tmp" "$entry"
(cd "$here/share/icons" && find hicolor -name 1loop.png) | while read -r icon; do
  mkdir -p "$data/icons/$(dirname "$icon")"
  cp "$here/share/icons/$icon" "$data/icons/$icon"
done
refresh
echo "Added 1loop to the applications menu ($entry)."
