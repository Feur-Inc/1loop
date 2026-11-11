# Headless 1loop server

The server runs the same agent loop, provider integrations, persistent tasks,
context policy, file tools, Browser Use and Terminal Use as the desktop. It
has no Fyne/GLFW dependency and does not offer Computer Use. Conversations,
files, shells and Chromium run on the server machine. Disconnecting a client
does not stop its conversations; Stop does.

## Build and run on Debian

Build with Go 1.25 or newer:

```sh
make server
```

`bin/1loop-server` is a static Go binary built with `CGO_ENABLED=0`. The runtime
needs Bash and Git. Browser Use runs the system's Chromium or Chrome, or else
downloads its own Chromium into the data directory (`chromium/`); a minimal
server may lack the libraries it needs, and installing the `chromium` package
brings them. It needs no desktop session, X server, Wayland compositor or
virtual display:

```sh
sudo apt install bash git chromium chromium-sandbox ca-certificates
mkdir -p ~/projects/my-project
export MODEL_API_KEY='your-provider-key'
./bin/1loop-server \
  -workspace ~/projects/my-project \
  -listen :47101 \
  -provider compatible \
  -base-url https://api.example.com/v1 \
  -model your-model-id \
  -api-key-env MODEL_API_KEY
```

Run it under an ordinary dedicated Linux user, with the permissions needed
for its projects. Shell tools have that user's OS permissions; pairing grants
control of this server, rather than a restricted viewer account. Chromium's
normal sandbox remains enabled when running as a non-root user.

The default private data directory is `~/.config/1loop-server`. Use `-data-dir`
to choose another dedicated directory. State, configuration and generated
private keys are written with mode `0600`; new data and browser directories
use `0700`. Keep that directory outside Git and back it up privately. The
server's workspace is fixed by `-workspace`: clients cannot select another
server-side project directory through the remote API.

## Docker

The image `ghcr.io/feur-inc/1loop-server` holds the server with Bash, Git and
Chromium, and runs it as a non-root user (uid 1000). Its data directory is
`/data` (keys, certificate, pairings, configuration and chats) and its
workspace is `/projects`: keep both on volumes. Browser Use runs Chromium
without its own sandbox (`ONELOOP_CHROMIUM_NO_SANDBOX=1`): Docker's default
profile allows none of them, so the container is what isolates it. Pairing happens in the
server's console, so start it with a terminal (`-it`):

```sh
docker run -d -it --name 1loop-server -p 47101:47101 \
  -v 1loop-data:/data -v ~/projects:/projects \
  ghcr.io/feur-inc/1loop-server:latest
docker logs 1loop-server          # the certificate's fingerprint
docker attach 1loop-server        # type pair …, devices or revoke …
```

