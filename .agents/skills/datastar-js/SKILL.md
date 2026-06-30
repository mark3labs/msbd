---
name: datastar-js
description: Client-side Datastar JS library reference (including Datastar Pro) for building reactive HTML with data-* attributes. Use whenever writing HTML templates with Datastar attributes like data-signals, data-bind, data-on, data-show, data-class, data-attr, data-text, data-effect, data-computed, data-indicator, data-init, data-ref, data-style, data-on-intersect, data-on-interval, data-on-signal-patch, data-on-signal-patch-filter, data-json-signals, data-ignore, data-ignore-morph, data-preserve-attr, or Pro attributes like data-animate, data-persist, data-query-string, data-match-media, data-replace-url, data-scroll-into-view, data-view-transition, data-custom-validity, data-on-raf, data-on-resize. Also use for SSE fetch actions (@get, @post, @put, @patch, @delete), other actions (@peek, @setAll, @toggleAll), Pro actions (@clipboard, @fit, @intl), or Datastar's reactive signal system ($signals). Covers expression syntax, modifiers, merge modes, and all plugins. Includes Datastar Pro's Rocket web-component API (JavaScript-based, beta.1+) for building custom elements with rocket(tagName, { mode, props, setup, render }) plus structural <template data-if> / data-else-if / data-else / data-for inside Rocket render output. Use this skill even if the user just says "make this reactive" or "add interactivity" in a Datastar project.
---

# Datastar JS — Client-Side Reactive Library

Datastar turns HTML `data-*` attributes into a reactive UI. No build step — just a `<script>` tag. Signals are reactive state, expressions reference them with `$`, actions trigger server communication via SSE.

## CRITICAL — Colon Syntax for Plugin:Key

Datastar v1.0 uses **colon (`:`)** to separate the plugin name from the key in attributes. This is different from older Datastar versions that used hyphens.

**Correct (v1.0):** `data-on:click`, `data-bind:query`, `data-attr:disabled`, `data-signals:count`
**Wrong:** `data-on-click`, `data-bind-query`, `data-attr-disabled`, `data-signals-count`

The colon separates `{plugin}` from `{key}`: `data-{plugin}:{key}="expression"`.

Plugins that don't take a dynamic key use the value form without a colon:
- `data-signals="{count: 0}"` — object form, no key
- `data-show="$visible"` — no key
- `data-effect="..."`, `data-init="..."`, `data-text="..."` — no key

Multi-word plugin names keep hyphens in the plugin name itself (these are separate plugins, NOT plugin:key pairs):
- `data-on-intersect`, `data-on-interval`, `data-on-signal-patch` — separate plugins
- `data-json-signals`, `data-ignore-morph` — separate plugins
- `data-scroll-into-view`, `data-view-transition`, `data-match-media` — Pro plugins

Modifiers always use double underscore `__` after the key (or after the plugin name if no key):
- `data-on:click__debounce.300ms` — plugin `on`, key `click`, modifier `debounce`
- `data-on:click__stop` — plugin `on`, key `click`, modifier `stop`
- `data-init__delay.500ms` — plugin `init`, no key, modifier `delay`
- `data-signals:count__ifmissing` — plugin `signals`, key `count`, modifier `ifmissing`

## CRITICAL — Never put a camelCase signal name in an attribute KEY

**The browser lowercases all HTML attribute _names_ before Datastar ever sees them.** An attribute _value_ is preserved verbatim, but the name is not. So a camelCase signal in the **key** position is silently mangled:

```html
<!-- You write this: -->
<input data-bind:envName />
<!-- The DOM actually stores: data-bind:envname  → Datastar creates signal `envname` -->
```

This is a **silent footgun**. Nothing errors. Two-way binding still appears to work locally. But you now have a signal named `envname`, NOT `envName` — so any code that references `$envName`, any `data-signals="{envName: ''}"` initializer, and any server handler that patches/clears `envName` (e.g. `MarshalAndPatchSignals`, `PatchSignals`) targets a **different signal** than the input is bound to. Classic symptom: **a form field that won't clear after a successful save**, because the server cleared `envName` while the input is wired to `envname`.

