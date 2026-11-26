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

### Run it as a systemd service

[`packaging/server/1loop-server.service`](../packaging/server/1loop-server.service)
runs the server at boot and restarts it when it fails. Copy it to
`/etc/systemd/system/`, set its `User`, paths and arguments, then
`sudo systemctl daemon-reload && sudo systemctl enable --now 1loop-server`.

On a small machine, builds and tests the agents run can fill the memory. The
unit's `OOMScoreAdjust=-500` and the `oom_score_adj` of 500 that 1loop gives
every process it starts for its agents (Claude Code, agy, shell commands,
background tasks, terminals, and what they start in turn) make the kernel's
out-of-memory killer pick that build, not the server and every chat with it.
To add it to a unit you already have: `sudo systemctl edit 1loop-server`, then

```ini
[Service]
OOMScoreAdjust=-500
```

When the server restarts all the same (a crash, a reboot, an update), a turn
it cut off says *Interrupted: the server restarted*. A subchat working in
Claude Code or Antigravity resumes in its session, once; the chats it was to
report to, its parent always, are told, with its last answer. Other chats
wait for a message to continue.

## Docker

The image `ghcr.io/feur-inc/1loop-server` holds the server with Bash, Git,
Chromium and [Claude Code](#claude-code-in-the-image), and runs it as a
non-root user (uid 1000). Its data directory is `/data` (keys, certificate,
pairings, configuration, chats and Claude Code's sign-in) and its
workspace is `/projects`: keep both on volumes. Browser Use runs Chromium
without its own sandbox (`ONELOOP_CHROMIUM_NO_SANDBOX=1`): Docker's default
profile allows none of them, so the container is what isolates it. The image
also serves the browser client: open `https://your-server:47101/` and pair it
like any other client.

```sh
docker run -d -it --name 1loop-server -p 47101:47101 \
  -v 1loop-data:/data -v ~/projects:/projects \
  ghcr.io/feur-inc/1loop-server:latest
docker logs 1loop-server          # the certificate's fingerprint and the setup link
docker exec 1loop-server 1loop-server pair <pairing-ID> <code>   # approve a client
docker exec 1loop-server 1loop-server devices                    # or revoke <number>, ssh, trust <number>
```

The client's pairing dialog shows the `docker exec …` command to run, with
the container's ID in place of its name (the container's hostname, unless you
set another). The console's commands work as well with `docker attach
1loop-server`: detach with **Ctrl+P Ctrl+Q**, as Ctrl+C would stop the
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

The slim image, without Chromium or Claude Code, is a much smaller download
for a server that needs neither Browser Use nor Claude Code connections: tags
`slim` and `0.3.3-slim`, `alpha-slim` and `alpha-slim-N`, or
`docker build --target slim -t 1loop-server:slim .`. `/api/state` says what
the server has: `"browser"` and `"claudeCode"` are `false` there. It sets `ONELOOP_BROWSER=none`: browser tabs do not open, and
the agent's browser tools answer that this server has no browser instead of
downloading one the image could not run. The same variable turns Browser Use
off on any server.

### Install Chromium and Claude Code later

A server without them (the slim image, or any server without Chromium or
`claude`) can download them when you need them: in a paired client, Settings →
General shows **Install Chromium** and **Install Claude Code** for what the
server lacks (desktop and browser client: under "On the server"; Android:
under Linked targets). The server downloads them into its data folder, as its
own user, without administrator rights, so they survive restarts and new
images (`/data` in Docker):

- **Chromium** goes to `<data>/chromium` (about 200 MB, plus the libraries
  below): Google's Chrome for Testing, as
  Browser Use downloads it on a desktop, checked against the MD5 checksum
  Google's storage gives with the file (Google publishes no signature for
  it), over HTTPS. It then has to start. The slim image lacks the system
  libraries it needs (about 25, from GLib to NSS): the server then fetches
  them with `apt-get` from the system's own Debian repositories, keeping
  apt's lists and downloads in its data folder (no root, no lock); apt
  checks the repositories' signatures and each package's checksum as for an
  install. The packages are unpacked, not installed, into
  `<data>/chromium/libraries` (about 250 MB, with the Liberation fonts), and
  Chromium starts with them. A Chromium that still does not start is removed,
  and the client shows why. Once installed, it overrides
  `ONELOOP_BROWSER=none`, and it updates itself like the desktop's (once a
  day, at most). Delete `<data>/chromium` to remove it.
- **Claude Code** goes to `<data>/claude-code/claude` (about 350 MB): the latest release
  from `downloads.claude.ai`, accepted only if its manifest carries
  Anthropic's release signature (key `31DDDE24DDFAB679F42D7BD2BAA929FF1A7ECACE`,
  built into 1loop) and the binary its SHA-256, and if it starts. It is used
  when there is no `claude` on `PATH` or in `~/.local/bin`. The slim image sets
  `DISABLE_AUTOUPDATER=1`, so it keeps its version: delete the file and
  install again to update it. Then sign in as above.

The API: `POST /api/install/chromium` or `POST /api/install/claude-code`
starts the download in the background (`202`, or `200` with
`{"installed": true}` when it is there already). `/api/state` follows it under
`"installs"`: `{"chromium": {"running": true, "text": "Downloading… 42% of
193 MB"}}`, then `"error"` if it failed; `"browser"` and `"claudeCode"` turn
`true` once installed. An `install` event says the same to the open clients.

### Behind a reverse proxy

The generated certificate is self-signed. The desktop and Android clients
pin its fingerprint, but a browser cannot: it shows a certificate warning on
every visit, and each refused handshake used to print
`http: TLS handshake error … unknown certificate` in the console. The server
now says so once and then sums those failures up at most once an hour, so
the pairing messages stay readable. To get rid of the warning, give the
server a certificate browsers trust, in one of two ways.

**A trusted certificate on the server.** Pass one, such as Let's Encrypt's
for the server's name, with `-tls-cert` and `-tls-key` (mount the files
read-only, for example `-v /etc/1loop-tls:/tls:ro` and
`-tls-cert /tls/fullchain.pem -tls-key /tls/privkey.pem`). The server reads
them when it starts, so restart it after each renewal. Clients then connect
to `https://your-server-name:47101` with the fingerprint left empty.

**A reverse proxy with a real certificate**, such as Caddy, or Traefik as
Dokploy sets it up. The proxy holds the certificate and renews it, and
forwards to the server's plain-HTTP listener, `-http-listen`. The server
still serves HTTPS on `-listen` too. Without TLS, tokens and conversations
cross the network in the clear between the proxy and the server, so
`-http-listen` only accepts addresses that the rest of the internet cannot
reach:

- loopback (`127.0.0.1:47100`) or a private address (`10.…`, `172.16–31.…`,
  `192.168.…`, `fd…`), or
- every interface (`:47100`) when all of them are private, as inside a
  container on a Docker network.

On a public address it refuses to start. Add `-http-allow-public` only if
a firewall lets the proxy alone in. The server then prints a warning every
time it starts. Never publish the plain port on the host's public interface
(`-p 47100:47100`): bind it to loopback (`-p 127.0.0.1:47100:47100`) or
leave it on the Docker network.

The proxy must pass the `Host` header and must not buffer
`text/event-stream` responses. Caddy and Traefik do both by default. Open the
proxy's `https://` address in the browser. On desktop and Android, add the
same address with the fingerprint left empty, since the certificate is now
one the system trusts. While no device is paired, the setup link the server
prints names the proxy, `https://<proxy>/app/#setup=…`. Once everything goes
through the proxy, you can stop publishing port 47101.

Caddy on the host, with the server's container publishing the plain port on
loopback only:

```sh
docker run -d -it --name 1loop-server -p 127.0.0.1:47100:47100 \
  -v 1loop-data:/data -v ~/projects:/projects \
  ghcr.io/feur-inc/1loop-server:latest -http-listen :47100
```

```
# Caddyfile: Caddy gets and renews the certificate itself.
1loop.example.com {
	reverse_proxy 127.0.0.1:47100
}
```

Dokploy (Traefik): deploy the image, or `packaging/docker/compose.yml`,
with `-http-listen :47100` added to the server's arguments (Compose:
`command: ["-http-listen", ":47100"]`) and no published ports. Then, under
the service's **Domains**, add your domain with container port `47100`,
HTTPS on and the Let's Encrypt certificate. Dokploy adds the Traefik labels
for you. On another Traefik, these are the labels to add, with the container
on Traefik's network:

```yaml
labels:
  - traefik.enable=true
  - traefik.http.routers.oneloop.rule=Host(`1loop.example.com`)
  - traefik.http.routers.oneloop.entrypoints=websecure
  - traefik.http.routers.oneloop.tls.certresolver=letsencrypt
  - traefik.http.services.oneloop.loadbalancer.server.port=47100
```

Pairing is unchanged: approve a new device from one already paired, or type
`pair …` in the console (`docker attach 1loop-server` on the host, since a
shell opened in the container is not the console). The Docker image's console still shows the certificate
fingerprint, which only matters to clients that connect to port 47101
directly.

### Claude Code in the image

The image (not the slim one) includes the Claude Code CLI (`claude`,
Anthropic's native build), so a Claude Code connection works in a new
container without installing anything. Its sign-in and settings are kept in the data volume, in
`/data/claude` (`CLAUDE_CONFIG_DIR`, which holds both its `.claude` folder and
its `.claude.json`): sign in once, and you stay signed in when the image is
updated. Sign in with your Claude subscription (Pro or Max), in the container:

```sh
docker exec -it 1loop-server claude auth login
```

It prints a link: open it in any browser, sign in, and paste the code it
shows back into the terminal. `docker exec -it 1loop-server claude` also works,
with `/login` in Claude Code's own screen. The Claude Code sign-in a paired
1loop client starts on the server goes to the same place. `docker exec
1loop-server claude auth status` tells whether it is signed in.

An API key is not used: 1loop runs Claude Code on the subscription only, so it
does not pass `ANTHROPIC_API_KEY` to it, and it stops a run that would be
billed to an API key. Without an interactive sign-in, give the container a
long-lived subscription token instead: run `claude setup-token` on any computer
where you are signed in, and pass it with `-e CLAUDE_CODE_OAUTH_TOKEN=…`.

Claude Code's version is the image's: it does not update itself in the
container, and a new image brings a new version. To build one with another
version: `docker build --build-arg CLAUDE_CODE_VERSION=2.1.311 -t 1loop-server .`
(a version from `https://downloads.claude.ai/claude-code-releases/stable`).
A Claude Code connection with its own login (`"claudeLogin": "own"`) keeps its
folder in the home folder instead, which an image update does not keep: use
the shared login in a container.

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
   machine's host key in its logs (`docker logs 1loop-server`):

   ```text
   SSH machine you@host.docker.internal (host.docker.internal:22) has never been connected to. Its host key is ssh-ed25519 SHA256:….
   Type trust 1 to accept it, refuse 1 otherwise.
   ```

   Compare it with `ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub` on the
   host, then run `docker exec 1loop-server 1loop-server trust 1` (or type
   `trust 1` in the console). The key is kept in `/data/known_hosts`, and a
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
starts and never renews it, since a new one would have a new fingerprint:
paired desktops and phones pin that exact certificate and ignore its dates,
so they keep connecting after its first year. It is kept as `server.crt`
and `server.key` in the data directory; back up both together. If one is missing or damaged, the server does not start and says
which file: restore it, or delete both to make a new certificate, and then
pair every device again. You can instead supply a certificate and private
key with `-tls-cert /path/server.crt -tls-key /path/server.key`.
Browsers cannot pin the fingerprint: for the web client without a
certificate warning, see [Behind a reverse proxy](#behind-a-reverse-proxy).

1. On desktop, click **This computer** under the message box and choose
   **Connect a server…**, or open **Settings → Remote → Where the agent runs →
   Connect a server…**, which lists these steps too. On Android, choose
   **Add by address**.
2. Enter `https://your-server:47101`. For a generated/private certificate,
   enter the fingerprint read directly from the server console. Leave it
   empty only when using a certificate trusted by the client's normal CA
   store and matching its hostname. A fingerprint mismatch fails closed;
   clients never silently accept a replacement certificate or downgrade to
   HTTP. Existing Android-to-desktop LAN connections still support their
   explicit `http://` address.
3. The client shows a six-digit code. Approve it from a device already paired
   with the server: 1loop there (desktop, web or Android) shows "<device>
   wants to use this server" and asks for that code. Or run the command the
   client also shows on the server's machine, as the server's user:
   `1loop-server pair <pairing-ID> <six-digit-code>` (in Docker,
   `docker exec <container> 1loop-server pair …`), or type
   `pair <pairing-ID> <six-digit-code>` in the server process's console. The
   client connects by itself once approved.
   Pairings expire after two minutes and allow five failed code attempts. At
   most eight may be pending.
4. The paired client retains a random bearer token. The server persists only
   its hash. Tokens travel in the Authorization header, including streams;
   redirects are not followed with credentials.

On desktop, the server then becomes the window's target, as on the phone: the
sidebar lists its projects and conversations, and they open, run and show in
the same conversation, message box and side panel (its browser, terminals,
tasks, changes and files). The same menu lists the server for the next time,
its hooks, copying a project to it and its update; **This computer** goes back
to the desktop's own projects.

**The first device.** While no device is paired and the server serves the
browser client (`-web-dir`, as in the Docker image), it prints a one-time
setup link at startup, which also shows in `docker logs`:

```
https://<server>:47101/app/#setup=<secret>
```

Open it with the server's address in place of `<server>`: that browser pairs
without the console, and can then approve the next devices. The secret is
random, travels in the URL's fragment (browsers never send it in a request
line, so proxies do not log it), works once, and stops working as soon as a
device is paired. Revoking the last device prints a new one.

A paired device can also unpair devices: Settings → Remote → Devices that
control this 1loop, on the desktop and in the browser (with the server in
use), lists the devices paired with it, each with **Remove**, and **Forget**
on a server (Settings → Remote → Where the agent runs) unpairs this device
there before forgetting it; the phone has the same under Settings → Remote.
All of them ask first. They use `GET /api/devices` and
`POST /api/devices/{id}/revoke`.

**Commands from another shell.** `1loop-server <command>` sends one of the
console's commands to the server that is running: `pair <id> <code>`,
`devices`, `revoke <number>`, `ssh` (and `ssh add …`, `ssh remove <number>`),
`trust <number>` and `refuse <number>`. It prints the answer and exits with
status 1 when the server refused the command. It reaches the server through
a Unix socket in its data directory, `admin.sock`: mode `0600` in the `0700`
directory, so only the server's own user (and root) can use it, as they could
read its keys. It finds the directory as the server does: `-data-dir`, else
`ONELOOP_DATA_DIR` (`/data` in the Docker image), else
`~/.config/1loop-server`. A server started with another `-data-dir` needs the
same one: `1loop-server -data-dir /srv/1loop pair …` (the dialog's command
includes it).

`devices` lists the paired devices, `revoke <number>` removes one: the
fallback when no paired device is at hand.
Revocation rejects new requests and disconnects active event/browser streams.
It does not undo an operation already accepted by the server. These commands
work as well in the console, typed in the server's standard input: with tmux,
or `docker attach` with a container started with `-it` (Compose:
`stdin_open` and `tty`).

Certificate replacement requires verifying and entering the new fingerprint
on each client. A private certificate's fingerprint replaces normal hostname
verification only for that explicitly pinned connection. The clients still
check the TLS proof of the private key, but not the pinned certificate's
validity dates. A browser does check them: after the generated certificate's
first year, it warns about it again.

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

Android has been adapted for the same HTTPS endpoints and pairing.
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
