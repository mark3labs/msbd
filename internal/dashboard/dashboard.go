package dashboard

// Package dashboard serves a self-contained web UI for managing everything the
// msbd REST API manages: sandboxes (lifecycle, exec/run, logs, metrics, files,
// terminal), volumes, images and snapshots.
//
// It is built with templ (server-rendered HTML), styled with templui components
// + Tailwind, and made reactive with Datastar (SSE-driven DOM patching). The
// package speaks only to core.Service — it never imports the microsandbox SDK,
// preserving the cgo isolation boundary the api package keeps.
//
// Routing model: every section is a REAL page at its own URL (bookmarkable,
// refreshable, back/forward-able). The /dashboard/api/* endpoints are the SSE
// fragment/action surface used for in-page updates only.

import (
	"crypto/subtle"
	"io/fs"
	"net/http"
	"sync"

	"github.com/mark3labs/msbd/internal/core"
)

// Config controls dashboard mounting and (optional) HTTP Basic auth.
type Config struct {
	// Enabled mounts the dashboard routes when true.
	Enabled bool
	// User and Pass gate the dashboard behind HTTP Basic auth. When BOTH are
	// empty the dashboard is served unauthenticated (dev only). Setting either
	// turns auth on; an empty counterpart then never matches.
	User string
	Pass string
	// Version is the msbd build version, shown in the shell header.
	Version string
}

// AuthEnabled reports whether Basic auth will be enforced.
func (c Config) AuthEnabled() bool { return c.User != "" || c.Pass != "" }

// Handler renders and serves the dashboard.
type Handler struct {
	svc *core.Service
	cfg Config

	// cancelled records jobs the user explicitly cancelled from the Run panel.
	// The runtime reports a SIGKILLed job as a plain exit 0, which would render
	// as a green "success" tick; this lets the streaming handler label it
	// "cancelled" instead. Entries are consumed by the stream that owns the job.
	cancelMu  sync.Mutex
	cancelled map[string]bool
}

// New builds a dashboard Handler over the given service.
func New(svc *core.Service, cfg Config) *Handler {
	return &Handler{svc: svc, cfg: cfg, cancelled: map[string]bool{}}
}

// markCancelled flags a job as user-cancelled.
func (h *Handler) markCancelled(sandbox, job string) {
	h.cancelMu.Lock()
	defer h.cancelMu.Unlock()
	h.cancelled[sandbox+"|"+job] = true
}

// takeCancelled reports (and clears) whether a job was user-cancelled.
func (h *Handler) takeCancelled(sandbox, job string) bool {
	h.cancelMu.Lock()
	defer h.cancelMu.Unlock()
	k := sandbox + "|" + job
	was := h.cancelled[k]
	delete(h.cancelled, k)
	return was
}