A `data-json-signals` dump makes the duplicate obvious:
```json
{ "envName": "",          // what data-signals + the server use (stays empty)
  "envname": "typed text" // what the lowercased input actually bound to
}
```

**This affects every plugin that takes a signal name in the KEY position:** `data-bind`, `data-signals`, `data-computed`, `data-indicator`, `data-ref`, `data-on:datastar-signal-patch-filter`. (It does NOT bite `data-attr`/`data-class`/`data-style` keys — those are HTML attrs / CSS props / classes, expected lowercase or kebab.)

**Two correct ways to use a camelCase signal name:**

```html
<!-- 1. VALUE form — attribute values are NOT lowercased, so the name is preserved -->
<input data-bind="envName" />
<div data-signals="{envName: ''}"></div>

<!-- 2. KEBAB key form — default __case.camel converts env-name → envName -->
<input data-bind:env-name />                    <!-- signal: envName -->
<div data-computed:full-name="$a + $b"></div>   <!-- signal: fullName -->
```

**Rule of thumb:** if a signal name has an uppercase letter, never write it after a colon. Use the value form (`data-bind="envName"`, `data-signals="{...}"`) or kebab-case the key and rely on the default camel casing. Keep client signal names and server JSON tags in the SAME case so the round-trip lines up.

## CRITICAL — Bundler Compatibility

The Datastar JS file (including Datastar Pro) is a **pre-built, self-initializing script**. It must NOT be processed by bundlers like esbuild, webpack, or rollup. These tools convert the self-executing code into an inert ESM module with exports, which breaks Datastar's auto-initialization.

**Correct:** Copy `datastar-pro.js` directly to your static assets directory and serve it as-is.
**Wrong:** Including it in an esbuild/webpack entry point or glob pattern like `assets/src/js/*.js`.

Include it with a `<script>` tag (module or regular):
```html
<script type="module" src="/assets/js/datastar-pro.js"></script>
```

## Expression Syntax

All attribute values are **JavaScript expressions** evaluated in a sandboxed context.

### Signal Access — `$`

`$` is the reactive signal store proxy. Access signals with dot notation:

```html
<!-- Reading -->
data-text="$count"
data-show="$user.loggedIn"
data-class:active="$tabs.current === 'home'"

<!-- Writing -->
data-on:click="$count++"
data-on:click="$user.name = 'Alice'"
```

`$signalName` is rewritten to `$['signalName']` internally. Deep paths like `$foo.bar` become `$['foo']['bar']`.

### Actions — `@`

Actions are callable functions available in expressions:

```html
data-on:click="@get('/api/data')"
data-on:submit="@post('/api/submit')"
data-on:click="@toggleAll({include: /^todo/})"
```

### Multiple Statements

Separate with `;`:

```html
data-on:click="$count++; @get('/api/refresh')"
```

### Event Object

In `data-on:*`, the `evt` variable holds the DOM event:

```html
data-on:keydown="if (evt.key === 'Enter') @post('/api/search')"
```

## Attribute Plugins

### `data-signals` — Declare Reactive State

Patches signals into the store. Replaces the old `data-store`.

```html
<!-- Object form (multiple signals) -->
<div data-signals="{count: 0, name: 'Alice', show: true}"></div>

<!-- Key form (single signal) — note the colon -->
<div data-signals:count="0"></div>
<div data-signals:user-name="'default'"></div>
```

**Modifier:** `__ifmissing` — only set if signal doesn't already exist:
```html
<div data-signals:count__ifmissing="0"></div>
```

Runs once on element load (not reactive). Use for initialization.

### `data-bind` — Two-Way Binding

Binds an element's value to a signal bidirectionally.

```html
<input type="text" data-bind:query />
<input type="checkbox" data-bind:agreed />
<input type="radio" data-bind:color value="red" />
<select data-bind:size>
  <option value="sm">Small</option>
  <option value="lg">Large</option>
</select>
<textarea data-bind:notes></textarea>
```

**Exclusive syntax:** use key (colon) OR value, not both:
```html
<input data-bind:query />        <!-- key form (colon) -->
<input data-bind="query" />      <!-- value form (no colon) -->
```

