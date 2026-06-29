# msbd — Microsandbox REST host
#
# Builds the cgo binary that wraps the microsandbox SDK (libkrun) and serves the
# REST API. MUST use a glibc base (NOT alpine/musl): the SDK's embedded FFI
# library and the downloaded `msb` supervisor binary are linked against
# glibc >= 2.28.
#
# RUNTIME REQUIREMENT: the container needs the host's KVM device. Run with:
#   docker run --device /dev/kvm -e MSBD_API_KEY=... -p 8099:8099 \
#     -v msbd-data:/root/.microsandbox  ghcr.io/<you>/msbd:latest
# On a Dokploy host, deploy as a COMPOSE service (not a Swarm/Application) so
# `devices: ["/dev/kvm:/dev/kvm"]` is honored.

# ---- build stage ----------------------------------------------------------
# Build glibc doesn't matter for the FFI dlopen — the runtime base is what the
# bundled .so links against. msbd is small and forward-compatible across glibc
# versions, so we use the default (bookworm-based) golang image here.
FROM golang:1.26 AS build

ENV CGO_ENABLED=1
WORKDIR /src

COPY go.mod go.sum* ./
RUN go mod download

COPY . .
RUN go build -ldflags="-s -w" -o /out/msbd ./cmd/msbd

# ---- runtime stage --------------------------------------------------------
# Debian trixie (13) ships glibc 2.41. The microsandbox v0.6.0 FFI bundle links
# against glibc 2.38 symbols, so older bases (bookworm = 2.36) fail to dlopen
# it. Do NOT downgrade to bookworm/alpine.
FROM debian:trixie-slim

# ca-certificates: registry/TLS for image pulls + the msb+libkrunfw download.
# libcap-ng0: the prebuilt `msb` supervisor links against libcap-ng.so.0.
# curl: handy for healthchecks / debugging.
RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        ca-certificates \
        libcap-ng0 \
        curl \
    && rm -rf /var/lib/apt/lists/*

COPY --from=build /out/msbd /usr/local/bin/msbd

# Persist the runtime (msb + libkrunfw) and the OCI image cache across restarts
# so EnsureInstalled and cold image pulls don't repeat on every boot.
VOLUME ["/root/.microsandbox"]

ENV MSBD_LISTEN=":8099"
EXPOSE 8099

# Readiness reflects FFI-loaded + /dev/kvm openable.
HEALTHCHECK --interval=30s --timeout=5s --start-period=120s --retries=3 \
    CMD curl -fsS http://localhost:8099/readyz || exit 1

ENTRYPOINT ["msbd"]
