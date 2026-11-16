// SPDX-License-Identifier: MPL-2.0

package ui

// appID is the window's application id (Wayland) and class (X11). The
// desktop finds the application's name and icon in the launcher entry that
// carries the same name: packaging/linux/io.github.Feur_Inc.OneLoop.desktop,
// which every Linux package installs, with the icon of the same name. 1loop
// never writes it itself.
//
// It was "1loop" until #431: a 1loop.desktop left in
// ~/.local/share/applications by an older install.sh, whose 1loop no longer
// existed, hid the package's entry of the same name, and GNOME showed the
// window with a generic icon. The reverse-DNS name is the Flatpak's too.
const appID = "io.github.Feur_Inc.OneLoop"
