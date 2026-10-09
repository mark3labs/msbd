# Dashboard browser regression tests

Runs **real headless Chromium through Playwright**, not a DOM simulator. No repo
`package.json`, npm lockfile, or browser download is needed.

## Setup and run

Requires Node (with built-in `fetch`), npm, Go/cgo build prerequisites, and
`chromium` on PATH. Set `CHROMIUM=/absolute/path/to/chromium` to override.

```sh
DEPS=$(mktemp -d)
npm install --prefix "$DEPS" --no-audit --no-fund playwright@1.64.0
PLAYWRIGHT_MODULE="$DEPS/node_modules/playwright" \
  node scripts/browser/dashboard.cjs
```

The script builds `bin/browser/msbd`, creates a fresh temporary HOME,
`MSB_HOME` and `MSBD_DATA_DIR`, seeds an admin with a randomly generated password
via stdin, starts a daemon bound to **127.0.0.1 on an ephemeral port**, and launches
Chromium. It deliberately does **not** inherit `MSBD_*` environment variables or
run the daemon/CLI from the checkout, so the checkout's `.env` is never loaded.
The isolated static REST key is generated in memory, not printed or persisted.
The microsandbox SDK provisions a runtime in that fresh HOME when necessary.

An existing, trusted **SDK-matching** runtime can speed this up:

```sh
PLAYWRIGHT_MODULE="$DEPS/node_modules/playwright" \
MSBD_BROWSER_RUNTIME_SEED=/path/to/isolated/runtime \
  node scripts/browser/dashboard.cjs
```

Only `bin/` and `lib/` are copied from the seed. Runtime database, cached images,
sandboxes, and existing daemon state are **never** reused. The seed itself is not
modified. Use a runtime that matches `go.mod`'s SDK version (currently 0.7.7).
Runtime copying assumes a normal self-contained SDK installation; do not pass a
runtime whose executable/library symlinks point to production installations.

## Optional real microVM coverage

Requires accessible `/dev/kvm`, matching runtime, and outbound network access for
an OCI image pull. The default smoke image is `microsandbox/python`.

```sh
PLAYWRIGHT_MODULE="$DEPS/node_modules/playwright" \
MSBD_BROWSER_RUNTIME_SEED=/path/to/isolated/runtime \
MSBD_BROWSER_VM=1 \
  node scripts/browser/dashboard.cjs
```

`MSBD_BROWSER_IMAGE` overrides the smoke image. Use a trusted image providing a
shell and writable `/tmp`. This mode boots a real isolated sandbox, writes a test
file, exercises detail tabs and the file dialog, and sends a command through the
xterm textarea/WebSocket. It verifies the command's guest file through the REST
API, then deletes the sandbox **before** stopping the daemon. Without this flag,
VM tests are explicitly reported as skipped, not passed.

All application URLs come from the freshly launched local daemon. There is no
option to target an external server or mutate a production environment.
The only external operations are dependency/runtime downloads and optional OCI
pulls. No browser mocking is used by default.

## Coverage and artifacts

- Admin sign-in and protected-page redirect; last-admin UI protection.
- Overview and all navigation links, browser back, theme persistence.
- Desktop sidebar/content non-overlap; mobile sheet open/navigate/Escape close.
- Auto-refresh checkbox hydration and toggling.
- Key expiry select, create and one-time token reveal, confirmation cancellation,
  revocation and deletion, SSE toast visibility.
- User role select and creation, viewer login/logout, direct settings/mutation
  403 checks, role promotion/demotion, password reset and subsequent login,
  confirmation cancellation and user deletion.
- Optional real sandbox tabs, file dialog, and terminal input.
- Browser `pageerror` collection.

Screenshots (token reveal masked), daemon log, built binary, and `results.json`
are under **ignored `bin/browser/`**. Each test continues after a failure so the
JSON report identifies independent failures. Exit status is nonzero if any test
fails. The daemon and browser are closed on completion. Isolated temporary state
is retained for debugging; the report prints its path. Delete it after inspection.
Passwords and API tokens are not logged by the harness, but treat retained
SQLite/session files as test credentials and do not publish those directories.
Screenshots and result files are overwritten on subsequent runs; archive them
before comparison.

## CSS diagnostic (not the acceptance run)

The migration can leave the committed CSS stale. To distinguish stale CSS from
JS/layout bugs **without editing shared files**, build CSS into ignored artifacts:

```sh
mkdir -p bin/browser
tailwindcss -i internal/dashboard/assets/css/input.css \
  -o bin/browser/current.css --minify
PLAYWRIGHT_MODULE="$DEPS/node_modules/playwright" \
MSBD_BROWSER_CSS="$PWD/bin/browser/current.css" \
  node scripts/browser/dashboard.cjs
```

This optional diagnostic uses Playwright routing to serve **only** the specified
CSS file in place of `/assets/css/output.css`. Application HTML, JS, auth, SSE,
WebSockets and REST calls remain real. Acceptance runs must omit
`MSBD_BROWSER_CSS` to test the binary's committed embedded assets.

See [REPORT.md](REPORT.md) for the migration bugs observed in Chromium and exact
reproduction selectors. The suite retains assertions for these regressions;
do not disable assertions to hide a regression.
