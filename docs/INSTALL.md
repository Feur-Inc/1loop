# Installing 1loop

Every download is on the **[release page](https://tpm28.github.io/1loop/)**
(also attached to each [GitHub release](https://github.com/tpm28/1loop/releases)).
Once installed, 1loop updates itself from that page: see
[Updates](../README.md#updates).

## Linux desktop (x86-64)

1. Download `1loop-VERSION-linux-amd64.tar.gz`.
2. Extract it in a folder you own, for example `~/Applications/1loop`:

   ```bash
   mkdir -p ~/Applications/1loop
   tar -xzf 1loop-VERSION-linux-amd64.tar.gz -C ~/Applications/1loop
   ~/Applications/1loop/1loop
   ```

3. 1loop adds itself to your applications menu on first start
   (`~/.local/share/applications/1loop.desktop`).

Keep both files, `1loop` and `1loop-wayland`, together: in a Wayland session
`1loop` hands over to `1loop-wayland`. The folder must stay writable by you for
updates to install.

Runtime needs: Bash, Git (for the *Git changes* tab), and Chrome or Chromium
for the agent's browser. Optional: `latex` and `dvipng` to render formulas,
and the Computer Use packages below.

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

Uninstalling: delete the folder you extracted, and these folders if you want
to remove your data too.
