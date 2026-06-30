---
name: datastar-go
description: Go SDK for building Datastar SSE backends. Use whenever writing Go HTTP handlers that stream SSE events to Datastar clients — patching DOM elements, updating signals, executing scripts, redirecting, or reading signals from requests. Covers the full datastar-go API including NewSSE, ReadSignals, PatchElements, PatchElementTempl, PatchSignals, ExecuteScript, Redirect, and all functional options. Use this skill whenever writing server-side Datastar handlers in Go, even if the user just says "add an endpoint" or "make this interactive" in a Datastar + Go project.
---

# datastar-go — Go SDK for Datastar SSE Backends

**Import:** `github.com/starfederation/datastar-go/datastar`
**Go version:** 1.24+

## Critical Rule: ReadSignals Before NewSSE

For non-GET requests, `ReadSignals` reads the request body. `NewSSE` takes ownership of the response writer. Wrong order = closed body error.

```go
// ✅ CORRECT
signals := &MySignals{}
datastar.ReadSignals(r, signals)  // reads body
sse := datastar.NewSSE(w, r)     // takes over writer

// ❌ WRONG — body already consumed
sse := datastar.NewSSE(w, r)
datastar.ReadSignals(r, signals)  // ERROR
```

For GET requests the order doesn't matter (signals come from `?datastar=` query param), but always put `ReadSignals` first as a habit.

## Handler Pattern

Every Datastar handler follows this shape:

```go
func myHandler(w http.ResponseWriter, r *http.Request) {
    // 1. Read signals (if needed)
    signals := &MySignals{}
    if err := datastar.ReadSignals(r, signals); err != nil {
        http.Error(w, err.Error(), http.StatusBadRequest)
        return
    }

    // 2. Create SSE stream
    sse := datastar.NewSSE(w, r)

    // 3. Do work + send events
    sse.PatchElements(`<div id="result">Done</div>`)
}
```

## Reading Signals

```go
func ReadSignals(r *http.Request, signals any) error
```

Unmarshals Datastar signals into a struct pointer. Source depends on HTTP method:
- **GET**: `?datastar=<JSON>` query parameter
- **POST/PUT/PATCH/DELETE**: JSON request body

```go
type SearchSignals struct {
    Query string `json:"query"`
    Page  int    `json:"page"`
}
```

Returns `nil` if GET with no `datastar` query param. Returns descriptive error if body already closed.

> **camelCase round-trip gotcha.** A struct field `json:"envName"` only lines up with the client if the input is bound with `data-bind="envName"` (value form) or `data-bind:env-name` (kebab key). The browser lowercases attribute _names_, so the **key form `data-bind:envName` silently binds the wrong signal** (`envname`) — your `ReadSignals` may still populate (Go JSON matching is case-insensitive) but a later `PatchSignals`/`MarshalAndPatchSignals` that clears `envName` updates a signal the input isn't watching, so the field never resets. Keep client signal names and Go json tags in the same case, and never put a camelCase signal after a `:` on the client. See the camelCase CRITICAL section in the `datastar-js` skill.

## Creating the SSE Stream

```go
func NewSSE(w http.ResponseWriter, r *http.Request, opts ...SSEOption) *ServerSentEventGenerator
```

Sets headers (`text/event-stream`, `no-cache`, `keep-alive`) and flushes immediately. Panics if initial flush fails.

### SSE Options

| Option | Purpose |
|---|---|
| `WithContext(ctx)` | Override context (must derive from request ctx). Useful for passing values to templ components. |
| `WithCompression(opts...)` | Enable stream compression (brotli/zstd/gzip/deflate). |

### Utility Methods

```go
sse.Context()   // returns the SSE context
sse.IsClosed()  // true if connection is dead
```

## Patching Elements (DOM Updates)

### Core Method

```go
func (sse *SSE) PatchElements(elements string, opts ...PatchElementOption) error
```

Sends HTML fragment(s) to merge into the DOM. Default mode: `outer` (morph).

