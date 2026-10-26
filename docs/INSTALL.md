# Installing 1loop

Every download is on the **[release page](https://feur-inc.github.io/1loop/)**
(also attached to each [GitHub release](https://github.com/Feur-Inc/1loop/releases)).
Once installed, 1loop updates itself from that page: see
[Updates](../README.md#updates).

## Linux desktop (x86-64)

Pick one format. Each includes a launcher entry; adding it to your applications
menu depends on the installation method below. 1loop itself never writes one.
The downloaded binaries use the build machine's libraries. Current alphas
built on Ubuntu 24.04 require glibc 2.38 or newer: Debian 13 and Ubuntu 24.04
work; Debian 12 and Ubuntu 22.04 do not. Check a `.deb`'s exact requirement
with `dpkg-deb -f FILE.deb Depends`. Build locally or use Nix/Flatpak if your
system is older; the AppImage does not bundle glibc either.

### Debian, Ubuntu and derivatives (`.deb`)

```bash
sudo apt install ./1loop-VERSION-amd64.deb
```

1loop goes to `/usr/lib/1loop`, with `1loop` on your `PATH`, the launcher
entry in `/usr/share/applications` and the icons in `/usr/share/icons/hicolor`.
Updates come from the release page as a new `.deb`: Settings → Updates tells
you when one is out but does not replace a packaged 1loop. Remove it with
`sudo apt remove 1loop`.

### Any distribution (`.tar.gz`)

1. Download `1loop-VERSION-linux-amd64.tar.gz`.
2. Extract it in a folder you own, for example `~/Applications/1loop`:

   ```bash
   mkdir -p ~/Applications/1loop
   tar -xzf 1loop-VERSION-linux-amd64.tar.gz -C ~/Applications/1loop
   ~/Applications/1loop/1loop
   ```

3. Optional: add it to your applications menu.

   ```bash
   ~/Applications/1loop/install.sh
   ```

   This writes `~/.local/share/applications/1loop.desktop` (pointing at this
   folder) and the icons in `~/.local/share/icons/hicolor`. Run it again if
   you move the folder; `install.sh --uninstall` removes both.

Keep `1loop` and `1loop-wayland` together, and the folder writable by you:
1loop updates itself there (Settings → Updates).

Versions before this one wrote `~/.local/share/applications/1loop.desktop`
themselves on every start. If you switch to a package, delete that file (or
run `install.sh --uninstall` from the old folder): it would hide the
package's own entry.

### AppImage

Download `1loop-VERSION-x86_64.AppImage`, then:

```bash
chmod +x 1loop-VERSION-x86_64.AppImage
./1loop-VERSION-x86_64.AppImage
```

It needs FUSE (`fuse3`/`fusermount3`); without it, run it with
`--appimage-extract-and-run`. The AppImage carries the launcher entry and the
icons; to add them to your menu, use an AppImage integration tool such as
Gear Lever or AppImageLauncher. It contains only 1loop: OpenGL, X11/Wayland,
Git, the browser and Python come from the system, as for the `.tar.gz`, so
Browser Use, Terminal Use and Computer Use work the same way. Update it by
downloading the new AppImage.

### Flatpak

Download `1loop-VERSION.flatpak` from an alpha release, or build it from
this repository (there is no Flathub listing yet). Set up Flathub for the
runtime, then install the bundle:

```bash
flatpak remote-add --user --if-not-exists flathub https://flathub.org/repo/flathub.flatpakrepo
flatpak install --user ./1loop-VERSION.flatpak
flatpak run io.github.Feur_Inc.OneLoop
```

To build it yourself, install `flatpak-builder` and `appstream-compose`, then:

```bash
flatpak install --user flathub org.freedesktop.Platform//25.08 org.freedesktop.Sdk//25.08 \
  org.freedesktop.Sdk.Extension.golang//25.08
make flatpak                       # dist/1loop-VERSION.flatpak
flatpak install --user dist/1loop-VERSION.flatpak
flatpak run io.github.Feur_Inc.OneLoop
```

A coding agent works on your real system, so this Flatpak is **not a tight
sandbox**: it can read and write all your files (`--filesystem=host`), and
it runs its commands, its terminal, Git, Chromium/Chrome and Computer Use's
helper **on the host** through `flatpak-spawn --host`
(`--talk-name=org.freedesktop.Flatpak`, see `packaging/flatpak/host-run`).
What that changes:

- **Auto mode's workspace sandbox (Landlock) is off.** Commands run on the
  host, outside the process Landlock restricts, so every command that is not
  plainly safe asks for your approval, as on systems without Landlock.
- **Browser Use** needs Chromium or Google Chrome installed on the host
  system, not as a Flatpak.
- **Computer Use** needs the Python packages listed below on the host, as
  for the other formats.
- **Terminal Use** opens your login shell on the host. The environment the
  sandbox sets for itself (`PATH`, `XDG_*_HOME`, `LD_*`…) is not passed on.
  Interactive job control (`Ctrl+Z`, `fg`) is not supported through this bridge.
- Stopping a command ends it on the host too (`flatpak-spawn --watch-bus`).
- In a Wayland session the Flatpak has no X11 socket
  (`--socket=fallback-x11`), so `ONELOOP_BACKEND=x11` does not work there.
- The window keeps the app id `1loop`; the entry,
  `io.github.Feur_Inc.OneLoop.desktop`, matches it through
  `StartupWMClass=1loop`.
- Updates: rebuild and reinstall.

### Nix and NixOS

The `dev` branch contains the flake (the default `main` branch does not yet):

```bash
nix run github:Feur-Inc/1loop/dev     # try it
nix profile install github:Feur-Inc/1loop/dev
```

On NixOS, add the flake as an input and the package to your configuration:

```nix
# flake.nix
inputs.oneloop.url = "github:Feur-Inc/1loop/dev";
# configuration.nix
environment.systemPackages = [ inputs.oneloop.packages.x86_64-linux.default ];
```

The package installs `1loop` with its launcher entry (`makeDesktopItem`) and
icons, and brings its own Python for Computer Use (`ONELOOP_COMPUTER_PYTHON`,
with PyGObject, dbus-python, Pillow, GTK, GStreamer and its PipeWire
plugin). It needs `hardware.graphics.enable = true;` (on by default with any
desktop). Git and Chromium/Chrome come from your system: add `git` and
`chromium` to your packages for the *Git changes* tab and Browser Use. On
another distribution, the Nix build cannot find your GPU driver: run it
through [nixGL](https://github.com/nix-community/nixGL)
(`nixGL 1loop`), or use the `.tar.gz`. Updates come with `nix flake update`
or `nix profile upgrade`.

### X11, Wayland and XWayland

1loop is built twice, because its window toolkit (GLFW) chooses X11 or
Wayland when it is compiled: `1loop` (X11) and `1loop-wayland` (native
Wayland). Always start `1loop`; it picks the backend when it starts:

| Session | Backend used |
|---|---|
| Wayland (`WAYLAND_DISPLAY` set and the compositor answers) | native Wayland, `1loop-wayland`: sharp text at any scale |
| X11 (`DISPLAY` set) | X11 |
| Wayland with a stale `WAYLAND_DISPLAY` (for example after an ssh or tmux reattach) and `DISPLAY` set | X11, through XWayland |
| Wayland answers but GLFW/OpenGL initialization fails or exceeds 10 seconds, with `DISPLAY` and both binaries available | retry once with X11/XWayland, before loading chats or accounts |
| No display at all | 1loop stops with a message |

`ONELOOP_BACKEND=x11` forces X11 (XWayland in a Wayland session), and
`ONELOOP_BACKEND=wayland` forces native Wayland. XWayland needs an X server
in the session: GNOME and KDE start one by themselves, niri uses
`xwayland-satellite`. Under XWayland with fractional scaling, text may be
blurry: that is XWayland upscaling, and the native backend avoids it.
`ONELOOP_SYSTEM_TITLEBAR=1` keeps the desktop's own title bar instead of
1loop's.

The automatic retry checks initial GLFW/OpenGL setup in a short-lived process,
including native driver crashes. It does not restart 1loop after a later crash
or override an explicitly forced backend. A missing library that prevents the
Wayland binary itself from loading is not covered. Flatpak's Wayland permissions
do not expose X11, so that retry is unavailable there.

### What 1loop uses from the system

Runtime needs, for every format: OpenGL, X11 or Wayland libraries (the `.deb`
pulls them in), Bash, Git (for the *Git changes* tab), and Chrome or Chromium
for the agent's browser (Browser Use). Optional: `latex` and `dvipng` to
render formulas, and the Computer Use packages below.

<details>
<summary>Computer Use on Linux (optional)</summary>

Computer Use controls the real desktop through the XDG RemoteDesktop portal.
It needs `/usr/bin/python3`, `python3-gi`, `python3-dbus`, `python3-pil`,
`gir1.2-gtk-3.0`, `gir1.2-gstreamer-1.0`, `gir1.2-gst-plugins-base-1.0` and
`gstreamer1.0-pipewire`, and a desktop whose portal implements RemoteDesktop
and ScreenCast (GNOME and KDE do).
</details>

## Windows desktop (Windows 10 1809 or newer, x64)

1. Download `1loop-VERSION-windows-amd64.zip`.
2. Extract it to a folder you own, for example
   `C:\Users\you\Applications\1loop`, and run `1loop.exe`.
3. Windows SmartScreen may say the app is unrecognized: choose
   **More info → Run anyway**.

If the window stays blank (no OpenGL driver), start `1loop-software.cmd`: it
uses the bundled Mesa CPU renderer. It needs the Microsoft Visual C++
2015–2022 x64 runtime.

Pairing a phone can trigger a Windows firewall prompt; desktop-only use does
not need inbound access. Git is needed for the *Git changes* tab; Browser Use
finds Chrome, Chromium or Microsoft Edge.

## Android (8.0 or newer)

1. On the phone, download `1loop-VERSION.apk` from the release page.
2. Open it. Android asks to allow your browser or file manager to install
   apps: allow it for this install.
3. **Google Play Protect may warn that the app is harmful, or block it.**
   1loop comes from outside the Play Store and can download and run a Debian
   environment, run commands and control the phone (Phone Use, only when you
   allow it), which Play Protect treats as risky. The code is open: you can
   check what it does in this repository. Choose **Install anyway** (or
   *More details → Install anyway*).
4. Open 1loop and choose where the agent works:
   - **Remote desktop**: a 1loop desktop on your network. On the desktop,
     **Settings → Phone remote → Pair a phone** shows a QR code; scan it with
     **Scan QR code**, or tap the computer in the list and type the 6-digit
     code it shows.
   - **Remote server**: a headless 1loop server, by its HTTPS address and
     certificate fingerprint.
   - **Local phone**: the agent runs on the phone. 1loop installs a Debian
     environment the first time. Phone Use needs
     [Shizuku](https://shizuku.rikka.app/) and your permission.

Updating: **Settings → Updates** downloads the next version and opens
Android's update screen. Your chats and settings are kept. The first time,
Android asks to allow 1loop to install apps: that permission is only used for
1loop's own updates.

## Headless server

Download `1loop-server-VERSION-linux-amd64` (or the Windows `.exe`), make it
executable, and follow [docs/SERVER.md](SERVER.md) to choose a model, run it as
a service and pair your devices. A device connected to the server tells you
when it can update, and updates it on request.

## Where 1loop keeps its data

| | Linux | Windows |
|---|---|---|
| Conversations, settings, tokens | `~/.config/1loop/` | `%AppData%\1loop\` |
| Logs, caches | `~/.cache/1loop/` | `%LocalAppData%\1loop\` |
| Server | `~/.config/1loop-server/` | `%AppData%\1loop-server\` |

The Flatpak keeps its conversations, settings and caches in
`~/.var/app/io.github.Feur_Inc.OneLoop/` (`config/1loop`, `cache/1loop`)
instead.

Uninstalling: remove the package (`apt remove 1loop`, `flatpak uninstall
io.github.Feur_Inc.OneLoop`, `nix profile remove`, or delete the AppImage),
or run `install.sh --uninstall` and delete the folder you extracted. Delete
these folders too if you want to remove your data.