**camelCase signal names MUST use the value form** (or a kebab key): the browser lowercases attribute names, so `data-bind:myField` silently binds to signal `myfield`, not `myField`. Write `data-bind="myField"` or `data-bind:my-field` (→ `myField`). See the CRITICAL section near the top of this file.

**Element-specific:** text inputs sync `el.value`, number/range coerce to number, checkboxes sync `el.checked`, radios sync `el.value` when checked (auto-set `name` attr), file inputs → one-way `[{name, contents, mime}]` as base64, `<select multiple>` → array of selected values.

**Array signals:** If initial signal is an array, each bound element gets an indexed path (`signal.0`, `signal.1`, …). Creates signal automatically (with `ifMissing`) from element's current value.

**Modifiers:**

| Modifier | Effect |
|---|---|
| `__case` | Signal-name casing: `camel` (default), `kebab`, `snake`, `pascal` |
| `__prop` | Bind to a specific property instead of the default. Tag is the property name. Example: `data-bind:is-checked__prop.checked` |
| `__event` | Override which events sync the element back to the signal. Tags are event names. Example: `data-bind:query__event.input.change` |

Native form controls use their built-in binding semantics automatically. Generic custom elements default to `value` and `change`. Use `__prop` and `__event` when a custom element's live state lives somewhere else:

```html
<my-toggle data-bind:is-checked__prop.checked__event.change></my-toggle>
```

### `data-on` — Event Listeners

```html
<button data-on:click="$count++">+1</button>
<input data-on:keydown="if (evt.key === 'Enter') @post('/search')" />
<form data-on:submit="@post('/api/submit')">...</form>
```

Key = event name (after colon). Forms auto-`preventDefault()` on submit.

**Modifiers:**

| Modifier | Effect |
|---|---|
| `__debounce` | Debounce. Tags: `Nms`/`Ns`, `leading`, `notrailing` |
| `__throttle` | Throttle. Tags: `Nms`/`Ns`, `noleading`, `trailing` |
| `__delay` | Delay execution. Tags: `Nms`/`Ns` |
| `__prevent` | `evt.preventDefault()` |
| `__stop` | `evt.stopPropagation()` |
| `__self` | Only fire when `evt.target === el` |
| `__capture` | Capture phase listener |
| `__passive` | Don't call `preventDefault` |
| `__once` | Fire once |
| `__outside` | Fire only when click is outside element |
| `__window` | Attach to `window` instead of element |
| `__document` | Attach to `document` instead of element (useful for events only on `document` like `DOMContentLoaded`) |
| `__viewtransition` | Wrap in View Transition API |
| `__case` | Event-name casing tags: `camel`, `kebab` (default), `snake`, `pascal` |

```html
<input data-on:input__debounce.300ms="@get('/search')" />
<button data-on:click__once="@post('/init')">Init</button>
<div data-on:click__outside="$menuOpen = false">Menu</div>
```

**Special event names (used with `data-on:`):**
- `data-on:datastar-fetch` — listens on `document` for SSE fetch lifecycle events
- `data-on:datastar-signal-patch` — listens on `document` for signal change events

### `data-show` — Conditional Visibility

Shows/hides element via `display: none`.

```html
<div data-show="$isVisible">Content</div>
<span data-show="$count > 0">Has items</span>
```

Stores and restores original `display` value. Supports `__viewtransition` modifier.

### `data-class` — Conditional CSS Classes

```html
<!-- Key form (colon syntax — single class or space-separated classes) -->
<div data-class:hidden="!$isVisible"></div>
<div data-class:font-bold="$isActive"></div>

<!-- Object form (multiple classes) -->
<div data-class="{'bg-red-500': $hasError, 'opacity-50': $isLoading}"></div>
```

Key casing defaults to **kebab**. Classes are split on whitespace — `data-class:my-class` toggles `my-class`.

### `data-attr` — Sync HTML Attributes

```html
<!-- Key form (colon syntax) -->
<button data-attr:disabled="$isLoading"></button>
<input data-attr:placeholder="$hint" />

<!-- Object form -->
<button data-attr="{disabled: $isLoading, 'aria-busy': $isLoading}"></button>
```

