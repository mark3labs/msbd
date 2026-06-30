# Rocket — Datastar Pro's Web-Component API

> **Rocket is currently in beta (Datastar Pro v1.0.1, Rocket beta.1).**
>
> Rocket has been **rewritten as a JavaScript API**. There is no straightforward upgrade path from the old `<template data-rocket:*>` markup-based system. If you find references to `data-prop:*`, `data-schema:*`, or `data-rocket:*` in older code or skill versions, those are obsolete.

Rocket is Datastar Pro's web-component API. You define a custom element with `rocket(tagName, { ... })`, describe public props with codecs, put non-DOM instance behavior in `setup`, use `onFirstRender` only when work depends on rendered refs or mounted DOM, and return DOM from `render`.

Rocket is built around the browser's custom-element model. Datastar handles reactivity, local signal scoping, action dispatch, and DOM application.

## Table of Contents

- [Quick Example](#quick-example)
- [Defining a Component](#defining-a-component)
- [Component Definition Fields](#component-definition-fields)
- [Mode (light / open / closed)](#mode)
- [Props and Codecs](#props-and-codecs)
- [Codec Reference](#codec-reference)
- [Custom Codecs](#custom-codecs)
- [Manifest (slots & events docs)](#manifest)
- [Setup](#setup)
- [Setup Context](#setup-context)
- [onFirstRender](#onfirstrender)
- [Render](#render)
- [renderOnPropChange](#renderonpropchange)
- [Conditional Rendering — `<template data-if>`](#conditional-rendering)
- [Loop Rendering — `<template data-for>`](#loop-rendering)
- [Local Actions](#local-actions)
- [Host Accessor Overrides](#host-accessor-overrides)
- [Watching Prop Changes — `observeProps`](#observeprops)
- [Rocket Scope Rewriting (`$$`)](#rocket-scope-rewriting)
- [Slots](#slots)
- [Complete Example](#complete-example)

## Quick Example

```html
<script type="module">
  import { rocket } from '/path/to/datastar-pro.js'

  rocket('demo-counter', {
    mode: 'light',
    props: ({ number, string }) => ({
      count: number.step(1).min(0),
      label: string.trim.default('Counter'),
    }),
    setup: ({ $$, observeProps, props }) => {
      $$.count = props.count
      observeProps(() => {
        $$.count = props.count
      }, 'count')
    },
    render: ({ html, props: { count, label } }) => {
      return html`
        <div class="stack gap-2">
          <button
            type="button"
            data-on:click="$$count += 1"
            data-text="${label} + ': ' + $$count"
          ></button>
          <template data-if="$$count !== ${count}">
            <button type="button" data-on:click="$$count = ${count}">Reset</button>
          </template>
        </div>
      `
    },
  })
</script>

<demo-counter count="5" label="Inventory"></demo-counter>
```

## Defining a Component

```ts
rocket(tag: string, options?: RocketDefinition<Defs>): void
```

- `tag` must contain a hyphen, must be unique, and is the actual HTML tag.
- Re-registering the same tag is **ignored** (safe during HMR).
- Always import `rocket` explicitly — do not rely on a global.

```js
import { rocket } from '/bundles/datastar-pro.js'

rocket('demo-user-card', {
  props: ({ string }) => ({ name: string.default('Anonymous') }),
  render: ({ html, props: { name } }) => html`<p>${name}</p>`,
})
```

## Component Definition Fields

| Field | Description |
|-------|-------------|
| `mode` | `'light'`, `'open'` (default), or `'closed'` — chooses light DOM, open shadow DOM, or closed shadow DOM. |
| `props` | `(codecs) => ({...})` — defines public props, codecs, defaults, and attribute reflection. |
| `setup` | `(ctx) => void` — runs once per connected instance. Local state, prop observers, timers, host APIs, cleanup. |
| `onFirstRender` | `(ctx & { refs }) => void` — runs after initial render + Datastar apply pass. Use only when refs/rendered DOM are required. |
| `render` | `(ctx) => RocketRenderValue` — returns the component DOM as Rocket `html` or `svg`. |
| `renderOnPropChange` | `boolean \| (ctx) => boolean` — defaults to `true`. Controls whether prop updates trigger rerendering. |
| `manifest` | Adds slot and event metadata to Rocket's generated component manifest (for tooling/docs). |

## Mode

`mode` chooses where the component renders.

| Value | Mount target | When to use |
|-------|--------------|-------------|
| `'light'` | The host element itself | Component should participate in page DOM and CSS |
| `'open'` | Open shadow root (default) | Style encapsulation but still want `element.shadowRoot` |
| `'closed'` | Closed shadow root | Internal DOM stays fully encapsulated |

Default is `'open'` because shadow DOM gives native slots and a normal shadow-root debugging surface, while CSS custom properties pierce the boundary for theming.

In **shadow DOM**, `<slot>` is the platform slot API.
In **light DOM**, `<slot>` is a Rocket placeholder for host-child projection (named + default + fallback content all supported, but it's a Rocket runtime feature, not browser slotting).

## Props and Codecs

`props` is a function called once at definition time. It receives the codec registry and returns the prop map. Each prop has a single codec used for:

1. Decoding the initial attribute value at construction.
2. Decoding new attribute values when an observed attribute changes.
3. Encoding property writes (`element.someProp = value`) back into an attribute.

Custom-element attributes are always strings. Codecs normalize them into typed values, supply defaults, and reflect property writes back. Each declared prop gets a normal element property accessor on the custom-element prototype automatically (no per-instance `Object.defineProperty` needed).

**Naming:** Prop names are written camelCase in JS and reflect to **kebab-case attributes** in HTML. `startDate` ↔ `start-date`.

### Fluent Codec Pattern

Codecs are immutable builders. Each method returns a new codec with another transform layered on:

```js
props: ({ string, number, object, array, oneOf }) => ({
  slug: string.trim.lower.kebab.maxLength(48),
  progress: number.clamp(0, 100).step(5),
  theme: oneOf('light', 'dark', 'system').default('system'),
  tags: array(string.trim.lower),
  profile: object({
    name: string.trim.default('Anonymous'),
    age: number.min(0),
  }),
})
```

Read chains left to right: `string.trim.lower.kebab` = "decode as string, trim, lowercase, then kebab-case."

`.default(...)` can appear anywhere in the chain but reads best at the end.

### Props vs Signals

**Default to props** when data is part of the component's public API — they map to attributes and element properties, reflect through the host, and give outside code a normal way to configure the component.

**Use local Rocket signals (`$$`)** for internal reactive state and imperative integrations (charts, third-party widgets, timers, fetch results). If a parent or page author should set it directly on the element, it's a prop.

## Codec Reference

| Codec | Decoded type | Typical input | Zero value |
|-------|--------------|---------------|------------|
| `string` | `string` | `" hello "` | `''` |
| `number` | `number` | `"42"` | `0` |
| `bool` | `boolean` | `""`, `"true"`, `"1"` | `false` |
| `date` | `Date` | `"2026-03-18T12:00:00.000Z"` | fresh `new Date()` |
| `json` | `any` | `'{"items":[1,2,3]}'` | `{}` |
| `js` | `any` | `"{ foo: 1, bar: [2, 3] }"` (JS-like, not strict JSON) | `{}` |
| `bin` | `Uint8Array` | base64 text | empty `Uint8Array` |
| `array(codec)` | `T[]` | `'["a","b"]'` | `[]` |
| `array(a, b, c)` | tuple | `'["en", 10, true]'` | per-position defaults |
| `object(shape)` | typed object | `'{"x":10,"y":20}'` | per-field defaults |
| `oneOf(...)` | union | `"primary"` | first allowed entry |

If a codec's `decode(...)` throws, Rocket calls `console.warn(...)` and falls back to that codec's default value.

### `string`

| Member | Effect | Example |
|--------|--------|---------|
| `.trim` | Trim whitespace | `"  Ada  "` → `"Ada"` |
| `.upper` | Uppercase | `"ion"` → `"ION"` |
| `.lower` | Lowercase | `"Rocket"` → `"rocket"` |
| `.kebab` | kebab-case | `"Demo Button"` → `"demo-button"` |
| `.camel` | camelCase | `"rocket button"` → `"rocketButton"` |
| `.snake` | snake_case | `"Rocket Button"` → `"rocket_button"` |
| `.pascal` | PascalCase | `"rocket button"` → `"RocketButton"` |
| `.title` | Title Case | `"hello world"` → `"Hello World"` |
| `.prefix(value)` | Add prefix if missing | `"42"` + `prefix('#')` → `"#42"` |
| `.suffix(value)` | Add suffix if missing | `"24"` + `suffix('px')` → `"24px"` |
| `.maxLength(n)` | Truncate | `"abcdef"` + `maxLength(4)` → `"abcd"` |
| `.default(v)` | Fallback string | |

### `number`

| Member | Effect | Example |
|--------|--------|---------|
| `.min(n)` | Lower bound | `-4` + `min(0)` → `0` |
| `.max(n)` | Upper bound | `120` + `max(100)` → `100` |
| `.clamp(min, max)` | Both bounds | `120` + `clamp(0, 100)` → `100` |
| `.step(step, base?)` | Snap to nearest increment | `13` + `step(5)` → `15` |
| `.round` | Round to integer | `3.6` → `4` |
| `.ceil(decimals?)` | Ceil w/ decimal precision | `1.231` + `ceil(2)` → `1.24` |
| `.floor(decimals?)` | Floor w/ decimal precision | `1.239` + `floor(2)` → `1.23` |
| `.fit(inMin, inMax, outMin, outMax, clamped?, rounded?)` | Map ranges | `50` from `0–100` → `0–1` = `0.5` |
| `.default(v)` | Fallback number | |

### `bool`

Empty-string attributes (`<demo-dialog open>`) decode to `true`.

```js
props: ({ bool }) => ({
  open: bool,
  disabled: bool,
  elevated: bool.default(true),
})
```

### `date`

Invalid input falls back to a valid `Date`. Prefer factory defaults to avoid sharing the same date across instances:

```js
startAt: date.default(() => new Date()),
endAt: date.default(() => new Date(Date.now() + 60_000)),
```

### `json`, `js`, `bin`

`json` parses strict JSON. `js` accepts JS-like object literals (forgiving). `bin` decodes base64 → `Uint8Array`.

```js
props: ({ json, js, bin }) => ({
  series: json.default(() => []),
  config: js.default(() => ({ scale: 1, axis: { x: true } })),
  payload: bin,
})
```

Always use **factory defaults** for objects/arrays so each instance gets its own copy.

### `array(...)`

Two forms:

| Form | Decoded type | Meaning |
|------|--------------|---------|
| `array(codec)` | `T[]` | Homogeneous list, each item decoded with `codec` |
| `array(codecA, codecB, codecC)` | tuple | Each position has its own codec |

```js
props: ({ array, string, number, bool }) => ({
  tags: array(string.trim.lower),
  point: array(number, number),                          // tuple [x, y]
  localeSpec: array(
    string.lower.default('en'),
    number.min(1).default(1),
    bool,
  ).default(() => ['en', 1, false]),
})
```

### `object(shape)`

```js
props: ({ object, string, number, bool, array }) => ({
  profile: object({
    id: string.trim,
    name: string.trim.default('Anonymous'),
    age: number.min(0),
    admin: bool,
    tags: array(string.trim.lower),
  }).default(() => ({
    id: '',
    name: 'Anonymous',
    age: 0,
    admin: false,
    tags: [],
  })),
})
```

### `oneOf(...)`

Constrains a prop to a known set of allowed values (literals, codecs, or both).

```js
props: ({ oneOf, string, number }) => ({
  tone: oneOf('neutral', 'info', 'success', 'warning', 'danger').default('neutral'),
  alignment: oneOf('start', 'center', 'end').default('start'),
  flexibleValue: oneOf(string.trim, number.round),
})
```

Without `.default(...)`, the zero value is the first allowed entry.

## Custom Codecs

Use `createCodec(...)` for fully custom encoding/decoding:

```js
import { createCodec, rocket } from '/bundles/datastar-pro.js'

const percent = createCodec({
  decode(value) {
    const text = String(value ?? '').trim().replace(/%$/, '')
    const n = Number.parseFloat(text)
    return Number.isFinite(n) ? Math.max(0, Math.min(100, n)) : 0
  },
  encode(value) {
    return String(Math.max(0, Math.min(100, value)))
  },
})

rocket('demo-meter', {
  props: ({ string }) => ({
    value: percent.default(50),
    label: string.trim.default('Progress'),
  }),
})
```

The codec contract is `{ decode(value: unknown): T, encode(value: T): string }`. `decode` uses `unknown` because Rocket reuses codec decode paths for missing values, nested members, and already-materialized JS values, not just attribute strings.

Types `Codec` and `CodecRegistry` are exported from the bundle.

## Manifest

Optional metadata for slots and events. Useful for documentation tooling and registries.

```js
import { publishRocketManifests, rocket } from '/bundles/datastar-pro.js'

rocket('demo-dialog', {
  props: ({ string, bool }) => ({
    title: string.default('Dialog'),
    open: bool,
  }),
  manifest: {
    slots: [
      { name: 'default', description: 'Dialog body content.' },
      { name: 'footer', description: 'Action row content.' },
    ],
    events: [
      {
        name: 'close',
        kind: 'custom-event',
        bubbles: true,
        composed: true,
        description: 'Fired when the dialog requests dismissal.',
      },
    ],
  },
  render: ({ html, props: { title, open } }) => html`
    <section data-show="${open}">
      <header>${title}</header>
      <slot></slot>
      <footer><slot name="footer"></slot></footer>
    </section>
  `,
})

// Inspect locally:
const entry = customElements.get('demo-dialog')?.manifest?.()

// Or publish full manifest document:
await publishRocketManifests({ endpoint: '/api/rocket/manifests' })
```

## Setup

`setup` runs once per connected instance after Rocket creates the component scope and before the initial Datastar apply pass. Use it for:

- Local signals and computed state on `$$`
- Prop observers (`observeProps`)
- Timers and external subscriptions
- Cleanup handlers
- Local actions (`action(name, fn)`)
- Host APIs (`overrideProp`, `defineHostProp`)

If your code needs rendered DOM or `data-ref:*` refs, move that part to `onFirstRender` instead.

```js
rocket('demo-timer', {
  props: ({ number, bool }) => ({
    intervalMs: number.min(50).default(1000),
    autoplay: bool,
  }),
  setup: ({ $$, cleanup, props, observeProps }) => {
    $$.seconds = 0
    let timerId = 0

    const syncTimer = () => {
      clearInterval(timerId)
      if (!props.autoplay) return
      timerId = window.setInterval(() => {
        $$.seconds += 1
      }, props.intervalMs)
    }

    syncTimer()
    observeProps(syncTimer)
    cleanup(() => clearInterval(timerId))
  },
  render: ({ html }) => html`<p data-text="$$seconds"></p>`,
})
```

## Setup Context

`setup`/`onFirstRender` receive a context object:

| Helper | Description |
|--------|-------------|
| `props` | Normalized prop values for the current instance |
| `host` | The custom-element instance |
| `$$` | Rocket-local signal proxy. `$$.name = value` creates `$$name`. `$$.name = () => expr` creates a local **computed** signal. |
| `$` | Global Datastar signal store root |
| `effect(fn)` | Reactive side effect with auto cleanup |
| `apply(root, merge?)` | Run Datastar `apply` on a root (useful when third-party code injects DOM) |
| `cleanup(fn)` | Register a disconnect cleanup callback |
| `actions` | Global Datastar actions (e.g. `actions.intl(...)`, `actions.clipboard(...)`) |
| `action(name, fn)` | Register a **local** action callable from rendered markup as `@name(...)` |
| `observeProps(fn, ...propNames)` | Run `fn` after decoding when listed props change. If no names given, watches all. |
| `overrideProp(name, getter?, setter?)` | Wrap an existing prop's host accessor for this instance |
| `defineHostProp(name, descriptor)` | Define a host-only property/method that is **not** a Rocket prop |
| `render(overrides, ...args)` | Manually rerun the component render (coarse structural patch only) |

> Local refs from `data-ref:name` are populated **after** the initial render. They are exposed via `onFirstRender({ refs })`. They are **not** on `$$` and **not** available in `setup`.

### `$$` Quick Reference

```js
$$.count = 0                     // creates $$count signal
$$.count = 5                     // updates it
$$.label = () => $$.count + ' items'   // computed signal — auto-updates
```

In templates, read with `$$count` or write with `data-on:click="$$count += 1"`.

## onFirstRender

`onFirstRender` runs once after Rocket completes the initial `render()`, Datastar `apply(...)`, and ref population pass.

```js
rocket('demo-input-bridge', {
  props: ({ string }) => ({ value: string.default('') }),
  render: ({ html, props: { value } }) => html`
    <input data-ref:input value="${value}">
  `,
  onFirstRender: ({ overrideProp, refs }) => {
    overrideProp(
      'value',
      (getDefault) => refs.input?.value ?? getDefault(),
      (value, setDefault) => {
        const next = String(value ?? '')
        if (refs.input && refs.input.value !== next) refs.input.value = next
        setDefault(next)
      },
    )
  },
})
```

If a piece of logic does not depend on `refs`, keep it in `setup`.

## Render

`render` receives the render context and returns the component DOM.

```ts
render?: (ctx: RenderContext<Props>, ...trailingArgs: any[]) => RocketRenderValue
```

| Field | Description |
|-------|-------------|
| `html` | HTML tagged template returning a fragment |
| `svg` | SVG tagged template (handles namespace) |
| `props` | Normalized prop values |
| `host` | The custom element instance |

Return value can be a `DocumentFragment`, primitive (string/number/boolean/Date/null/undefined), or iterable of composed values. **Booleans render as nothing** in normal data positions — pass `String(value)` if you want literal `"true"`/`"false"`.

In **attribute** interpolation, `false`/`null`/`undefined` omit the attribute, and `true` writes the empty-string boolean form.

Up to 8 trailing args may be passed when calling `ctx.render(overrides, a1, a2, ...)` from setup. Treat manual render calls like coarse DOM branch updates — for high-frequency state, use signals and Datastar bindings.

If you omit `render`, Rocket still registers the element, runs `setup`, and wires action dispatch — it just won't morph a rendered subtree.

### Example: Counter

```js
rocket('demo-stepper', {
  mode: 'light',
  props: ({ number, string }) => ({
    start: number.min(0),
    step: number.min(1).default(1),
    label: string.trim.default('Count'),
  }),
  setup: ({ $$, props }) => {
    $$.count = props.start
  },
  render: ({ html, props: { label, step } }) => html`
    <section class="stack gap-2">
      <h3>${label}</h3>
      <div class="row gap-2">
        <button data-on:click="$$count -= ${step}" data-attr:disabled="$$count <= 0">-</button>
        <output data-text="$$count"></output>
        <button data-on:click="$$count += ${step}">+</button>
      </div>
    </section>
  `,
})
```

### Example: List Rendering with `html` composition

```js
rocket('demo-nav-list', {
  props: ({ array, object, string }) => ({
    items: array(object({
      href: string.trim.default('#'),
      label: string.trim.default('Untitled'),
    })),
    title: string.trim.default('Navigation'),
  }),
  render: ({ html, props: { items, title } }) => html`
    <nav aria-label="${title}">
      <h3>${title}</h3>
      <ul>
        ${items.map((item) => html`
          <li><a href="${item.href}">${item.label}</a></li>
        `)}
      </ul>
    </nav>
  `,
})
```

### Example: SVG

```js
rocket('demo-meter-ring', {
  props: ({ number, string }) => ({
    value: number.clamp(0, 100),
    stroke: string.default('#0f172a'),
  }),
  render: ({ html, svg, props: { value, stroke } }) => {
    const c = 2 * Math.PI * 28
    return html`
      <figure class="stack gap-2">
        ${svg`
          <svg viewBox="0 0 64 64" width="64" height="64" aria-hidden="true">
            <circle cx="32" cy="32" r="28" fill="none" stroke="#e5e7eb" stroke-width="8"></circle>
            <circle cx="32" cy="32" r="28" fill="none"
              stroke="${stroke}" stroke-width="8"
              stroke-dasharray="${c}"
              stroke-dashoffset="${c - (value / 100) * c}"
              transform="rotate(-90 32 32)"></circle>
          </svg>
        `}
        <figcaption>${value}%</figcaption>
      </figure>
    `
  },
})
```

## renderOnPropChange

```ts
renderOnPropChange?:
  | boolean
  | ((ctx: { host, props, changes }) => boolean)
```

Defaults to `true`. Rocket coalesces multiple prop updates in the same turn into a single queued render call. Prop updates still update `props` synchronously and notify `observeProps` immediately — only the DOM rerender is deduplicated.

Use this to skip rerendering when the component updates DOM imperatively (e.g. drawing on a canvas):

```js
rocket('demo-chart', {
  props: ({ json, string }) => ({
    series: json.default(() => []),
    theme: string.default('light'),
  }),
  mode: 'light',
  renderOnPropChange: ({ changes }) => 'theme' in changes,
  setup: ({ host, observeProps, props }) => {
    observeProps(() => {
      drawChart(host, props.series, props.theme)
    }, 'series', 'theme')
  },
  render: ({ html }) => html`<canvas width="640" height="320"></canvas>`,
})
```

## Conditional Rendering

`<template data-if>`, `data-else-if`, `data-else` are **Rocket-runtime** structural conditionals. They work **inside Rocket render output** (not as standalone Datastar plugins outside Rocket).

```js
rocket('demo-status', {
  mode: 'light',
  setup: ({ $$ }) => { $$.step = 0 },
  render: ({ html }) => html`
    <div class="stack gap-2">
      <button type="button" data-on:click="$$step = ($$step + 1) % 3">Next</button>

      <template data-if="$$step === 0">
        <p>Idle</p>
      </template>
      <template data-else-if="$$step === 1">
        <p>Loading</p>
      </template>
      <template data-else>
        <p>Ready</p>
      </template>
    </div>
  `,
})
```

Only one branch is mounted at a time. Switching unmounts the old branch and mounts a fresh one.

If you need an element to stay mounted and only toggle visibility, use `data-show` instead.

## Loop Rendering

`<template data-for>` is Rocket's structural list rendering.

```js
rocket('demo-letter-list', {
  mode: 'light',
  setup: ({ $$ }) => { $$.letters = ['A', 'B', 'C'] },
  render: ({ html }) => html`
    <ul>
      <template data-for="letter, row in $$letters">
        <li>
          <strong data-text="row + 1"></strong>
          <span data-text="letter"></span>
        </li>
      </template>
    </ul>
  `,
})
```

### Accepted Forms

| Form | Loop locals |
|------|-------------|
| `data-for="$$letters"` | `item`, `i` (defaults) |
| `data-for="letter in $$letters"` | `letter`, `i` |
| `data-for="letter, row in $$letters"` | `letter`, `row` |

The source can be any iterable expression — `$$items.filter(Boolean)`, `$page.items`, etc.

**No index-only form.** `data-for=", row in $$letters"` is invalid. To customize the index, also customize the item alias.

Loop aliases are only available inside Datastar expressions in the repeated subtree. Outside the loop body, normal component locals (`$$selected`) and global signals (`$page`) keep their meaning.

### Identity Caveat

Rocket keeps row slots **by position** and updates the current `item`/`i` bindings for each slot. If you reorder the source, Rocket does **not** preserve item identity across rows in this version.

## Local Actions

Register local actions inside `setup` with `action(name, fn)`. Templates call them as `@name(...)`.

```js
rocket('demo-copy', {
  props: ({ string, number }) => ({
    text: string.default('Copy me'),
    resetMs: number.min(100).default(1200),
  }),
  setup: ({ $$, $, action, actions, cleanup, props }) => {
    $$.copied = false
    $$.label = () => ($$.copied ? 'Copied' : 'Copy')
    $$.resetMsLabel = actions.intl('number', props.resetMs, { maximumFractionDigits: 0 }, 'en-US')
    let timerId = 0

    action('copy', async () => {
      await navigator.clipboard.writeText(props.text)
      $$.copied = true
      if ($.analyticsEnabled !== false) $.lastCopiedText = props.text
      clearTimeout(timerId)
      timerId = window.setTimeout(() => { $$.copied = false }, props.resetMs)
    })

    cleanup(() => clearTimeout(timerId))
  },
  render: ({ html, props: { text } }) => html`
    <button data-on:click="@copy()">
      <span data-text="$$label"></span>
      <small>${text} ($$resetMsLabel ms)</small>
    </button>
  `,
})
```

Prefer plain Datastar expressions (`data-on:click="$$count += 1"`) when state changes are simple. Use `action(name, fn)` only when the markup needs a named imperative entry point.

## Host Accessor Overrides

Each declared prop already has a host accessor. Use these only when the default isn't enough:

### `overrideProp(name, getter?, setter?)`

Wrap a declared prop's accessor. Useful when `host.value` should mirror a live inner control:

```js
overrideProp(
  'value',
  (getDefault) => refs.input?.value ?? getDefault(),
  (value, setDefault) => {
    const next = String(value ?? '')
    if (refs.input && refs.input.value !== next) refs.input.value = next
    setDefault(next)
  },
)
```

Omit `getter` to use `getDefault()`. Omit `setter` to use `setDefault(value)`.

> If the override depends on rendered refs, put it in `onFirstRender`, not `setup`.

### `defineHostProp(name, descriptor)`

Define a host-only property/method that is **not** a Rocket prop:

```js
setup: ({ defineHostProp }) => {
  defineHostProp('version', { get() { return '1' } })
  defineHostProp('reset', { value() { console.log('reset') } })
}
```

## observeProps

```ts
observeProps(fn: (props, changes) => void, ...propNames: string[]): void
```

Runs `fn` after decoding when matching props change. Receives the full normalized `props` object plus a `changes` object with only the props that changed. Omit `propNames` to watch all props.

```js
onFirstRender: ({ refs, observeProps }) => {
  observeProps((props, changes) => {
    if (!(refs.video instanceof HTMLVideoElement)) return
    if ('src' in changes) refs.video.src = props.src
    if ('currentTime' in changes) refs.video.currentTime = props.currentTime
  })
}
```

## Rocket Scope Rewriting

Inside rendered Datastar expressions, Rocket rewrites `$$name` to an instance-specific signal path under `$._rocket`.

The instance segment comes from the host element's `id` (normalized to a path-safe identifier). If the element has no `id`, Rocket generates a sequential fallback id.

```js
// You write:
html`
  <button data-on:click="$$count += 1"></button>
  <span data-text="$$count"></span>
`

// For <demo-counter id="inventory-panel">, Rocket rewrites to:
// <button data-on:click="_rocket.demo_counter.inventory_panel.count += 1"></button>
// <span data-text="_rocket.demo_counter.inventory_panel.count"></span>
```

Local `@actionName(args)` is rewritten similarly to a per-instance dispatch.

## Slots

Rocket supports default and named `<slot>` markers in both light DOM and shadow DOM.

In **shadow DOM**, this is real platform slotting.
In **light DOM**, Rocket replaces the `<slot>` placeholders with the host's original children (Rocket runtime feature).

```js
rocket('demo-card', {
  mode: 'light',
  render: ({ html }) => html`
    <article class="card">
      <header><slot name="header">Default Header</slot></header>
      <div class="body"><slot>Default content</slot></div>
      <footer><slot name="footer"></slot></footer>
    </article>
  `,
})
```

```html
<demo-card>
  <h2 slot="header">My Title</h2>
  <p>Card body content</p>
  <button slot="footer">Action</button>
</demo-card>
```

If a slot receives no matching host children, its **fallback content** renders instead.

## Complete Example

```html
<script type="module">
  import { rocket } from '/bundles/datastar-pro.js'

  rocket('demo-todo-app', {
    mode: 'light',
    props: ({ string, oneOf }) => ({
      title: string.trim.default('My Todos'),
      defaultPriority: oneOf('low', 'medium', 'high').default('medium'),
    }),
    setup: ({ $$, props }) => {
      $$.todos = []
      $$.newText = ''
      $$.newPriority = props.defaultPriority

      $$.empty = () => $$.todos.length === 0
    },
    render: ({ html, props: { title } }) => html`
      <section>
        <h2>${title}</h2>

        <div class="row gap-2">
          <input data-bind:new-text placeholder="Add todo..." />
          <select data-bind:new-priority>
            <option value="low">Low</option>
            <option value="medium">Medium</option>
            <option value="high">High</option>
          </select>
          <button data-on:click="
            $$todos = [...$$todos, {
              text: $$newText,
              done: false,
              priority: $$newPriority,
              id: Date.now()
            }];
            $$newText = ''
          ">Add</button>
        </div>

        <ul>
          <template data-for="todo, i in $$todos">
            <li data-class="{done: todo.done}">
              <input type="checkbox"
                data-on:click="$$todos = $$todos.map((t, idx) => idx === i ? {...t, done: !t.done} : t)" />
              <span data-text="todo.text"></span>
              <small data-text="'[' + todo.priority + ']'"></small>
              <button data-on:click="$$todos = $$todos.filter((_, idx) => idx !== i)">×</button>
            </li>
          </template>
        </ul>

        <template data-if="$$empty">
          <p>No todos yet!</p>
        </template>
      </section>
    `,
  })
</script>

<demo-todo-app title="Work Tasks"></demo-todo-app>
<demo-todo-app title="Personal" default-priority="high"></demo-todo-app>
```

## Common Mistakes

1. **Using the old template-based syntax.** `<template data-rocket:name>`, `data-prop:name="str"`, and `data-schema:name` are obsolete (pre-beta.1). Use the JS API: `rocket('tag-name', { ... })`.
2. **Reading refs in `setup`.** Refs don't exist yet during `setup`. Move ref-dependent code to `onFirstRender`.
3. **Sharing default objects across instances.** Pass a factory to `.default(() => ({...}))` for `json`, `js`, `array`, and `object` so each instance gets its own copy.
4. **Forgetting that booleans render as nothing.** In data positions, `true`/`false` render nothing. Use `String(value)` for literal text. In attribute positions, `true` writes the empty-string form.
5. **Calling `ctx.render(...)` for high-frequency updates.** Treat manual `render()` calls as coarse structural patches. For frequently-changing state, use signals and Datastar bindings.
6. **Reordering items in `data-for` and expecting identity preservation.** Rocket keeps row slots by position; reordering causes content to shift slots rather than DOM nodes to move.
7. **Mixing `data-if`/`data-for` with non-Rocket DOM.** These structural templates only work inside Rocket render output (not as standalone Datastar plugins on a regular page).
8. **Tag without a hyphen.** Custom-element names must contain a hyphen — `rocket('counter', ...)` will fail; use `rocket('demo-counter', ...)`.
