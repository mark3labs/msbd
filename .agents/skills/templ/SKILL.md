---
name: templ
description: Guide for building HTML templates in Go using the templ templating language. Use whenever working with .templ files, creating Go web components, server-side rendering with Go, or integrating templ with HTMX, Datastar, or other frontend frameworks. This skill covers templ syntax, components, CSS/JS integration, HTTP handlers, and testing.
---

# Templ - HTML Templating for Go

Templ is a HTML templating language for Go that compiles to Go code. It provides type-safe, performant HTML generation with a syntax similar to HTML but with Go expressions for dynamic content.

## Core Concepts

### File Structure
- Files use `.templ` extension
- Start with `package` declaration and imports (like Go)
- Components are defined with `templ Name(params) { ... }`
- Generated Go files have `_templ.go` suffix (auto-generated, don't edit)

### Basic Component
```templ
package main

templ Greeting(name string) {
  <div>Hello, { name }!</div>
}
```

## Quick Reference

### Elements & Attributes
- All tags must be closed (either `</tag>` or self-closing `<br/>`)
- Void elements (img, br, hr, input) render without closing `/`
- Use `{ expression }` for dynamic content
- Attributes: `attr={ value }` for dynamic values
- Boolean attributes: `attr?={ bool }` (renders only if true)
- Spread attributes: `{ attrs... }` where attrs is `templ.Attributes`

### Control Flow
```templ
// If/else
if condition {
  <p>Yes</p>
} else {
  <p>No</p>
}

// Switch
switch value {
  case "a":
    <p>A</p>
  case "b":
    <p>B</p>
  default:
    <p>Other</p>
}

// For loops
for i, item := range items {
  <div>{ i }: { item }</div>
}

// While
for condition {
  <p>Looping</p>
}
```

### Template Composition
```templ
// Include another component
@OtherComponent()

// Pass children
@Wrapper() {
  <div>Child content</div>
}

// Children receiver in wrapper
templ Wrapper() {
  <div class="wrapper">
    { children... }
  </div>
}

// Pass component as parameter
@Layout(PageContent())

// Join components
@templ.Join(comp1(), comp2())
```

### CSS
```templ
// CSS component (scoped, auto-generated class name)
css className() {
  background-color: #ffffff;
  color: { red };
}

// CSS with parameters
css loading(percent int) {
  width: { fmt.Sprintf("%d%%", percent) };
}

// Use in template
<button class={ className() }>Click</button>

// Conditional classes
templ.KV("is-primary", isPrimary)
map[string]bool{"class-name": condition}

// Dynamic styles
<button style={ styleValue }>Styled</button>
```

### JavaScript
```templ
// Script templates (legacy - prefer standard <script> tags)
script greet(name string) {
  alert("Hello, " + name);
}

// Event handlers
<button onClick={ greet("World") }>Click</button>

// Pass data to JS
templ.JSFuncCall("functionName", arg1, arg2)
templ.JSONString(data)  // For HTML attributes
templ.JSONScript("id", data)  // Creates <script type="application/json">

// Render once (prevent duplicate scripts)
var handle = templ.NewOnceHandle()
@handle.Once() {
  <script>/* once per request */</script>
}
```

### Forms
```templ
// Form with method and action
<form method="POST" action="/submit">
  <input type="text" name="username" required?={ true }/>
  <button type="submit">Submit</button>
</form>

// CSRF protection
<form method="POST" action={ templ.SafeURL("/submit") }>
  @csrf.Token()  // If using CSRF middleware
</form>
```

### Context
```templ
// Access implicit ctx variable
<div>{ ctx.Value(key).(string) }</div>

// Type-safe accessor
func GetTheme(ctx context.Context) string {
  if theme, ok := ctx.Value(themeKey).(string); ok {
    return theme
  }
  return ""
}

// In template
<div>{ GetTheme(ctx) }</div>
```

### HTTP Integration
```go
// Static handler
http.Handle("/", templ.Handler(Component()))

// With options
http.Handle("/", templ.Handler(Component(), 
  templ.WithStatus(http.StatusOK),
  templ.WithContentType("text/html; charset=utf-8"),
))

// Dynamic handler
http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
  data := getData()
  Component(data).Render(r.Context(), w)
})

// With middleware (for context values)
handler := Middleware(templ.Handler(Component()))
```

### Raw HTML & Fragments
```templ
// Bypass escaping (security risk - only trusted content)
@templ.Raw("<div>HTML</div>")

// Render once handle
var handle = templ.NewOnceHandle()
@handle.Once() {
  <script src="global.js"></script>
}

// Fragments (render without full HTML document)
templ Fragment() {
  <div>Partial content</div>
}
```

## Security

- All expressions are HTML-escaped by default (XSS protection)
- URL attributes (href, src, action) are sanitized (javascript: blocked)
- CSS values are sanitized
- Use `templ.SafeURL()`, `templ.SafeCSS()`, `templ.Raw()` to bypass (with caution)

## Testing

```go
// Component testing with goquery
r, w := io.Pipe()
go func() {
    _ = Component().Render(context.Background(), w)
    _ = w.Close()
}()
doc, _ := goquery.NewDocumentFromReader(r)

// Find elements
doc.Find(`[data-testid="myComponent"]`).Length() // > 0 if present
doc.Find("h1").Text() // Get text content

// HTTP handler testing with httptest
w := httptest.NewRecorder()
r := httptest.NewRequest(http.MethodGet, "/", nil)
handler.ServeHTTP(w, r)
doc, _ := goquery.NewDocumentFromReader(w.Result().Body)
```

## CLI Commands

```bash
templ generate                    # Generate Go code from .templ files
templ generate -watch            # Watch for changes
templ generate -f file.templ     # Single file
templ fmt                        # Format .templ files
```

## Integration with Frontend Libraries

### HTMX
```templ
<button hx-post="/click" hx-swap="outerHTML">
  Click Me
</button>
```

### Datastar
```templ
<div data-on-click="/endpoint">
  Content
</div>
```

### Alpine.js
```templ
<div x-data={ templ.JSONString(data) }>
  <span x-text="message"></span>
</div>
```

## Best Practices

1. **Component naming**: Use PascalCase for exported components, camelCase for private
2. **Props**: Pass data through parameters rather than global state
3. **Context**: Use sparingly for cross-cutting concerns (auth, theme, locale)
4. **Testing**: Add `data-testid` attributes for reliable test selectors
5. **CSS**: Use CSS components for scoped styles, class utilities for dynamic classes
6. **JavaScript**: Prefer external JS files over inline scripts; use `templ.OnceHandle` for inline scripts
7. **Project structure**: Separate components, handlers, services, and database layers

## Common Patterns

See the full reference at `references/templ-reference.md` for detailed patterns, examples, and edge cases.