- `true` or `''` → `setAttribute(key, '')` (boolean attribute)
- `false`/`null`/`undefined` → `removeAttribute(key)`
- String → `setAttribute(key, value)`

### `data-text` — Set Text Content

```html
<span data-text="$count"></span>
<p data-text="'Hello, ' + $name + '!'"></p>
```

Sets `el.textContent`. Supports `__viewtransition` modifier.

### `data-style` — Reactive Inline Styles

```html
<!-- Key form (colon syntax) -->
<div data-style:color="$textColor"></div>
<div data-style:font-size="$size + 'px'"></div>

<!-- Object form -->
<div data-style="{color: $textColor, opacity: $isActive ? 1 : 0.5}"></div>
```

Key casing defaults to **kebab**. Uses `MutationObserver` to stay in sync.

**Empty / null / undefined / false values** restore the original inline style value if one existed, or remove the property if there was no initial value. This means you can use `&&` for conditional styles:

```html
<!-- When $x is false, color reverts to red from inline style -->
<div style="color: red;" data-style:color="$x && 'green'"></div>

<!-- When $hiding is true, display becomes none; when false, reverts to flex -->
<div style="display: flex;" data-style:display="$hiding && 'none'"></div>
```

### `data-computed` — Derived Signals

```html
<!-- Key form (colon syntax) -->
<div data-computed:full-name="$firstName + ' ' + $lastName"></div>

<!-- Object form -->
<div data-computed="{
  fullName: () => $firstName + ' ' + $lastName,
  total: () => $price * $quantity
}"></div>
```

Object form values **must** be functions (`() => ...`). Computed signals update reactively when dependencies change. Persist forever (no cleanup on element removal).

### `data-effect` — Reactive Side Effects

```html
<div data-effect="console.log('count is', $count)"></div>
<div data-effect="document.title = 'Count: ' + $count"></div>
```

Runs immediately, re-runs when any referenced signal changes. No key allowed.

### `data-init` — Run Once on Load

```html
<div data-init="$count = 0"></div>
<div data-init="@get('/api/initial-data')"></div>
<div data-init__delay.500ms="@get('/api/lazy-load')"></div>
```

Fires once. Supports `__delay` (with duration tags) and `__viewtransition` modifiers.

### `data-ref` — Element Reference

```html
<canvas data-ref:myCanvas></canvas>
```

Stores the DOM element as a signal: `$myCanvas` === the `<canvas>` element. Exclusive key/value syntax.

### `data-indicator` — SSE Loading State

```html
<div data-indicator:is-loading>
  <button data-on:click="@get('/api/data')">Load</button>
  <span data-show="$isLoading">Loading...</span>
</div>
```

Creates a boolean signal (`false` by default). Automatically set to `true` when an SSE fetch starts from this element, `false` when it finishes. Scoped to the element.

Exclusive key/value syntax. Key casing modifiers supported.

### `data-on-intersect` — Intersection Observer

This is a **separate plugin** (not `data-on:intersect`). Uses hyphens in the plugin name.

```html
<div data-on-intersect="@get('/api/lazy')">Lazy loaded content</div>
<div data-on-intersect__once__half="@get('/api/visible')">...</div>
```

**Modifiers:**

| Modifier | Effect |
|---|---|
| `__once` | Fire once, then disconnect observer |
| `__exit` | Trigger when the element **exits** the viewport (instead of enters) |
| `__half` | Threshold 0.5 (50% visible) |
| `__full` | Threshold 1.0 (fully visible) |
| `__threshold` | Custom threshold. Tag = percentage. Example: `__threshold.25` (25% visible), `__threshold.75` (75% visible) |
| `__viewtransition` | Wrap in View Transition API |

Plus all timing modifiers (`__delay`, `__debounce`, `__throttle`).

### `data-on-interval` — Periodic Execution

This is a **separate plugin** (not `data-on:interval`). Uses hyphens in the plugin name.

```html
<div data-on-interval.1s="@get('/api/poll')">Polls every 1s</div>
<div data-on-interval.5000ms="$elapsed += 5">Timer</div>
```

