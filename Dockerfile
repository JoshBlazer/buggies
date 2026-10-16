# syntax=docker/dockerfile:1
#
# Buggies server image — `buggies serve` as a container.
#
# IMPORTANT — build context is the PARENT (kernel/) directory, not buggies/.
# Buggies is a second module nested inside the kernel repo
# (documents/kernel/buggies) and its go.mod carries `replace
# github.com/joshblazer/kernel => ..`, so the kernel module must be present
# one directory up from buggies at build time. Build it the same way the
# kernel Makefile builds the egress image — with -f and the kernel root as
# context:
#
#   # from documents/kernel  (the PARENT of buggies)
#   docker build -f buggies/Dockerfile -t buggies/serve:latest .
#
# The .dockerignore that trims the context lives at the kernel root; this
# repo's buggies/.dockerignore documents the patterns to copy or symlink
# there (Docker only reads .dockerignore from the context root).
#
# This container is the APP, not the sandbox. Reproductions still run in a
# separate gVisor (runsc) Docker daemon; the serve process only shells out to
# the `docker` CLI against that daemon's socket. Bind-mount the gVisor socket
# into this container and point DOCKER_HOST at it (see deploy/docker-compose.yml
# and deploy/docker-gvisor.service). The container never runs untrusted PR code
# itself.

# ---- build stage -------------------------------------------------------------
# golang tag matches go.mod (go 1.26.0).
FROM golang:1.26-bookworm AS build

# Copy the whole kernel repo (context root). This brings in BOTH the kernel
# module (at /src) and the nested buggies module (at /src/buggies), so the
# `replace => ..` from buggies/go.mod resolves to the kernel working tree.
WORKDIR /src
COPY . /src

WORKDIR /src/buggies

# GOWORK=off ignores the dev-only go.work; GOFLAGS=-mod=mod lets the build
# update the module graph across the two-module kernel closure (a plain
# `go build` may not resolve everything). Matches the documented build line.
# CGO must stay ENABLED: the kernel's tree-sitter grammar packages are C
# bindings, so CGO_ENABLED=0 excludes all their Go files and the build fails
# ("build constraints exclude all Go files in .../grammar"). The golang-bookworm
# builder ships gcc; the resulting binary links glibc, which the slim runtime
# base provides.
ENV GOWORK=off GOFLAGS=-mod=mod CGO_ENABLED=1
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    go build -o /out/buggies ./cmd/buggies

# ---- runtime stage -----------------------------------------------------------
FROM debian:bookworm-slim AS runtime

# ca-certificates: TLS to the GitHub API and (optionally) the Anthropic API.
# curl: used by the HEALTHCHECK below.
# docker-ce-cli: the serve process shells out to `docker` to drive the gVisor
#   daemon — only the CLI client, never the daemon (that stays on the host).
RUN set -eux; \
    apt-get update; \
    apt-get install -y --no-install-recommends ca-certificates curl gnupg; \
    install -m 0755 -d /etc/apt/keyrings; \
    curl -fsSL https://download.docker.com/linux/debian/gpg \
      | gpg --dearmor -o /etc/apt/keyrings/docker.gpg; \
    chmod a+r /etc/apt/keyrings/docker.gpg; \
    echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/debian bookworm stable" \
      > /etc/apt/sources.list.d/docker.list; \
    apt-get update; \
    apt-get install -y --no-install-recommends docker-ce-cli; \
    apt-get purge -y --auto-remove gnupg; \
    rm -rf /var/lib/apt/lists/*

COPY --from=build /out/buggies /usr/local/bin/buggies

# Non-root. BUGGIES_DATA defaults to ~/.buggies; give the user a real home and
# a state dir, and override BUGGIES_DATA to a mounted volume in compose.
RUN useradd --create-home --home-dir /home/buggies --shell /usr/sbin/nologin buggies \
    && mkdir -p /var/lib/buggies \
    && chown buggies:buggies /var/lib/buggies
USER buggies

ENV BUGGIES_ADDR=:8080 \
    BUGGIES_DATA=/var/lib/buggies

EXPOSE 8080

# Liveness: `serve` answers GET /healthz with "ok" on its --addr port.
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
    CMD curl -fsS http://127.0.0.1:8080/healthz || exit 1

ENTRYPOINT ["buggies", "serve"]