### Convenience Methods

| Method | Purpose |
|---|---|
| `PatchElementf(format, args...)` | Sprintf-style HTML |
| `PatchElementTempl(component, opts...)` | Render templ component & patch |
| `PatchElementGostar(child, opts...)` | Render GoStar component & patch |
| `RemoveElement(selector, opts...)` | Remove by CSS selector |
| `RemoveElementf(selectorFmt, args...)` | Remove with Sprintf selector |
| `RemoveElementByID(id)` | Remove by `#id` |

### PatchElement Options

**Selector** (which element to target):
```go
WithSelector("#my-element")         // CSS selector
WithSelectorf("#item-%d", id)       // formatted selector
WithSelectorID("my-element")        // shorthand for "#my-element"
```

**Mode** (how the fragment is applied):

| Option | Value | Behavior |
|---|---|---|
| `WithModeOuter()` | `"outer"` | **Default.** Morph into existing element |
| `WithModeInner()` | `"inner"` | Replace inner HTML |
| `WithModeRemove()` | `"remove"` | Remove the element |
| `WithModeReplace()` | `"replace"` | Full replace (no morphing, resets state) |
| `WithModePrepend()` | `"prepend"` | Prepend inside element |
| `WithModeAppend()` | `"append"` | Append inside element |
| `WithModeBefore()` | `"before"` | Insert before element |
| `WithModeAfter()` | `"after"` | Insert after element |

**Namespace** (for SVG/MathML):
```go
WithNamespaceHTML()    // default (not sent on wire)
WithNamespaceSVG()
WithNamespaceMathML()
```

**View Transitions:**
```go
WithViewTransitions()       // enable
WithoutViewTransitions()    // disable
WithUseViewTransitions(b)   // bool toggle
```

**Event metadata:**
```go
WithPatchElementsEventID(id)  // SSE event ID
WithRetryDuration(d)          // override retry
```

### Example: Templ Component Patch

```go
func handler(w http.ResponseWriter, r *http.Request) {
    sse := datastar.NewSSE(w, r)

    // Render a templ component and send it
    sse.PatchElementTempl(views.MyComponent(data))

    // Or with options
    sse.PatchElementTempl(
        views.ItemList(items),
        datastar.WithSelectorID("item-list"),
        datastar.WithModeInner(),
    )
}
```

### Example: Raw HTML Patch

```go
sse.PatchElements(`<div id="counter">42</div>`)

// Append to a list
sse.PatchElements(
    `<li>New item</li>`,
    datastar.WithSelector("#my-list"),
    datastar.WithModeAppend(),
)
```

### Example: Remove Elements

```go
sse.RemoveElement("#notification")
sse.RemoveElementByID("old-item")
sse.RemoveElementf("#item-%d", itemID)
```

## Patching Signals (Client State)

### Core Method

```go
func (sse *SSE) PatchSignals(signalsContents []byte, opts ...PatchSignalsOption) error
```

Sends raw JSON bytes to merge into client signal store.

### Convenience Methods

| Method | Purpose |
|---|---|
| `MarshalAndPatchSignals(signals, opts...)` | Marshal struct → JSON, then patch. **Panics** on marshal failure. |
| `MarshalAndPatchSignalsIfMissing(signals, opts...)` | Only patch signals that don't exist on client |
| `PatchSignalsIfMissingRaw(jsonString)` | Raw JSON string, only if missing |

### PatchSignals Options

| Option | Purpose |
|---|---|
| `WithOnlyIfMissing(bool)` | Only set signals that don't already exist |
| `WithPatchSignalsEventID(id)` | SSE event ID |
| `WithPatchSignalsRetryDuration(d)` | Override retry duration |

### Example

```go
type CounterSignals struct {
    Count int  `json:"count"`
    Show  bool `json:"show"`
}

sse.MarshalAndPatchSignals(&CounterSignals{Count: 42, Show: true})

// Only set defaults (don't overwrite existing client values)
sse.MarshalAndPatchSignalsIfMissing(&CounterSignals{Count: 0, Show: false})
```

