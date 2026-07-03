package views

import (
	"context"
	"strings"
	"testing"
)

// TestDatastarExpressionInjection verifies that user/guest-controlled strings
// interpolated into Datastar expression attributes cannot break out of the JS
// string literal (the stored-XSS class the jsExpr/pathSeg helpers close).
func TestDatastarExpressionInjection(t *testing.T) {
	const payload = `a'+alert(1)+'`

	cases := map[string]func() string{
		"volume": func() string {
			var b strings.Builder
			_ = VolumesPage([]VolumeRow{{Name: payload, Kind: "disk", Path: "/x", Used: "0", Capacity: "—"}}).Render(context.Background(), &b)
			return b.String()
		},
		"image": func() string {
			var b strings.Builder
			_ = ImagesPage([]ImageRow{{Reference: payload, Architecture: "x", OS: "linux", Layers: 1, Size: "1"}}).Render(context.Background(), &b)
			return b.String()
		},
		"snapshot": func() string {
			var b strings.Builder
			_ = SnapshotsPage([]SnapshotRow{{Digest: payload, Name: "n", ImageRef: "i", Format: "f", Size: "1"}}).Render(context.Background(), &b)
			return b.String()
		},
		"workdir": func() string {
			var b strings.Builder
			_ = SandboxDetailPage(SandboxDetail{SandboxRow: SandboxRow{ID: "sbx_1", Workdir: payload}, Config: "{}"}).Render(context.Background(), &b)
			return b.String()
		},
	}

	for name, fn := range cases {
		out := fn()
		// The raw breakout sequence (unescaped quote adjacent to +alert) must
		// NOT appear: jsExpr JSON-encodes it, so a literal ' becomes \u0027 or
		// stays inside a quoted string, and templ HTML-escapes ' to &#39;.
		if strings.Contains(out, "'+alert(1)+'") {
			t.Errorf("%s: unescaped injection payload present in output", name)
		}
	}
}