Duration from modifier tags: `Nms` or `Ns`. Supports `__viewtransition` modifier.

### `data-on-signal-patch` — React to Signal Changes

This is a **separate plugin** (not `data-on:signal-patch`). Uses hyphens in the plugin name. Provides additional modifiers (`__include`/`__exclude`) not available via `data-on:datastar-signal-patch`.

Fires whenever signals are patched. Modifiers: `__include` / `__exclude` (tags are regex patterns), plus all timing modifiers.

```html
<div data-on-signal-patch="@post('/api/autosave')"></div>
<div data-on-signal-patch__include./^form/__debounce.500ms="@post('/save')"></div>
```

### `data-json-signals` — Debug Signal Display

```html
<pre data-json-signals></pre>
<pre data-json-signals__terse></pre>
<pre data-json-signals="{include: /^user/}"></pre>
```

Renders the signal store as pretty-printed JSON. `__terse` = compact (0-space indent). Optional filter: `{include: RegExp, exclude: RegExp}`.

## SSE Fetch Actions

The core server communication mechanism. All use the Fetch API (not `EventSource`) to stream SSE.

### HTTP Methods

```html
data-on:click="@get('/api/data')"
data-on:click="@post('/api/submit')"
data-on:click="@put('/api/update')"
data-on:click="@patch('/api/partial')"
data-on:click="@delete('/api/remove')"
```

**Default behavior:** GET keeps connection open only while tab visible. POST/PUT/PATCH/DELETE keep connection alive even when tab is hidden.

### Signal Serialization

Headers auto-set: `Accept: text/event-stream, text/html, application/json`, `Datastar-Request: true`, `Content-Type: application/json` (POST/PUT/PATCH).

- **POST/PUT/PATCH:** Body = `JSON.stringify(filteredSignals)`
- **GET/DELETE:** URL param `?datastar=<JSON>`
- **Default filter:** includes all signals, excludes `_`-prefixed at any nesting level (`/(^|\.)_/`)

### Options (2nd argument)

```html
data-on:click="@post('/api/submit', {contentType: 'form', selector: '#myform'})"
```

| Option | Type | Default | Description |
|---|---|---|---|
| `contentType` | `'json' \| 'form'` | `'json'` | Body serialization format |
| `selector` | `string` | — | CSS selector for form element (with `contentType: 'form'`) |
| `headers` | `object` | — | Additional HTTP headers |
| `filterSignals` | `{include?, exclude?}` | excl. `_`-prefixed | Signal filter regexes |
| `payload` | `any` | — | Override auto-serialized body |
| `openWhenHidden` | `boolean` | GET:false, others:true | Keep alive when tab hidden |
| `requestCancellation` | `string \| AbortController` | `'auto'` | `'auto'`, `'cleanup'`, `'disabled'` |
| `retry` | `string` | `'auto'` | `'auto'`, `'error'`, `'always'`, `'never'` |
| `retryInterval` | `number` | `1000` | Base retry ms |
| `retryScaler` | `number` | `2` | Exponential backoff multiplier |
| `retryMaxWaitMs` | `number` | `30000` | Max retry delay |
| `retryMaxCount` | `number` | `10` | Max retries before giving up |

### Form Submission

```html
<form data-on:submit="@post('/api/submit', {contentType: 'form'})">
  <input name="email" type="email" />
  <button type="submit">Submit</button>
</form>
```

- Validates via `checkValidity()` / `reportValidity()` before sending
- `multipart/form-data` enctype → sends raw `FormData`
- Otherwise → `application/x-www-form-urlencoded`
- Submit buttons with `name` attribute get their value included

### Request Cancellation

`'auto'` (default) aborts previous request from same element. `'cleanup'` also aborts on element removal. `'disabled'` = no auto-cancellation.

### Non-SSE Response Handling

Also handles non-SSE responses: `text/html` → patches DOM, `application/json` → patches signals, `text/javascript` → executes script. Reads merge config from response headers (`datastar-selector`, `datastar-mode`, etc.).

## SSE Watchers (Server → Client)

These process SSE events from the server. The Go SDK sends these automatically.

