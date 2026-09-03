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
- **Interactive terminals.** A real kernel-PTY shell over WebSocket — colors, line editing, window resize, and full-screen TUIs (vim, top) all work.
- **Persisted credentials.** Rotatable API keys and dashboard accounts (admin/viewer) in a local database, managed from the CLI or the web UI — no restart to add or revoke one.

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
     -X POST localhost:8099/api/v1/sandboxes \
     -d '{"image":"alpine:3.19","resources":{"memory_mb":512,"cpu":1}}'
```

```json
{
  "id": "sbx_1ea598fdaabd2a46",
  "image": "alpine:3.19",
  "state": "running",
  "workdir": "/",
  "uptime_seconds": 0,
  "labels": null
}
```

### 3. Run a command in it

```bash
ID=sbx_1ea598fdaabd2a46
curl -s -H "Authorization: Bearer devkey" \
     -X POST localhost:8099/api/v1/sandboxes/$ID/exec \
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
curl -s -H "Authorization: Bearer devkey" -X DELETE localhost:8099/api/v1/sandboxes/$ID
```

> Browse the full API interactively at **`http://localhost:8099/docs`** (Swagger UI), or fetch the raw spec from `/openapi.yaml`. Both are unauthenticated.

## Nix

msbd is packaged as a flake. cgo is enabled at build time, but the only thing
the C side links is `libdl` — the microsandbox Rust FFI library is `dlopen`'d at
_runtime_, so no Rust toolchain is needed to build msbd.

```bash
# Build the binary
nix build github:mark3labs/msbd

# Run it (uses the FHS-wrapped variant — works on NixOS too)
nix run github:mark3labs/msbd
```

**Why the FHS wrapper?** msbd itself is a normal Nix-built binary, but the `msb`
supervisor it downloads on first run and the embedded FFI `.so` it extracts are
vanilla glibc binaries that expect a dynamic loader at `/lib64/ld-linux-*.so.2`
and `libcap-ng.so.0` on a standard path. Plain NixOS has neither, so the
`msbd-fhs` package (what `nix run` and the NixOS module use) provides that FHS
layout. On a regular glibc distro (Debian/Ubuntu/Fedora) the plain `msbd`
package is enough.

Flake outputs:

| Output | What |
|---|---|
| `packages.default` / `packages.msbd` | The bare cgo binary (good on any glibc distro). |
| `packages.msbd-fhs` | FHS-wrapped binary for NixOS hosts. |
| `devShells.default` | Go + gcc + the runtime libs, `CGO_ENABLED=1`. |
| `nixosModules.default` | `services.msbd` — runs it as a hardened systemd service with `/dev/kvm` access. |

As a NixOS service:

```nix
{
  inputs.msbd.url = "github:mark3labs/msbd";

  # in your system config:
  imports = [ msbd.nixosModules.default ];
  services.msbd = {
    enable = true;
    listen = ":8099";
    apiKeyFile = "/run/secrets/msbd.env";   # systemd EnvironmentFile with MSBD_API_KEY=...
    openFirewall = true;
  };
}
```

The module joins the service to the `kvm` group, allows `/dev/kvm`, and keeps the
runtime + image cache under `/var/lib/msbd`.

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

