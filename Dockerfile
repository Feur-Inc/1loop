# The headless 1loop server (docs/SERVER.md#docker). The data directory and
# the projects are volumes; pairing happens in the console: docker attach.
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

FROM debian:trixie-slim
# What the server runs besides itself: Bash and Git for the agent's commands,
# Chromium for Browser Use, tini to pass signals on.
RUN apt-get update \
 && apt-get install -y --no-install-recommends bash git chromium ca-certificates tini \
 && rm -rf /var/lib/apt/lists/* \
 && useradd --create-home --uid 1000 --shell /bin/bash oneloop \
 && mkdir -p /data /projects \
 && chown oneloop:oneloop /data /projects \
 && chmod 0700 /data
COPY --from=build /out/1loop-server /usr/local/bin/1loop-server
# Docker's default profile allows none of Chromium's sandboxes: the container
# isolates the browser instead.
ENV ONELOOP_CHROMIUM_NO_SANDBOX=1
USER oneloop
WORKDIR /projects
VOLUME ["/data", "/projects"]
EXPOSE 47101
ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/1loop-server", "-data-dir", "/data", "-workspace", "/projects", "-listen", ":47101"]
