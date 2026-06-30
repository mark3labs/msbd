# Datastar Pro Reference

Pro adds plugins on top of the open-source Datastar core. Requires the `datastar-pro.js` bundle. The core engine and all open-source plugins work identically in both bundles.

## Table of Contents

- [Pro Attributes](#pro-attributes) — persist, query-string, replace-url, match-media, animate, scroll-into-view, view-transition, custom-validity, on-raf, on-resize
- [Pro Actions](#pro-actions) — @clipboard, @fit, @intl
- [Rocket Component System](#rocket-component-system) — JavaScript API: `rocket(tag, { mode, props, setup, render })` (see `rocket.md` for full reference)

## Pro Attributes

### `data-persist` — Persist Signals to Storage

Two-way syncs signals to `localStorage` (default) or `sessionStorage`.

```html
<div data-persist>...</div>
<div data-persist:myKey__session="{include: /^theme/}">...</div>
```

- Key = storage key (default: `'datastar'`)
- Expression = `SignalFilterOptions` (optional filter for which signals to persist)
- **Modifier:** `__session` → use `sessionStorage` instead of `localStorage`
- On load: reads from storage → `mergePatch` into signal store
- Sets up a reactive effect that serializes matching signals to storage on every change
- Parse errors from corrupted storage are caught and logged (no crash)

### `data-query-string` — Sync Signals ↔ URL Params

```html
<div data-query-string>...</div>
<div data-query-string__history__filter>...</div>
<div data-query-string__filter="{include: /^search|page$/}">...</div>
```

Bidirectional sync between URL query parameters and signals.

**Modifiers:**
- `__history` — uses `pushState` instead of `replaceState` (enables back/forward navigation, adds `popstate` listener)
- `__filter` — only includes truthy signal values in URL (removes falsy params)

**URL value parsing:** `'true'`→`true`, `'false'`→`false`, numeric strings→numbers, else string. Supports dot-notation paths (`?user.name=John` → signal `user.name`).

During `popstate` events, URL is not updated back (prevents circular updates). If all params become empty, URL is set to just the pathname (removes `?`).

### `data-replace-url` — Reactively Replace Browser URL

```html
<div data-replace-url="'/page/' + $slug"></div>
```

Calls `history.replaceState()` reactively whenever referenced signals change. Resolves relative URLs against `window.location.href` via `new URL(url, baseUrl)`.

### `data-match-media` — Reactive Media Queries

```html
<div data-match-media:is-mobile="max-width: 768px">
  <span data-show="$isMobile">Mobile view</span>
</div>
<div data-match-media:prefers-dark="prefers-color-scheme: dark"></div>
```

- Key = signal name (processed through casing modifiers)
- Value = media query string (auto-wrapped in `()` if missing parentheses, quotes stripped)
- Creates a boolean signal from `window.matchMedia()`
- Auto-updates when viewport/preference changes
- On cleanup: signal set to `null`, listener removed
- If `matchMedia()` throws (invalid query), signal defaults to `false`

### `data-animate` — Animate Element Attributes

Animates numeric attributes (especially SVG) from current value to target using `requestAnimationFrame`.

```html
<rect data-animate:width__duration.500ms__ease.outsine="$targetWidth"></rect>
<circle data-animate:r__duration.2s__loop__pingpong="50"></circle>
<div data-animate="{width: $w, height: $h}"></div>
```

Two modes: with key (single attribute) or without key (object of attribute→value pairs).

**Modifiers:**

| Modifier | Tags | Default |
|---|---|---|
| `__duration` | `Nms`/`Ns` | `1000ms` |
| `__ease` | easing name | `linear` |
| `__delay` | `Nms`/`Ns` | `0` |
| `__loop` | — | repeat infinitely |
| `__pingpong` | — | reverse direction each iteration |

**37 easing functions:**
`linear`, `quadratic`, `cubic`, `elastic`,
`inquad`/`outquad`/`inoutquad`, `incubic`/`outcubic`/`inoutcubic`,
`inquart`/`outquart`/`inoutquart`, `inquint`/`outquint`/`inoutquint`,
`insine`/`outsine`/`inoutsine`, `inexpo`/`outexpo`/`inoutexpo`,
`incirc`/`outcirc`/`inoutcirc`, `inelastic`/`outelastic`/`inoutelastic`,
`inback`/`outback`/`inoutback`, `inbounce`/`outbounce`/`inoutbounce`,
`ingolden`/`outgolden`/`inoutgolden`

**Value parsing:** Values can have unit suffixes (`100px`, `50%`, `3.5em`). Parsed via regex. Start and end values must share the same unit suffix or an error is thrown. If current = target, attribute is set immediately (no animation).

Reactive: re-evaluates expression when signals change, starts new animation to new target. Cancels in-progress animation on same element.

### `data-scroll-into-view` — Scroll Element into View

```html
<div data-scroll-into-view__smooth__center>New content</div>
<div data-scroll-into-view__instant__vstart__hcenter>...</div>
```

Calls `el.scrollIntoView()` when element loads. Fire-and-forget (no reactive updates).

**Modifiers:**

| Modifier | Effect |
|---|---|
| `__smooth` | `behavior: 'smooth'` |
| `__instant` | `behavior: 'instant'` |
| `__auto` | `behavior: 'auto'` (default) |
| `__start` / `__center` / `__end` / `__nearest` | Both `block` and `inline` alignment |
| `__hstart` / `__hcenter` / `__hend` / `__hnearest` | `inline` alignment only |
| `__vstart` / `__vcenter` / `__vend` / `__vnearest` | `block` alignment only |
| `__focus` | Also calls `el.focus()` after scrolling |

### `data-view-transition` — Scoped View Transition Names

```html
<!-- Key form: key becomes the transition name -->
<div data-view-transition:hero-image></div>

<!-- Value form: expression result is the name -->
<div data-view-transition="'item-' + $id"></div>
```

Sets `el.style.viewTransitionName` reactively. Cleans up (removes the style) on element removal. Uses casing modifiers on key.

### `data-custom-validity` — Custom Form Validation

```html
<input data-custom-validity="$password.length < 8 ? 'Must be 8+ chars' : ''" />
<input data-custom-validity="$email.includes('@') ? '' : 'Invalid email'" />
```

Reactively calls `el.setCustomValidity(result)`. Empty string `''` = valid. Any other string = validation error message.

Only works on `<input>`, `<select>`, `<textarea>`. Throws `CustomValidityInvalidElement` on other elements. Throws `CustomValidityInvalidExpression` if expression returns non-string.

### `data-on-raf` — Run on Every Animation Frame

```html
<canvas data-on-raf="drawFrame()"></canvas>
<div data-on-raf__throttle.16ms="$fps = calculateFps()"></div>
```

Runs expression via `requestAnimationFrame` loop. Expression runs inside `beginBatch()`/`endBatch()`. Supports timing modifiers (`__debounce`, `__throttle`, `__delay`) and `__viewtransition`. Cleanup cancels the rAF loop.

### `data-on-resize` — Run on Element Resize

```html
<div data-on-resize="$width = el.offsetWidth; $height = el.offsetHeight"></div>
<div data-on-resize__debounce.200ms="@get('/api/layout')"></div>
```

Uses `ResizeObserver` to watch element dimensions. Runs expression inside `beginBatch()`/`endBatch()`. Supports timing and view transition modifiers. Cleanup disconnects the observer.

## Pro Actions

### `@clipboard(text, isBase64?)` — Copy to Clipboard

```html
<button data-on-click="@clipboard($shareUrl)">Copy Link</button>
<button data-on-click="@clipboard($encodedContent, true)">Copy Decoded</button>
```

Uses `navigator.clipboard.writeText()`. If `isBase64` is `true`, decodes the text via `atob()` before writing. Throws `ClipboardNotAvailable` if clipboard API is not present (non-HTTPS context, unsupported browser).

### `@fit(value, oldMin, oldMax, newMin, newMax, clamp?, round?)` — Range Mapping

```html
<span data-text="@fit($progress, 0, 100, 0, 1)"></span>
<div data-style:width="@fit($score, 0, 10, 0, 100, true) + '%'"></div>
<span data-text="@fit($raw, 0, 255, 0, 100, true, true) + '%'"></span>
```

Maps a value from `[oldMin, oldMax]` to `[newMin, newMax]` using inverse-lerp then lerp.
- `clamp` (default `false`) — clamp result to `[newMin, newMax]`
- `round` (default `false`) — round to nearest integer via `Math.round`

### `@intl(type, value, options?, locales?)` — Internationalization

```html
<span data-text="@intl('number', 1234.5, {style:'currency', currency:'USD'})"></span>
<span data-text="@intl('datetime', $createdAt, {dateStyle:'long'})"></span>
<span data-text="@intl('relativeTime', -3, {unit:['day']})"></span>
<span data-text="@intl('list', ['Alice','Bob','Carol'], {type:'conjunction'})"></span>
<span data-text="@intl('pluralRules', $count)"></span>
<span data-text="@intl('displayNames', 'en-US', {type:'language'})"></span>
```

**Supported types:**

| Type | Intl API | Value | Notes |
|---|---|---|---|
| `datetime` | `DateTimeFormat` | Date, string, or number | Throws `IntlInvalidDate` if unparseable |
| `number` | `NumberFormat` | number or numeric string | |
| `pluralRules` | `PluralRules` | number | Returns category: `zero`/`one`/`two`/`few`/`many`/`other` |
| `relativeTime` | `RelativeTimeFormat` | number | Unit from `options.unit[0]` (default `'day'`) |
| `list` | `ListFormat` | array | Non-arrays wrapped in single-element array |
| `displayNames` | `DisplayNames` | string | `options.type` defaults to `'language'` |

Locales fallback: `locales` → `navigator.language` → `'en-US'`. Throws `IntlTypeNotSupported` for unrecognized type strings.

## Rocket Component System

> **Rocket has been rewritten as a JavaScript API in Rocket beta.1 (shipped with Datastar Pro v1.0.1).**
>
> The previous markup-based system (`<template data-rocket:name>`, `data-prop:*`, `data-schema:*`, scoped CSS via `<style>` blocks, etc.) is **obsolete** — there is no automatic upgrade path. If you encounter that syntax in this codebase or older docs, replace it with the new JS API.

Rocket is Datastar Pro's web-component API. You define a custom element with `rocket(tag, { ... })`, describe public props with codecs, put non-DOM instance behavior in `setup`, use `onFirstRender` only when work depends on rendered refs, and return DOM from `render`.

### Quick Example

```html
<script type="module">
  import { rocket } from '/path/to/datastar-pro.js'

  rocket('demo-counter', {
    mode: 'light',                              // 'light' | 'open' (default) | 'closed'
    props: ({ number, string }) => ({
      count: number.min(0).default(0),
      label: string.trim.default('Counter'),
    }),
    setup: ({ $$, props, observeProps, cleanup, action }) => {
      $$.count = props.count                     // create local signal $$count
      $$.label = () => $$.count + ' items'       // computed signal (function form)
      observeProps(() => { $$.count = props.count }, 'count')
    },
    render: ({ html, props: { label } }) => html`
      <button data-on:click="$$count += 1" data-text="${label} + ': ' + $$count"></button>
    `,
  })
</script>

<demo-counter count="5" label="Inventory"></demo-counter>
```

### Key Concepts

- **`rocket(tag, options)`** — `tag` must contain a hyphen.
- **Codecs** (`number`, `string`, `bool`, `date`, `json`, `js`, `bin`, `array`, `object`, `oneOf`) are **immutable builders** chained fluently: `string.trim.lower.kebab.maxLength(48)`. Custom codecs via `createCodec({ decode, encode })`.
- **`setup`** runs once per instance, before initial render. Use it for local signals (`$$`), prop observers, timers, cleanup, and local actions.
- **`onFirstRender`** runs after the first render — use it when code depends on `data-ref:*` refs or measured DOM.
- **`render`** returns Rocket `html` or `svg` tagged-template output.
- **`$$name`** in templates is rewritten per-instance to `$._rocket.<tag>.<id>.<name>`. Each instance has isolated local state.
- **Local actions** registered with `action('copy', fn)` are callable from markup as `@copy()`.
- **Mode** — `'light'` (host element itself), `'open'` (open shadow root, default), `'closed'`.
- **Manifest** — `manifest: { slots, events }` adds documentation metadata; `publishRocketManifests({ endpoint })` posts JSON.
- **Bundle exports**: `rocket`, `createCodec`, `publishRocketManifests`, types `Codec`, `CodecRegistry`, `RocketDefinition`, plus signal helpers (`signal`, `computed`, `effect`, `mergePatch`, etc.).

### Structural Templates Inside Rocket Render

These only work inside Rocket render output (not as standalone Datastar plugins):

```html
<!-- Conditionals -->
<template data-if="$$step === 0"><p>Idle</p></template>
<template data-else-if="$$step === 1"><p>Loading</p></template>
<template data-else><p>Ready</p></template>

<!-- Loops -->
<template data-for="letter, row in $$letters">
  <li>
    <strong data-text="row + 1"></strong>
    <span data-text="letter"></span>
  </li>
</template>
```

`data-for` accepts: `expr`, `item in expr`, or `item, i in expr`. **No index-only form.** Rows are kept by **position** — reordering does not preserve item identity in this version.

### Full Reference

See **`rocket.md`** in this same `references/` directory for the complete Rocket reference, including:

- All codec methods (`string.trim.upper.lower.kebab.camel.snake.pascal.title.prefix.suffix.maxLength.default`, `number.min.max.clamp.step.round.ceil.floor.fit.default`, etc.)
- Setup context details (`$$`, `$`, `effect`, `apply`, `cleanup`, `actions`, `action`, `observeProps`, `overrideProp`, `defineHostProp`, `render`)
- `onFirstRender` and ref handling
- Slot projection (light DOM and shadow DOM)
- `renderOnPropChange` for imperative DOM updates
- Rocket scope rewriting and host id behavior
- Complete worked example (todo app)