### `datastar-patch-elements` — DOM Updates

Server sends HTML fragments. Client merges them into the DOM.

**SSE data fields:**

| Field | Default | Description |
|---|---|---|
| `elements` | required | HTML content |
| `selector` | `''` | CSS selector for target. Empty = use element IDs from content |
| `mode` | `'outer'` | Merge mode (see below) |
| `namespace` | `'html'` | `html`, `svg`, `mathml` |
| `useViewTransition` | `''` | `'true'` to enable |

**Merge modes:**

| Mode | Behavior |
|---|---|
| `outer` | **Default.** Morphs target element (preserves state) |
| `inner` | Morphs children of target |
| `replace` | Full replacement (no morph, resets state) |
| `prepend` | Prepend inside target |
| `append` | Append inside target |
| `before` | Insert before target |
| `after` | Insert after target |
| `remove` | Remove target element(s) |

**Morph behavior:** Built-in morph (not morphdom/idiomorph). Tracks persistent IDs for optimal reuse. Preserves form state. `data-ignore-morph` skips morphing. `data-*-preserve-attr="name1 name2"` preserves listed attributes. Scripts are re-executed.

### `datastar-patch-signals` — Signal Updates

Server sends JSON to merge into the client signal store.

**SSE data fields:**

| Field | Default | Description |
|---|---|---|
| `signals` | required | JSON string of signals to merge |
| `onlyIfMissing` | `false` | Only set signals that don't exist |

## Other Actions

### `@peek(fn)` — Read Without Subscribing

```html
data-effect="console.log(@peek(() => $count))"
```

Reads signal values without creating a dependency. The enclosing effect won't re-run when peeked signals change.

### `@setAll(value, filter?)` — Bulk Set

```html
data-on:click="@setAll(false, {include: /^checkbox/})"
```

Sets every matching signal to `value`.

### `@toggleAll(filter?)` — Bulk Toggle

```html
data-on:click="@toggleAll({include: /^selected/})"
```

Toggles (`!value`) every matching signal.

## Other Attribute Plugins

### `data-ignore` — Skip Datastar Processing

Tells Datastar to ignore an element and its descendants (no plugin processing).

```html
<div data-ignore data-show-thirdpartylib="">
  <div>Datastar will not process this element.</div>
</div>
```

**Modifier:** `__self` — only ignore the element itself, not its descendants.

Useful for preventing naming conflicts with third-party libraries or when you can't escape user input.

### `data-ignore-morph` — Skip Morphing

Tells the `PatchElements` watcher to skip morphing an element and its children.

```html
<div data-ignore-morph>
  This element will not be morphed when patched.
</div>
```

To remove the behavior, patch the element with the `data-ignore-morph` attribute removed.

### `data-preserve-attr` — Preserve Attribute Values During Morph

Keeps specific attribute values intact when the element is morphed.

```html
<details open data-preserve-attr="open">
  <summary>Title</summary>
  Content
</details>
```

Multiple attributes can be space-separated:

```html
<details open class="foo" data-preserve-attr="open class">
  <summary>Title</summary>
  Content
</details>
```

### `data-on-signal-patch-filter` — Filter Signal Patch Events

Filters which signal changes a `data-on-signal-patch` listener responds to. Used as a sibling attribute on the same element.

```html
<!-- React only to changes on the `counter` signal -->
<div data-on-signal-patch="@post('/save')"
     data-on-signal-patch-filter="{include: /^counter$/}"></div>

<!-- React to all changes except those ending with "changes" -->
<div data-on-signal-patch="..."
     data-on-signal-patch-filter="{exclude: /changes$/}"></div>

<!-- Combine include and exclude -->
<div data-on-signal-patch="..."
     data-on-signal-patch-filter="{include: /user/, exclude: /password/}"></div>
```

## Modifier Syntax

Modifiers are appended with `__` (double underscore) after the key. Tags follow with `.`:

```
data-on:click__debounce.300ms__prevent="..."
data-on:input__throttle.500ms="..."
data-init__delay.1s__viewtransition="..."
data-signals:count__ifmissing="0"
```

### Timing Modifiers (shared across plugins)