All via environment variables (also settable as `--flag` — see `msbd serve --help`). A `.env` file in the working directory is loaded on startup via [godotenv](https://github.com/joho/godotenv); shell env and `-e` still win over `.env`, and CLI flags win over env. Copy `.env.example` to `.env` for a documented starting point.

| Var | Default | Description |
|---|---|---|
| `MSBD_LISTEN` | `:8099` | HTTP listen address. |
| `MSBD_API_KEY` | *(empty)* | Bearer token(s) required on every request; comma-separated to accept several (zero-downtime rotation). **Empty = unauthenticated (dev only).** |
| `MSBD_API_KEY_FILE` | *(empty)* | Read the bearer token from a file instead of the env (Docker/K8s secrets). Takes precedence over `MSBD_API_KEY`. |
| `MSBD_DEFAULT_IMAGE` | `microsandbox/python` | OCI image used when create omits `image`. |
| `MSBD_MAX_SANDBOXES` | `0` (unlimited) | Hard cap on concurrent sandboxes; rejects new creates above this with 507 `capacity`. Admission is serialized (no overshoot). |
| `MSBD_CREATE_TIMEOUT_SECS` | `300` | Boot deadline (covers cold OCI pulls). |
| `MSBD_PULL_TIMEOUT_SECS` | `900` | Deadline for a standalone image pull (`POST /api/v1/images/pull`); larger than create since a cold pull of a big image can outlast a boot. |
| `MSBD_JOB_MAX_BYTES` | `0` (1 MiB) | Per-stream cap on an async job's stdout/stderr ring buffer. `0` uses the built-in 1 MiB default; older output is dropped once the cap is hit. |
| `MSBD_JOB_TTL_SECS` | `0` (15 min) | How long a finished job's output is retained before the janitor evicts it. `0` uses the built-in 15-minute default. |
| `MSBD_SHUTDOWN_TIMEOUT_SECS` | `60` | Graceful-drain deadline on SIGTERM/Ctrl-C. A drain overrun warns and exits 0 (no spurious restart failure). |
| `MSBD_HOST_PATHS` | *(empty)* | Comma-separated allowlist of host path prefixes the host-transfer endpoints (`copy-from-host`, `copy-to-host`, snapshot `export`/`import`) may touch. **Empty = all host transfers denied (403).** Symlinks are resolved to block escapes. |
| `MSBD_LOG_LEVEL` | `info` | Log verbosity: `debug`, `info`, `warn`, `error`. Invalid values fail fast. Output is colorized on a TTY, plain otherwise. |
| `MSBD_DATA_DIR` | `~/.microsandbox/msbd` | Directory holding the SQLite database of dashboard users, API keys and sessions. Created `0700`, database `0600`. See [Users & API keys](#users--api-keys). |
| `MSBD_SESSION_TTL_SECS` | `0` (12 h) | Dashboard login lifetime. |
| `MSBD_DASHBOARD` | `true` | Serve the web dashboard at `/`. Set `false` to disable. |
| `MSBD_DASHBOARD_USER` | *(empty)* | **Legacy** single-account HTTP Basic auth username. Setting user **or** pass turns it on. Superseded once a stored account exists. |
| `MSBD_DASHBOARD_PASS` | *(empty)* | Legacy Basic auth password. **Both empty and no stored users = dashboard is unauthenticated.** When an API key IS set but the dashboard has no auth, the dashboard is **refused** (it would bypass the API token) unless `MSBD_DASHBOARD_ALLOW_INSECURE=true`. |
| `MSBD_DASHBOARD_ALLOW_INSECURE` | `false` | Override the safety refusal above and serve the dashboard without auth even when an API key is set (unsafe). |

Request bodies are size-capped (1 MiB control-plane, 64 MiB file writes) and reject unknown JSON fields (a typo'd field is a 400, not a silent no-op). The server sets `IdleTimeout` and echoes an `X-Request-Id` on every response.

Flags mirror every var (`--dashboard`, `--host-paths`, `--shutdown-timeout`, `--api-key-file`, …); flag › env › default.

## Users & API keys

msbd keeps a small SQLite database (pure-Go driver — no extra cgo) of **API keys**, **dashboard users** and **login sessions**. It lives at `$MSBD_DATA_DIR` (default `~/.microsandbox/msbd/msbd.db`), which is inside the directory every deployment path already persists: the Docker `VOLUME`, the compose named volume, the NixOS `StateDirectory`.

Nothing about this is mandatory. `MSBD_API_KEY` and `MSBD_DASHBOARD_USER`/`_PASS` keep working exactly as before; stored credentials are accepted **in addition** to them.

### API keys

```bash
msbd keys create ci-runner            # prints the token ONCE
msbd keys create temp --expires 30d
msbd keys list
msbd keys revoke ci-runner            # by name, token prefix, or numeric id
msbd keys rm 3
```

Only `sha256(token)` is stored, so the token cannot be recovered — losing it means minting a new one. Keys are accepted alongside `MSBD_API_KEY`, and creating the first one flips an otherwise-open server to authenticated **without a restart** (the daemon and the CLI share the same database file).

Revoking is fail-safe: a revoked or expired key stops working within a few seconds, but the server stays *authenticated* — revoking your last key locks the API down rather than throwing it open. `msbd keys rm` (deleting every row) is the explicit way back to an unauthenticated dev server.

### Dashboard users

```bash
msbd users add alice                  # prompts for a password, twice
msbd users add ci --role viewer
echo "$PW" | msbd users add bot --password-stdin
msbd users list
msbd users passwd alice               # also signs alice out everywhere
msbd users role alice viewer
msbd users rm alice
```

Creating the first user upgrades the dashboard from HTTP Basic (or no auth) to a **login page with server-side sessions** — again with no restart. Passwords are bcrypt-hashed; there is deliberately no `--password` flag, since it would leak the secret into the process list and shell history.

Two roles: **admin** (everything, including managing users and keys) and **viewer** (read-only — every mutating endpoint is refused server-side, not merely hidden). Removing or demoting the last admin is refused so the dashboard can never become unreachable.

### Maintenance

```bash
msbd db path        # where the database is
msbd db migrate     # create/migrate explicitly (serve does it automatically)
msbd db sweep       # drop expired sessions
```

Back it up by copying that one file. Every command takes `--data-dir` (or `MSBD_DATA_DIR`) — it must match what the daemon uses.

## Web dashboard

A self-contained web UI lives at the **root (`/`)** and manages everything the REST API does — sandboxes (create, start/stop/delete, inspect, run commands, live logs & metrics, a file browser, and a real **kernel-PTY terminal**), volumes, images and snapshots. The REST API is namespaced under `/api/v1`, so the two never collide.

```bash
# Recommended: a real account with a login page and sessions.
msbd users add admin
msbd serve
# → open http://localhost:8099/

# Legacy single-account HTTP Basic auth (still supported):
MSBD_DASHBOARD_USER=admin MSBD_DASHBOARD_PASS=s3cret msbd serve
```

Every section is a **real, bookmarkable URL** — `/` (overview), `/sandboxes`, `/sandboxes/{id}`, `/volumes`, `/images`, `/snapshots`, `/settings/keys`, `/settings/users` — so refresh, browser back/forward and shared links all work. Datastar SSE is used for in-page updates only, over a separate `/ui/*` endpoint namespace.

| Page | What you get |
|---|---|
| **Overview** | Fleet counts by state, capacity headroom against `--max-sandboxes`, aggregate CPU/memory across running sandboxes, cache sizes, recent sandboxes. |
| **Sandboxes** | Sortable + searchable table with state filters, live auto-refresh (pausable, and gated on tab visibility), per-row start/stop/terminal/snapshot/delete. |
| **Sandbox detail** | Live header (state + uptime stream in), full lifecycle actions, and tabs for **Overview** (metadata + charted live metrics), **Run**, **Logs**, **Files** and an embedded **Terminal**. |
| **Run** | Commands execute as async **jobs**: output streams in as it is produced, long commands are **cancellable**, and the last 25 commands are offered as autocomplete. |
| **Logs** | Timestamped and source-coloured, with source/tail filters, search, wrap and follow toggles, jump-to-bottom, and a plain-text download. |
| **Files** | A real browser: breadcrumb navigation, view/edit/save, upload, download, new folder, delete, and a hidden-files toggle. Binary files get a read-only hex preview. |
| **Settings → API keys / Users** | Create, revoke and delete REST API keys (the token is revealed exactly once), and manage dashboard accounts, passwords and roles. Admin-only. |
| **Volumes / Images / Snapshots** | Searchable, sortable tables showing creation and last-used times. Images can be inspected (OCI config + layers), are flagged when a live sandbox uses them, and can seed a new sandbox in one click. Prune reports exactly what it reclaimed. |

Other niceties: a **light/dark/system theme** toggle (persisted, no flash of the wrong palette), a **responsive** layout with a mobile nav drawer and horizontally scrollable tables, **styled confirmation dialogs** (never `window.confirm`), busy states on every mutating control so a double-click can't boot two sandboxes, sticky error toasts plus inline errors next to the control that failed, and keyboard/screen-reader support (skip link, `aria-label`s on icon-only controls, table captions, `aria-sort` on sorted columns).

It is server-rendered with [templ](https://templ.guide) + [templui](https://templui.io) components, styled with Tailwind, and made reactive with [Datastar](https://data-star.dev) (SSE-driven DOM patching). Everything — the compiled CSS, the Datastar runtime, xterm.js and the component JavaScript — is **embedded in the binary** (`//go:embed`); there are no external assets to deploy. Auth is independent of `MSBD_API_KEY`: the API stays bearer-gated while the dashboard has its own. It picks the strongest option available — a **login page with `HttpOnly`, `SameSite=Lax` session cookies** once you have created an account (`msbd users add`), the legacy single-account **HTTP Basic** if only `MSBD_DASHBOARD_USER`/`_PASS` are set, and open otherwise. The terminal page never embeds the API key — it uses a short-lived, single-use ticket.

If the API requires a key but the dashboard would have no auth at all, msbd **locks the dashboard** — every route serves a short page telling you to run `msbd users add`, which takes effect on the next reload with no restart. Override with `MSBD_DASHBOARD_ALLOW_INSECURE=true`.


## REST API

| Method & path | Purpose |
|---|---|
| `GET /healthz` · `GET /readyz` | Liveness · readiness (runtime loaded + `/dev/kvm` accessible). |
| `GET /docs` · `GET /openapi.yaml` | Swagger UI · raw OpenAPI spec (unauthenticated). |
| `GET /` | Web management UI with its own auth — see [Web dashboard](#web-dashboard). |
| `GET /api/v1/version` | Default image + runtime/SDK versions (diagnostics). |
| `GET /metrics` | Prometheus text-exposition operational metrics (sandbox counts, jobs, terminals, request classes). |
| `POST /api/v1/terminal-tickets` | Mint a short-lived single-use terminal ticket (browser WS auth without exposing the API key). |
| `POST /api/v1/sandboxes` · `GET /api/v1/sandboxes` · `GET/DELETE /api/v1/sandboxes/{id}` | Lifecycle. Create accepts `user`, `hostname`, `network_policy`, `ports`, `secrets`, `mounts`. |
| `GET /api/v1/sandboxes/{id}/inspect` | Sandbox metadata + raw SDK config blob. |
| `POST /api/v1/sandboxes/{id}/stop` · `.../start` | Pause / ensure-running. |
| `POST /api/v1/sandboxes/{id}/exec` · `.../run` | Synchronous exec — `exec` is short, `run` is long-safe and ensures-running. |
| `GET /api/v1/sandboxes/{id}/terminal` | Interactive **kernel-PTY** terminal over **WebSocket** (binary stdin/stdout; text control frames for resize/signal). Colors, line editing, resize, vim/top all work. Auth via header, `?key=`, or a single-use `?ticket=` (see `POST /api/v1/terminal-tickets`). |
| `POST /api/v1/sandboxes/{id}/jobs` · `GET /api/v1/sandboxes/{id}/jobs/{job}` | Async (background) jobs. Output is a **bounded ring buffer** (1 MiB/stream); poll reports `truncated` + `stdout_bytes`/`stderr_bytes`, and finished jobs are evicted after a TTL. |
| `POST /api/v1/sandboxes/{id}/jobs/{job}/stdin` · `.../signal` | Write to a job's stdin (launch with `stdin:true`) · send a signal (≤0 = kill). |
| `POST /api/v1/sandboxes/{id}/files/read` · `.../files/write` | Native file IO, base64-encoded. |
| `POST /api/v1/sandboxes/{id}/files/{list,stat,exists,mkdir,remove,copy,rename}` | Extended filesystem operations. |
| `POST /api/v1/sandboxes/{id}/files/{copy-from-host,copy-to-host}` | Copy between an **allowlisted** host path (`MSBD_HOST_PATHS`) and the sandbox. Denied (403) when the allowlist is empty or the path escapes it. |
| `GET /api/v1/metrics` · `GET /api/v1/sandboxes/{id}/metrics` | Point-in-time per-sandbox resource metrics (all / one). For scrapeable ops telemetry use `GET /metrics`. |
| `GET /api/v1/sandboxes/{id}/logs` | Read persisted stdout/stderr/system logs (`?tail=`, `?sources=`). |
| `POST/GET /api/v1/volumes` · `GET/DELETE /api/v1/volumes/{name}` | Named persistent volumes. |
| `POST /api/v1/volumes/{name}/files/{read,write,mkdir,remove,exists}` | Volume file IO. |
| `GET /api/v1/images` · `GET /api/v1/images/inspect` · `POST /api/v1/images/pull` · `DELETE /api/v1/images` · `POST /api/v1/images/prune` | Cached OCI image inventory. `pull` fetches an image into the cache (long-running; boots a throwaway microVM). |
| `POST/GET /api/v1/snapshots` · `GET/DELETE /api/v1/snapshots/{name}` · `.../verify` | Sandbox rootfs snapshots. |
| `POST /api/v1/snapshots/{export,import,reindex}` | Export/import snapshot archives · rebuild the index. |

Full schemas: see [`openapi.yaml`](./openapi.yaml).

## Lifecycle semantics

- **Detached by default.** Every sandbox is created detached, so the microVM keeps running when msbd restarts.
- **Reconnect at boot.** On startup msbd lists all known sandboxes and re-attaches by name. A sandbox that existed before the restart is still callable through the same id.
- **Transparent resume.** `run`, `launch`, and `files/*` all ensure-running first — a paused box silently resumes on the next call. `exec` (one round-trip helpers) deliberately does not, so it stays cheap.
- **Jobs and terminals are in-memory.** A job that was running when msbd restarts polls as `gone`, and an open terminal's WebSocket simply closes (the VM survives; the streaming attach does not). Re-launch / reconnect from the client side.
- **Names are ids.** Sandbox names (≤128 bytes UTF-8) ARE the provider id. msbd generates them as `sbx_<16hex>`; you can also pass your own.

## What it is, what it isn't

✅ A simple way to expose microsandbox over HTTP so any language can drive it.
✅ A single-host device server with enough auth to be safe on its own: API keys, dashboard accounts and an admin/viewer split. Auth your real *end users* upstream.

❌ Not a multi-host scheduler. Capacity = the one host.
❌ Not a multi-tenant platform with quotas, billing or fine-grained RBAC. (Bring your own.)
❌ Not a re-implementation of microsandbox's own cloud backend.

## Development

```bash
# Build (or `task build`)
go build -o ./bin/msbd ./cmd/msbd

# Run (these are equivalent — the bare binary defaults to `serve`)
MSBD_API_KEY=devkey ./bin/msbd
./bin/msbd serve --api-key devkey --listen :8099

# Explore the CLI (styled help, version, shell completions)
./bin/msbd --help
./bin/msbd serve --help
./bin/msbd users --help
./bin/msbd keys --help
./bin/msbd --version

# Lint, format, test (or `task lint` / `task fmt` / `task test`)
golangci-lint run ./...
gofmt -w .
go test ./...
```

The CLI is built on [cobra](https://github.com/spf13/cobra) and styled with
[charmbracelet/fang](https://github.com/charmbracelet/fang). Every `MSBD_*` env
var has a matching `serve` flag (flag overrides env overrides default), and
Ctrl-C / SIGTERM trigger a graceful drain of in-flight requests.

### Repo layout

```
cmd/msbd/main.go              # entrypoint — EnsureInstalled, reconcile, serve
cmd/msbd/admin.go             # `msbd users` / `msbd keys` / `msbd db` subcommands
assets.go                     # //go:embed openapi.yaml (served at /docs)
internal/api/router.go        # HTTP router + middleware (auth, recover, log)
internal/api/handlers.go      # core lifecycle/exec/jobs/files handlers
internal/api/handlers_ext.go  # inspect, metrics, logs, fs, volumes, images, snapshots
internal/api/terminal.go      # interactive terminal WebSocket handler
internal/api/docs.go          # Swagger UI (/docs) + raw spec (/openapi.yaml)
internal/api/dto.go           # wire shapes
internal/core/service.go      # SDK-facing business logic (lifecycle/exec/jobs/files)
internal/core/terminal.go     # interactive PTY terminal: Session interface + OpenTerminal
internal/core/terminal_agent.go # kernel-PTY backend over the raw agent protocol (CBOR)
internal/core/fs.go           # extended filesystem ops + host transfer
internal/core/metrics.go      # point-in-time resource metrics
internal/core/logs.go         # persisted log reads
internal/core/volume.go       # named persistent volumes + volume file IO
internal/core/image.go        # cached OCI image inventory
internal/core/snapshot.go     # sandbox rootfs snapshots
internal/core/registry.go     # live handle cache + workdir cache + reconcile
internal/core/jobs.go         # async job registry (+ stdin/signal/cancel)
internal/core/version.go      # SDK / runtime version helpers
internal/store/store.go       # SQLite state: open + embedded migrations (the only SQL)
internal/store/users.go       # dashboard accounts (bcrypt) + roles
internal/store/apikeys.go     # REST bearer tokens (sha256-hashed, shown once)
internal/store/sessions.go    # dashboard login sessions
internal/store/cache.go       # TTL cache in front of token verification
internal/dashboard/dashboard.go    # web UI: page + SSE routes and their guards (Mount on the api mux)
internal/dashboard/auth.go         # open / basic / session auth modes, guards, cookies
internal/dashboard/handlers.go     # page handlers (one real URL per section) + shared SSE helpers
internal/dashboard/handlers_*.go   # Datastar SSE handlers (overview, sandboxes, files, volumes, images, snapshots, settings)
internal/dashboard/views/*.templ   # templ pages/fragments (templui components + Datastar attrs)
internal/dashboard/components/      # vendored templui components (via `templui add`)
internal/dashboard/assets/          # input.css + committed output.css, datastar/xterm, component JS (embedded)
openapi.yaml                  # the contract
VERSION                       # release version (single source of truth)
Taskfile.yml                  # dev + release tasks (go-task)
flake.nix                     # Nix package + dev shell + NixOS module
Dockerfile                    # build from source
Dockerfile.release            # used by goreleaser
docker-compose.yml            # example compose deploy
```

### Releasing

The git tag is the source of truth for the version. Use the release task so the
`VERSION` file and the tag are bumped atomically (you type the version once):

```bash
task release NEW_VERSION=1.2.3      # bump VERSION, commit, tag locally
git push origin HEAD v1.2.3         # push to trigger the release workflow

# or in one shot:
task release:push NEW_VERSION=1.2.3
```

The task refuses to run on a dirty tree, validates semver, and won't clobber an
existing tag. The release workflow then verifies `v$(cat VERSION)` equals the
pushed tag and fails on mismatch.

GoReleaser injects the version from the tag (`-X main.version`); the Nix flake
reads the same number from `VERSION` (flakes can't see git tags), so `nix build`
off a tagged checkout reports an identical version. `commit`/`date` are filled
from the tag's revision in both paths.

## License

Apache-2.0 — see [`LICENSE`](./LICENSE) and [`NOTICE`](./NOTICE).

msbd wraps the [microsandbox](https://github.com/superradcompany/microsandbox) Go SDK (also Apache-2.0). The microVM runtime it drives — `msb` + `libkrunfw` (LGPL) — is **not** bundled with msbd; the SDK downloads it to `~/.microsandbox/` on first run. See [`NOTICE`](./NOTICE) for details.