## Executing Scripts

### Core Method

```go
func (sse *SSE) ExecuteScript(scriptContents string, opts ...ExecuteScriptOption) error
```

Sends a `<script>` appended to `<body>`. Auto-removes after execution by default (`data-effect="el.remove()"`).

### Convenience Methods

| Method | Purpose |
|---|---|
| `ConsoleLog(msg, opts...)` | `console.log("msg")` |
| `ConsoleLogf(format, args...)` | Formatted console.log |
| `ConsoleError(err, opts...)` | `console.error("err")` |

### ExecuteScript Options

| Option | Purpose |
|---|---|
| `WithExecuteScriptAutoRemove(bool)` | Auto-remove after execution (default: `true`) |
| `WithExecuteScriptAttributes(attrs...)` | Raw attributes, e.g. `type="module"` |
| `WithExecuteScriptAttributeKVs(kvs...)` | Key-value pairs. **Panics on odd count.** |
| `WithExecuteScriptEventID(id)` | SSE event ID |
| `WithExecuteScriptRetryDuration(d)` | Override retry duration |

### Example

```go
sse.ExecuteScript(`alert("Hello!")`)
sse.ConsoleLog("debug info")
sse.ConsoleError(fmt.Errorf("something broke"))
```

## Navigation & URL

All implemented via `ExecuteScript` internally.

```go
// Client-side redirect
sse.Redirect("/dashboard")
sse.Redirectf("/users/%d", userID)

// Replace browser history entry (no navigation)
sse.ReplaceURL(newURL)
sse.ReplaceURLQuerystring(r, url.Values{"page": {"2"}})

// Prefetch via Speculation Rules API
sse.Prefetch("/next-page", "/other-page")
```

## Custom DOM Events

```go
func (sse *SSE) DispatchCustomEvent(eventName string, detail any, opts ...DispatchCustomEventOption) error
```

Dispatches a `CustomEvent` on targeted elements. Defaults: `selector="document"`, `bubbles=true`, `cancelable=true`, `composed=true`.

| Option | Purpose |
|---|---|
| `WithDispatchCustomEventSelector(sel)` | Target selector (default: `"document"`) |
| `WithDispatchCustomEventBubbles(b)` | Event bubbles |
| `WithDispatchCustomEventCancelable(b)` | Event cancelable |
| `WithDispatchCustomEventComposed(b)` | Event crosses shadow DOM |

## SSE Action Attribute Helpers

Standalone functions for generating `data-on-*` attribute values in templ templates:

```go
datastar.GetSSE("/api/search")      // → `@get('/api/search')`
datastar.PostSSE("/api/submit")     // → `@post('/api/submit')`
datastar.PutSSE("/api/update/%d", id)
datastar.PatchSSE("/api/partial")
datastar.DeleteSSE("/api/item/%d", id)
```

Use in templ:
```templ
<button data-on-click={ datastar.GetSSE("/api/refresh") }>
    Refresh
</button>

<form data-on-submit={ datastar.PostSSE("/api/submit") }>
    <input data-bind-search type="text"/>
    <button type="submit">Search</button>
</form>
```

## Low-Level Send

For custom event types or advanced use cases:

```go
func (sse *SSE) Send(eventType EventType, dataLines []string, opts ...SSEEventOption) error
```

| Option | Purpose |
|---|---|
| `WithSSEEventId(id)` | Set `id:` field |
| `WithSSERetryDuration(d)` | Override retry (default 1000ms) |

## Constants & Enums

### Event Types

```go
EventTypePatchElements = "datastar-patch-elements"
EventTypePatchSignals  = "datastar-patch-signals"
```

No separate script event type — scripts are sent via `PatchElements` (append `<script>` to body).

### Element Patch Modes

