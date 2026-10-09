package views

import (
	"context"
	"strings"
	"testing"

	"github.com/a-h/templ"
	"github.com/mark3labs/msbd/internal/dashboard/components/toast"
)

func migrationHTML(t *testing.T, c templ.Component) string {
	t.Helper()
	var out strings.Builder
	if err := c.Render(context.Background(), &out); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func TestShadcnShellAndConfirmation(t *testing.T) {
	m := testMeta()
	out := migrationHTML(t, Page(m, LoadingPanel()))
	for _, want := range []string{"/assets/js/shadcn-templ-", `data-slot="sidebar"`, `data-slot="sidebar-menu-button"`, `href="/sandboxes"`, `id="content"`, `id="toaster"`, `data-slot="alert-dialog-content"`, `data-text="$cfmbody"`} {
		if !strings.Contains(out, want) {
			t.Errorf("shell missing %q", want)
		}
	}
	if strings.Contains(out, "window.tui") {
		t.Error("legacy runtime reference")
	}
	if strings.Contains(out, `href="/settings/keys"`) {
		t.Error("Settings exposed without ShowSettings")
	}
	m.ShowSettings = true
	if !strings.Contains(migrationHTML(t, Page(m, LoadingPanel())), `href="/settings/keys"`) {
		t.Error("admin Settings navigation missing")
	}
}

func TestControlledShadcnControls(t *testing.T) {
	out := migrationHTML(t, CreateSandboxDialog(testMeta()))
	for _, want := range []string{`data-slot="field"`, `data-slot="select-trigger"`, `data-on:select-change=`, "evt.detail.value", "window.templ.select.setValue", `id="create-sandbox-error"`} {
		if !strings.Contains(out, want) {
			t.Errorf("create form missing %q", want)
		}
	}
	if !strings.Contains(migrationHTML(t, FilesPanel("sbx_test", "/", crumbs(), nil)), `data-slot="breadcrumb"`) {
		t.Error("files panel missing upstream breadcrumb")
	}
	if got := selectChange("logtail", "fetch()"); got != "$logtail = Number(evt.detail.value);fetch()" {
		t.Errorf("numeric select handler = %q", got)
	}
	detail := migrationHTML(t, SandboxDetailPage(SandboxDetail{SandboxRow: SandboxRow{ID: "sbx_test", State: "running"}}, nil))
	for _, want := range []string{`data-on:tabs-value-change=`, "evt.detail.value", "window.templ.tabs.setActive", `src="/terminal/sbx_test?embed=1"`, `data-on:checkbox-change=`, "evt.detail.checked", "window.templ.checkbox.setChecked"} {
		if !strings.Contains(detail, want) {
			t.Errorf("detail missing %q", want)
		}
	}
}

func TestStickyShadcnErrorToast(t *testing.T) {
	out := migrationHTML(t, Notify(toast.TypeError, `<script>bad</script>`, `"guest"`))
	if !strings.Contains(out, `data-templ-timeout="-1"`) {
		t.Error("error must remain sticky")
	}
	if strings.Contains(out, "<script>bad</script>") {
		t.Error("toast title was not escaped")
	}
	success := migrationHTML(t, Notify(toast.TypeSuccess, "Saved", "ok"))
	if !strings.Contains(success, `data-templ-timeout="4000"`) {
		t.Error("success timeout missing")
	}
}

func TestPatchedDialogsUseShadcnLifecycle(t *testing.T) {
	for name, c := range map[string]templ.Component{
		"key":    NewKeyDialog("ci", "secret"),
		"file":   FileViewContent("sbx_test", "/etc/hosts", "localhost", true, ""),
		"folder": NewFolderDialog("sbx_test"),
	} {
		out := migrationHTML(t, c)
		if !strings.Contains(out, `data-slot="dialog-content"`) {
			t.Errorf("%s missing dialog content", name)
		}
		if strings.Contains(out, "showModal") || strings.Contains(out, "<dialog") {
			t.Errorf("%s retained native dialog runtime", name)
		}
	}
	if got := openNativeJS("new-key"); got != `window.templ.dialog.open("new-key");` {
		t.Errorf("historical opener = %q", got)
	}
}