Detach from the console with **Ctrl+P Ctrl+Q**: Ctrl+C would stop the
server. Arguments after the image name are added to the server's own, for
example the first connection: `-provider compatible -base-url … -model …
-api-key-env MODEL_API_KEY` with `-e MODEL_API_KEY=…`.
[`packaging/docker/compose.yml`](../packaging/docker/compose.yml) does the same
with Docker Compose. A bind-mounted projects folder must be writable by uid
1000. To let the agent reach more of the host, see
[How much of the host the agent reaches](#how-much-of-the-host-the-agent-reaches).

Tags: `latest` and the version (`0.3.3`) for a stable release, `alpha` and
`alpha-N` for the newest builds of `dev`. The server does not update itself
in a container: pull the new image and recreate the container, and the
volumes keep everything. To build it yourself: `docker build -t 1loop-server .`
from the repository root.

## How much of the host the agent reaches

In the image, the agent's commands run in the container's own Debian as
`oneloop` (uid 1000), and its files are `/projects`. Choose one of three
levels; [`compose.yml`](../packaging/docker/compose.yml) has each one as
comments to uncomment.

1. **Isolated** (the default): only the `/data` and `/projects` volumes.
   Nothing else of the host is visible.
2. **Host folders**: bind-mount host folders under `/projects`, for example
   `- ~/dev:/projects/dev`. Each one is then a folder the phone can add as a
   project, and the agent changes the host's files directly, still with the
   container's tools.
3. **Host over SSH**: the server signs in to the host as an SSH machine. A
   project can be a folder there, and the agent's commands, files, terminals
   and Git changes run on the host itself, as that user, with the host's
   tools. The server, its models and Chromium stay in the container.

Not offered, at any level: `--privileged`, `--pid host` or mounting the Docker
socket (`/var/run/docker.sock`). Each one gives whoever drives the agent root on
the host, without anything in 1loop showing that a line was crossed. Use
level 3 with a user whose rights you chose instead.

### Host folders: users and permissions

The container runs as uid 1000 and gid 1000, whatever user owns a mounted
folder on the host:

- A folder owned by another uid is read-only to the agent (or unreadable,
  depending on its mode). Give uid 1000 access (`chown`, a group, or an ACL:
  `setfacl -R -m u:1000:rwX ~/dev`) rather than making it world-writable.
- Files the agent creates belong to uid 1000 on the host. If your host user is
  not uid 1000, they are not yours: `id -u` tells.
- Mount only what the agent may change, and add `:ro` to what it should only
  read. A mount outside `/projects` is not a project: the phone browses and
  adds projects inside `/projects` only.
- Rootless Docker and Podman map uids: uid 1000 in the container is a
  subordinate uid on the host, so the folder needs that one's access.
- With SELinux (Fedora, RHEL), add `:z` to the mount, or the container cannot
  read it.

### Host over SSH

1. Make a key for the server, without a passphrase, and allow it on the host
   (for the user the agent should act as; a dedicated user limits what it
   reaches):

   ```sh
   ssh-keygen -t ed25519 -N '' -f ~/.ssh/1loop_ed25519 -C 1loop-server
   cat ~/.ssh/1loop_ed25519.pub >> ~/.ssh/authorized_keys
   ```

   The host needs an SSH server with SFTP (OpenSSH has both) listening where
   the container reaches it. The key file must be readable by uid 1000: Compose
   bind-mounts a secret with the file's own owner and mode.

2. Give the server the machine, in `compose.yml` (uncomment `extra_hosts`,
   `secrets` and `ONELOOP_SSH_MACHINE`), or with `docker run`:

   ```sh
   docker run -d -it --name 1loop-server -p 47101:47101 \
     --add-host host.docker.internal:host-gateway \
     -v ~/.ssh/1loop_ed25519:/run/secrets/1loop_ssh_key:ro \
     -v 1loop-data:/data -v ~/projects:/projects \
     -e ONELOOP_SSH_MACHINE="you@host.docker.internal key=/run/secrets/1loop_ssh_key projects=~/dev" \
     ghcr.io/feur-inc/1loop-server:latest
   ```

   A machine is `user@host[:port]` then options: `key=` the private key file
   (required), `projects=` the folder projects go in (absolute or `~/…`,
   default `~`), `name=` how clients name it, and `host-key=SHA256:…` to pin
   its host key. Separate them with spaces, or with commas when a name has
   spaces. `-ssh-machine` takes the same text, once per machine;
   `ONELOOP_SSH_MACHINE` takes several separated by `;`. Outside Docker, the
   same flags work with any host the server reaches.

3. Confirm the host key. On its first connection the server prints the
   machine's host key in its console (`docker attach 1loop-server`):

   ```text
   SSH machine you@host.docker.internal (host.docker.internal:22) has never been connected to. Its host key is ssh-ed25519 SHA256:….
   Type trust 1 to accept it, refuse 1 otherwise.
   ```

   Compare it with `ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub` on the
   host, then type `trust 1`. The key is kept in `/data/known_hosts`, and a
   different key later is refused. To skip this step, pin the key with
   `host-key=` instead: the server then accepts that key only.

4. On the phone, **Add project** shows the machine beside the server: choose
   it, then a folder in its projects folder or a new one. The project shows
   *on* the machine in the sidebar and under the composer, and its chats run
   there. Claude Code, Antigravity and ChatGPT (Web) work on the server's own
   projects only; choose another model for a machine's projects. The file
   browser shows the server's own projects only for now: use a terminal on
   the machine, or ask the agent.

The console's `ssh` lists the machines and the host keys waiting for an
answer; `ssh add <machine>` adds one (the same text as `ONELOOP_SSH_MACHINE`,
kept in the server's configuration), and `ssh remove <number>` forgets a
machine and its projects (their chats stay, unable to run; the files stay on
the machine). A machine from the environment or a flag is added again at each
start, with its options then.

## HTTPS and pairing

On first start the server generates an ECDSA TLS certificate, valid for one
year, and prints its SHA-256 fingerprint. It reuses that certificate on later
starts. You can instead supply a certificate and private key with
`-tls-cert /path/server.crt -tls-key /path/server.key`.

1. On desktop, click **This computer** under the message box and choose
   **Connect a server…**, or open **Settings → General → Remote server →
   Connect a server…**, which lists these steps too. On Android, choose
   **Add by address**.
2. Enter `https://your-server:47101`. For a generated/private certificate,
   enter the fingerprint read directly from the server console. Leave it
   empty only when using a certificate trusted by the client's normal CA
   store and matching its hostname. A fingerprint mismatch fails closed;
   clients never silently accept a replacement certificate or downgrade to
   HTTP. Existing Android-to-desktop LAN connections still support their
   explicit `http://` address.
3. The client displays `pair <pairing-ID> <six-digit-code>`. Type that command
   in the server process's console, then click **Connect after approval** on
   the client. Pairings expire after two minutes and allow five failed code
   attempts. At most eight may be pending.
4. The paired client retains a random bearer token. The server persists only
   its hash. Tokens travel in the Authorization header, including streams;
   redirects are not followed with credentials.

On desktop, the server then becomes the window's target, as on the phone: the
sidebar lists its projects and conversations, and they open, run and show in
the same conversation, message box and side panel (its browser, terminals,
tasks, changes and files). The same menu lists the server for the next time,
its hooks, copying a project to it and its update; **This computer** goes back
to the desktop's own projects.

The console also accepts `devices` and `revoke <number>`. Revocation rejects
new requests and disconnects active event/browser streams. It does not undo
an operation already accepted by the server. Keep access to this console;
for a detached deployment, use a terminal multiplexer such as tmux.

Certificate replacement requires verifying and entering the new fingerprint
on each client. A private certificate's fingerprint replaces normal hostname
verification only for that explicitly pinned connection. The clients still
check certificate validity dates and the TLS proof of the private key.

## Models and configuration

The initial CLI flags create a connection in the server's private
`config.json`. You can supply a configuration file with `-config`. Its shape
uses the same connection/model preferences as desktop; secrets stay on the
server and are never included in state responses:

```json
{
  "config": {
    "connectionId": "primary",
    "provider": "compatible",
    "baseURL": "https://api.example.com/v1",
    "model": "your-model-id"
  },
  "connections": [
    {
      "id": "primary",
      "name": "Primary API",
      "provider": "compatible",
      "baseURL": "https://api.example.com/v1",
      "enabled": true,
      "allModels": true
    }
  ],
  "keys": { "primary": "YOUR_PRIVATE_API_KEY" },
  "approvalMode": "approval",
  "userInstructions": "",
  "classicCompaction": false
}
```

Configure additional accounts in this array, including their existing
`authProfile`, model visibility and model options. Credentials for ChatGPT
accounts must exist on the server in the existing 1loop account credential
store; the desktop does not copy local OAuth credentials automatically.
Claude Code and Antigravity CLI use the CLI and sign-in installed on the
server (`claude`, `agy`); a Claude Code connection with `"claudeLogin": "own"`
uses its own Claude Code folder on the server instead
(`<config>/1loop/claude-code/<id>`: sign it in there with
`CLAUDE_CONFIG_DIR=<that folder> claude auth login`).
Restart the server after editing connection configuration. Clients can
select its available models and reasoning levels per conversation.

## Remote tools

The desktop opens a separate remote window, keeping local work independent.
Its **+** menu opens browser and terminal tabs on the server. Browser tabs
show the model cursor and accept clicks, text, keyboard shortcuts and scroll.
The console sends JavaScript and displays browser logs. Adaptive viewports
follow the panel; Apply sets fixed dimensions for responsive checks. Desktop
receives the original PNG frames; Android uses the existing lighter stream.
Only the active desktop browser is streamed. Hidden terminals remain alive
on the server and resume output when selected again.

Terminal tabs use real persistent PTYs, including keyboard input, live
output, resizing and screenshots for model vision. They belong to their
conversation. Browser tabs and task lists survive server restarts; running
shell processes do not. Git changes reuse the desktop diff parser and show
unstaged/untracked or staged changes. Task progress, elapsed time and context
policy are read from the server.

Android has been adapted for the same HTTPS endpoints and console pairing.
Compilation is separate from device verification: phone installation and
interactive tests must wait for the user's explicit signal.

## Updates

The server checks the [release page](https://feur-inc.github.io/1loop/) when a
device asks (`GET /api/update`). A device connected to an outdated server
shows *Update server*; on confirmation the server downloads its new version,
checks its signature and SHA-256, replaces its own binary (the previous one
stays beside it as `.old`) and restarts in place with the same arguments. Under
systemd the process keeps its PID. The binary's folder must be writable by the
server's user; otherwise update it by hand from the release page.
