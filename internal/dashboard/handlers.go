package dashboard

import (
	"encoding/json"
	"net/http"
	"time"

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

// handleTerminalPage serves the standalone xterm.js terminal for a sandbox. It
// mints a short-lived, single-use terminal ticket (bound to this sandbox) and
// hands THAT to the page instead of the long-lived REST bearer token — so the
// API key never lands in page source, the WS URL, or proxy access logs. The
// sandbox is verified to exist first so a bogus id renders a clean 404 rather
// than a page whose socket immediately fails.
func (h *Handler) handleTerminalPage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := h.svc.Get(r.Context(), id); err != nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		_ = views.TerminalNotFound(id).Render(r.Context(), w)
		return
	}
	scheme := "ws"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "wss"
	}
	wsBase := scheme + "://" + r.Host + "/v1/sandboxes/" + id + "/terminal"
	ticket, _ := h.svc.MintTerminalTicket(id)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = views.TerminalPage(id, wsBase, ticket).Render(r.Context(), w)
}

// ---------------------------------------------------------------------------
// Datastar helpers
// ---------------------------------------------------------------------------

// terminalTicket mints a fresh short-lived, single-use terminal ticket for the
// reconnect flow (the initial page ticket is single-use). Requires the same
// dashboard Basic auth as every other route.
func (h *Handler) terminalTicket(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := h.svc.Get(r.Context(), id); err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	tok, exp := h.svc.MintTerminalTicket(id)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"ticket":     tok,
		"expires_at": exp.Format(time.RFC3339),
	})
}

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
