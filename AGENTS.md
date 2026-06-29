# AGENTS.md

## Project overview

**msbd** is a small Go HTTP server that wraps the [microsandbox](https://github.com/superradcompany/microsandbox) Go SDK (`github.com/superradcompany/microsandbox/sdk/go`) and exposes a REST API for managing fast, local microVMs. It exists so that long-running applications can drive microsandbox without linking libkrun / cgo themselves: msbd quarantines all of that to one binary on one KVM-equipped host, and everything else talks plain HTTP.

Module path: `github.com/mark3labs/msbd`.

## How it's wired up

```
cmd/msbd/main.go         entrypoint: loadConfig → EnsureInstalled →
                         core.NewService → svc.Reconcile → api.NewServer → ListenAndServe

internal/core/           SDK-facing business logic. EVERY call to the
                         microsandbox SDK happens here (and only here).
                         The api/ package never imports the SDK.

internal/api/            HTTP surface. Routes, middleware (bearer auth,
                         panic recover, request log), DTOs that mirror
                         the value types in core/.

openapi.yaml             the wire contract. Source of truth for client
                         generators and reviewers.
```

The two-package split (`api` ↔ `core`) is the boundary that keeps DTO churn from leaking into business logic and vice versa.

## Layout

- **`cmd/msbd/main.go`** — flag/env parse, `msb.EnsureInstalled` (downloads `msb` + `libkrunfw` into `~/.microsandbox/` on first run), startup reconcile, HTTP serve. Also defines the `/readyz` probe (FFI loaded + `/dev/kvm` openable r/w).
- **`internal/core/service.go`** — `Service` is the single owner of all SDK calls: lifecycle (`Create`/`Get`/`List`/`Stop`/`Start`/`Delete`), exec (`Exec`/`Run`), jobs (`Launch`/`Poll`), file IO (`ReadFile`/`WriteFile`). Provider-neutral input/output types (`CreateParams`, `Instance`, `ExecParams`, `ExecResult`).
- **`internal/core/registry.go`** — `Registry` is the in-process cache: name → live `*msb.Sandbox` handle, name → first-seen time (uptime), name → resolved native workdir. `resolve()` is the single choke point that folds **transparent resume** and **reconnect-after-restart** into every exec/run/file path. `Reconcile()` re-attaches to pre-existing VMs at boot.
- **`internal/core/jobs.go`** — `JobRegistry` backs the async API. `launch` starts an `sb.ShellStream` and a drain goroutine that consumes `ExecHandle.Recv` events into per-job stdout/stderr ring buffers and records the exit code. In-memory only — jobs poll as `gone` after a msbd restart.
- **`internal/core/version.go`** — `RuntimeVersion()` / `SDKVersion()` shims for diagnostics.
- **`internal/api/router.go`** — stdlib `http.ServeMux` (Go 1.22+ pattern matching), bearer-auth middleware, panic recover, request logger. `SetPrebaked(bool)` toggles the `prebaked_image` flag reported in `/v1/capabilities`.
- **`internal/api/handlers.go`** — one handler per endpoint, each a near-1:1 DTO ⇄ `core` translation.
- **`internal/api/dto.go`** — the JSON wire shapes. **Keep in lockstep with `openapi.yaml` and downstream clients.**

## Adding a new endpoint

1. Add (or reuse) DTOs in `internal/api/dto.go`. Tags: `json:"..."` — no `omitempty` on input fields that should appear in the schema.
2. Add the business method to `internal/core/service.go`. Keep all SDK calls inside `core`.
3. Add the handler in `internal/api/handlers.go`. Pattern: `decode → svc.X → encode | notFoundOr`.
4. Wire the route in `internal/api/router.go` under the appropriate verb/path. Apply `s.auth(...)` unless the endpoint is health-only.
5. Document it in `openapi.yaml` — schemas under `components/schemas`, response examples, error envelopes.
6. Update the table in `README.md` if it's user-visible.

## Conventions & gotchas

- **The `api` package never imports the microsandbox SDK.** All `github.com/superradcompany/microsandbox/sdk/go` references stay in `internal/core/`. This is the cgo isolation boundary — if you find yourself reaching for `msb.X` from a handler, lift it into `core` first.
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

## Tests

- `go test ./...` from the repo root. CI runs `go test -race ./...`.
- Integration tests that actually boot a microVM need `/dev/kvm` and are not run in CI by default — gate them behind `-tags integration` if you add them.

## Releasing

Bump the `VERSION` file to `X.Y.Z`, commit, then tag a commit `vX.Y.Z` and push — or just run `task release:push NEW_VERSION=X.Y.Z`, which does the bump+commit+tag+push atomically. GoReleaser builds linux/amd64 + linux/arm64 binaries, multi-arch Docker images pushed to `ghcr.io/mark3labs/msbd`, and a GitHub release with the rendered changelog. See `Taskfile.yml`, `.github/workflows/release.yml` and `.goreleaser.yaml`.

The tag is the source of truth for the version. `cmd/msbd/main.go` declares `version`/`commit`/`date` package vars; GoReleaser injects them from the tag via `-ldflags -X main.*`. The Nix flake reads the version from the `VERSION` file (flakes can't see git tags) and `commit`/`date` from flake metadata. A CI guard fails the release if `v$(cat VERSION)` doesn't match the pushed tag, so both build paths report the same number.

CGO is enabled in the release build because the SDK is cgo. Cross-compilation across CPU architectures uses native runners (one job per arch) so we don't have to chase a cross-compiling C toolchain.

## See also

- Upstream: [`microsandbox`](https://github.com/superradcompany/microsandbox) (the runtime + Go SDK we wrap).
- Spec: [`openapi.yaml`](./openapi.yaml).
- Deploy: [`Dockerfile`](./Dockerfile), [`docker-compose.yml`](./docker-compose.yml), [`flake.nix`](./flake.nix) (Nix package + NixOS module).
