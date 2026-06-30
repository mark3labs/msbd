package dashboard

import (
	"net/http"

	"github.com/starfederation/datastar-go/datastar"

	"github.com/mark3labs/msbd/internal/core"
	"github.com/mark3labs/msbd/internal/dashboard/components/toast"
	"github.com/mark3labs/msbd/internal/dashboard/views"
)

// handleIndex renders the full application shell.
func (h *Handler) handleIndex(w http.ResponseWriter, r *http.Request) {
	rt, _ := core.RuntimeVersion()
	m := views.Meta{
		Version:        orDash(h.cfg.Version),
		DefaultImage:   h.svc.DefaultImage(),
		RuntimeVersion: orDash(rt),
		SDKVersion:     core.SDKVersion(),
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = views.Layout(m).Render(r.Context(), w)
}

// handleTerminalPage serves the standalone xterm.js terminal for a sandbox.
func (h *Handler) handleTerminalPage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	scheme := "ws"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "wss"
	}
	wsBase := scheme + "://" + r.Host + "/v1/sandboxes/" + id + "/terminal"
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = views.TerminalPage(id, wsBase, h.cfg.APIKey).Render(r.Context(), w)
}

// ---------------------------------------------------------------------------
// Datastar helpers
// ---------------------------------------------------------------------------

// notify appends a transient toast to the live region.
func notify(sse *datastar.ServerSentEventGenerator, v toast.Variant, title, desc string) {
	_ = sse.PatchElementTempl(
		views.Notify(v, title, desc),
		datastar.WithSelectorID("toaster"),
		datastar.WithModeAppend(),
	)
}

// notifyErr reports an error as a destructive toast and returns true when err
// is non-nil (so callers can early-return).
func notifyErr(sse *datastar.ServerSentEventGenerator, action string, err error) bool {
	if err == nil {
		return false
	}
	notify(sse, toast.VariantError, action+" failed", err.Error())
	return true
}

// closeDialog clicks a dialog's close control client-side after a successful
// action so the modal dismisses itself.
func closeDialog(sse *datastar.ServerSentEventGenerator, id string) {
	_ = sse.ExecuteScript(
		"document.querySelector('#" + id + "')?.querySelector('[data-tui-dialog-close]')?.click()",
	)
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}
