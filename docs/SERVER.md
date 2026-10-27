# Headless 1loop server

The server runs the same agent loop, provider integrations, persistent tasks,
context policy, file tools, Browser Use and Terminal Use as the desktop. It
has no Fyne/GLFW dependency and does not offer Computer Use. Conversations,
files, shells and Chromium run on the server machine. Disconnecting a client
does not stop its conversations; Stop does.

## Build and run on Debian

Build with Go 1.23 or newer:

```sh
make server
```

`bin/1loop-server` is a static Go binary built with `CGO_ENABLED=0`. The runtime
needs Bash and Git, plus Chromium for Browser Use. It needs no desktop session,
X server, Wayland compositor or virtual display:

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

## HTTPS and pairing

On first start the server generates an ECDSA TLS certificate, valid for one
year, and prints its SHA-256 fingerprint. It reuses that certificate on later
starts. You can instead supply a certificate and private key with
`-tls-cert /path/server.crt -tls-key /path/server.key`.

1. On desktop, click **This computer** under the message box and choose
   **Connect a server…**, or open **Settings → General → Remote server →
   Connect…**. On Android, choose **Add by address**.
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
Claude Code uses the CLI and authentication installed on the server.
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

The server checks the [release page](https://tpm28.github.io/1loop/) when a
device asks (`GET /api/update`). A device connected to an outdated server
shows *Update server*; on confirmation the server downloads its new version,
checks its signature and SHA-256, replaces its own binary (the previous one
stays beside it as `.old`) and restarts in place with the same arguments. Under
systemd the process keeps its PID. The binary's folder must be writable by the
server's user; otherwise update it by hand from the release page.
