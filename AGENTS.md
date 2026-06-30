# AGENTS.md

## Project overview

**msbd** is a small Go HTTP server that wraps the [microsandbox](https://github.com/superradcompany/microsandbox) Go SDK (`github.com/superradcompany/microsandbox/sdk/go`) and exposes a REST API for managing fast, local microVMs. It exists so that long-running applications can drive microsandbox without linking libkrun / cgo themselves: msbd quarantines all of that to one binary on one KVM-equipped host, and everything else talks plain HTTP.

Module path: `github.com/mark3labs/msbd`.

## How it's wired up

```
cmd/msbd/main.go         entrypoint: fang/cobra CLI → serve cmd → loadConfig →
                         EnsureInstalled → core.NewService → svc.Reconcile →
                         api.NewServer → ListenAndServe (graceful drain on signal)

internal/core/           SDK-facing business logic. EVERY call to the
                         microsandbox SDK happens here (and only here).
                         The api/ package never imports the SDK.

internal/api/            HTTP surface. Routes, middleware (bearer auth,
                         panic recover, request log), DTOs that mirror
                         the value types in core/.

openapi.yaml             the wire contract. Source of truth for client
                         generators and reviewers. Embedded into the binary
                         via assets.go (//go:embed) and served at /openapi.yaml
                         + /docs (Swagger UI).
```

The two-package split (`api` ↔ `core`) is the boundary that keeps DTO churn from leaking into business logic and vice versa.

## Layout

- **`cmd/msbd/main.go`** — cobra CLI styled with `charmbracelet/fang`. The root command defaults to (and also exposes) a `serve` subcommand whose flags mirror the `MSBD_*` env vars (flag › env › default). `serve` does `msb.EnsureInstalled` (downloads `msb` + `libkrunfw` into `~/.microsandbox/` on first run), startup reconcile, then HTTP serve with graceful shutdown on Ctrl-C / SIGTERM. Also defines the `/readyz` probe (FFI loaded + `/dev/kvm` openable r/w).
- **`assets.go`** (module root) — `//go:embed openapi.yaml` into `OpenAPISpec`. Lives at the root because `go:embed` can't reference a parent directory from `internal/api`. `main.go` hands the bytes to `Server.SetOpenAPI`.
- **`internal/core/service.go`** — `Service` is the single owner of all SDK calls: lifecycle (`Create`/`Get`/`Inspect`/`List`/`Stop`/`Start`/`Delete`), exec (`Exec`/`Run`), jobs (`Launch`/`Poll` + `WriteJobStdin`/`CloseJobStdin`/`SignalJob`), file IO (`ReadFile`/`WriteFile`). Provider-neutral input/output types (`CreateParams`, `Instance`, `ExecParams`, `ExecResult`).
- **`internal/core/terminal.go`** — interactive terminal sessions (`OpenTerminal`). Returns a transport-agnostic `Session` interface (`Output`/`Write`/`Resize`/`Signal`/`Close`/`Wait`); goes through `resolve()` like `Run`, then hands off to the agent-PTY backend. In-memory only.
- **`internal/core/terminal_agent.go`** — the **real kernel-PTY** backend. Drives the microsandbox agent protocol directly (`ConnectAgentSandbox` + `AgentClient.Stream`/`Send`/`Next`) with hand-rolled CBOR frames (`fxamacker/cbor`), replicating what the SDK's `Attach` does but sourcing stdin from the WebSocket instead of a local TTY. Sends `core.exec.request{tty:true,rows,cols}` and relays `core.exec.stdin`/`resize`/`signal` ↔ `core.exec.stdout`/`stderr`/`exited`. **The wire schema (protocol v5) is reverse-engineered from upstream Rust, NOT a public SDK API** — a microsandbox protocol bump can break this file. Constants in this file mirror `crates/protocol/lib`.
- **`internal/core/fs.go`** — extended filesystem ops over `sb.FS()`: `ListDir`/`Stat`/`Exists`/`Mkdir`/`Remove`/`Copy`/`Rename` plus host transfer (`CopyFromHost`/`CopyToHost`). All route through `resolve()`.
- **`internal/core/metrics.go`** — `Metrics(id)` and `AllMetrics()` point-in-time resource snapshots.
- **`internal/core/logs.go`** — `Logs(id, LogQuery)` reads persisted stdout/stderr/output/system logs with tail + source filters.
- **`internal/core/volume.go`** — named persistent volumes (`CreateVolume`/`ListVolumes`/`GetVolume`/`RemoveVolume`) and volume file IO. Volumes are independent of sandboxes (not cached in `Registry`); mount them at create via `CreateParams.Mounts`.
- **`internal/core/image.go`** — cached OCI image inventory (`ListImages`/`InspectImage`/`RemoveImage`/`PruneImages`) over the SDK `msb.Image` factory.
- **`internal/core/snapshot.go`** — sandbox rootfs snapshots over the `msb.Snapshot` factory (`Create`/`List`/`Get`/`Verify`/`Remove`/`Export`/`Import`/`Reindex`).
- **`internal/core/registry.go`** — `Registry` is the in-process cache: name → live `*msb.Sandbox` handle, name → first-seen time (uptime), name → resolved native workdir. `resolve()` is the single choke point that folds **transparent resume** and **reconnect-after-restart** into every exec/run/file path. `Reconcile()` re-attaches to pre-existing VMs at boot.
- **`internal/core/jobs.go`** — `JobRegistry` backs the async API. `launch` starts an `sb.ShellStream` and a drain goroutine that consumes `ExecHandle.Recv` events into per-job stdout/stderr ring buffers and records the exit code. Optionally opens a stdin pipe (`ExecParams.Stdin`) for `writeStdin`/`closeStdin`/`signal`. In-memory only — jobs poll as `gone` after a msbd restart.
- **`internal/core/version.go`** — `RuntimeVersion()` / `SDKVersion()` shims for diagnostics.
- **`internal/api/router.go`** — stdlib `http.ServeMux` (Go 1.22+ pattern matching), bearer-auth middleware, panic recover, request logger. `SetOpenAPI([]byte)` enables `/docs` + `/openapi.yaml`.
- **`internal/api/handlers.go`** — handlers for the core lifecycle/exec/jobs/files surface, each a near-1:1 DTO ⇄ `core` translation.
- **`internal/api/handlers_ext.go`** — handlers for the extended surface: inspect, metrics, logs, extended filesystem, job stdin/signal, volumes, images, snapshots. Same `decode → svc.X → encode | notFoundOr` shape.
- **`internal/api/terminal.go`** — the `GET /v1/sandboxes/{id}/terminal` WebSocket handler. Opens a `core.Session` BEFORE upgrading (so an unknown sandbox surfaces as a clean `404`, not a flapping socket), then splices the WebSocket ↔ `Session`: binary frames = stdin/stdout bytes, text frames = JSON control (`resize`/`signal`) in / events (`exit`) out. Uses `github.com/gorilla/websocket`. Auth via `authWS` (header or `?key=`). The guest PTY emits canonical CRLF, so output passes through verbatim.
- **`internal/api/docs.go`** — `/docs` Swagger UI page (CDN assets) + `/openapi.yaml` raw spec. Both are unauthenticated (the spec is not a secret).
- **`internal/api/dto.go`** — the JSON wire shapes. **Keep in lockstep with `openapi.yaml` and downstream clients.**

## Adding a new endpoint

1. Add (or reuse) DTOs in `internal/api/dto.go`. Tags: `json:"..."` — no `omitempty` on input fields that should appear in the schema.
2. Add the business method to `internal/core/`. Lifecycle/exec/jobs/file-IO go in `service.go`; otherwise use (or add) the topical file — `fs.go`, `metrics.go`, `logs.go`, `volume.go`, `image.go`, `snapshot.go`. Keep all SDK calls inside `core`.
3. Add the handler in `internal/api/handlers.go` (core surface) or `internal/api/handlers_ext.go` (everything else). Pattern: `decode → svc.X → encode | notFoundOr`.
4. Wire the route in `internal/api/router.go` under the appropriate verb/path. Apply `s.auth(...)` unless the endpoint is health- or docs-only. For a WebSocket upgrade endpoint use `s.authWS(...)` instead — it also accepts the bearer token as a `?key=` query param, since browsers can't set headers on a WS handshake.
5. Document it in `openapi.yaml` — schemas under `components/schemas`, response examples, error envelopes. The spec is embedded, so a rebuild reflects it at `/docs`.
6. Update the endpoint table in `README.md` if it's user-visible.

## Conventions & gotchas

- **The `api` package never imports the microsandbox SDK.** All `github.com/superradcompany/microsandbox/sdk/go` references stay in `internal/core/`. This is the cgo isolation boundary — if you find yourself reaching for `msb.X` from a handler, lift it into `core` first. (The terminal handler honors this: it speaks only to the `core.Session` interface, never an `msb.ExecHandle`.)
- **Always `WithDetached()`.** Sandboxes MUST be created detached so they survive an msbd restart. The detached → reconnect-by-name dance is the whole point of the daemon.
- **Sandbox names ARE the provider id.** Server-generated as `sbx_<16hex>` in `core.newName()`. Names are limited to 128 UTF-8 bytes by the SDK.
- **`resolve()` is the choke point.** Don't grab a `*msb.Sandbox` directly from the registry cache map — always go through `Registry.resolve(ctx, name)` so reconnect + transparent resume work uniformly. Bypassing it leaks "no handle after restart" bugs.
- **`Run` is long-safe; `Exec` is not.** `Exec` is the fast path for one-shot provisioning helpers and intentionally does NOT ensure-running. `Run` blocks until completion and resumes a paused box first. Put no low-timeout proxy in front of `/run`.
- **`Delete` stops before remove.** The SDK's `RemoveSandbox` refuses a running box; `core.Service.Delete` does a best-effort `Stop` first.
- **Workdir resolution.** Create runs `pwd` in the booted guest and caches the result so `Instance.Workdir` reflects the image's real `WORKDIR` (e.g. `/workspace` for the kit image) instead of the SDK's `cfg.Workdir`, which only contains an explicitly-pinned value.
- **glibc, not musl.** The SDK's embedded FFI and the downloaded `msb` supervisor link against glibc ≥ 2.28. The Dockerfile uses `debian:bookworm-slim` and apt-installs `libcap-ng0` because the prebuilt supervisor links it.
- **Errors flow through `notFoundOr`.** `core.ErrNotFound` → 404; anything else → 500 (or 507 from `Create` when capacity is hit). Always return a typed error from `core`, never an HTTP status.
- **No `omitempty` on REST inputs.** It drops fields from the OpenAPI schema and breaks generated clients.
- **DTO names are stable.** They're the wire contract — renaming a JSON field is a breaking change for every downstream client. Use a new field, deprecate, then remove.
- **Volumes / images / snapshots aren't sandboxes.** They're standalone resources keyed by name/reference, not cached in `Registry` and not subject to `resolve()`. Their `core` methods call the SDK factories (`msb.Image`, `msb.Snapshot`) or `msb.*Volume` directly and map `GetX`-miss to `ErrNotFound`.
- **Host-path operations touch the daemon's filesystem.** `files/copy-from-host`, `files/copy-to-host`, and `snapshots/export|import` read/write paths on the msbd host, not the guest. There's no allowlist enforced server-side yet — front them with auth and trust the caller.
- **`/docs` and `/openapi.yaml` are unauthenticated.** They're registered without `s.auth(...)` only when `SetOpenAPI` was given a non-empty spec. The embedded `OpenAPISpec` is the same `openapi.yaml` at the module root.
- **The terminal rides a reverse-engineered wire protocol.** `internal/core/terminal_agent.go` hand-encodes the microsandbox agent protocol (CBOR over `AgentClient`), whose schema is NOT a public SDK API — the `wireMessage`/`wireExec*` structs and `mtExec*`/`protocolVersion` constants mirror upstream `crates/protocol/lib`. The format is pinned to the SDK version (the embedded FFI and downloaded `msb` runtime both track it), so it can't drift at runtime, but a deliberate SDK bump can change it. `TestPinnedSDKVersion` (in `terminal_agent_test.go`) fails on any SDK version change: re-verify the constants/structs against the new protocol crate, confirm the terminal works end-to-end, then bump `verifiedSDKVersion`.

## Tests

- `go test ./...` from the repo root. CI runs `go test -race ./...`.
- Integration tests that actually boot a microVM need `/dev/kvm` and are not run in CI by default — gate them behind `-tags integration` if you add them.

## Lint & toolchain

- Go **1.26** (the `go` directive in `go.mod`, the `golang:1.26` build image, and CI's `go-version` all track this — bump them together).
- `task lint` runs `golangci-lint run ./...` + `go vet ./...`. Config is `.golangci.yml` (golangci-lint v2): `errcheck`, `govet`, `ineffassign`, `modernize`, `staticcheck`, `unused`, with the `gofmt` formatter. `modernize` rewrites old idioms to current Go — run `task fmt` / `gofmt -w .` to apply formatting.
- Build output goes to `./bin/` (gitignored). Use `task build`; never commit binaries.

## Releasing

Bump the `VERSION` file to `X.Y.Z`, commit, then tag a commit `vX.Y.Z` and push — or just run `task release:push NEW_VERSION=X.Y.Z`, which does the bump+commit+tag+push atomically. GoReleaser builds linux/amd64 + linux/arm64 binaries, multi-arch Docker images pushed to `ghcr.io/mark3labs/msbd`, and a GitHub release with the rendered changelog. See `Taskfile.yml`, `.github/workflows/release.yml` and `.goreleaser.yaml`.

The tag is the source of truth for the version. `cmd/msbd/main.go` declares `version`/`commit`/`date` package vars; GoReleaser injects them from the tag via `-ldflags -X main.*`. The Nix flake reads the version from the `VERSION` file (flakes can't see git tags) and `commit`/`date` from flake metadata. A CI guard fails the release if `v$(cat VERSION)` doesn't match the pushed tag, so both build paths report the same number.

CGO is enabled in the release build because the SDK is cgo. Cross-compilation across CPU architectures uses native runners (one job per arch) so we don't have to chase a cross-compiling C toolchain.

## See also

- Upstream: [`microsandbox`](https://github.com/superradcompany/microsandbox) (the runtime + Go SDK we wrap).
- Spec: [`openapi.yaml`](./openapi.yaml).
- Deploy: [`Dockerfile`](./Dockerfile), [`docker-compose.yml`](./docker-compose.yml), [`flake.nix`](./flake.nix) (Nix package + NixOS module).
