# Templ Complete Reference

## Table of Contents
1. [File Structure & Basics](#file-structure--basics)
2. [Elements](#elements)
3. [Attributes](#attributes)
4. [Expressions](#expressions)
5. [Control Flow](#control-flow)
6. [Template Composition](#template-composition)
7. [CSS Style Management](#css-style-management)
8. [JavaScript Integration](#javascript-integration)
9. [Comments](#comments)
10. [Context](#context)
11. [Raw HTML](#raw-html)
12. [Render Once](#render-once)
13. [Fragments](#fragments)
14. [Components Deep Dive](#components-deep-dive)
15. [Code Generation](#code-generation)
16. [Testing](#testing)
17. [HTTP Server Integration](#http-server-integration)
18. [Project Structure](#project-structure)
19. [Security](#security)
20. [Gotchas & Edge Cases](#gotchas--edge-cases)

---

## File Structure & Basics

Templ files use `.templ` extension and mirror Go file structure:

```templ
package main

import "fmt"
import "time"

// Ordinary Go code outside templ blocks
var greeting = "Welcome!"

templ headerTemplate(name string) {
  <header data-testid="headerTemplate">
    <h1>{ name }</h1>
    <h2>{ greeting }</h2>
  </header>
}
```

Key rules:
- Package declaration and imports identical to Go
- Go code (vars, funcs, types, consts) lives outside `templ` blocks
- `templ` blocks define components that compile to `func() templ.Component`

---

## Elements

```templ
templ button(text string) {
  <button class="button">{ text }</button>
}
```

Rendering:
```go
button("Click me").Render(context.Background(), os.Stdout)
```

### Tags Must Be Closed

Unlike HTML, templ requires all elements closed:
```templ
templ component() {
  <div>Test</div>
  <img src="images/test.png"/>   // Self-closing required
  <br/>                           // Self-closing required
}
```

Output (templ knows void elements):
```html
<div>Test</div>
<img src="images/test.png">
<br>
```

### Expressions in Elements

```templ
templ button(name string, content string) {
  <button value={ name }>{ content }</button>
}
```

---

## Attributes

### Constant Attributes
```templ
<p data-testid="paragraph">Text</p>
```

### String Expression Attributes
```templ
templ component(testID string) {
  <p data-testid={ testID }>Text</p>
}
```

Values are automatically HTML attribute encoded. Functions returning `(string, error)` are supported — errors propagate through `Render`.

### Boolean Attributes
```templ
// Static boolean
<hr noshade/>

// Dynamic boolean — use ?= syntax
<hr noshade?={ false }/>       // Won't render noshade
<input disabled?={ true }/>    // Renders: <input disabled>
```

### Conditional Attributes
```templ
<hr style="padding: 10px"
  if true {
    class="itIsTrue"
  }
/>
```

### Attribute Key Expressions
```templ
templ paragraph(testID string) {
  <p { "data-" + testID }="paragraph">Text</p>
}
```

Warning: Expression keys don't get special handling for href, onClick, etc.

### Spread Attributes
```templ
templ component(attrs templ.Attributes) {
  <p { attrs... }>Text</p>
}

// Usage
@component(templ.Attributes{"data-testid": "paragraph", "disabled": true})
```

`templ.Attributes` is `map[string]any`. Values:
- `string` → `name="value"`
- `bool` → boolean attribute if true
- `templ.KeyValue[string, bool]` → conditional string attribute
- `templ.KeyValue[bool, bool]` → conditional boolean attribute

Conditional spread:
```templ
<hr
  if shouldBeUsed {
    { attrs... }
  }
/>
```

### URL Attributes
`href`, `src`, `action` attributes auto-sanitize URLs (blocks `javascript:` etc.):
```templ
<a href={ p.URL }>{ p.Name }</a>
```

Bypass with `templ.SafeURL(myURL)` (security risk).

For non-standard URL attributes (e.g. htmx):
```templ
<div hx-get={ templ.URL(fmt.Sprintf("/contacts/%s/email", contact.ID)) }>
```

### JavaScript Event Attributes
`onClick`, `on*` handlers expect a `script` template reference:
```templ
script withParameters(a string, b string, c int) {
  console.log(a, b, c);
}

templ Button(text string) {
  <button onClick={ withParameters("test", text, 123) } type="button">{ text }</button>
}
```

### JSON Attributes
Serialize values for htmx `hx-vals`, Alpine `x-data`, etc.:
```go
func countriesJSON() string {
  countries := []string{"Czech Republic", "Slovakia"}
  bytes, _ := json.Marshal(countries)
  return string(bytes)
}
```
```templ
<search-webcomponent suggestions={ countriesJSON() }/>
```

---

## Expressions

### Supported Types for Interpolation
- Strings, Numbers (`int`, `uint`, `float32`, `complex64`, etc.), Booleans
- Any type based on above (e.g. `type Age int`, `type Name string`)

### Literals
```templ
<div>{ "print this" }</div>
<div>{ `backtick string` }</div>
<div>Number: { 42 }</div>
```

### Variables
```templ
templ greet(prefix string, p Person) {
  <div>{ prefix } { p.Name }{ exclamation }</div>
  <div>Age: { p.Age }</div>
}
```

### Functions
Functions returning value or `(value, error)`:
```templ
<div>{ strings.ToUpper("abc") }</div>
<div>{ getString() }</div>  // func getString() (string, error)
```

Errors propagate through `Render`.

### Auto-Escaping
All expressions are HTML-escaped:
```templ
<div>{ `</div><script>alert('xss')</script>` }</div>
// Output: &lt;/div&gt;&lt;script&gt;...
```

---

## Control Flow

### If/Else
```templ
templ login(isLoggedIn bool) {
  if isLoggedIn {
    <div>Welcome back!</div>
  } else {
    <input type="button" value="Log in"/>
  }
}
```

### Switch
```templ
templ greeting(tod string) {
  switch tod {
    case "morning":
      <p>Good morning!</p>
    case "evening":
      <p>Good evening!</p>
    default:
      <p>Hello!</p>
  }
}
```

### For Loops
```templ
templ list(items []string) {
  <ul>
    for _, item := range items {
      <li>{ item }</li>
    }
  </ul>
}
```

### Raw Go Code
Use Go code blocks for variables/logic:
```templ
templ example() {
  {{ x := "hello" }}
  <div>{ x }</div>
}
```

### Text Starting with if/switch/for

The parser treats text starting with `if`, `switch`, `for` as control flow. To use them as literal text:
```templ
// These work:
<p>Switch to Linux</p>          // Capital letter
<p>{ "for a day" }</p>          // String expression
<p>{ `switch to Linux` }</p>    // Backtick expression
```

---

## Template Composition

### Including Components
```templ
templ showAll() {
  @left()
  @middle()
  @right()
}
```

### Children
```templ
templ wrapChildren() {
  <div id="wrapper">
    { children... }
  </div>
}

templ page() {
  @wrapChildren() {
    <div>Inserted content</div>
  }
}
```

Programmatic children:
```go
contents := templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
  _, err := io.WriteString(w, "<div>From Go</div>")
  return err
})
ctx := templ.WithChildren(context.Background(), contents)
wrapChildren().Render(ctx, os.Stdout)
```

### Components as Parameters
```templ
templ layout(contents templ.Component) {
  <div id="contents">
    @contents
  </div>
}

templ page() {
  @layout(paragraph("Dynamic contents"))
}
```

### Joining Components
```templ
@templ.Join(hello(), world())
```

### Exporting / Importing
```templ
// Exported (capitalized) - accessible from other packages
templ Hello() {
  <div>Hello</div>
}

// Import from another package
import "mymodule/components"

templ Home() {
  @components.Hello()
}
```

---

## CSS Style Management

### Static Class & Style
```templ
<button class="button is-primary" style="background-color: red">Click</button>
```

### Dynamic Style Attribute
```templ
<button style={ styleVar }>Styled</button>
<button style={ style1, style2 }>Multiple</button>
```

Supported style types:
- `string` — CSS properties
- `templ.SafeCSS` — Unsanitized CSS
- `map[string]string` — Key-value CSS properties
- `map[string]templ.SafeCSSProperty` — Unsanitized values
- `templ.KeyValue[string, bool]` — Conditional CSS
- Functions returning any of the above

### Dynamic Class Attribute
```templ
<button class={ "button", className }>Text</button>

// Conditional classes
<button class={
  "button",
  templ.KV("is-primary", isPrimary),
  templ.KV(red(), isPrimary),
}>Text</button>

// Map syntax
<button class={ map[string]bool{"active": isActive, "disabled": isDisabled} }>
```

### CSS Components (Scoped Styles)
```templ
var red = "#ff0000"

css primaryClassName() {
  background-color: #ffffff;
  color: { red };
}

templ button(text string, isPrimary bool) {
  <button class={ "button", templ.KV(primaryClassName(), isPrimary) }>{ text }</button>
}
```

CSS components:
- Auto-generate unique class names
- Only rendered once per HTTP request
- Can take parameters

### CSS with Parameters
```templ
css loading(percent int) {
  width: { fmt.Sprintf("%d%%", percent) };
}

templ index() {
  <div class={ loading(50) }></div>
  <div class={ loading(100) }></div>
}
```

### CSS Sanitization
Dynamic values sanitized by default. Unsafe values replaced with `zTemplUnsafeCSSPropertyValue`.

Bypass with `templ.SafeCSSProperty`:
```templ
css windVaneRotation(degrees float64) {
  transform: { templ.SafeCSSProperty(fmt.Sprintf("rotate(%ddeg)", int(math.Round(degrees)))) };
}
```

### CSS Middleware
Serve CSS as global stylesheet instead of inline `<style>` tags:
```go
c1 := className()
handler := NewCSSMiddleware(httpRoutes, c1)
http.ListenAndServe(":8000", handler)
```
Don't forget: `<link rel="stylesheet" href="/styles/templ.css">`

---

## JavaScript Integration

### Standard Script Tags
```templ
templ body() {
  <script>
    function handleClick(event) {
      alert(event + ' clicked');
    }
  </script>
  <button onclick="handleClick(this)">Click me</button>
}
```

### Pass Go Data to JavaScript

**Event handler with data:**
```templ
templ Component(data CustomType) {
  <button onclick={ templ.JSFuncCall("alert", data.Message) }>Show alert</button>
}
```

**Pass event objects:**
```templ
<button onclick={ templ.JSFuncCall("clickHandler", templ.JSExpression("event"), "message") }>Click</button>
```

**Call client-side functions:**
```templ
templ Init(data CustomType) {
  @templ.JSFuncCall("functionToCall", data.Name, data.Age)
}
// Output: <script>functionToCall("John", 42);</script>
```

**Data in HTML attributes:**
```templ
<button alert-data={ templ.JSONString(data) }>Show alert</button>
```

**Data in script element:**
```templ
@templ.JSONScript("myData", data)
// Output: <script id="myData" type="application/json">{"key":"value"}</script>
```

**Interpolation in script tags:**
```templ
<script>
  const message = "Your message: {{ msg }}";  // Within strings
  const data = {{ structData }};               // Outside strings (JSON encoded)
</script>
```

### Render Once Pattern (Avoid Duplicate Scripts)
```templ
var helloHandle = templ.NewOnceHandle()

templ hello(name string) {
  @helloHandle.Once() {
    <script>
      function hello(name) {
        alert('Hello, ' + name + '!');
      }
    </script>
  }
  <div>
    <input type="button" value="Say Hi" data-name={ name }/>
    <script>
      (() => {
        let el = document.currentScript.closest('div').querySelector('input[data-name]');
        el.addEventListener('click', function() {
          hello(el.getAttribute('data-name'));
        })
      })()
    </script>
  </div>
}
```

### Script Templates (Legacy)
```templ
script graph(data []TimeValue) {
  const chart = LightweightCharts.createChart(document.body, { width: 400, height: 300 });
  lineSeries.setData(data);
}

templ page(data []TimeValue) {
  <body onload={ graph(data) }></body>
}
```

Warning: Script templates are legacy. Prefer `templ.JSFuncCall`, `templ.JSONString`, and standard `<script>` tags.

### Importing External Scripts
```templ
templ head() {
  <head>
    <script src="https://cdn.example.com/lib.js"></script>
  </head>
}
```

Serve local scripts:
```go
mux.Handle("/assets/", http.StripPrefix("/assets/", http.FileServer(http.Dir("assets"))))
```

---

## Comments

### HTML Comments (inside templ blocks)
```templ
templ template() {
  <!-- Single line comment -->
  <!--
    Multi-line comment.
    Rendered to output.
  -->
}
```

### Go Comments (outside templ blocks)
```templ
// Standard Go comment
var greeting = "Hello!"
```

---

## Context

Templ components have an implicit `ctx` variable (from `Render`'s context parameter).

### Avoiding Prop Drilling
```templ
// Instead of passing through every layer...
type contextKey string
var themeKey contextKey = "theme"

func GetTheme(ctx context.Context) string {
  if theme, ok := ctx.Value(themeKey).(string); ok {
    return theme
  }
  return ""
}

templ themeName() {
  <div>{ GetTheme(ctx) }</div>
}
```

### Setting Context via Middleware
```go
func ThemeMiddleware(next http.Handler) http.Handler {
  return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    ctx := context.WithValue(r.Context(), themeKey, "dark")
    next.ServeHTTP(w, r.WithContext(ctx))
  })
}
```

Use sparingly: context is not type-safe at compile time. Missing keys cause runtime panics.

---

## Raw HTML

Bypass all escaping (trusted content only):
```templ
templ Example() {
  @templ.Raw("<div>Hello, World!</div>")
}
```

Warning: Security risk. Only use with content you fully trust.

---

## Render Once

Ensure content renders only once per HTTP response:
```templ
var handle = templ.NewOnceHandle()

templ Component() {
  @handle.Once() {
    <script src="required-lib.js"></script>
  }
  <div>Component content</div>
}
```

Even if `Component()` is called multiple times, the script renders once.

---

## Fragments

Components that render partial HTML (useful for HTMX/Datastar partial updates):
```templ
templ UserRow(user User) {
  <tr>
    <td>{ user.Name }</td>
    <td>{ user.Email }</td>
  </tr>
}
```

No special syntax needed — any component can serve as a fragment.

---

## Components Deep Dive

### The Component Interface
```go
type Component interface {
  Render(ctx context.Context, w io.Writer) error
}
```

### Code-Only Components
```go
func button(text string) templ.Component {
  return templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
    _, err := io.WriteString(w, "<button>"+templ.EscapeString(text)+"</button>")
    return err
  })
}
```

Warning: You're responsible for HTML escaping in code-only components.

### Method Components
```templ
type Data struct {
  message string
}

templ (d Data) Method() {
  <div>{ d.message }</div>
}

// Inline usage
templ Page() {
  @Data{message: "Hello"}.Method()
}
```

### Export Rules
- Capitalized component names are exported (public)
- Lowercase component names are private to the package
- Same rules as Go functions

---

## Code Generation

```bash
templ generate                     # All .templ files recursively
templ generate -f header.templ     # Single file
templ generate -watch              # Watch mode (dev only, not optimized)
templ generate -lazy               # Only if .templ is newer than .go
templ generate -w 4                # Set worker count
templ fmt                          # Format .templ files
```

Generated files: `component.templ` → `component_templ.go` (never edit generated files).

---

## Testing

### Component Testing with goquery
```go
import "github.com/PuerkitoBio/goquery"

func TestHeader(t *testing.T) {
  r, w := io.Pipe()
  go func() {
    _ = headerTemplate("Posts").Render(context.Background(), w)
    _ = w.Close()
  }()
  doc, err := goquery.NewDocumentFromReader(r)
  if err != nil {
    t.Fatalf("failed to read template: %v", err)
  }

  // Find by data-testid
  if doc.Find(`[data-testid="headerTemplate"]`).Length() == 0 {
    t.Error("expected headerTemplate to be rendered")
  }

  // Check text content
  if actual := doc.Find("h1").Text(); actual != "Posts" {
    t.Errorf("expected 'Posts', got %q", actual)
  }
}
```

### HTTP Handler Testing
```go
func TestHandler(t *testing.T) {
  w := httptest.NewRecorder()
  r := httptest.NewRequest(http.MethodGet, "/", nil)
  handler.ServeHTTP(w, r)

  doc, err := goquery.NewDocumentFromReader(w.Result().Body)
  if err != nil {
    t.Fatalf("failed to parse: %v", err)
  }

  if doc.Find(`[data-testid="postsTemplate"]`).Length() == 0 {
    t.Error("expected posts to be rendered")
  }
}
```

### Snapshot Testing
```go
//go:embed expected.html
var expected string

func Test(t *testing.T) {
  component := render("sample content")
  actual, diff, err := htmldiff.Diff(component, expected)
  if err != nil {
    t.Fatal(err)
  }
  if diff != "" {
    t.Error(diff)
  }
}
```

### Best Practices
- Add `data-testid` attributes for reliable selectors
- Test components individually, then pages at higher level
- Use table-driven tests for multiple scenarios
- Don't retest child component internals at page level

---

## HTTP Server Integration

### Static Handler
```go
http.Handle("/", templ.Handler(hello()))
```

### With Options
```go
http.Handle("/404", templ.Handler(notFound(),
  templ.WithStatus(http.StatusNotFound),
  templ.WithContentType("text/html"),
  templ.WithErrorHandler(func(r *http.Request, err error) http.Handler { ... }),
))
```

### Dynamic Handler
```go
http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
  data := fetchData(r.Context())
  component := Page(data)
  component.Render(r.Context(), w)
})
```

### Handler Struct Pattern
```go
type NowHandler struct {
  Now func() time.Time
}

func (nh NowHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
  timeComponent(nh.Now()).Render(r.Context(), w)
}

http.Handle("/", NowHandler{Now: time.Now})
```

### Best Practice
Components should be pure functions — same parameters always produce same HTML. Don't make network/DB calls inside components; do that in handlers and pass results as parameters.

---

## Project Structure

Recommended onion architecture:
```
project/
├── components/    # templ components
├── handlers/      # HTTP handlers (uses services + components)
├── services/      # Business logic (uses db)
├── db/            # Database access
├── models/        # Shared data structures
└── main.go        # Entrypoint, wiring
```

Flow: `HTTP Handler → Service → DB`, then `Handler → Component(data) → HTML`

### Dependency Injection
```go
type CountService interface {
  Get(ctx context.Context, id string) (Counts, error)
}

func New(log *slog.Logger, cs CountService) *Handler {
  return &Handler{Log: log, CountService: cs}
}
```

No DI framework needed — use constructor functions and interfaces.

---

## Security

### Automatic Protections
- **HTML escaping**: All `{ expression }` output is escaped
- **URL sanitization**: `href`, `src`, `action` block `javascript:` protocol
- **CSS sanitization**: Dynamic CSS values checked for injection
- **Attribute escaping**: `&`, `"`, `'` escaped in all attributes

### Bypass Functions (Use With Caution)
| Function | Purpose |
|----------|---------|
| `templ.Raw(html)` | Render unescaped HTML |
| `templ.SafeURL(url)` | Skip URL sanitization |
| `templ.SafeCSS(css)` | Skip CSS sanitization |
| `templ.SafeCSSProperty(val)` | Skip CSS property value sanitization |
| `templ.JSExpression(expr)` | Raw JS expression (no JSON encoding) |
| `templ.JSUnsafeFuncCall(...)` | Skip script sanitization |

### Sanitization Markers
When sanitization blocks content:
- URL: replaced with `about:invalid#TemplFailedSanitizationURL`
- CSS property name: `zTemplUnsafeCSSPropertyName`
- CSS property value: `zTemplUnsafeCSSPropertyValue`

---

## Gotchas & Edge Cases

1. **Text starting with `if`/`switch`/`for`**: Treated as control flow. Use string expressions or capitalize first letter.

2. **HTML comments render to output**: They're not stripped. Use Go comments outside templ blocks for non-rendered comments.

3. **Context panics**: Accessing missing context keys or wrong type assertions cause runtime panics. Always use type-safe accessor functions.

4. **Void elements**: templ requires self-closing syntax (`<br/>`), but outputs standard HTML (`<br>`).

5. **CSS class names are auto-generated**: Don't rely on `css` component class names being consistent across builds.

6. **`templ generate -watch`**: Output not optimized for production. Use plain `templ generate` for builds.

7. **Partial writes on error**: `Render` may write partial HTML before returning an error. Buffer if you need all-or-nothing.

8. **Import context package explicitly**: As of v0.2.731, `context` is no longer implicitly imported in .templ files.

9. **Expression attribute encoding**: String values in attributes are HTML-entity encoded (`&` → `&amp;`). This is correct and doesn't affect functionality.

10. **Children propagation**: Use `templ.ClearChildren(ctx)` to prevent children from being passed down the tree unintentionally.