```go
ElementPatchModeOuter   // "outer" — default, morph
ElementPatchModeInner   // "inner"
ElementPatchModeRemove  // "remove"
ElementPatchModeReplace // "replace"
ElementPatchModePrepend // "prepend"
ElementPatchModeAppend  // "append"
ElementPatchModeBefore  // "before"
ElementPatchModeAfter   // "after"
```

### Namespaces

```go
NamespaceHTML    // "html"
NamespaceSVG     // "svg"
NamespaceMathML  // "mathml"
```

### Defaults

| Setting | Default |
|---|---|
| SSE retry duration | `1000ms` |
| Element patch mode | `outer` (morph) |
| View transitions | `false` |
| Only-if-missing | `false` |
| Script auto-remove | `true` |
| Query param key | `"datastar"` |

## Complete Handler Examples

### Search with Templ

```go
type SearchSignals struct {
    Query string `json:"query"`
}

func handleSearch(w http.ResponseWriter, r *http.Request) {
    signals := &SearchSignals{}
    if err := datastar.ReadSignals(r, signals); err != nil {
        http.Error(w, err.Error(), http.StatusBadRequest)
        return
    }

    sse := datastar.NewSSE(w, r)

    results := db.Search(signals.Query)
    sse.PatchElementTempl(views.SearchResults(results))
}
```

### Counter with Signal Update

```go
type CounterSignals struct {
    Count int `json:"count"`
}

func handleIncrement(w http.ResponseWriter, r *http.Request) {
    signals := &CounterSignals{}
    if err := datastar.ReadSignals(r, signals); err != nil {
        http.Error(w, err.Error(), http.StatusBadRequest)
        return
    }

    sse := datastar.NewSSE(w, r)

    signals.Count++
    sse.MarshalAndPatchSignals(signals)
    sse.PatchElementTempl(views.Counter(signals.Count))
}
```

### Form Submit with Redirect

```go
type FormSignals struct {
    Name  string `json:"name"`
    Email string `json:"email"`
}

func handleSubmit(w http.ResponseWriter, r *http.Request) {
    signals := &FormSignals{}
    if err := datastar.ReadSignals(r, signals); err != nil {
        http.Error(w, err.Error(), http.StatusBadRequest)
        return
    }

    sse := datastar.NewSSE(w, r)

    if err := db.CreateUser(signals.Name, signals.Email); err != nil {
        sse.PatchElementTempl(views.ErrorMessage("Failed to create user"))
        return
    }

    sse.Redirect("/users")
}
```

### Streaming Updates (Long-Running)

```go
func handleProgress(w http.ResponseWriter, r *http.Request) {
    sse := datastar.NewSSE(w, r)

    for i := 0; i <= 100; i += 10 {
        if sse.IsClosed() {
            return
        }
        sse.PatchElementf(`<div id="progress">%d%%</div>`, i)
        time.Sleep(500 * time.Millisecond)
    }

    sse.PatchElements(`<div id="progress">Complete!</div>`)
}
```

### Append to List

```go
func handleAddItem(w http.ResponseWriter, r *http.Request) {
    signals := &ItemSignals{}
    datastar.ReadSignals(r, signals)
    sse := datastar.NewSSE(w, r)

    item := db.CreateItem(signals.Text)
    sse.PatchElementTempl(
        views.ListItem(item),
        datastar.WithSelector("#item-list"),
        datastar.WithModeAppend(),
    )
}
```

## Common Mistakes

1. **Calling `NewSSE` before `ReadSignals`** — body already consumed, signals read fails
2. **Returning JSON instead of SSE** — Datastar expects `text/event-stream`, not `application/json`
3. **Using `http.Redirect`** — use `sse.Redirect()` instead (sends via SSE)
4. **Forgetting element IDs** — without a selector, `outer` mode needs the fragment's root element to have an `id` matching an existing DOM element
5. **Not checking `IsClosed()`** in long-running loops — wastes resources on dead connections
6. **Panics from `MarshalAndPatchSignals`** — it panics on marshal failure; ensure your struct is JSON-serializable
