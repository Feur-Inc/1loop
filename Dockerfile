# The headless 1loop server (docs/SERVER.md#docker). The data directory and
# the projects are volumes; pairing happens in the console: docker attach.
# Claude Code is included and keeps its sign-in in the data volume: sign in
# once with docker exec -it <container> claude auth login.
#
#   docker build -t 1loop-server .
#   docker run -it -p 47101:47101 -v 1loop-data:/data -v ~/projects:/projects 1loop-server

# The server is static Go (CGO_ENABLED=0): built for the target on the
# build machine, without emulation. BuildKit sets BUILDPLATFORM; the default
# is for Docker's legacy builder, which does not.
ARG BUILDPLATFORM=linux/amd64
FROM --platform=$BUILDPLATFORM golang:1.25-trixie AS build
ARG TARGETOS TARGETARCH
WORKDIR /src
# Only build inputs, as for the Flatpak: no local tokens, keys or caches.
COPY go.mod go.sum ./
COPY third_party third_party
RUN go mod download
COPY VERSION ./
COPY cmd cmd
COPY internal internal
COPY assets assets
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath \
      -ldflags="-s -w -X 1loop/internal/update.Version=$(cat VERSION) -X 1loop/internal/update.Packaged=Docker" \
      -o /out/1loop-server ./cmd/1loop-server
# The browser client (make web), served at /app/: the same wasm on every
# platform, with its gzipped copy for the browsers that accept it.
RUN mkdir -p /out/web \
 && GOOS=js GOARCH=wasm go build -trimpath \
      -ldflags="-s -w -X 1loop/internal/update.Version=$(cat VERSION) -X 1loop/internal/update.Packaged=Docker" \
      -o /out/web/1loop.wasm ./cmd/1loop-web \
 && cp "$(go env GOROOT)/lib/wasm/wasm_exec.js" cmd/1loop-web/index.html /out/web/ \
 && gzip -9kf /out/web/1loop.wasm

# Claude Code's CLI, for Claude Code connections: Anthropic's native build
# (one self-contained binary, no Node.js), downloaded as its installer and
# 1loop's own (internal/agent/claude_install.go) do, and refused unless its
# SHA-256 is the release manifest's. Also on the build machine: it is only a
# download. Bump the version here, or with --build-arg CLAUDE_CODE_VERSION=…;
# downloads.claude.ai/claude-code-releases/stable names the newest stable.
FROM --platform=$BUILDPLATFORM golang:1.25-trixie AS claude
ARG TARGETARCH
ARG CLAUDE_CODE_VERSION=2.1.311
SHELL ["/bin/bash", "-o", "pipefail", "-c"]
RUN set -eu \
 && case "$TARGETARCH" in amd64) platform=linux-x64 ;; arm64) platform=linux-arm64 ;; \
      *) echo "Claude Code has no build for $TARGETARCH" >&2; exit 1 ;; esac \
 && releases=https://downloads.claude.ai/claude-code-releases/$CLAUDE_CODE_VERSION \
 && checksum=$(curl -fsSL "$releases/manifest.json" | tr -d ' \n' \
      | sed -n "s/.*\"$platform\":{\"binary\":\"claude\",\"checksum\":\"\([0-9a-f]\{64\}\)\".*/\1/p") \
 && test -n "$checksum" \
 && mkdir -p /out \
 && curl -fsSL -o /out/claude "$releases/$platform/claude" \
 && echo "$checksum  /out/claude" | sha256sum -c - \
 && chmod 0755 /out/claude

FROM debian:trixie-slim
# What the server runs besides itself: Bash and Git for the agent's commands,
# Chromium for Browser Use, tini to pass signals on, Claude Code (below).
RUN apt-get update \
 && apt-get install -y --no-install-recommends bash git chromium ca-certificates tini \
 && rm -rf /var/lib/apt/lists/* \
 && useradd --create-home --uid 1000 --shell /bin/bash oneloop \
 && mkdir -p /data /projects \
 && chown oneloop:oneloop /data /projects \
 && chmod 0700 /data
COPY --from=build /out/1loop-server /usr/local/bin/1loop-server
COPY --from=build /out/web /usr/local/share/1loop/web
# Root's, so the server cannot change it: a new version comes with a new
# image, never from Claude Code's own updater.
COPY --from=claude /out/claude /usr/local/bin/claude
# Docker's default profile allows none of Chromium's sandboxes: the container
# isolates the browser instead.
ENV ONELOOP_CHROMIUM_NO_SANDBOX=1
# Claude Code's sign-in and settings (its .claude folder and .claude.json
# both go in CLAUDE_CONFIG_DIR) live in the data volume, so they survive
# image updates.
ENV CLAUDE_CONFIG_DIR=/data/claude DISABLE_AUTOUPDATER=1
# "docker exec <container> 1loop-server pair <id> <code>" finds the running
# server's admin socket in /data without -data-dir.
ENV ONELOOP_DATA_DIR=/data
USER oneloop
WORKDIR /projects
VOLUME ["/data", "/projects"]
EXPOSE 47101
ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/1loop-server", "-data-dir", "/data", "-workspace", "/projects", "-listen", ":47101", "-web-dir", "/usr/local/share/1loop/web"]
