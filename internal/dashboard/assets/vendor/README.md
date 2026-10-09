# Vendored frontend runtimes

These are unmodified upstream release artifacts, served locally by the embedded
asset filesystem. No npm install or frontend build is required. Only stable
releases are selected; do not use floating CDN URLs or npm prerelease tags.

| Files | Previous version | Vendored version | Source |
| --- | --- | --- | --- |
| `datastar.js` | 1.0.0 | 1.0.4 | https://cdn.jsdelivr.net/gh/starfederation/datastar@v1.0.4/bundles/datastar.js |
| `xterm.js`, `xterm.css` | @xterm/xterm 5.5.0 | @xterm/xterm 6.0.0 | https://registry.npmjs.org/@xterm/xterm/-/xterm-6.0.0.tgz (`package/lib/xterm.js`, `package/css/xterm.css`) |
| `addon-fit.js` | @xterm/addon-fit 0.10.0 | @xterm/addon-fit 0.11.0 | https://registry.npmjs.org/@xterm/addon-fit/-/addon-fit-0.11.0.tgz (`package/lib/addon-fit.js`) |

The old artifacts were identified by byte-for-byte comparisons with the pinned
upstream releases. New npm archives were verified against registry SHA-512
`dist.integrity` before extraction:

- xterm: `sha512-TQwDdQGtwwDt+2cgKDLn0IRaSxYu1tSUjgKarSDkUM0ZNiSRXFpjxEsvc/Zgc5kq5omJ+V0a8/kIM2WD3sMOYg==`
- addon-fit: `sha512-jYcgT6xtVYhnhgxh3QgYDnnNMYTcf8ElbxxFzX0IZo+vabQqSPAjC3c1wJrKB5E19VwQei89QCiZZP86DCPF7g==`

Datastar was cross-checked byte-for-byte against
https://raw.githubusercontent.com/starfederation/datastar/v1.0.4/bundles/datastar.js.
Its GitHub release is stable; the older `@starfederation/datastar` npm package's
`latest` tag points to a beta and is deliberately not used. Licenses are copied
from the same upstream tag/packages into the adjacent `*-LICENSE*` files.

## Artifact SHA-256

```
727844adfc825ee651fb93c544a2a739986f9a21820a94524b35f0cac470cf91  datastar.js
14903579ff54664cd72f8e8699e6961a6272c21863ec1c3b118cdc8af5d4a972  xterm.js
854a7c0fb70e8b1a083c16797ab827299fb18744f5ad34f227b48337e33293c6  xterm.css
ba3ea256ce0620a0992a197d6c9baea64823fc93d8da07a9e366ca9943c18527  addon-fit.js
```

## Compatibility and verification

Reviewed https://github.com/xtermjs/xterm.js/releases/tag/6.0.0 before taking
the major upgrade. The dashboard does not use the removed `windowsMode`,
`fastScrollModifier`, canvas addon, or old `overviewRulerWidth` option. Its UMD
`Terminal` / `FitAddon.FitAddon` globals and used APIs remain available. The fit
addon is upgraded together with xterm because it depends on private renderer
and viewport internals. xterm 6 changes scrollbar behavior and removes the old
Alt-to-Ctrl-arrow mapping; these upstream behavior changes still apply.

Verification performed:

- `node --check` on all three JavaScript files.
- `go test ./internal/dashboard/...` and `go test -race ./internal/dashboard/...`.
- Headless Chromium smoke test using locally served assets: terminal creation,
  open/fit, binary output, resizing and font-size changes, onData subscription,
  clear/focus/dispose; Datastar signal initialization, click handling,
  `@post(..., {retry:'never'})`, SSE element and signal patches.

Initial runtime-only verification limitations (before the dashboard migration):
no live microVM/WebSocket terminal integration or exhaustive visual,
keyboard, scrolling, browser-matrix or dropped-connection retry testing. The
headless test used a synthetic SSE endpoint, not the full dashboard. npm integrity
and CDN/GitHub equality checks establish artifact consistency, not independent
publisher-signature verification. Upstream source-map comments are retained,
but source maps are not vendored (as before). That initial runtime-only update
left component assets untouched; the subsequent shadcn-templ migration and live
browser coverage are documented in `components/UPSTREAM.md` and
`scripts/browser/REPORT.md` (relative to the dashboard and repository roots).