| Modifier | Tags | Default behavior |
|---|---|---|
| `__debounce` | `Nms`/`Ns`, `leading`, `notrailing` | Trailing edge, no leading |
| `__throttle` | `Nms`/`Ns`, `noleading`, `trailing` | Leading edge, no trailing |
| `__delay` | `Nms`/`Ns` | Simple setTimeout |

### Casing Modifier

`__case` with tags: `camel` (default for most), `kebab` (default for `data-class`, `data-on`), `snake`, `pascal`.

Transforms the key portion of the attribute name (e.g., `data-bind:my-input__case.snake` → signal name `my_input`).

## Common Patterns

### SSE-Driven UI

```html
<div data-signals="{search: '', results: []}">
  <input data-bind:search
         data-on:input__debounce.300ms="@get('/api/search')" />
  <div id="results">
    <!-- Server patches this via SSE -->
  </div>
</div>
```

### Loading Indicator

```html
<div data-indicator:loading>
  <button data-on:click="@post('/api/action')">
    <span data-show="!$loading">Go</span>
    <span data-show="$loading">Loading...</span>
  </button>
</div>
```

### Computed + Effect

```html
<div data-signals="{firstName: 'John', lastName: 'Doe'}"
     data-computed:full-name="$firstName + ' ' + $lastName"
     data-effect="document.title = $fullName">
  <input data-bind:first-name />
  <input data-bind:last-name />
  <span data-text="$fullName"></span>
</div>
```

### Polling

```html
<div data-on-interval.5s="@get('/api/status')">
  <div id="status">Waiting...</div>
</div>
```

### Infinite Scroll

```html
<div id="feed">
  <!-- items -->
  <div data-on-intersect__once="@get('/api/feed?page=2')">
    Loading more...
  </div>
</div>
```

### Click Outside to Close

```html
<div data-signals="{menuOpen: false}">
  <button data-on:click="$menuOpen = !$menuOpen">Menu</button>
  <div data-show="$menuOpen"
       data-on:click__outside="$menuOpen = false">
    Menu content
  </div>
</div>
```

## Datastar Pro

Pro adds plugins on top of the open-source core. Requires the `datastar-pro.js` bundle. The core engine and all open-source plugins above work identically in both bundles.

**Read `references/datastar-pro.md` for the full Pro reference** when working with any Pro feature.

### Quick Reference — Pro Plugins

**Attributes:**
- `data-persist` — Two-way sync signals to localStorage/sessionStorage
- `data-query-string` — Sync signals ↔ URL query parameters (modifiers: `__filter`, `__history`)
- `data-replace-url` — Reactively replace browser URL via `history.replaceState()`
- `data-match-media` — Reactive media query matching → boolean signal
- `data-animate` — Animate numeric attributes with easing
- `data-scroll-into-view` — Scroll element into view on load (`__smooth`/`__instant`/`__auto`, alignment, `__focus`)
- `data-view-transition` — Set `viewTransitionName` reactively
- `data-custom-validity` — Custom form validation messages
- `data-on-raf` — Run expression every `requestAnimationFrame`
- `data-on-resize` — Run expression on element resize (ResizeObserver)

**Actions:**
- `@clipboard(text, isBase64?)` — Copy to clipboard
- `@fit(v, oldMin, oldMax, newMin, newMax, clamp?, round?)` — Linearly map value between ranges
- `@intl(type, value, options?, locales?)` — Intl formatting (datetime, number, list, etc.)

### Rocket — Web-Component API (JavaScript)

> **Rocket has been rewritten as a JavaScript API in beta.1.** The old `<template data-rocket:*>` markup with `data-prop:*`, `data-schema:*` is **obsolete** — there is no automatic upgrade path.

Define custom elements with the `rocket()` function imported from the bundle:

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
      $$.count = props.count                     // create local signal
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

