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
	sort := TableSort{Col: "name", Dir: "asc"}
	m := testMeta()

	cases := map[string]func() string{
		"volume": func() string {
			var b strings.Builder
			_ = VolumesPage([]VolumeRow{{Name: payload, Kind: "disk", Path: "/x", Used: "0", Capacity: "—"}}, sort).Render(context.Background(), &b)
			return b.String()
		},
		"image": func() string {
			var b strings.Builder
			_ = ImagesPage(m, []ImageRow{{Reference: payload, Architecture: "x", OS: "linux", Layers: 1, Size: "1"}}, sort).Render(context.Background(), &b)
			return b.String()
		},
		"snapshot": func() string {
			var b strings.Builder
			_ = SnapshotsPage([]SnapshotRow{{Digest: payload, Name: "n", ImageRef: "i", Format: "f", Size: "1"}}, nil, sort).Render(context.Background(), &b)
			return b.String()
		},
		"workdir": func() string {
			var b strings.Builder
			_ = SandboxDetailPage(SandboxDetail{SandboxRow: SandboxRow{ID: "sbx_1", Workdir: payload}, Config: "{}"}, nil).Render(context.Background(), &b)
			return b.String()
		},
		"sandbox-id": func() string {
			var b strings.Builder
			_ = SandboxTable([]SandboxRow{{ID: payload, State: "running", Image: "i", Workdir: "/"}}, sort).Render(context.Background(), &b)
			return b.String()
		},
		"file-path": func() string {
			var b strings.Builder
			_ = FilesPanel("sbx_1", "/", []Crumb{{Label: payload, Path: payload}},
				[]FileRow{{Name: payload, Path: payload, Kind: "file", Size: "0", Mode: "0644"}}).Render(context.Background(), &b)
			return b.String()
		},
		"confirm-body": func() string {
			var b strings.Builder
			_ = SandboxTable([]SandboxRow{{ID: payload, State: "stopped"}}, sort).Render(context.Background(), &b)
			return b.String()
		},
		"log-line": func() string {
			var b strings.Builder
			_ = LogsPanel([]LogLine{{Source: "stdout", Text: payload}}).Render(context.Background(), &b)
			return b.String()
		},
		// Account and key names are operator-supplied, but they still reach the
		// same confirm-dialog and row-action expressions as guest-controlled data.
		"api-key-name": func() string {
			var b strings.Builder
			_ = KeysPage([]KeyRow{{ID: "1", Name: payload, Prefix: payload, Status: "active", Active: true}}, sort).Render(context.Background(), &b)
			return b.String()
		},
		"username": func() string {
			var b strings.Builder
			_ = UsersPage([]UserRow{{Username: payload, Role: "viewer"}}, sort).Render(context.Background(), &b)
			return b.String()
		},
		// The freshly-minted token is echoed straight back to the browser; a
		// breakout there would be self-inflicted but is worth pinning.
		"new-key-token": func() string {
			var b strings.Builder
			_ = NewKeyDialog(payload, payload).Render(context.Background(), &b)
			return b.String()
		},
		// ?next= is attacker-influenceable via a crafted link.
		"login-next": func() string {
			var b strings.Builder
			_ = LoginPage(payload, "v").Render(context.Background(), &b)
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

// TestConfirmBodyIsDataNotCode pins the design of the confirm dialog: the
// resource name flows through a text-only signal, never into executable markup.
func TestConfirmBodyIsDataNotCode(t *testing.T) {
	var b strings.Builder
	_ = ConfirmDialog().Render(context.Background(), &b)
	out := b.String()
	if !strings.Contains(out, `data-text="$cfmbody"`) {
		t.Error("confirm body should be rendered via data-text (textContent), not interpolated markup")
	}
	if strings.Contains(out, "innerHTML") {
		t.Error("confirm dialog must not assign innerHTML")
	}
}

// TestSetPasswordTargetIsDataNotCode — the shared "set password" dialog names
// its target through a text-only signal for the same reason the confirm dialog
// does: a username must never become executable markup.
func TestSetPasswordTargetIsDataNotCode(t *testing.T) {
	var b strings.Builder
	_ = SetPasswordDialog().Render(context.Background(), &b)
	if !strings.Contains(b.String(), `data-text="$pwuser"`) {
		t.Error("target username should be rendered via data-text, not interpolated markup")
	}
}
