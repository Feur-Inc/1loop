# Using 1loop

How to set up 1loop and what it can do. To install it first, see
[INSTALL.md](INSTALL.md).

- [Getting started](#getting-started)
- [Approval modes](#approval-modes)
- [Tools and the side panel](#tools-and-the-side-panel)
- [Prompts, models and context](#prompts-models-and-context)
- [Skills, plugins and MCP servers](#skills-plugins-and-mcp-servers)
- [SSH machines](#ssh-machines)
- [The phone app](#the-phone-app)
- [Shortcuts](#shortcuts)
- [Updates](#updates)
- [Security and privacy](#security-and-privacy)

## Getting started

1. **Open a project.** Start 1loop in a folder (`./1loop -workspace ~/my-project`),
   or add folders later from the sidebar.
2. **Connect a model.** Open **Settings → Connections**:
   - **ChatGPT**: *Continue with ChatGPT*, then sign in in your browser
     (1loop listens on `localhost:1455` during sign-in).
   - **Claude Code**: offered automatically when `claude` is installed; 1loop
     drives it with your Claude subscription. Without it, choosing Claude Code
     offers to install it with Anthropic's official installer. Add another
     Claude Code connection to use a second Claude account: it gets a login of
     its own ([several accounts](INSTALL.md#several-claude-code-accounts)).
   - **Antigravity CLI**: add it by hand once `agy` is installed and signed in
     (run `agy` once in a terminal). 1loop drives your own `agy` in its
     headless mode with your Google account; see
     [Antigravity CLI](INSTALL.md#antigravity-cli) for what that means.
   - **OpenRouter**: paste a key, or start 1loop with `OPENROUTER_API_KEY`.
   - **vLLM** or any **OpenAI-compatible API**: enter its `/v1` address.
   *Load / refresh models* fetches the catalog; tick the models to show.
3. **Ask.** Type what you want and press Enter. The agent works in the folder;
   its tool calls, diffs and task list appear as it goes.
4. **Pair your phone (optional).** Desktop: **Settings → Phone remote → Pair a
   phone** shows a QR code. Phone: install the app, choose **Remote desktop**,
   then **Scan QR code**.

## Approval modes

Choose in the composer:

- **Approval required**: every command and edit waits for you.
- **Auto mode** (ChatGPT; Claude Code uses its own auto mode): reads and
  workspace edits run directly; destructive or outgoing commands (`rm`,
  `git push`, `curl … | sh`, …) always go to a reviewer model; everything else
  runs in a sandbox that can only write in the project (Landlock on Linux).
- **YOLO mode**: everything runs without asking and without a sandbox.
  Claude Code and Antigravity CLI run with `--dangerously-skip-permissions`
  (Claude Code refuses it as root: 1loop then approves each of its requests).

Commands run with your user's permissions: approving one lets it act on your
machine.

## Tools and the side panel

The agent reads and edits files, runs shell commands (60 s by default, up to
10 minutes, or in the background with no limit), and keeps a **task list**.
The side panel holds its **Tasks**, **Terminal** tabs, a **Browser** it can use,
**Git changes** and **Files**: browse the project, preview code and images,
attach a file to your message, or add files to the project. On Linux the
agent can also use the whole desktop (**Computer Use**) after you grant screen
sharing.

## Prompts, models and context

- Every conversation keeps its own **draft**: what you type survives a crash,
  closing 1loop or switching chats, with Ctrl+Z / Ctrl+Y.
- Changing **provider, account or model** mid-conversation asks how the new
  model receives it: as it is, or summarized (by the previous model, which
  still has it cached, or by the new one), with 1loop's recommendation.
- Long conversations are **compacted** when they near the model's real context
  window; details stay reachable through the agent's `recall` tool.

## Skills, plugins and MCP servers

**Settings → Extensions** (also on the phone) has three tabs:

- **Store**: skills and plugins published on GitHub (topics `agent-skills`
  and `claude-code-plugin`), with their picture, stars and README, sorted by
  stars, last update or relevance. **Install** downloads the repository and
  first shows what it adds, the folders it writes, the commands it may run
  (MCP servers, hooks) and the risks; for a marketplace, you pick its plugins.
- **Installed**: turn a skill or plugin off, or remove it.
- **MCP servers**: add a server by its command (stdio) or its address
  (Streamable HTTP), see whether it connects and which tools it has.

The agent sees the skills' names and descriptions and loads one with its
`skill` tool when a task matches; each MCP server's tools load on demand
(`tool_search`), and a tool that does not declare itself read-only asks for
approval. Plugins bring skills, MCP servers and hooks. Claude Code receives
the same extensions (`--plugin-dir`, `--mcp-config`), without changing its
own settings. Plugins' commands and agents are kept for Claude Code: 1loop has
no `/` commands. The ChatGPT (Web) connection has a fixed tool list and does
not get them.

Without a `GITHUB_TOKEN` (or `GH_TOKEN`), GitHub allows about ten searches a
minute.

## SSH machines

**Settings → SSH machines** adds a computer the agent can work on over SSH:
its host, port and user, a password, a private key (with its passphrase) or
the SSH agent, and its **projects folder**. 1loop asks you to trust its host
key the first time and refuses the connection if that key ever changes.
Choose the machine in the target menu under the composer: its projects are
folders of that projects folder (**Projects, +** makes one there), its chats
stay apart from this computer's, and the agent's file edits, shell commands,
background tasks, **Terminal**, **Git changes** and **Files** happen on the
machine. The agent, its models, Browser Use and Computer Use stay on this
computer. The machine has no sandbox: in Auto mode, any command that is not
plainly safe waits for your approval. Claude Code, Antigravity CLI and
ChatGPT (Web) work on this computer's projects only.

## The phone app

- **Remote desktop / Remote server**: drive a desktop or a server from the
  phone, with approvals, screenshots and notifications.
- **Local phone**: the agent runs on the phone itself, in a Debian environment,
  and can use the phone (Phone Use, through Shizuku) when you allow it.
- **Share this phone**: let the desktop or another phone use the phone's agent.
- **Copy project**: copy a project with its conversations between devices;
  later copies only send what changed.
- **Share → 1loop** from any app adds a file to the project or the next message.

## Shortcuts

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
[`updates/latest.json`](https://feur-inc.github.io/1loop/updates/latest.json)
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

## Security and privacy

- Conversations, projects and settings stay on your machine, in
  `~/.config/1loop/` (the server: `~/.config/1loop-server/`). ChatGPT tokens
  are stored there with mode `0600`.
- The phone and the desktop talk over HTTPS with a certificate pinned by its
  fingerprint at pairing; pairing tokens are stored as hashes.
- Reads and edits stay inside the project, including symbolic links. The
  shell runs with your permissions; Auto mode's sandbox limits its writes.
- Skills, plugins and MCP servers are installed in
  `~/.config/1loop/extensions/`. 1loop does not review them: a skill steers
  the model, and a plugin's MCP servers and hooks run commands with your
  permissions.
- Project copies leave out `.env` files, private keys, credentials and
  generated caches, but they are not a secret scanner: review a copy before
  confirming it.

---

Integration references: [OpenCode's OAuth flow](https://github.com/anomalyco/opencode/blob/dev/packages/opencode/src/plugin/openai/codex.ts),
[vLLM](https://docs.vllm.ai/en/stable/serving/openai_compatible_server/),
[OpenRouter](https://openrouter.ai/docs/guides/features/tool-calling).
