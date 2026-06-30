package dashboard

import "embed"

// assetFS holds the compiled CSS, the vendored Datastar runtime, and the templui
// component JavaScript. output.css is produced by `task dashboard:assets`
// (tailwindcss) and committed so a plain `go build` works without Node/Tailwind.
//
//go:embed assets/css/output.css assets/vendor assets/js
var assetFS embed.FS
