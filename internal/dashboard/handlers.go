package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/a-h/templ"
	"github.com/starfederation/datastar-go/datastar"

	"github.com/mark3labs/msbd/internal/core"
	"github.com/mark3labs/msbd/internal/dashboard/components/toast"
	"github.com/mark3labs/msbd/internal/dashboard/views"
)

// ---------------------------------------------------------------------------
// Page handlers — full HTML documents, one per section.
// ---------------------------------------------------------------------------

func (h *Handler) pageOverview(w http.ResponseWriter, r *http.Request) {
	// Registered as "GET /{$}", so the mux already guarantees an exact "/".
	// Anything else 404s through the mux rather than silently rendering here.
	d := h.overviewData(r.Context())
	h.render(w, r, views.SectionOverview, "Overview", "", views.OverviewPage(h.meta(r.Context(), views.SectionOverview, "Overview"), d))
}

func (h *Handler) pageSandboxes(w http.ResponseWriter, r *http.Request) {
	m := h.meta(r.Context(), views.SectionSandboxes, "Sandboxes")
	s := parseSort(r, "id")
	rows, err := h.sandboxRows(r.Context(), s)
	if err != nil {
		h.errorPage(w, r, m, "Could not list sandboxes", err)
		return
	}
	h.render(w, r, views.SectionSandboxes, "Sandboxes", "", views.SandboxesPage(m, rows, s))
}

func (h *Handler) pageSandboxDetail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	m := h.meta(r.Context(), views.SectionSandboxes, id)
	ins, err := h.svc.Inspect(r.Context(), id)
	if err != nil {
		h.errorPage(w, r, m, "Sandbox "+id+" is not available", err)
		return
	}
	d := views.SandboxDetail{
		SandboxRow: toSandboxRow(&ins.Instance),
		Config:     prettyJSON(ins.ConfigJSON),
	}
	all, _ := h.sandboxRows(r.Context(), views.TableSort{Col: "id", Dir: "asc"})
	h.render(w, r, views.SectionSandboxes, id, "", views.SandboxDetailPage(d, all))
}

func (h *Handler) pageVolumes(w http.ResponseWriter, r *http.Request) {
	m := h.meta(r.Context(), views.SectionVolumes, "Volumes")
	s := parseSort(r, "name")
	rows, err := h.volumeRows(r.Context(), s)
	if err != nil {
		h.errorPage(w, r, m, "Could not list volumes", err)
		return
	}
	h.render(w, r, views.SectionVolumes, "Volumes", "", views.VolumesPage(rows, s))
}

func (h *Handler) pageImages(w http.ResponseWriter, r *http.Request) {
	m := h.meta(r.Context(), views.SectionImages, "Images")
	s := parseSort(r, "reference")
	rows, err := h.imageRows(r.Context(), s)
	if err != nil {
		h.errorPage(w, r, m, "Could not list images", err)
		return
	}
	h.render(w, r, views.SectionImages, "Images", "", views.ImagesPage(m, rows, s))
}

func (h *Handler) pageSnapshots(w http.ResponseWriter, r *http.Request) {
	m := h.meta(r.Context(), views.SectionSnapshots, "Snapshots")
	s := parseSort(r, "created")
	rows, err := h.snapshotRows(r.Context(), s)
	if err != nil {
		h.errorPage(w, r, m, "Could not list snapshots", err)
		return
	}
	all, _ := h.sandboxRows(r.Context(), views.TableSort{Col: "id", Dir: "asc"})
	h.render(w, r, views.SectionSnapshots, "Snapshots", "", views.SnapshotsPage(rows, all, s))
}

// render wraps a section body in the app shell and writes it.
func (h *Handler) render(w http.ResponseWriter, r *http.Request, sec views.Section, title, subtitle string, body templ.Component) {
	m := h.meta(r.Context(), sec, title)
	m.Subtitle = subtitle
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = views.Page(m, body).Render(r.Context(), w)
}

// errorPage renders the shell with a prominent inline error instead of a blank
// section, so a failing daemon call is legible rather than a white screen. The
// status mirrors the cause so crawlers, proxies and tests see the truth: a
// missing sandbox is a 404, not a server fault.
func (h *Handler) errorPage(w http.ResponseWriter, r *http.Request, m views.Meta, title string, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, core.ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, core.ErrForbidden):
		status = http.StatusForbidden
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = views.Page(m, views.InlineError("page-error", title, cleanErr(err))).Render(r.Context(), w)
}

