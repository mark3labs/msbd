# Migrated dashboard: Chromium findings

Tested 2026-10-09 with Chromium **152.0.7977.82**, Playwright **1.64.0**, and
microsandbox SDK/runtime **0.7.7**, real `/dev/kvm`. Each suite run used a new
local daemon, temporary HOME/runtime/database, randomly generated credentials,
and newly created microVM. No user's `.env`, existing sandbox or production
server was used. Only new test infrastructure files were edited by this work.

## Final acceptance

After fixes and a full embedded asset rebuild, **two consecutive isolated
Chromium runs passed all 19 checks**, including real microVM files and xterm
WebSocket command input; no JavaScript errors occurred. Latest artifacts are
in ignored `bin/browser/`. The historical findings below are retained for
regression context, not outstanding defects.

Fixes: rebuilt CSS with upstream state/orientation variants and animations;
used `DefaultOpen` for key reveal; preserved password target signals on portal;
hid mutation controls from viewers; fixed delayed-bundle checkbox hydration.
The xterm harness now waits for a prompt and verifies iframe focus before
typing, retaining the exact guest-file assertion.

## Initial defects / proposed fixes (resolved)

### 1. Committed CSS is stale after component migration

- At 1440×1000, `#content` has x=0 while
  `[data-slot="sidebar-container"]` ends at x≈230.125. The fixed sidebar clips
  the heading and first table column.
- At 390×844, click `[aria-label="Toggle navigation"]`; then
  `#main-sidebar-mobile a[href="/sandboxes"]` is outside the viewport and
  cannot be clicked.
- Detail tab `#sbxtabs-tab-overview` is intercepted by the sidebar link to
  `/images` because of the overlap.
- SSE success toast `[data-slot="toast-viewport"] [data-slot="toast-title"]`
  exists but is hidden in the embedded-CSS run.
- **Controlled diagnostic:** serve freshly generated Tailwind CSS from ignored
  `bin/browser/current.css` through Playwright routing. Desktop overlap, mobile
  navigation, toasts, and all detail tabs immediately pass, with no HTML/JS
  changes.
- **Proposed fix:** run the normal dashboard asset build and include updated
  `internal/dashboard/assets/css/output.css`. Do not patch the layout to work
  around missing generated classes.
- Screenshots: `bin/browser/embedded-css/failure-desktop-layout.png`,
  `bin/browser/embedded-css/failure-mobile-sidebar.png`, and fresh-CSS
  `bin/browser/mobile-navigation.png`, `bin/browser/overview.png`.

### 2. Created API key's one-time reveal dialog never opens

Reproduce at `/settings/keys`:

1. Click `button` named `New key`.
2. Fill `#keyname`; open `#keyexpires`; select `[role="option"]` named `7 days`.
3. Click `#create-key button` named `Create`.
4. The new key appears in `#key-table`, but `#new-key` remains hidden, including
   after a ten-second wait. The token is not presented to the user.

This reproduces with both embedded and fresh CSS; no browser `pageerror` occurs.
`internal/dashboard/handlers_settings.go` patches `#new-key-slot`, immediately
executes `window.templ?.dialog?.open('new-key')`, then refreshes the table.
`internal/dashboard/components/dialog/dialog.js`'s `stateOf()` only returns an
already-initialized dialog from its internal map. The insertion lifecycle is
asynchronous, so the immediate call likely sees no state.

**Proposed fix:** render this SSE-inserted reveal dialog with `DefaultOpen: true`
(as the working dynamic file dialog already does), or make the public open API
initialize a newly inserted dialog synchronously before opening. Do not use an
arbitrary sleep. Candidate files: `internal/dashboard/views/settings.templ` +
generated counterpart, `internal/dashboard/handlers_settings.go`, or upstream
component lifecycle JS if fixing it generically. Rebuild the bundle for JS changes.
Screenshot: `bin/browser/failure-key-create-select-reveal.png` (token masked).

### 3. Password reset loses selected username on dialog portal

Reproduce at `/settings/users` after creating `browser-viewer`:

1. Click `[aria-label="Set password for browser-viewer"]`.
2. Fill `#pwvalue` with a valid password.
3. Click `#set-password button` named `Set password`.
4. `#set-password-error` reads **“No user selected / Close the dialog and pick a
   user again.”** The dialog remains open and the reset cannot complete.

The row sets `$pwuser` then calls the dialog open API. The dialog contains
`data-signals="{pwuser:'', pwvalue:''}"`; moving the content into its portal
appears to reinitialize the signals and clear the selected username. Its
text-only description is also missing the target name. Reproduces with both CSS
versions and no browser `pageerror`.

**Proposed fix:** initialize dialog-owned signals using Datastar's if-missing
semantics, or put initialization on a persistent ancestor outside portaled
content, retaining the row's deliberate `$pwvalue=''` reset. Check other dialog
signals for the same reparenting behavior. Candidate file:
`internal/dashboard/views/settings.templ` + generated counterpart.
Screenshot: `bin/browser/failure-user-password-update.png`.

### 4. Viewer is offered mutation controls

After logging in as the newly created viewer, `/sandboxes` still renders two
buttons named `New sandbox` (page toolbar and empty state).

**Server-side authorization is correct:** the viewer gets 403 for both Settings
pages and direct POST/DELETE requests to `/ui/users`, `/ui/keys`,
`/ui/sandboxes`, and `/ui/users/browser-admin`. Settings nav links are hidden.
This is a UI courtesy defect, **not** an auth bypass.

**Proposed fix:** gate New sandbox triggers on admin permissions in
`internal/dashboard/views/sandboxes.templ`, including the empty-state action,
using the view's `Meta` role helper. Keep `guardWrite` as enforcement.

## Results

Acceptance run against the binary's **unchanged embedded CSS**:
11 passed / 8 failed. Failures comprise the stale-CSS effects, token reveal,
password reset, and offered viewer-create button. Raw report archived at
`bin/browser/results-embedded-css.json`.

Final diagnostic with **fresh CSS only**:
**16 passed / 3 failed**. Remaining failures are token reveal, password reset,
and the offered viewer-create button. Latest report: `bin/browser/results.json`.

Passing real-browser behaviors after CSS regeneration:

- Admin login, next-path redirect and last-admin protection.
- All navigation pages and browser back.
- Theme change and persisted theme on reload.
- Mobile sheet open, real link navigation and Escape close.
- Auto-refresh checkbox (wait for `live:true` hydration before toggling).
- Key expiry select, key creation storage, confirmation cancel/accept,
  revoke/delete, and visible SSE success toast.
- User role select and creation, viewer login/logout and server-side 403s,
  role promotion/demotion and deletion cancel/accept.
- Real isolated sandbox creation and cleanup.
- Overview/Run/Logs/Files detail tabs.
- Guest file dialog for `/tmp/browser-test.txt`.
- Embedded xterm connects to the real terminal WebSocket; Chromium types
  `printf BROWSER_TERMINAL_OK > /tmp/browser-terminal-ok`, and the test reads the
  guest file through the REST API and checks its content.
- No browser `pageerror` events in the final run.

Artifacts are ignored, not committed. The reproducible script retains assertions for these regressions. No shared runtime,
view, handler, asset or build file was changed by this browser-test work.
