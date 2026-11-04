<p align="center">
  <img src="docs/images/banner.png" alt="1loop — Your project. Your model. One loop." width="100%">
</p>

<p align="center">
  <a href="https://feur-inc.github.io/1loop/"><img src="https://img.shields.io/badge/download-latest-6e56cf?style=for-the-badge" alt="Download"></a>
  <a href="https://github.com/Feur-Inc/1loop/actions/workflows/ci.yml"><img src="https://img.shields.io/github/actions/workflow/status/Feur-Inc/1loop/ci.yml?branch=dev&style=for-the-badge&label=CI" alt="CI"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MPL--2.0-blue?style=for-the-badge" alt="License: MPL-2.0"></a>
  <a href="https://ko-fi.com/tpm28"><img src="https://img.shields.io/badge/Ko--fi-support-ff5e5b?style=for-the-badge&logo=ko-fi&logoColor=white" alt="Support on Ko-fi"></a>
</p>

<p align="center">
  <b>A coding agent you run on your own machine, with the models you choose.</b><br>
  Desktop · Headless server · Android
</p>

<p align="center">
  <a href="docs/INSTALL.md">Install</a> ·
  <a href="docs/USAGE.md">Guide</a> ·
  <a href="docs/SERVER.md">Server</a> ·
  <a href="docs/BUILD.md">Build</a>
</p>

---

1loop works in a project folder: it reads and edits files, runs commands,
browses the web and keeps a task list, while you watch and approve. The same
agent runs on the desktop, on a server and on your phone, and the phone can
drive the others from anywhere on your network.

<table>
  <tr>
    <td width="50%" valign="top">
      <h3>🧠 Your models</h3>
      ChatGPT (with your account), Claude Code, Antigravity CLI, OpenRouter,
      vLLM and any OpenAI-compatible API. Several accounts at once, and switch
      mid-conversation.
    </td>
    <td width="50%" valign="top">
      <h3>🛠️ A real workspace</h3>
      Files, shell, terminals, a browser it can use, Git changes and
      background tasks, in a side panel next to the chat. Skills, plugins and
      MCP servers too.
    </td>
  </tr>
  <tr>
    <td width="50%" valign="top">
      <h3>🛡️ You stay in control</h3>
      Approve each action, let Auto mode review the risky ones in a sandbox,
      or go YOLO. Your drafts survive crashes, and your data stays on your
      machine.
    </td>
    <td width="50%" valign="top">
      <h3>📱 Everywhere</h3>
      Pair your phone with a QR code, run the agent on the phone itself, work
      on SSH machines, and copy projects and their chats between devices.
    </td>
  </tr>
</table>

## 📦 Install

Grab the latest version from the **[release page](https://feur-inc.github.io/1loop/)**;
the [install guide](docs/INSTALL.md) has every step.

| Platform | Download |
|---|---|
| 🐧 Linux (x86-64) | `.tar.gz`, `.deb`, AppImage, [Flatpak, Nix](docs/INSTALL.md#linux-desktop-x86-64) |
| 🪟 Windows 10 1809+ (x64) | `.zip` |
| 🤖 Android 8+ | `.apk` |
| 🖥️ Server (Linux, Windows) | [headless server](docs/SERVER.md) |

> [!WARNING]
> **Android may call 1loop harmful.** It's installed from outside the Play
> Store and runs shell commands, so Play Protect may warn you. Choose
> **Install anyway**, and only install APKs from the release page: their
> updates are signed and checked by the app.

## 🚀 Getting started

1. **Open a project:** `./1loop -workspace ~/my-project`, or add folders from the sidebar.
2. **Connect a model** in **Settings → Connections** ([details](docs/USAGE.md#getting-started)).
3. **Ask.** The agent works in the folder; its tool calls, diffs and tasks appear as it goes.
4. **Pair your phone** (optional): **Settings → Phone remote → Pair a phone**, then scan the QR code in the app.

Then read the **[guide](docs/USAGE.md)** for approval modes, the side panel,
extensions, SSH machines, the phone app and shortcuts.

## 📚 Documentation

| Page | What's in it |
|---|---|
| [Install](docs/INSTALL.md) | every platform and package, sign-ins, where 1loop keeps its data |
| [Guide](docs/USAGE.md) | using 1loop, updates, security and privacy |
| [Server](docs/SERVER.md) | running 1loop headless and pairing devices to it |
| [Build](docs/BUILD.md) | building from source, the CI, publishing a release |

## 🤝 Contributing

Pull requests target `dev`, cite the issue they fix, and show before/after
screenshots for visible changes; see the
[pull request template](.github/pull_request_template.md).

## 💜 Support

1loop is free, and made for free. If it's useful to you, a donation on
**[Ko-fi](https://ko-fi.com/tpm28)** is really appreciated, even $1. Thank you!

## 📄 License

Copyright (c) 2026 The 1loop contributors. 1loop is licensed under the
[Mozilla Public License 2.0](LICENSE). Bundled third-party code keeps its own
license, for example `third_party/fyne` (BSD-3-Clause) and the tools the
Android app ships, listed in `android/app/src/main/assets/local/licenses/`.
