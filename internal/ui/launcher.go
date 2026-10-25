package ui

// appID is the window's application id (Wayland) and class (X11). The
// desktop finds the application's name and icon in the launcher entry that
// carries the same name: packaging/linux/1loop.desktop, which every Linux
// package installs (StartupWMClass=1loop). 1loop never writes it itself.
const appID = "1loop"
