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
- [Publishing an update](#publishing-an-update)
- [Building from source](#building-from-source)
- [Security and privacy](#security-and-privacy)

## Install

Download the latest version from the **[release page](https://tpm28.github.io/1loop/)**.
The [install guide](docs/INSTALL.md) has every step; in short:

| Platform | Download | Run |
|---|---|---|
| Linux (x86-64) | `1loop-VERSION-linux-amd64.tar.gz` | extract, then `./1loop` (`./install.sh` adds it to the menu) |
| Debian, Ubuntu (x86-64) | `1loop-VERSION-amd64.deb` | `sudo apt install ./1loop-VERSION-amd64.deb` |
| Linux (x86-64) | `1loop-VERSION-x86_64.AppImage` | `chmod +x`, then run it |
| Linux, other formats | Flatpak, Nix flake | built from this repository: see the [install guide](docs/INSTALL.md#linux-desktop-x86-64) |
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

1loop reads the signed manifest at
[`updates/latest.json`](https://tpm28.github.io/1loop/updates/latest.json)
on GitHub Pages. Desktop checks shortly after startup and every six hours
while running. Android checks when the app opens and schedules background
checks every six hours; Android may delay these to save battery.

- **Desktop**: a notification, an automatic proposal and *Update available* in the sidebar.
  **Settings → Updates → Update and restart** replaces 1loop's files (the
  previous ones stay beside them as `.old`) and restarts.
- **Android**: an automatic proposal in the app and a notification when
  notifications are allowed. **Settings → Updates → Check now** also checks
  manually. The update button downloads the APK and opens Android's installer;
  confirm the installation there. Your chats and settings stay. Android asks
  once to allow 1loop to install its updates.
- **Server**: a device connected to an outdated server shows *Update server*;
  the server downloads its new version, replaces itself and restarts.

Each release's manifest is signed with the maintainer's release key, and every
download is checked against it before anything is installed.

## Publishing an update

This is the maintainer's procedure. Publishing the signed manifest and files
on GitHub Pages makes a new version available to installed apps; it does not
silently install it on users' devices.

1. **Check Pages once.** In the repository's **Settings → Pages**, select
   **Deploy from a branch**, branch **gh-pages**, folder **/ (root)**.
   The site is <https://tpm28.github.io/1loop/>. This uses GitHub's Pages
   deployment. Stable releases still publish this site through `release.sh`;
   the GitHub Actions workflow below runs CI and publishes test alpha releases
   from `dev` without changing the stable Pages manifest.
2. **Prepare the release.** Increase `VERSION` (for example, `0.3.2` to
   `0.3.3`) and write `docs/releases/0.3.3.md` with the release notes.
   Commit, review and merge the changes, then publish from the intended clean
   source commit. Use the build requirements below and a GitHub account with
   repository, release and Pages publishing access.
3. **Keep the existing signing keys.** Use the same private manifest key that
   matches the public key embedded in installed apps, and the same Android
   APK signing keystore. Android rejects an update signed by another APK key.
   The ignored local `release-key.pem` is also stored in the repository's
   Actions secret `RELEASE_KEY` so CI can sign alpha build checksums; configure
   it with `gh secret set RELEASE_KEY --repo Feur-Inc/1loop < release-key.pem`.
   Never commit either private key or an access token.
4. **Run the publisher from the repository root:**

   ```bash
   gh auth login                 # once, if not already authenticated
   GH_TOKEN="$(gh auth token)" \
     RELEASE_KEY=/path/to/existing/release-key.pem \
     SKIP_CI=1 bash release.sh
   ```

   `release.sh` consumes the downloads already built and checked following
   [docs/BUILD.md](docs/BUILD.md), signs the manifest, pushes files to `gh-pages`, and
   creates the matching GitHub release. The results are also kept locally in
   `dist/VERSION/`.
   Do not reuse a version already published. `SKIP_CI=1` confirms the documented
   build checks passed for exactly the source commit and downloads being published.
5. **Wait for Pages to deploy, then verify.** Open the release page and
   `updates/latest.json` above. The manifest is a signed envelope, rather
   than plain release notes; let an installed app verify it with
   **Settings → Updates → Check now**. Confirm the offered version and try
   the update. On Android, complete the system install confirmation yourself.

The published files live at `updates/VERSION/`; `updates/latest.json` selects
the release offered to apps. Creating only a GitHub release, or uploading an
APK without updating that signed manifest, does not publish an in-app update.
Do not edit the manifest by hand: its signature must match its contents.

## Building from source

See [docs/BUILD.md](docs/BUILD.md) for prerequisites and the commands to build
and check Linux, Windows and Android manually with Go parallelism set to two.
The CI executes its build and packaging commands directly in its YAML files;
there is no local build wrapper.

### Self-hosted CI

`.github/workflows/ci.yml` runs Linux, Windows and Android as separate jobs
on the organization runner labeled `self-hosted`, `Linux`, `X64`,
`TPM28_RUNNER`. The jobs currently run sequentially on that runner. `mopi` is
temporarily excluded until its build dependencies are prepared; remove the
`TPM28_RUNNER` selector from each job to enable both runners again. The runner
group must share its runners with this repository. No Actions artifact or cache storage is used.

Builds use the runner's installed compiler and system libraries directly.
There is no Docker build, sysroot or older-distribution compatibility layer.
The `.deb` records the minimum glibc version required by its executables;
older distributions may need to build from source instead.
Preinstall these packages in the runner image (the workflow does not invoke sudo):

```sh
apt-get install build-essential cmake patch patchelf pkg-config libgl-dev \
  xorg-dev libwayland-dev libxkbcommon-dev binutils zip unzip dpkg-dev \
  python3 curl ca-certificates git gh jq openssl gcc-mingw-w64-x86-64 p7zip-full
```

Actions set up Go, Java 21 and the Android SDK/NDK. Go uses `-p=2` in CI
and in the manual commands documented in [docs/BUILD.md](docs/BUILD.md).
Windows is cross-compiled directly using MinGW; this does not run Windows tests.
Linux packages retain the launcher, icons and per-format settings from #57/#59;
the packaging commands are now inline in the CI. Flatpak and Nix definitions
remain available with manual commands in the build documentation.

Pull requests from this repository and builds of `main` run verification only.
Use a dedicated runner isolated from personal files and unrelated workloads;
contributors with branch push access can execute code on it. Fork PRs do not
automatically execute on persistent self-hosted runners:
review them before testing their code on an isolated runner. PR verification
receives no signing secrets: debug uses a local debug key and release is unsigned.

On a trusted push to `dev`, each platform builds and verifies once, then uploads
its outputs to this run's **draft** prerelease (`alpha-RUN_ID`). Only after
all three jobs succeed does the publisher download the complete set, sign
`SHA256SUMS`, and publish the alpha. Failed builds leave an unpublished draft
for inspection; rerunning failed jobs reuses that draft. A completed run is not
republished on rerun. Only the three most recently published `alpha-N`
prereleases are retained; stable releases and other tags are excluded. These
alpha builds do not change the stable Pages update manifest.

Verify the downloads with the trusted `.github/release-key.pub` from the source
commit linked in the release notes:

```sh
openssl dgst -sha256 -verify release-key.pub -signature SHA256SUMS.sig SHA256SUMS
sha256sum --check SHA256SUMS
```

### Android signing

Debug and release use the **same publication certificate**, loaded from the
ignored `android/signing/release.properties` (Java properties format):

```properties
storeFile=1loop-release.p12
storePassword=YOUR_STORE_PASSWORD
keyAlias=1loop-release
keyPassword=YOUR_KEY_PASSWORD
```

`storeFile` is relative to this properties file. For another worktree, set
`ONELOOP_ANDROID_SIGNING_PROPERTIES` to its absolute path. Without that file,
debug uses Android's local debug keystore so contributors can install it;
release remains unsigned. When publication credentials are present, both
variants use the publication certificate.
An existing app signed with the old debug key cannot be updated directly with
the publication key. Do not uninstall an existing app without preserving its
data and agreeing on migration first.

Keep encrypted backups of `android/signing/` and `release-key.pem`; the latter
signs update manifests/checksums and is a different key from the Android
certificate. Never commit private keys or passwords. `.pem`, `.key`, `.p12`,
`.jks`, `.keystore` and `android/signing/` are ignored by Git.

CI requires two repository Actions secrets:

- `RELEASE_KEY`: the existing PEM contents of `release-key.pem`, matching
  `.github/release-key.pub`.
- `ANDROID_SIGNING_BUNDLE`: base64 of a gzip tar archive containing only
  `1loop-release.p12` and `release.properties`, with those exact archive names.

Set them without placing credentials in command arguments:

```sh
gh secret set RELEASE_KEY --repo Feur-Inc/1loop < release-key.pem
tar -C android/signing -czf - 1loop-release.p12 release.properties | base64 -w0 | \
  gh secret set ANDROID_SIGNING_BUNDLE --repo Feur-Inc/1loop
```

The Android key is restored only for trusted `dev` publication and removed
from the runner after use. The build verifies both APK signatures and compares
their certificate SHA-256 digests before uploading the release APK.

For local development, use the commands in [docs/BUILD.md](docs/BUILD.md).
Start the `1loop` executable: it hands over to `1loop-wayland` when appropriate.
`ONELOOP_BACKEND=x11` or `wayland` forces a backend (see
[X11, Wayland and XWayland](docs/INSTALL.md#x11-wayland-and-xwayland)).

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