// NOTE: there is deliberately no catch-all "/" route rendering a styled 404
// page. Registering a bare "/" pattern on the mux makes EVERY unmatched
// request match it, which destroys ServeMux's built-in 405 Method Not Allowed
// for the REST API (e.g. `PUT /api/v1/sandboxes` would answer 404 "page not
// found" instead of 405). Correct status codes on the machine-facing API are
// worth more than a prettier 404 on a mistyped dashboard URL, so unmatched
// paths fall through to the mux's own plain-text 404.

// meta assembles the shell-level context, including the image/volume pickers
// the create-sandbox dialog offers as autocomplete, and the identity fields the
// shell uses to show the account box, the Settings nav and (for viewers) to
// hide controls that would only 403.
func (h *Handler) meta(ctx context.Context, sec views.Section, title string) views.Meta {
	rt, _ := core.RuntimeVersion()
	id := identityFromContext(ctx)
	m := views.Meta{
		Version:        orDash(h.cfg.Version),
		DefaultImage:   h.svc.DefaultImage(),
		RuntimeVersion: orDash(rt),
		SDKVersion:     core.SDKVersion(),
		Section:        sec,
		Title:          title,
		Username:       id.Name,
		Role:           id.Role,
		CanAdmin:       id.IsAdmin(),
		ShowSettings:   h.settingsEnabled() && id.IsAdmin(),
		CanSignOut:     id.Mode == modeSession,
	}
	if imgs, err := h.svc.ListImages(ctx); err == nil {
		for i := range imgs {
			m.Images = append(m.Images, imgs[i].Reference)
		}
		sort.Strings(m.Images)
	}
	if vols, err := h.svc.ListVolumes(ctx); err == nil {
		for i := range vols {
			m.Volumes = append(m.Volumes, vols[i].Name)
		}
		sort.Strings(m.Volumes)
	}
	return m
}

// ---------------------------------------------------------------------------
// Terminal
// ---------------------------------------------------------------------------

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
	wsBase := scheme + "://" + r.Host + "/api/v1/sandboxes/" + id + "/terminal"
	ticket, _ := h.svc.MintTerminalTicket(id)
	embed := r.URL.Query().Get("embed") == "1"
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = views.TerminalPage(id, wsBase, ticket, embed).Render(r.Context(), w)
}

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

// notifyErr reports an error as a sticky destructive toast and returns true
// when err is non-nil (so callers can early-return).
func notifyErr(sse *datastar.ServerSentEventGenerator, action string, err error) bool {
	if err == nil {
		return false
	}
	notify(sse, toast.VariantError, action+" failed", cleanErr(err))
	return true
}

// failInline reports an error BOTH as a toast and as an inline banner inside the
// panel/dialog that owns slotID, so the message lands next to the control that
// failed instead of only floating past in the corner.
func failInline(sse *datastar.ServerSentEventGenerator, slotID, action string, err error) bool {
	if err == nil {
		_ = sse.PatchElementTempl(views.ClearInline(slotID))
		return false
	}
	notify(sse, toast.VariantError, action+" failed", cleanErr(err))
	_ = sse.PatchElementTempl(views.InlineError(slotID, action+" failed", cleanErr(err)))
	return true
}

// cleanErr trims the noisiest parts of a wrapped SDK error for display.
func cleanErr(err error) string {
	s := strings.TrimSpace(err.Error())
	if s == "" {
		return "unknown error"
	}
	return s
}

// closeDialog dismisses a templui dialog after a successful action.
func closeDialog(sse *datastar.ServerSentEventGenerator, id string) {
	_ = sse.ExecuteScript("window.tui?.dialog?.close(" + jsString(id) + ")")
}

// closeNative dismisses a plain <dialog> element.
func closeNative(sse *datastar.ServerSentEventGenerator, id string) {
	_ = sse.ExecuteScript("document.getElementById(" + jsString(id) + ")?.close()")
}

func jsString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// parseSort reads ?sort=&dir= with a per-table default.
func parseSort(r *http.Request, def string) views.TableSort {
	col := r.URL.Query().Get("sort")
	if col == "" {
		col = def
	}
	dir := r.URL.Query().Get("dir")
	if dir != "desc" {
		dir = "asc"
	}
	return views.TableSort{Col: col, Dir: dir}
}

// sortRows applies a comparison in the requested direction.
func sortRows[T any](rows []T, s views.TableSort, less func(a, b T) bool) {
	sort.SliceStable(rows, func(i, j int) bool {
		if s.Dir == "desc" {
			return less(rows[j], rows[i])
		}
		return less(rows[i], rows[j])
	})
}
