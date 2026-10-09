# Vendored shadcn-templ components

Source: https://github.com/axadrn/shadcn-templ/tree/v2.0.0-beta.13
Commit: `d50c6a60c9373deb9ed94871e5eb5ac6b145128f` (MIT; see LICENSE).

The initial migration was staged in an isolated checkout. Sources were built by
upstream `registryapi.BuildStyleItem("base-nova", name, inliner.Options{})`,
including registry dependencies, then imports rebased to this module. This
expands style markers into ordinary Tailwind utilities without replacing the
application's theme stylesheet. `copybutton` is a docs component, absent from
the registry: its marker-free .templ and .js were copied directly from the tag.
Generated Go is produced with templ v0.3.1070.

## Local integration

- `components.json` replaces `.templui.json`.
- Component JS source now lives beside its component. The CLI builds a single
  hashed, minified asset in `assets/js` plus `scripts_bundle.go`.
- Load `@components.Scripts()` once in the layout head, replacing every old
  per-component `Script()` call. This is a classic deferred script, not modules.
- The existing embedded asset server serves the bundle at `/assets/js` without
  upstream imports, CDN assets, runtime builds, or extra Go dependencies.
- `utils.CN` retains upstream's Tailwind v4 syntax and ordering fixes, but calls
  the local thread-safe `internal/dashboard/twmerge`. `utils.TwMerge` remains a
  compatibility helper. Never restore the unsafe package-global merge.
- `task dashboard:components` regenerates only owned components and their JS.
  `task dashboard` also generates views and builds CSS; `dev:scripts` watches JS.
  Build tooling pins the CLI to beta.13 independently of go.mod.
- Future CLI `add --overwrite` can overwrite the local utility adaptation and
  update CSS. Review those changes; don't run `init` or `apply` over this app.

## Views/runtime handoff (breaking API)

Views are migrated to these APIs; the following notes describe integration constraints.

- `window.tui` is gone. Dialogs use
  `window.templ.dialog.open(id)`, `.close(id)`, `.toggle(id)`, `.isOpen(id)`.
  The dialog root only provides context. Use `Dialog(Props{ID: ...})` around
  `Content { ... }` (Content includes Portal and Overlay); trigger/close are attribute helpers
  (`Trigger(ctx)`, `TriggerFor(id)`, `Close(ctx)`, `CloseFor(id)`), not templ
  wrappers. `ContentProps.HideCloseButton` replaces the old close-button knob.
  `Open *bool` is controlled; `DefaultOpen` permits browser-owned state.
- Shared Base UI runtime uses a MutationObserver to initialize/destroy patched
  DOM, portals, focus traps, outside inertness, and scroll locks. Destroying an
  owner also retires its portaled DOM. No manual reinitialization is required.
- `dropdown` -> `dropdownmenu`; `selectbox` -> `select` (package `selectcomp`).
  Old `form` has no upstream replacement; use `field` composition.
- Select uses `Value *string`/`DefaultValue`, `Items []ItemData`, hidden inputs,
  and cancelable bubbling `select-change`/`select-open-change` events. Native
  hidden input `change` still bubbles. Review signal bindings in views.
- Tabs uses `Value *string`/`DefaultValue`; changes emit cancelable bubbling
  `tabs-value-change`. Other controls also use Base UI-style state/attributes.
- Toast composition is `Toaster` plus `Toast(Props{Title, Description, Type,
  Timeout})`. `window.templ.toast` is a manager (`add`, `update`, `close`,
  `promise`), not the old `window.tui` toast API.
- Input/textarea/label and many purely visual parts no longer need individual
  scripts. Copybutton now accepts `Props.Attrs` and optional `Value`.
- Style has changed to upstream Nova. CSS must be rebuilt after the views
  migration; no application theme/CSS was overwritten in this step. Review
  any animation/token CSS requirements in browser testing.

## Added building blocks

All use optional variadic props unless noted; common props carry ID, Class,
and templ.Attributes. Exact signatures live in the adjacent .templ sources.

- `empty`: `Empty`, `Header`, `Media` (default/icon variant), `Title`,
  `Description`, `Content`.
- `field`: `Set`, `Legend` (legend/label variant), `Group`, `Field`
  (vertical/horizontal/responsive orientation), `Content`, `Label`, `Title`,
  `Description`, `Separator`, `Error` (render error text as children).
- `breadcrumb`: `Breadcrumb`, `List`, `Item`, `Link`, `Page`, `Separator`,
  `Ellipsis()`.
- `buttongroup`: `ButtonGroup` (horizontal/vertical), `Text`, `Separator`.
- `spinner`: `Spinner`; default LoaderCircle with size-4, animate-spin,
  role=status and aria-label=Loading; override via props/attributes.
- `alertdialog`: `AlertDialog`, `Portal`, `Overlay`, `Content`
  (default/sm size), `Header`, `Footer`, `Media`, `Title`, `Description`,
  `Action`, `Cancel`; trigger/close attribute helpers like dialog.
  This uses the shared dialog runtime, not a native window.confirm.

## Required stylesheet support

`assets/css/shadcn-tailwind.css` and `tw-animate.css` are copied unmodified from
the same pinned tag’s `assets/css/` directory and imported by `input.css`.
They supply orientation/state variants and animations used by Nova components;
omitting them breaks tabs, checked styling, and transitions. Application theme
tokens remain in `input.css`.