// Mount registers every dashboard route on the provided mux. All routes live
// under /dashboard and are gated by optional Basic auth (static assets too —
// they're tiny and it keeps the surface uniform).
func (h *Handler) Mount(mux *http.ServeMux) {
	// Static assets (CSS, vendored Datastar, templui component JS).
	sub, _ := fs.Sub(assetFS, "assets")
	assets := http.StripPrefix("/dashboard/assets/", http.FileServer(http.FS(sub)))
	mux.HandleFunc("GET /dashboard/assets/", h.basic(assets.ServeHTTP))

	// ---- Pages (full documents, one per section) ----
	mux.HandleFunc("GET /dashboard", h.basic(h.pageOverview))
	mux.HandleFunc("GET /dashboard/", h.basic(h.pageOverview))
	mux.HandleFunc("GET /dashboard/sandboxes", h.basic(h.pageSandboxes))
	mux.HandleFunc("GET /dashboard/sandboxes/{id}", h.basic(h.pageSandboxDetail))
	mux.HandleFunc("GET /dashboard/volumes", h.basic(h.pageVolumes))
	mux.HandleFunc("GET /dashboard/images", h.basic(h.pageImages))
	mux.HandleFunc("GET /dashboard/snapshots", h.basic(h.pageSnapshots))

	// Terminal (standalone page; connects to the existing WS terminal endpoint).
	mux.HandleFunc("GET /dashboard/terminal/{id}", h.basic(h.handleTerminalPage))
	mux.HandleFunc("POST /dashboard/api/sandboxes/{id}/terminal-ticket", h.basic(h.terminalTicket))

	// ---- Datastar API (SSE fragments + actions) ----
	// Overview.
	mux.HandleFunc("GET /dashboard/api/overview", h.basic(h.overviewFragment))

	// Sandboxes.
	mux.HandleFunc("GET /dashboard/api/sandboxes/table", h.basic(h.sandboxTable))
	mux.HandleFunc("POST /dashboard/api/sandboxes", h.basic(h.sandboxCreate))
	mux.HandleFunc("POST /dashboard/api/sandboxes/{id}/start", h.basic(h.sandboxStart))
	mux.HandleFunc("POST /dashboard/api/sandboxes/{id}/stop", h.basic(h.sandboxStop))
	mux.HandleFunc("DELETE /dashboard/api/sandboxes/{id}", h.basic(h.sandboxDelete))
	mux.HandleFunc("POST /dashboard/api/sandboxes/{id}/run", h.basic(h.sandboxRun))
	mux.HandleFunc("POST /dashboard/api/sandboxes/{id}/jobs/{job}/cancel", h.basic(h.sandboxJobCancel))
	mux.HandleFunc("GET /dashboard/api/sandboxes/{id}/logs", h.basic(h.sandboxLogs))
	mux.HandleFunc("GET /dashboard/api/sandboxes/{id}/logs/download", h.basic(h.sandboxLogsDownload))
	mux.HandleFunc("GET /dashboard/api/sandboxes/{id}/metrics", h.basic(h.sandboxMetricsStream))

	// Sandbox files.
	mux.HandleFunc("POST /dashboard/api/sandboxes/{id}/files", h.basic(h.filesList))
	mux.HandleFunc("GET /dashboard/api/sandboxes/{id}/files/view", h.basic(h.filesView))
	mux.HandleFunc("POST /dashboard/api/sandboxes/{id}/files/save", h.basic(h.filesSave))
	mux.HandleFunc("GET /dashboard/api/sandboxes/{id}/files/download", h.basic(h.filesDownload))
	mux.HandleFunc("POST /dashboard/api/sandboxes/{id}/files/upload", h.basic(h.filesUpload))
	mux.HandleFunc("POST /dashboard/api/sandboxes/{id}/files/mkdir", h.basic(h.filesMkdir))
	mux.HandleFunc("DELETE /dashboard/api/sandboxes/{id}/files", h.basic(h.filesRemove))

	// Volumes.
	mux.HandleFunc("GET /dashboard/api/volumes/table", h.basic(h.volumeTable))
	mux.HandleFunc("POST /dashboard/api/volumes", h.basic(h.volumeCreate))
	mux.HandleFunc("DELETE /dashboard/api/volumes/{name}", h.basic(h.volumeDelete))

	// Images.
	mux.HandleFunc("GET /dashboard/api/images/table", h.basic(h.imageTable))
	mux.HandleFunc("GET /dashboard/api/images/inspect", h.basic(h.imageInspect))
	mux.HandleFunc("POST /dashboard/api/images/pull", h.basic(h.imagePull))
	mux.HandleFunc("DELETE /dashboard/api/images", h.basic(h.imageRemove))
	mux.HandleFunc("POST /dashboard/api/images/prune", h.basic(h.imagePrune))

	// Snapshots.
	mux.HandleFunc("GET /dashboard/api/snapshots/table", h.basic(h.snapshotTable))
	mux.HandleFunc("POST /dashboard/api/snapshots", h.basic(h.snapshotCreate))
	mux.HandleFunc("POST /dashboard/api/snapshots/{name}/verify", h.basic(h.snapshotVerify))
	mux.HandleFunc("DELETE /dashboard/api/snapshots/{name}", h.basic(h.snapshotDelete))
}

// basic wraps a handler with optional HTTP Basic auth.
func (h *Handler) basic(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !h.cfg.AuthEnabled() {
			next(w, r)
			return
		}
		user, pass, ok := r.BasicAuth()
		userOK := subtle.ConstantTimeCompare([]byte(user), []byte(h.cfg.User)) == 1
		passOK := subtle.ConstantTimeCompare([]byte(pass), []byte(h.cfg.Pass)) == 1
		if ok && userOK && passOK {
			next(w, r)
			return
		}
		w.Header().Set("WWW-Authenticate", `Basic realm="msbd dashboard", charset="UTF-8"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}
}