**Key points:**
- `rocket(tag, options)` — `tag` must contain a hyphen.
- **Codecs** (`number`, `string`, `bool`, `date`, `json`, `js`, `bin`, `array`, `object`, `oneOf`) are immutable builders chained fluently: `string.trim.lower.kebab.maxLength(48)`.
- `setup` runs once per instance, before initial render. Use it for local signals (`$$`), prop observers, timers, cleanup, and local actions.
- `onFirstRender` runs after the first render — use it when code depends on `data-ref:*` refs or measured DOM.
- `render` returns Rocket `html` or `svg` tagged-template output.
- `$$name` in templates is rewritten to `$._rocket.<tag>.<id>.<name>` per instance — each instance has isolated local state.
- Local actions registered with `action('copy', fn)` are callable from markup as `@copy()`.
- **Structural templates inside Rocket render** — these only work inside Rocket render output:
  - `<template data-if="...">`, `<template data-else-if="...">`, `<template data-else>` — only one branch is mounted.
  - `<template data-for="item, i in $$items">` — accepts `expr`, `item in expr`, or `item, i in expr` (no index-only form).
- `manifest: { slots, events }` adds documentation metadata; `publishRocketManifests({ endpoint })` posts the full manifest JSON.
- Available exports from the bundle: `rocket`, `createCodec`, `publishRocketManifests`, types `Codec`, `CodecRegistry`, `RocketDefinition`, plus signal helpers (`signal`, `computed`, `effect`, `mergePatch`, etc.).

**See `references/rocket.md` for the full Rocket reference** including all codec methods, the complete setup context (`$$`, `effect`, `apply`, `cleanup`, `actions`, `action`, `observeProps`, `overrideProp`, `defineHostProp`, `render`), `onFirstRender`, slots, scoped expression rewriting, and complete examples.

## Common Mistakes

1. **Using hyphens instead of colons for plugin:key** — `data-on-click` is WRONG. Use `data-on:click`. The colon separates the plugin name from the key. Only multi-word plugin names like `data-on-intersect`, `data-on-interval`, `data-json-signals` use hyphens (because the entire hyphenated string is the plugin name).
2. **Using `data-store` instead of `data-signals`** — `data-store` is Datastar v0.x. Current version uses `data-signals`.
3. **Forgetting `$` prefix** — `data-show="isVisible"` evaluates the identifier `isVisible` (undefined). Use `data-show="$isVisible"`.
4. **Using `@` actions outside event handlers** — `@get()` triggers an SSE connection. Place in `data-on:*`, `data-init`, `data-on-intersect`, `data-on-interval`, not in reactive attributes like `data-show` or `data-text`.
5. **Missing element IDs for SSE patches** — Server `PatchElements` in `outer` mode (default) matches by element ID. Ensure target elements have `id` attributes.
6. **`data-bind` with both key and value** — It's exclusive: `data-bind:name` OR `data-bind="name"`, never both.
7. **camelCase signal name after a colon** — `data-bind:envName` / `data-signals:envName` are silently lowercased by the browser to bind signal `envname`, NOT `envName`. The form looks fine until the server tries to patch/clear `envName` and nothing happens (e.g. a field that won't reset after save). Use the value form `data-bind="envName"` / `data-signals="{envName: ''}"`, or a kebab key `data-bind:env-name` (→ `envName`). See the CRITICAL section near the top.
8. **`data-computed` object values not functions** — Object form requires `() =>` functions: `data-computed="{total: () => $a + $b}"`, not `{total: $a + $b}`.
9. **Signals starting with `_` excluded from fetch** — Default `filterSignals` excludes `_`-prefixed signals. Use `filterSignals: {include: /./}` to override.
10. **Expecting `data-on:submit` to NOT preventDefault** — Forms auto-preventDefault on submit. This is intentional; use SSE actions for form handling.
11. **Running Datastar through a JS bundler** — Do NOT process `datastar-pro.js` with esbuild, webpack, or rollup. The bundler converts the self-executing script into an inert module with exports, breaking auto-initialization. Copy and serve the file as-is.
12. **Using the old Rocket markup syntax** — `<template data-rocket:my-component>`, `data-prop:name="str"`, and `data-schema:name` are **obsolete** (pre-beta.1). The current Rocket API is JavaScript-based: `import { rocket } from '/path/to/datastar-pro.js'` and call `rocket('tag-name', { mode, props, setup, render })`. See `references/rocket.md`.
