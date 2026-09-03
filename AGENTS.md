# AGENTS.md

## Project overview

**msbd** is a small Go HTTP server that wraps the [microsandbox](https://github.com/superradcompany/microsandbox) Go SDK (`github.com/superradcompany/microsandbox/sdk/go`) and exposes a REST API for managing fast, local microVMs. It exists so that long-running applications can drive microsandbox without linking libkrun / cgo themselves: msbd quarantines all of that to one binary on one KVM-equipped host, and everything else talks plain HTTP.

Module path: `github.com/mark3labs/msbd`.

## How it's wired up

```
cmd/msbd/main.go         entrypoint: fang/cobra CLI → serve cmd → loadConfig →
                         store.Open → EnsureInstalled → core.NewService →
                         svc.Reconcile → api.NewServer → ListenAndServe
                         (graceful drain on signal)

cmd/msbd/admin.go        the `msbd users` / `msbd keys` / `msbd db` command
                         trees — same store file the daemon reads.

internal/core/           SDK-facing business logic. EVERY call to the
                         microsandbox SDK happens here (and only here).
                         The api/ package never imports the SDK.

internal/store/          persisted state: dashboard users, API keys, sessions.
                         The ONLY package that speaks SQL. Knows nothing about
                         HTTP or the SDK.

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

- **`cmd/msbd/main.go`** — cobra CLI styled with `charmbracelet/fang`. The root command defaults to (and also exposes) a `serve` subcommand whose flags mirror the `MSBD_*` env vars (flag › env › `.env` › default — `godotenv.Load()` runs in `main()` before cobra so a `.env` in the working directory seeds the env, with shell/Docker vars still winning). `serve` does `msb.EnsureInstalled` (downloads `msb` + `libkrunfw` into `~/.microsandbox/` on first run), startup reconcile, then HTTP serve with graceful shutdown on Ctrl-C / SIGTERM. Also defines the `/readyz` probe (FFI loaded + `/dev/kvm` openable r/w).
- **`assets.go`** (module root) — `//go:embed openapi.yaml` into `OpenAPISpec`. Lives at the root because `go:embed` can't reference a parent directory from `internal/api`. `main.go` hands the bytes to `Server.SetOpenAPI`.
- **`internal/core/service.go`** — `Service` is the single owner of all SDK calls: lifecycle (`Create`/`Get`/`Inspect`/`List`/`Stop`/`Start`/`Delete`), exec (`Exec`/`Run`), jobs (`Launch`/`Poll` + `WriteJobStdin`/`CloseJobStdin`/`SignalJob`), file IO (`ReadFile`/`WriteFile`). Provider-neutral input/output types (`CreateParams`, `Instance`, `ExecParams`, `ExecResult`).
- **`internal/core/terminal.go`** — interactive terminal sessions (`OpenTerminal`). Returns a transport-agnostic `Session` interface (`Output`/`Write`/`Resize`/`Signal`/`Close`/`Wait`); goes through `resolve()` like `Run`, then hands off to the agent-PTY backend. In-memory only.
- **`internal/core/terminal_agent.go`** — the **real kernel-PTY** backend. Drives the microsandbox agent protocol directly (`ConnectAgentSandbox` + `AgentClient.Stream`/`Send`/`Next`) with hand-rolled CBOR frames (`fxamacker/cbor`), replicating what the SDK's `Attach` does but sourcing stdin from the WebSocket instead of a local TTY. Sends `core.exec.request{tty:true,rows,cols}` and relays `core.exec.stdin`/`resize`/`signal` ↔ `core.exec.stdout`/`stderr`/`exited`. **The wire schema (protocol generation 6 as of SDK 0.6.7) is reverse-engineered from upstream Rust, NOT a public SDK API** — a microsandbox protocol bump can break this file. Constants in this file mirror `crates/protocol/lib`; `TestPinnedSDKVersion` fails the build on any SDK change so the wire format is re-verified before `verifiedSDKVersion` is bumped (the file carries a per-version verification log).
- **`internal/core/fs.go`** — extended filesystem ops over `sb.FS()`: `ListDir`/`Stat`/`Exists`/`Mkdir`/`Remove`/`Copy`/`Rename` plus host transfer (`CopyFromHost`/`CopyToHost`). All route through `resolve()`.
- **`internal/core/metrics.go`** — `Metrics(id)` and `AllMetrics()` point-in-time resource snapshots.
- **`internal/core/logs.go`** — `Logs(id, LogQuery)` reads persisted stdout/stderr/output/system logs with tail + source filters.
- **`internal/core/volume.go`** — named persistent volumes (`CreateVolume`/`ListVolumes`/`GetVolume`/`RemoveVolume`) and volume file IO. Volumes are independent of sandboxes (not cached in `Registry`); mount them at create via `CreateParams.Mounts`.
- **`internal/core/image.go`** — cached OCI image inventory (`ListImages`/`InspectImage`/`RemoveImage`/`PruneImages`) over the SDK `msb.Image` factory.
- **`internal/core/snapshot.go`** — sandbox rootfs snapshots over the `msb.Snapshot` factory (`Create`/`List`/`Get`/`Verify`/`Remove`/`Export`/`Import`/`Reindex`).
- **`internal/core/registry.go`** — `Registry` is the in-process cache: name → live `*msb.Sandbox` handle, name → first-seen time (uptime), name → resolved native workdir. `resolve()` is the single choke point that folds **transparent resume** and **reconnect-after-restart** into every exec/run/file path. `Reconcile()` re-attaches to pre-existing VMs at boot.
- **`internal/core/jobs.go`** — `JobRegistry` backs the async API. `launch` starts an `sb.ShellStream` and a drain goroutine that consumes `ExecHandle.Recv` events into per-job stdout/stderr **bounded ring buffers** (default 1 MiB/stream; overflow reported via `JobStatus.Truncated`/`*Bytes`) and records the exit code. All `*ExecHandle` calls are serialized behind a per-job mutex (the SDK handle is not goroutine-safe) and a per-job context lets `Close`/`dropSandbox` cancel a `Recv` wedged on a hung guest. A TTL janitor evicts finished jobs. Optionally opens a stdin pipe (`ExecParams.Stdin`) for `writeStdin`/`closeStdin`/`signal`. In-memory only — jobs poll as `gone` after a msbd restart. **To terminate a job use `Service.CancelJob` (`cancelJob`), not `SignalJob`** — see the deadlock note under Conventions.
- **`internal/core/version.go`** — `RuntimeVersion()` / `SDKVersion()` shims for diagnostics.
- **`internal/store/`** — the persisted auth state, on **SQLite via `modernc.org/sqlite` (pure Go — do NOT swap in `mattn/go-sqlite3`, that would add a second cgo chain to the one binary whose whole job is quarantining cgo)**. `store.go` owns `Open` + the embedded `schema/*.sql` migrations (append-only; never edit a shipped file). `users.go` = bcrypt accounts + `admin`/`viewer` roles. `apikeys.go` = bearer tokens stored as **sha256, never bcrypt** (the token is already 256 bits of CSPRNG, so a KDF would only tax every request) and shown exactly once. `sessions.go` = opaque server-side dashboard sessions. `cache.go` = the `KeyCache` TTL memo in front of verification, including **negative** results. Path resolution is `path.go`: `$MSBD_DATA_DIR` else `~/.microsandbox/msbd`, i.e. inside the directory the Dockerfile `VOLUME`, compose and the NixOS `StateDirectory` already persist.
- **`internal/api/router.go`** — stdlib `http.ServeMux` (Go 1.22+ pattern matching), bearer-auth middleware, panic recover, request logger. `SetOpenAPI([]byte)` enables `/docs` + `/openapi.yaml`. **Every REST route lives under `/api/v1`** — see the URL-space map under Conventions.
- **`internal/api/handlers.go`** — handlers for the core lifecycle/exec/jobs/files surface, each a near-1:1 DTO ⇄ `core` translation.
- **`internal/api/handlers_ext.go`** — handlers for the extended surface: inspect, metrics, logs, extended filesystem, job stdin/signal, volumes, images, snapshots. Same `decode → svc.X → encode | notFoundOr` shape.
- **`internal/api/terminal.go`** — the `GET /api/v1/sandboxes/{id}/terminal` WebSocket handler. Opens a `core.Session` BEFORE upgrading (so an unknown sandbox surfaces as a clean `404`, not a flapping socket), then splices the WebSocket ↔ `Session`: binary frames = stdin/stdout bytes, text frames = JSON control (`resize`/`signal`) in / events (`exit`) out. Uses `github.com/gorilla/websocket`. Auth via `authWS` (header or `?key=`). The guest PTY emits canonical CRLF, so output passes through verbatim.
- **`internal/api/docs.go`** — `/docs` Swagger UI page (CDN assets) + `/openapi.yaml` raw spec. Both are unauthenticated (the spec is not a secret).
- **`internal/dashboard/auth.go`** — the dashboard's three auth modes, resolved **per request** (so the CLI can change things under a running server): `modeOpen` (no accounts, no Basic creds), `modeBasic` (legacy `--dashboard-user`/`--dashboard-pass`), `modeSession` (≥1 stored account → login page + `HttpOnly`/`SameSite=Lax` cookie scoped to `/`). Session mode **wins** over basic. Exposes the four guards `guardPage` (redirect to login), `guardAPI` (401 — never a redirect, which Datastar would patch into the page), `guardWrite` (guardAPI + admin role) and `guardForm` (no auth, cross-origin check only — for the login/logout POSTs).
- **`internal/dashboard/`** — the optional web UI mounted at the **root** (`/`). `dashboard.go` registers all routes on the api mux (via `Server.SetDashboard`) and applies its own auth (see `auth.go`: login sessions, or legacy Basic, or open). It talks to `core.Service` and `store.Store` — like `api`, it never imports the SDK. **Routing model: every section is a real page at its own top-level URL** (`/` overview, `/sandboxes`, `/sandboxes/{id}`, `/volumes`, `/images`, `/snapshots`, `/settings/keys`, `/settings/users`), rendered server-side by the page handlers in `handlers.go` via `views.Page(meta, body)`; the `/ui/*` endpoints are SSE fragments/actions for in-page updates only. The overview is registered as `GET /{$}` (exact match) so the dashboard is NOT a catch-all that swallows 404s for mistyped API routes. Pages/fragments are [templ](https://templ.guide) (`views/*.templ`) built from vendored [templui](https://templui.io) components (`components/`, added by the `templui` CLI), styled with Tailwind, and driven by [Datastar](https://data-star.dev): handlers (`handlers_*.go`) are SSE endpoints that `PatchElementTempl` fragments into the page. `views/models.go` holds display-only view structs plus the shared formatting/escaping helpers. All static assets — the **committed** `assets/css/output.css`, the vendored Datastar + xterm runtimes, the templui component JS and `favicon.svg` — are `//go:embed`'d (`embed.go`) and served from the embedded FS, so a plain `go build` is self-contained. The terminal page (`views/terminal.templ`) is a standalone xterm.js page that bridges to the existing `/api/v1/sandboxes/{id}/terminal` WebSocket (using a short-lived ticket, not the API key); `?embed=1` trims its chrome so the sandbox detail page can iframe it as a Terminal tab.
- **`internal/api/dto.go`** — the JSON wire shapes. **Keep in lockstep with `openapi.yaml` and downstream clients.**

## Adding a new endpoint

1. Add (or reuse) DTOs in `internal/api/dto.go`. Tags: `json:"..."` — no `omitempty` on input fields that should appear in the schema.
2. Add the business method to `internal/core/`. Lifecycle/exec/jobs/file-IO go in `service.go`; otherwise use (or add) the topical file — `fs.go`, `metrics.go`, `logs.go`, `volume.go`, `image.go`, `snapshot.go`. Keep all SDK calls inside `core`.
3. Add the handler in `internal/api/handlers.go` (core surface) or `internal/api/handlers_ext.go` (everything else). Pattern: `decode → svc.X → encode | notFoundOr`.
4. Wire the route in `internal/api/router.go` under the appropriate verb/path, **prefixed `/api/v1`**. Apply `s.auth(...)` unless the endpoint is health- or docs-only. For a WebSocket upgrade endpoint use `s.authWS(...)` instead — it also accepts the bearer token as a `?key=` query param, since browsers can't set headers on a WS handshake.
5. Document it in `openapi.yaml` — schemas under `components/schemas`, response examples, error envelopes. The spec is embedded, so a rebuild reflects it at `/docs`.
6. Update the endpoint table in `README.md` if it's user-visible.

## The URL space

One mux serves three tenants, and the prefixes are what keep them from
colliding. Do not add a route that blurs them:

| Prefix | Owner | Auth |
|---|---|---|
| `/api/v1/…` | the REST API (`internal/api`) | bearer token (`s.auth` / `s.authWS`) |
| `/ui/…` | the dashboard's Datastar SSE fragments + actions | dashboard session (`guardAPI` / `guardWrite`) |
| `/assets/…` | embedded dashboard CSS/JS/favicon | none, on purpose |
| `/login`, `/logout` | dashboard sign-in | none, by necessity |
| `/healthz`, `/readyz`, `/metrics`, `/docs`, `/openapi.yaml` | ops + spec | none except `/metrics` (bearer) |
| everything else at the root (`/`, `/sandboxes`, `/sandboxes/{id}`, `/volumes`, `/images`, `/snapshots`, `/settings/*`, `/terminal/{id}`) | dashboard pages | dashboard session (`guardPage` / `guardAdminPage`) |

The dashboard owns the root, so **a new top-level dashboard page permanently
claims that path name** for the whole server — check it against the table above
before adding one. The overview is `GET /{$}` (exact match), never a bare `GET
/`, so an unknown path still 404s instead of silently rendering the overview.

Routing is plain `net/http` on Go 1.26 — no third-party router, and the modern
stdlib affordances are used rather than reimplemented:

- **`GET /{$}`** — exact-match root. A bare `"/"` pattern is a catch-all that
  matches every unmatched request and flattens ServeMux's automatic `405 Method
  Not Allowed` into a `404` across the whole API. `TestNoCatchAllRoute` guards it.
- **`r.Pattern`** (Go 1.23) — the matched route template. The request logger
  emits it as `route=` beside the concrete `path=`, which is what makes route
  shadowing diagnosable now that two packages register on one mux. It is set by
  the mux on the same `*Request`, so outer middleware reads it after
  `next.ServeHTTP` returns; `(unmatched)` means the mux matched nothing.
- **`mux.Handler(r)`** — returns the pattern that *would* match, without
  serving. `TestDashboardNeverShadowsTheAPI` mounts the dashboard alone and
  asserts it claims no `/api/…` path.
- **`http.CrossOriginProtection`** (Go 1.25) — the dashboard's CSRF defence,
  see the note under Conventions.
- **`http.FileServerFS`** — serves the embedded asset FS directly, no
  `http.FS()` adapter.

## Conventions & gotchas

- **The `api` package never imports the microsandbox SDK.** All `github.com/superradcompany/microsandbox/sdk/go` references stay in `internal/core/`. This is the cgo isolation boundary — if you find yourself reaching for `msb.X` from a handler, lift it into `core` first. (The terminal handler honors this: it speaks only to the `core.Session` interface, never an `msb.ExecHandle`.)
- **Always `WithDetached()`.** Sandboxes MUST be created detached so they survive an msbd restart. The detached → reconnect-by-name dance is the whole point of the daemon.
- **Sandbox names ARE the provider id.** Server-generated as `sbx_<16hex>` in `core.newName()`. Names are limited to 128 UTF-8 bytes by the SDK.
- **`resolve()` is the choke point.** Don't grab a `*msb.Sandbox` directly from the registry cache map — always go through `Registry.resolve(ctx, name)` so reconnect + transparent resume work uniformly. Bypassing it leaks "no handle after restart" bugs.
- **`Run` is long-safe; `Exec` is not.** `Exec` is the fast path for one-shot provisioning helpers and intentionally does NOT ensure-running. `Run` blocks until completion and resumes a paused box first. Put no low-timeout proxy in front of `/run`.
- **`Delete` stops before remove.** The SDK's `RemoveSandbox` refuses a running box; `core.Service.Delete` does a best-effort `Stop` first.
- **Workdir resolution.** Create runs `pwd` in the booted guest and caches the result so `Instance.Workdir` reflects the image's real `WORKDIR` (e.g. `/workspace` for the kit image) instead of the SDK's `cfg.Workdir`, which only contains an explicitly-pinned value.
- **glibc, not musl.** The SDK's embedded FFI and the downloaded `msb` supervisor link against glibc ≥ 2.28. The Dockerfile uses `debian:bookworm-slim` and apt-installs `libcap-ng0` because the prebuilt supervisor links it.
- **The dashboard locks itself rather than becoming a bypass.** If the REST API requires a token but the dashboard has no auth of its own, `Handler.locked` refuses every route with a page explaining the fix. It is evaluated PER REQUEST, not at boot, so `msbd users add` unlocks a running server — deciding it once in `runServe` would make that fix require a restart.
- **Never signal a *running* job — cancel it.** A running job's drain goroutine is parked in `ExecHandle.Recv` on the same SDK stream that `Signal`/`Kill` use, so a "live" `Signal` waits on an ack the parked `Recv` already swallowed and **blocks until the command finishes on its own** (pinning the HTTP request open for the whole duration). `JobRegistry.cancelJob` does it correctly: cancel the job context first (unblocking `Recv`), *then* take `handleMu` and `Kill` — the sequence `dropSandbox` already used. `signal()` delegates `sig <= 0` to it and bounds any real signal with `jobSignalTimeout`. A cancelled job is marked `JobKilled`, because the runtime reports a killed process as a plain **exit 0** — without that state, "user cancelled" is indistinguishable from "succeeded". Regression tests live in `internal/core/jobs_cancel_test.go`.
- **Errors flow through `notFoundOr`.** `core.ErrNotFound` → 404; anything else → 500 (or 507 from `Create` when capacity is hit). Always return a typed error from `core`, never an HTTP status.
- **No `omitempty` on REST inputs.** It drops fields from the OpenAPI schema and breaks generated clients.
- **DTO names are stable.** They're the wire contract — renaming a JSON field is a breaking change for every downstream client. Use a new field, deprecate, then remove.
- **Volumes / images / snapshots aren't sandboxes.** They're standalone resources keyed by name/reference, not cached in `Registry` and not subject to `resolve()`. Their `core` methods call the SDK factories (`msb.Image`, `msb.Snapshot`) or `msb.*Volume` directly and map `GetX`-miss to `ErrNotFound`.
- **Host-path operations touch the daemon's filesystem.** `files/copy-from-host`, `files/copy-to-host`, and `snapshots/export|import` read/write paths on the msbd host, not the guest. They are gated by an **allowlist** (`--host-paths` / `MSBD_HOST_PATHS`) enforced in `Service.checkHostPath` (Abs + EvalSymlinks + prefix match; empty allowlist denies everything with `ErrForbidden` → 403). Still, front them with auth and trust the caller.
- **Auth is additive, never a migration.** Static `--api-key` / `MSBD_API_KEY` values and store-backed keys are BOTH accepted (`Server.tokenOK` checks the static list first, without a DB round trip, then `KeyCache`). Same for the dashboard: `--dashboard-user`/`--dashboard-pass` keep working until a real account exists. Never make an existing config stop working.
- **`authRequired` is dynamic and fails closed.** Whether the API needs a token is re-evaluated per request (`KeyCache.AnyActive`, 5s TTL) so `msbd keys create` in another process takes effect without a restart. If the store can't be read, `AnyActive` returns **true** — a database hiccup must never silently un-authenticate the API.
- **Never bcrypt an API key; never sha256 a password.** Tokens are full-entropy random (sha256 is enough, and a KDF would tax every request); passwords are low-entropy human input (bcrypt cost 12, and `hashCost()` drops to `bcrypt.MinCost` under `testing.Testing()` so CI isn't a minute of pure KDF).
- **The dashboard's guards are not interchangeable.** `guardPage` redirects (a 401 body strands someone who simply hasn't signed in); `guardAPI` returns 401 (Datastar would follow a redirect and patch the login page into whatever element it was updating); `guardWrite` adds the admin check; `guardForm` is the odd one out — no authentication at all, just the cross-origin check, and it belongs ONLY on login/logout. A read expressed as a POST (`files`, `files/view`) is still a read — use `guardAPI`, or viewers lose the file browser.
- **Role checks are server-side.** Hiding a button for a viewer is a courtesy; `guardWrite` is the enforcement. `TestViewerCannotMutate` asserts the 403s.
- **CSRF is `http.CrossOriginProtection`, not a hand-rolled Origin check.** `internal/dashboard/auth.go` holds one package-level `crossOrigin` value; `guardAPI` calls `crossOrigin.Check(r)`, and so does `guardForm`, which exists solely to wrap the two UNAUTHENTICATED state-changing routes (`POST /login`, `POST /logout`) — they have no session to guard, so they would otherwise be the only unprotected mutations on the server (login-CSRF signs a victim into an *attacker's* account; logout-CSRF is drive-by sign-out). `SameSite=Lax` does not cover either: a login POST carries no cookie. It checks `Sec-Fetch-Site` first (all browsers since 2023) and falls back to Origin-vs-Host; the hand-rolled predicate it replaced compared Origin to Host ONLY, so a cross-site mutation that simply omitted `Origin` sailed through. Requests carrying neither header (curl, non-browser clients) are still allowed by design — the `/ui/*` endpoints get scripted against. It is deliberately NOT applied to `/api/v1`: bearer tokens are not ambient browser credentials, so there is no CSRF exposure there, and gating it would break legitimate cross-origin API clients. `TestCrossOriginMutationRefused` covers the `Sec-Fetch-Site` cases that the old check failed.
- **The session cookie is scoped to `/`, and that is safe only because the REST API ignores cookies.** The dashboard spans the root, so the cookie cannot be path-scoped away from `/api/v1` the way it once was from `/dashboard`. `Server.auth`/`authWS` read the `Authorization` header (plus `?key=`/`?ticket=` for the WS upgrade) and never a cookie, so the cookie is not an ambient credential for the API. If you ever teach the API to read a cookie, you have created a CSRF hole — don't.
- **Dashboard static assets are UNAUTHENTICATED on purpose** — the login page needs its stylesheet before anyone has signed in, and they're embedded, immutable CSS/JS.
- **Never delete or demote the last admin.** `store.ErrLastAdmin` guards it in the store; the users table also hides the buttons so the user isn't offered a click that only errors.
- **`/docs` and `/openapi.yaml` are unauthenticated.** They're registered without `s.auth(...)` only when `SetOpenAPI` was given a non-empty spec. The embedded `OpenAPISpec` is the same `openapi.yaml` at the module root.
- **The terminal rides a reverse-engineered wire protocol.** `internal/core/terminal_agent.go` hand-encodes the microsandbox agent protocol (CBOR over `AgentClient`), whose schema is NOT a public SDK API — the `wireMessage`/`wireExec*` structs and `mtExec*`/`protocolVersion` constants mirror upstream `crates/protocol/lib`. The format is pinned to the SDK version (the embedded FFI and downloaded `msb` runtime both track it), so it can't drift at runtime, but a deliberate SDK bump can change it. `TestPinnedSDKVersion` (in `terminal_agent_test.go`) fails on any SDK version change: re-verify the constants/structs against the new protocol crate, confirm the terminal works end-to-end, then bump `verifiedSDKVersion`.

## Working on the dashboard

The dashboard (`internal/dashboard/`) has a **codegen + asset build step** that the rest of the project doesn't. The generated `*_templ.go` and the compiled `assets/css/output.css` are **committed** so CI / goreleaser / `go build` need no Node or templ toolchain — but if you edit any `.templ` or add Tailwind classes you MUST regenerate and re-commit:

```
task dashboard          # templ generate + tailwindcss --minify (run after editing .templ)
task dashboard:watch    # live-rebuild during development
```

### Adding a page vs. a fragment

- **A new section** = a real page: add a `pageX` handler in `handlers.go` that calls `h.render(w, r, views.SectionX, "Title", "", views.XPage(...))`, register `GET /x` **with the right guard** (`h.guardPage` normally, `h.guardAdminPage` for anything under Settings), add a `Section` const + a `navItem` in `views/layout.templ`. Users expect to bookmark it.
- **A new in-page update** = an SSE fragment: add the handler under `/ui/...` behind `h.guardAPI` (read) or `h.guardWrite` (mutation), and have it `PatchElementTempl` a fragment whose root `id` matches the element already on the page.
- Tables follow a fixed shape: `XPage` (header + filters + card) wraps `XTable` (the refreshable fragment, `id="x-table"`). Sorting is server-side via `?sort=&dir=` + `parseSort`/`sortRows`; text filtering is client-side via the `q` signal and per-row `data-show`.

Gotchas:
- **templ steals `for` (and `if`/`switch`) at the start of a text node.** `<code>X</code> for legacy auth` fails to parse with a baffling `expected nodes, but none were found` pointing at the *enclosing* component. Reword, or wrap the word in an element. Same trap for a line that begins with `if`.
- **Datastar v1 attribute syntax.** Event handlers use a colon: `data-on:click`, `data-on:submit`. Run-once-on-load is `data-init` (NOT `data-on-load`). Polling is `data-on-interval.5s` (hyphenated plugin name, `.Ns` duration — NOT `__duration`). Signals via `data-signals` / `data-bind`; keep signal names **all-lowercase** to dodge the `data-signals-*` attribute-lowercasing vs `data-bind` camelCase round-trip trap.
- **Mutating actions must opt out of retry.** Datastar retries a dropped fetch by default, and `@post`/`@delete` here are NOT idempotent — a retried create boots a *second* microVM, a retried run re-executes the command. Always build them with the `dsPost`/`dsDelete` helpers in `views/models.go` (they append `{retry:'never'}`); `TestMutatingActionsDisableRetry` fails the build if a new call site forgets. `@get` fragments are reads and stay retryable.
- **`data-indicator` must sit on the element that STARTS the fetch.** Putting it on a wrapper `<div>` silently never fires. For a form submitted by both Enter and a click, put it on the `<form>` *and* the button. Without it the busy/disabled state never engages and users double-submit.
- **Dialogs:** templui dialogs are driven from JS via the component's public `window.tui.dialog.open/close(id)` API (`openDialogJS`/`closeDialogJS`), not by synthesising clicks on hidden triggers. Dialogs that are *patched in* over SSE (the file viewer) are plain `<dialog>` elements driven with `showModal()`/`close()` (`openNativeJS`/`closeNativeJS`).
- **Never `window.confirm`.** Destructive actions arm the shared `ConfirmDialog` via `confirmAction(title, body, label, action)`, which stashes the action as a thunk on `window` and passes the resource name through a **text-only** signal (`data-text`), so guest-controlled names can never become executable markup. `TestNoNativeConfirm` enforces this.
- **Long-running SSE streams break `networkidle`.** The sandbox detail page holds an open metrics stream by design, so browser automation must wait on `domcontentloaded` + an explicit condition, never `networkidle`.
- **Adding a templui component:** `templui add <name>` (config in `.templui.json` points at `internal/dashboard/...`). The CLI also drops the component's JS into `assets/js/` — load it from `views/layout.templ` with `@<comp>.Script()`. The vendored `utils/templui.go` was trimmed to drop the upstream `templui/components` import (we serve JS ourselves); don't re-add `SetupScriptRoutes`.
- **`utils.TwMerge` must stay pointed at `internal/dashboard/twmerge`.** Upstream templui calls tailwind-merge-go's package-global `twmerge.Merge`, which is **not goroutine-safe** — its lazy init writes captured vars unsynchronized and its default LRU mutates one linked list under two different mutexes. templ renders on per-request goroutines, so that global raced under `go test -race` (it broke CI once). `internal/dashboard/twmerge` wraps the same library in a single instance built under a `sync.Once` with a properly locked cache. A `templui` upgrade will overwrite `utils/templui.go` and re-import the global — re-apply the one-line delegation (there's a NOTE in the file). `TestMergeConcurrent` and `TestConcurrentRendersAreRaceFree` catch a regression.
- **Lint exclusions.** `internal/dashboard/components` and `internal/dashboard/utils` are vendored generated code and are excluded in `.golangci.yml`. Your own dashboard `.go` files are NOT excluded — keep them gofmt/modernize-clean.
- **Never import the SDK from `internal/dashboard`** — same cgo-isolation rule as `api`. Go through `core.Service`.

## Tests

- `go test ./...` from the repo root. CI runs `go test -race ./...`.
- `internal/store` tests run against `store.MemoryPath` (`:memory:`), which forces `MaxOpenConns(1)` — an in-memory SQLite database lives inside a single connection, so a larger pool would hand out separate empty databases.
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
