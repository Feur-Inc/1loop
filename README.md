<p align="center">
  <img src="docs/images/banner.png" alt="1loop" width="100%">
</p>

<p align="center">
  <a href="https://feur-inc.github.io/1loop/"><img src="https://img.shields.io/badge/download-latest-6e56cf?style=flat-square" alt="Download"></a>
  <a href="https://github.com/Feur-Inc/1loop/actions/workflows/ci.yml"><img src="https://img.shields.io/github/actions/workflow/status/Feur-Inc/1loop/ci.yml?branch=dev&style=flat-square&label=CI" alt="CI"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MPL--2.0-blue?style=flat-square" alt="License: MPL-2.0"></a>
  <a href="https://ko-fi.com/tpm28"><img src="https://img.shields.io/badge/Ko--fi-support-ff5e5b?style=flat-square&logo=ko-fi&logoColor=white" alt="Ko-fi"></a>
</p>

<p align="center">
  <a href="docs/INSTALL.md">Install</a> &nbsp;·&nbsp;
  <a href="docs/USAGE.md">Guide</a> &nbsp;·&nbsp;
  <a href="docs/SERVER.md">Server</a> &nbsp;·&nbsp;
  <a href="docs/BUILD.md">Build</a>
</p>

1loop is a coding agent that runs on your own machine, with the models you
pick. Point it at a project folder and it reads and edits files, runs
commands, browses the web and keeps a task list, asking for your approval as
it goes.

It runs on the desktop, as a headless server and on Android, and the phone can
drive the other two over your network.

## Features

- **Any model.** ChatGPT (with your own account), Claude Code, Antigravity CLI,
  OpenRouter, vLLM, or any OpenAI-compatible API. Use several accounts and
  switch mid-conversation.
- **A real workspace.** Files, shell, terminals, a browser the agent can drive,
  Git changes and background tasks, in a side panel next to the chat. Supports
  skills, plugins and MCP servers.
- **Approval modes.** Approve every action, let Auto mode check risky ones in a
  sandbox, or turn approvals off. Drafts survive crashes and nothing leaves
  your machine.
- **Phone and remote hosts.** Pair a phone with a QR code, run the agent on the
  phone itself, work on machines over SSH, and move projects and their chats
  between devices.

## Install

Download the latest build from the [release page](https://feur-inc.github.io/1loop/).
Full instructions are in the [install guide](docs/INSTALL.md).

| Platform                 | Packages                                                                              |
| ------------------------ | ------------------------------------------------------------------------------------- |
| Linux x86-64             | `.tar.gz`, `.deb`, AppImage, [Flatpak, Nix](docs/INSTALL.md#linux-desktop-x86-64)     |
| Windows 10 1809+ x64     | `.zip`                                                                                |
| Android 8+               | `.apk`                                                                                |
| Server (Linux, Windows)  | [Headless server](docs/SERVER.md)                                                     |

> [!NOTE]
> Play Protect may flag the Android app, since it's installed outside the Play
> Store and runs shell commands. Tap **Install anyway**, and only use APKs from
> the release page: the app verifies the signature of every update.

## Quick start

```sh
./1loop -workspace ~/my-project
```

1. Open a project, either with the command above or from the sidebar.
2. Connect a model in **Settings → Connections** ([details](docs/USAGE.md#getting-started)).
3. Ask for something. Tool calls, diffs and tasks show up as the agent works.
4. Optionally, pair your phone from **Settings → Phone remote → Pair a phone**
   and scan the QR code in the app.

The [guide](docs/USAGE.md) covers approval modes, the side panel, extensions,
SSH machines, the phone app and keyboard shortcuts.

## Documentation

| Page                       | Contents                                                     |
| -------------------------- | ------------------------------------------------------------ |
| [Install](docs/INSTALL.md) | Platforms, packages, sign-ins, where 1loop stores its data   |
| [Guide](docs/USAGE.md)     | Day-to-day use, updates, security and privacy                |
| [Server](docs/SERVER.md)   | Running headless and pairing devices                         |
| [Build](docs/BUILD.md)     | Building from source, CI, publishing a release               |

## Contributing

Open pull requests against `dev`, link the issue they fix, and include
before/after screenshots for UI changes. See the
[pull request template](.github/pull_request_template.md).

## Support

1loop is free. If it saves you time, you can support development on
[Ko-fi](https://ko-fi.com/tpm28).

## License

Copyright © 2026 the 1loop contributors. Licensed under the
[Mozilla Public License 2.0](LICENSE).

Bundled third-party code keeps its own license, for example `third_party/fyne`
(BSD-3-Clause) and the tools shipped with the Android app, listed in
`android/app/src/main/assets/local/licenses/`.
