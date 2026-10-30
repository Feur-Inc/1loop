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
- **Browser Use** runs the host's Chromium or Google Chrome (not a
  Flatpak). Without one, 1loop downloads its own Chromium, which runs on the
  host from `~/.var/app/io.github.Feur_Inc.OneLoop/config/1loop/chromium`.
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
desktop). It also brings nixpkgs' Chromium for Browser Use
(`ONELOOP_CHROMIUM`). Git comes from your system: add `git` to your packages
for the *Git changes* tab. On
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

### Import browser cookies on desktop

In **Settings → General → Browser → Import cookies**, choose a JSON file
or a compatible local browser profile. Read the account-access warning,
turn on **I understand**, then select **Import**. This is desktop-only;
no cookie import is exposed on Android or through the remote API.

For Chrome, Edge and other browsers, including protected Windows profiles:

1. Open the source browser/profile and the site whose account you want to share.
2. Use a cookie-export extension you trust, such as
   [Cookie-Editor](https://github.com/Moustachauve/cookie-editor), installed
   through its official browser-store link. Grant access only to the sites
   you intend to export, then choose **Export → JSON**.
3. If the extension copies JSON to the clipboard, paste it into a local text
   editor and save it as `cookies.json`. Choose that file in 1loop.
4. Open the site in Browser Use to verify whether the session works. Some
   sites require signing in again; copied cookies alone cannot guarantee login.
5. Keep the export private. It contains sensitive session data. Remove it
   yourself when finished, and disable/remove the export extension if unused.
   1loop reads the selected file locally without changing or deleting it.

The importer accepts a JSON array of cookies, or an object with a `cookies`
array, using `name`, `value`, `domain`, `path`, `secure`, `httpOnly`,
`sameSite`, `hostOnly` and `expirationDate` (or DevTools `expires`). Session
cookies are accepted from JSON but may end when Browser Use closes.
Expired, invalid, container and partitioned cookies are skipped.

Direct profile import supports Firefox-family profiles on Windows/Linux and
compatible Chromium-family profiles on Linux. Close the source browser first.
It reads a temporary copy; Linux Chromium uses its own browser and system
keyring. Protected Windows Chromium profiles require a voluntary JSON export.
An inaccessible keyring or browser installation can prevent direct import.

Cookies can grant access to accounts you did not intend to share. Agents may
act as those accounts, and malicious pages can influence their actions.
**Clear browsing data** removes Browser Use's local sessions and data; it
neither revokes every server-side session nor undoes previous actions. No
passwords, history, bookmarks or local storage are imported.

### What 1loop uses from the system

Runtime needs, for every format: OpenGL, X11 or Wayland libraries (the `.deb`
pulls them in), Bash and Git (for the *Git changes* tab). Optional: `latex`
and `dvipng` to render formulas, and the Computer Use packages below.

Browser Use runs the Chromium, Chrome or Edge it finds on the computer. Without
one, it downloads its own Chromium the first time (Chrome for Testing, Google's
build made to be driven, about 200 MB), into `~/.config/1loop/chromium` on
Linux and `%AppData%\1loop\chromium` on Windows: no package to install and no
administrator rights. 1loop checks once a day for a newer stable version and
uses it from the next start. On Linux it uses the system libraries every
desktop has (NSS, GTK, ALSA…). Where the system allows user namespaces only to
browsers installed by the administrator (Ubuntu 23.10 and later), it runs
without Chromium's own sandbox.

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
finds Chrome, Chromium or Microsoft Edge, or downloads its own Chromium into
`%AppData%\1loop\chromium`.

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

## GitHub connector

1. On desktop, open **Settings → Connectors → GitHub**. On Android, open
   **Settings → Connectors · GitHub**. The connection belongs to the execution
   target: the computer, server or Local phone currently running the agent.
2. Choose **Choose repositories on GitHub** and install
   [1loop GitHub Connector](https://github.com/apps/1loop-github-connector)
   on the repositories you want to grant. The app requests read/write access
   to contents, issues and pull requests, and read access to metadata.
3. Choose **Connect GitHub**, open the GitHub link, enter the temporary code
   shown by 1loop, and review the authorization in your browser.
4. Once connected, GitHub's official hosted MCP tools become available to
   the agent. Their actual capabilities depend on the app's permissions and
   your own repository access. Write operations follow the selected approval
   mode.
5. Turn **Enable GitHub tools** off to remove them from later model requests
   and reject later calls. An operation already sent to GitHub may finish.
   **Disconnect** clears the locally saved authorization; **Review access on
   GitHub** lets you revoke the app. **Manage repository access** opens the
   app's installation settings to change its repository grants.

Access and refresh tokens are saved in `github-connector.json` inside the
execution target's private `1loop` configuration folder, separately from
model configuration. They are not included in tool schemas or given to
Claude Code subprocesses. Expiring authorizations are renewed automatically;
after revocation, connect again. No app secret or private key is embedded in
the application; its GitHub App client ID is public.

### Images and videos

The agent can upload screenshots and recordings for issues, pull requests and
comments (`github_upload_attachments`), and look at images already attached
to them (`github_view_attachment`). A file comes from the workspace, here or on
the SSH machine, or is a fresh screenshot of a Browser Use tab. GitHub accepts
png, jpg, jpeg, gif, webp and svg images up to 10 MiB, and mp4, mov and webm
videos up to 100 MiB.

GitHub refuses uploads signed by a GitHub App such as the connector (only
GitHub's own apps may use its attachment endpoint), so uploads need one of:

- a personal access token saved under **Images and videos** in the connector's
  settings: a classic token with the `repo` scope (`public_repo` for public
  repositories only), or a fine-grained token with write access to the
  repositories. It is saved in `github-connector.json` with the connector's
  authorization and removed by **Disconnect**;
- otherwise GitHub CLI's sign-in on the execution target (`gh auth login`).
  1loop reads it with `gh auth token` at each upload and never saves it.

The uploading account must be able to write to the repository. Each upload
asks for approval and names the repository, the account and the files. GitHub
has no way to delete an attachment; it shows only where its address is
referenced, which the agent then does with the issue, pull request or comment
tools. Opening an attached image uses the same credential, else the
connector's, so images of private repositories open too.

Connecting or reconnecting GitHub during an active Claude Code turn takes
effect on the next turn in the same chat, even with the same account. Calls
belonging to the previous connection are rejected. Refreshing an access token
or re-enabling the existing connection keeps the same connection.

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

## Image generation with a ChatGPT account

With a connected **ChatGPT account** (Codex subscription authentication), ask
for an image in a project chat. The `imagegen` tool uses the subscription's
Codex image service with `gpt-image-2.5-sunburst`, shows an animated placeholder while it works, and displays
the resulting image in the conversation. The PNG is saved in the project's
`generated-images/` folder, with a unique filename; use Files to open or export
it. The model receives the image too and can inspect it before replying.

For edits, the tool accepts up to five workspace image paths, or the last
one to five conversation images. These options cannot be combined. A transparent
background can be requested. Existing files are never overwritten.
Like Codex image generation, this tool does not request a separate action
approval: referenced images are sent to the connected ChatGPT account’s
image service, and generation consumes its image quota.

The tool is absent for Claude Code, vLLM, compatible APIs, OpenRouter and
ChatGPT (Web). Access and image quotas depend on the connected subscription;
a refusal is reported without switching accounts or charging an API key.

## Claude Code sign-in

On desktop, open Settings → Connections, add or edit a Claude Code connection,
and choose **Sign in**. Complete Claude Code’s own browser authorization.
If it supplies a code, paste it into the dialog and submit it. Close the dialog
after success, then load the connection’s models. The phone keeps its existing
Claude Code sign-in action; both interfaces use the same CLI login flow.

For scripts or a desktop launched with a subscription token, `claude setup-token`
prints a long-lived token. Supply it through `CLAUDE_CODE_OAUTH_TOKEN` in the
environment that launches 1loop. It takes precedence over the stored login;
1loop preserves this variable and still removes API-key billing overrides.
Do not put the token in a prompt or commit it.

A model catalog is not a live token-validation request. 1loop reports an explicitly
signed-out CLI, but an expired or revoked credential may still be rejected when
a turn starts. Sign in again if necessary and keep Claude Code up to date.
Graceful shutdown reduces forced-interruption risks; it cannot repair an upstream
OAuth renewal failure. See [Claude Code authentication](https://code.claude.com/docs/en/authentication).
