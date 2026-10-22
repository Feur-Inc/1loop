<p align="center">
  <img src="docs/images/banner.png" alt="1loop — Your project. Your model. One loop." width="100%">
</p>

<p align="center">
  <a href="https://tpm28.github.io/1loop/"><b>Download</b></a> ·
  <a href="docs/INSTALL.md">Install guide</a> ·
  <a href="docs/SERVER.md">Headless server</a> ·
  <a href="https://ko-fi.com/tpm28">Donate</a>
</p>

<p align="center">
  <a href="https://ko-fi.com/tpm28"><img src="https://ko-fi.com/img/githubbutton_sm.svg" alt="Support 1loop on Ko-fi"></a>
</p>

> [!IMPORTANT]
> **1loop is free, and I make it for free.** If it is useful to you, I would
> really appreciate a donation on **[Ko-fi](https://ko-fi.com/tpm28)**, even
> $1. Thank you!

1loop is a coding agent you run on your own machine, with the models you
choose. It works in a project folder: it reads and edits files, runs commands,
browses the web and keeps a task list, while you watch and approve. The same
agent runs on the **desktop** (Linux and Windows), on a **headless server**,
and on an **Android phone**, and the phone can also drive the desktop or the
server from anywhere on your network.

- **Your models.** ChatGPT (sign in with your account), Claude Code, OpenRouter,
  vLLM and any OpenAI-compatible API, several accounts at once.
- **A real workspace.** Files, shell, terminals, a browser it can use, Git
  changes, a task list and background tasks, in a side panel next to the chat.
- **You stay in control.** Approve each action, let an Auto mode review risky
  ones, or go YOLO. Your prompts are never lost, and switching models asks how
  the new one should receive the conversation.
- **Everywhere.** Pair your phone with a QR code, copy a project and its chats
  between devices, and keep every install up to date from the release page.

## Contents

- [Install](#install)
- [Getting started](#getting-started)
- [Using 1loop](#using-1loop)
- [Updates](#updates)
- [Building from source](#building-from-source)
- [Security and privacy](#security-and-privacy)

## Install

Download the latest version from the **[release page](https://tpm28.github.io/1loop/)**.
The [install guide](docs/INSTALL.md) has every step; in short:

| Platform | Download | Run |
|---|---|---|
| Linux (x86-64) | `1loop-VERSION-linux-amd64.tar.gz` | extract, then `./1loop` |
| Windows 10 1809+ (x64) | `1loop-VERSION-windows-amd64.zip` | extract, then `1loop.exe` |
| Android 8+ | `1loop-VERSION.apk` | open the APK on the phone |
| Server (Linux or Windows) | `1loop-server-VERSION-…` | see [docs/SERVER.md](docs/SERVER.md) |

> [!WARNING]
> **Android may call 1loop harmful.** 1loop is installed from outside the Play
> Store, and it does things Google Play Protect associates with risky apps: it
> downloads and runs a Debian environment, runs shell commands, and can control
> the phone (Phone Use) when you allow it. Play Protect may warn you or block
> the install. 1loop is open source: you can read every line of what it does
> in this repository. To install it, choose **Install anyway** (or
> *More details → Install anyway*). Only install an APK downloaded from the
> release page above; its updates are signed and checked by the app.

## Getting started

1. **Open a project.** Start 1loop in a folder (`./1loop -workspace ~/my-project`),
   or add folders later from the sidebar.
2. **Connect a model.** Open **Settings → Connections**:
   - **ChatGPT**: *Continue with ChatGPT*, then sign in in your browser
     (1loop listens on `localhost:1455` during sign-in).
   - **Claude Code**: offered automatically when `claude` is installed; 1loop
     drives it with your Claude subscription.
   - **OpenRouter**: paste a key, or start 1loop with `OPENROUTER_API_KEY`.
   - **vLLM** or any **OpenAI-compatible API**: enter its `/v1` address.
   *Load / refresh models* fetches the catalog; tick the models to show.
3. **Ask.** Type what you want and press Enter. The agent works in the folder;
   its tool calls, diffs and task list appear as it goes.
4. **Pair your phone (optional).** Desktop: **Settings → Phone remote → Pair a
   phone** shows a QR code. Phone: install the app, choose **Remote desktop**,
   then **Scan QR code**.

## Using 1loop

### Approval modes

Choose in the composer:

- **Approval required**: every command and edit waits for you.
- **Auto mode** (ChatGPT; Claude Code uses its own auto mode): reads and
  workspace edits run directly; destructive or outgoing commands (`rm`,
  `git push`, `curl … | sh`, …) always go to a reviewer model; everything else
  runs in a sandbox that can only write in the project (Landlock on Linux).
- **YOLO mode**: everything runs without asking and without a sandbox.

Commands run with your user's permissions: approving one lets it act on your
machine.

### Tools and the side panel

The agent reads and edits files, runs shell commands (60 s by default, up to
10 minutes, or in the background with no limit), and keeps a **task list**.
The side panel holds its **Tasks**, **Terminal** tabs, a **Browser** it can use,
**Git changes** and **Files**: browse the project, preview code and images,
attach a file to your message, or add files to the project. On Linux the
agent can also use the whole desktop (**Computer Use**) after you grant screen
sharing.

### Prompts, models and context

- Every conversation keeps its own **draft**: what you type survives a crash,
  closing 1loop or switching chats, with Ctrl+Z / Ctrl+Y.
- Changing **provider, account or model** mid-conversation asks how the new
  model receives it: as it is, or summarized (by the previous model, which
  still has it cached, or by the new one), with 1loop's recommendation.
- Long conversations are **compacted** when they near the model's real context
  window; details stay reachable through the agent's `recall` tool.

### The phone app

- **Remote desktop / Remote server**: drive a desktop or a server from the
  phone, with approvals, screenshots and notifications.
- **Local phone**: the agent runs on the phone itself, in a Debian environment,
  and can use the phone (Phone Use, through Shizuku) when you allow it.
- **Share this phone**: let the desktop or another phone use the phone's agent.
- **Copy project**: copy a project with its conversations between devices;
  later copies only send what changed.
- **Share → 1loop** from any app adds a file to the project or the next message.

### Shortcuts

| Keys | Action |
|---|---|
| Enter | send (queued while the agent works) |
| Ctrl+Enter | steer the running task with this message |
| Shift+Enter | new line |
| Ctrl+Z / Ctrl+Y | undo / redo in the prompt |
| Ctrl+K | new chat |
| Ctrl+B | show or hide the sidebar |
| Ctrl+Shift+R | restart 1loop |

## Updates

1loop checks its [release page](https://tpm28.github.io/1loop/) when it starts
and every six hours, and tells you when a new version is out:

- **Desktop**: a notification and *Update available* in the sidebar.
  **Settings → Updates → Update and restart** replaces 1loop's files (the
  previous ones stay beside them as `.old`) and restarts.
- **Android**: a notification; **Settings → Updates** downloads the new APK
  and opens Android's update screen. Your chats and settings stay. Android asks
  once to allow 1loop to install its updates.
- **Server**: a device connected to an outdated server shows *Update server*;
  the server downloads its new version, replaces itself and restarts.

Each release's manifest is signed with the maintainer's release key, and every
download is checked against it before anything is installed.

## Building from source

Requirements: Go 1.23+, a C compiler, and the OpenGL, X11 and Wayland
development libraries (`libgl-dev`, `xorg-dev`, `libwayland-dev`,
`libxkbcommon-dev`) for the desktop.

```bash
make            # bin/1loop (X11) and bin/1loop-wayland (native Wayland)
make server     # bin/1loop-server, static, no CGO
make run        # build, then open 1loop on the current folder
make dev        # same, with the developer log (~/.cache/1loop/dev.log)
bash ci.sh      # all checks and builds: Go, Linux, Android, Windows (Docker)
```

Always start `bin/1loop`: in a Wayland session it hands over to
`bin/1loop-wayland`. `ONELOOP_BACKEND=x11` forces X11.

- **Android**: JDK 21 and the Android SDK (`platforms;android-37.0`,
  `build-tools;37.0.0`, NDK `30.0.16248370` for the native helpers), then
  `cd android && ./gradlew assembleRelease`.
- **Windows**: `ci.sh` cross-builds `bin/windows/` from Linux with MinGW in
  Docker. Natively: MinGW-w64 GCC on `PATH`, then
  `go build -trimpath -ldflags="-s -w -H=windowsgui -extldflags=-static" -o bin/1loop.exe ./cmd/1loop`.
- **Releases**: `bash release.sh` builds everything, signs the update manifest
  with `release-key.pem` (never committed), publishes the files on the release
  page (the `gh-pages` branch) and creates the GitHub release. The version
  comes from the `VERSION` file.

## Security and privacy

- Conversations, projects and settings stay on your machine, in
  `~/.config/1loop/` (the server: `~/.config/1loop-server/`). ChatGPT tokens
  are stored there with mode `0600`.
- The phone and the desktop talk over HTTPS with a certificate pinned by its
  fingerprint at pairing; pairing tokens are stored as hashes.
- Reads and edits stay inside the project, including symbolic links. The
  shell runs with your permissions; Auto mode's sandbox limits its writes.
- Project copies leave out `.env` files, private keys, credentials and
  generated caches, but they are not a secret scanner: review a copy before
  confirming it.

---

Integration references: [OpenCode's OAuth flow](https://github.com/anomalyco/opencode/blob/dev/packages/opencode/src/plugin/openai/codex.ts),
[vLLM](https://docs.vllm.ai/en/stable/serving/openai_compatible_server/),
[OpenRouter](https://openrouter.ai/docs/guides/features/tool-calling).
