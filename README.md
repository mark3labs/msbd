# msbd — Microsandbox REST host

<p align="center">
  <em>A small HTTP server that wraps <a href="https://microsandbox.dev">microsandbox</a> and exposes its microVMs over a clean REST API.</em>
</p>

<p align="center">
  <a href="https://github.com/mark3labs/msbd/actions/workflows/ci.yml"><img src="https://github.com/mark3labs/msbd/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="https://github.com/mark3labs/msbd/releases/latest"><img src="https://img.shields.io/github/v/release/mark3labs/msbd?style=flat&color=blue" alt="Release"></a>
  <a href="https://github.com/mark3labs/msbd/pkgs/container/msbd"><img src="https://img.shields.io/badge/ghcr.io-msbd-blue?logo=docker" alt="Container"></a>
  <a href="https://github.com/mark3labs/msbd/blob/main/LICENSE"><img src="https://img.shields.io/github/license/mark3labs/msbd?style=flat" alt="License"></a>
</p>

## What is this?

[microsandbox](https://github.com/superradcompany/microsandbox) is a local microVM runtime — fast, hardware-isolated sandboxes booted from OCI images via libkrun. It's terrific, but the SDK is in-process and Go-only.

**msbd** puts a small daemon and a REST API in front of it, so any language can drive microsandbox over plain HTTP. Run msbd once on a host that has `/dev/kvm`, then `curl` it (or generate a client from the OpenAPI spec) from wherever.

- **Simple.** ~12 endpoints, OpenAPI 3.1 spec, JSON in/out, bearer auth.
- **MicroVMs survive restarts.** Sandboxes are created detached; msbd reconnects them by name on boot.
- **Native primitives.** Real exec sessions for async jobs, real file IO over the guest filesystem.

## Quickstart

### 1. Run the server

```bash
docker run --rm \
  --device /dev/kvm \
  -p 8099:8099 \
  -e MSBD_API_KEY=devkey \
  -v msbd-data:/root/.microsandbox \
  ghcr.io/mark3labs/msbd:latest
```

The first start downloads the microsandbox runtime (~30 MB) into the mounted volume. Subsequent starts skip it. Wait for `/readyz` to return 200:

```bash
curl -fsS localhost:8099/readyz   # → ready
```

### 2. Boot a microVM

```bash
curl -s -H "Authorization: Bearer devkey" \
     -X POST localhost:8099/v1/sandboxes \
     -d '{"image":"alpine:3.19","resources":{"memory_mb":512,"cpu":1}}'
```

```json
{
  "id": "sbx_1ea598fdaabd2a46",
  "image": "alpine:3.19",
  "state": "running",
  "workdir": "/",
  "uptime_seconds": 0,
  "cost_usd": 0,
  "labels": null
}
```

### 3. Run a command in it

```bash
ID=sbx_1ea598fdaabd2a46
curl -s -H "Authorization: Bearer devkey" \
     -X POST localhost:8099/v1/sandboxes/$ID/exec \
     -d '{"cmd":"uname -a && whoami"}'
```

```json
{
  "exit_code": 0,
  "stdout": "Linux sbx_1ea598fdaabd2a46 6.12.68 ... x86_64 GNU/Linux\nroot\n",
  "stderr": ""
}
```

### 4. Clean up

```bash
curl -s -H "Authorization: Bearer devkey" -X DELETE localhost:8099/v1/sandboxes/$ID
```

## Host requirements

msbd boots real microVMs, so **the host machine must have working hardware virtualization**:

| Host | Needs |
|---|---|
| Bare-metal Linux | `/dev/kvm` present (almost always) |
| Linux VM | **Nested virtualization** enabled by the parent hypervisor; `/dev/kvm` exposed |
| Docker container | Run with `--device /dev/kvm` (or `--privileged`). The host kernel still has to expose KVM. |
| macOS / Windows | Use the upstream microsandbox SDK directly; msbd is Linux-only by design. |

Quick host check:

```bash
ls -l /dev/kvm                                          # device exists
egrep -c '(vmx|svm)' /proc/cpuinfo                      # CPU virt flag present
cat /sys/module/kvm_{intel,amd}/parameters/nested 2>/dev/null   # Y/1 if VM
```

## Configuration

All via environment variables.

| Var | Default | Description |
|---|---|---|
| `MSBD_LISTEN` | `:8099` | HTTP listen address. |
| `MSBD_API_KEY` | *(empty)* | Bearer token required on every request. **Empty = unauthenticated (dev only).** |
| `MSBD_DEFAULT_IMAGE` | `microsandbox/python` | OCI image used when create omits `image`. |
| `MSBD_PREBAKED` | `false` | Set `true` when the default image already ships your toolchain; reported via `/v1/capabilities` so clients can skip provisioning. |
| `MSBD_MAX_SANDBOXES` | `0` (unlimited) | Hard cap on concurrent sandboxes; rejects new creates above this with 507. |
| `MSBD_CREATE_TIMEOUT_SECS` | `300` | Boot deadline (covers cold OCI pulls). |

## REST API

| Method & path | Purpose |
|---|---|
| `GET /healthz` · `GET /readyz` | Liveness · readiness (runtime loaded + `/dev/kvm` accessible). |
| `GET /v1/capabilities` | Backend features + default image + runtime version. |
| `POST /v1/sandboxes` · `GET /v1/sandboxes` · `GET/DELETE /v1/sandboxes/{id}` | Lifecycle. |
| `POST /v1/sandboxes/{id}/stop` · `.../start` | Pause / ensure-running. |
| `POST /v1/sandboxes/{id}/exec` · `.../run` | Synchronous exec — `exec` is short, `run` is long-safe and ensures-running. |
| `POST /v1/sandboxes/{id}/jobs` · `GET /v1/sandboxes/{id}/jobs/{job}` | Async (background) jobs with streaming output buffers. |
| `POST /v1/sandboxes/{id}/files/read` · `.../files/write` | Native file IO, base64-encoded. |

Full schemas: see [`openapi.yaml`](./openapi.yaml).

## Lifecycle semantics

- **Detached by default.** Every sandbox is created detached, so the microVM keeps running when msbd restarts.
- **Reconnect at boot.** On startup msbd lists all known sandboxes and re-attaches by name. A sandbox that existed before the restart is still callable through the same id.
- **Transparent resume.** `run`, `launch`, and `files/*` all ensure-running first — a paused box silently resumes on the next call. `exec` (one round-trip helpers) deliberately does not, so it stays cheap.
- **Jobs are in-memory.** A job that was running when msbd restarts polls as `gone` (the VM survives; the streaming attach does not). Re-launch from the client side.
- **Names are ids.** Sandbox names (≤128 bytes UTF-8) ARE the provider id. msbd generates them as `sbx_<16hex>`; you can also pass your own.

## What it is, what it isn't

✅ A simple way to expose microsandbox over HTTP so any language can drive it.
✅ A single-host, single-tenant device server. Auth your real users *upstream*.

❌ Not a multi-host scheduler. Capacity = the one host.
❌ Not a multi-tenant platform with quotas, billing, RBAC. (Bring your own.)
❌ Not a re-implementation of microsandbox's own cloud backend.

## Development

```bash
# Build
go build -o ./tmp/msbd ./cmd/msbd

# Run
MSBD_API_KEY=devkey ./tmp/msbd

# Test
go test ./...
```

### Repo layout

```
cmd/msbd/main.go              # entrypoint — EnsureInstalled, reconcile, serve
internal/api/router.go        # HTTP router + middleware (auth, recover, log)
internal/api/handlers.go      # per-endpoint handlers
internal/api/dto.go           # wire shapes
internal/core/service.go      # SDK-facing business logic
internal/core/registry.go     # live handle cache + workdir cache + reconcile
internal/core/jobs.go         # async job registry
internal/core/version.go      # SDK / runtime version helpers
openapi.yaml                  # the contract
Dockerfile                    # build from source
Dockerfile.release            # used by goreleaser
docker-compose.yml            # example compose deploy
```

## License

Apache-2.0
