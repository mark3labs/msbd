package dashboard

import "embed"

// assetFS holds the compiled CSS, the vendored Datastar runtime, and the shadcn-templ
// component JavaScript. output.css is produced by `task dashboard`
// (tailwindcss) and committed so a plain `go build` works without Node/Tailwind.
//
//go:embed assets/css/output.css assets/vendor assets/js assets/favicon.svg
var assetFS embed.FS
