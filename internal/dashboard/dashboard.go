package dashboard

// Package dashboard serves a self-contained web UI for managing everything the
// msbd REST API manages: sandboxes (lifecycle, exec/run, logs, metrics, files,
// terminal), volumes, images and snapshots.
//
// It is built with templ (server-rendered HTML), styled with templui components
// + Tailwind, and made reactive with Datastar (SSE-driven DOM patching). The
// package speaks only to core.Service — it never imports the microsandbox SDK,
// preserving the cgo isolation boundary the api package keeps.

import (
	"crypto/subtle"
	"io/fs"
	"net/http"

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
	// APIKey is the msbd REST bearer token. The terminal page needs it to open
	// the WebSocket terminal (browsers can't set headers on a WS handshake, so
	// it's forwarded as ?key=). Empty when the API is unauthenticated.
	APIKey string
	// Version is the msbd build version, shown in the shell header.
	Version string
}

// AuthEnabled reports whether Basic auth will be enforced.
func (c Config) AuthEnabled() bool { return c.User != "" || c.Pass != "" }

// Handler renders and serves the dashboard.
type Handler struct {
	svc *core.Service
	cfg Config
}

// New builds a dashboard Handler over the given service.
func New(svc *core.Service, cfg Config) *Handler {
	return &Handler{svc: svc, cfg: cfg}
}

// Mount registers every dashboard route on the provided mux. All routes live
// under /dashboard and are gated by optional Basic auth (static assets too —
// they're tiny and it keeps the surface uniform).
func (h *Handler) Mount(mux *http.ServeMux) {
	// Static assets (CSS, vendored Datastar, templui component JS).
	sub, _ := fs.Sub(assetFS, "assets")
	assets := http.StripPrefix("/dashboard/assets/", http.FileServer(http.FS(sub)))
	mux.HandleFunc("GET /dashboard/assets/", h.basic(assets.ServeHTTP))

	// Shell.
	mux.HandleFunc("GET /dashboard", h.basic(h.handleIndex))
	mux.HandleFunc("GET /dashboard/", h.basic(h.handleIndex))

	// Terminal (full page; connects to the existing WS terminal endpoint).
	mux.HandleFunc("GET /dashboard/terminal/{id}", h.basic(h.handleTerminalPage))

	// ---- Datastar API (SSE fragments) ----
	// Sandboxes.
	mux.HandleFunc("GET /dashboard/api/sandboxes", h.basic(h.sandboxList))
	mux.HandleFunc("GET /dashboard/api/sandboxes/table", h.basic(h.sandboxTable))
	mux.HandleFunc("POST /dashboard/api/sandboxes", h.basic(h.sandboxCreate))
	mux.HandleFunc("GET /dashboard/api/sandboxes/{id}", h.basic(h.sandboxDetail))
	mux.HandleFunc("POST /dashboard/api/sandboxes/{id}/start", h.basic(h.sandboxStart))
	mux.HandleFunc("POST /dashboard/api/sandboxes/{id}/stop", h.basic(h.sandboxStop))
	mux.HandleFunc("DELETE /dashboard/api/sandboxes/{id}", h.basic(h.sandboxDelete))
	mux.HandleFunc("POST /dashboard/api/sandboxes/{id}/run", h.basic(h.sandboxRun))
	mux.HandleFunc("GET /dashboard/api/sandboxes/{id}/logs", h.basic(h.sandboxLogs))
	mux.HandleFunc("GET /dashboard/api/sandboxes/{id}/metrics", h.basic(h.sandboxMetricsStream))
	mux.HandleFunc("POST /dashboard/api/sandboxes/{id}/files", h.basic(h.sandboxFiles))

	// Volumes.
	mux.HandleFunc("GET /dashboard/api/volumes", h.basic(h.volumeList))
	mux.HandleFunc("POST /dashboard/api/volumes", h.basic(h.volumeCreate))
	mux.HandleFunc("DELETE /dashboard/api/volumes/{name}", h.basic(h.volumeDelete))

	// Images.
	mux.HandleFunc("GET /dashboard/api/images", h.basic(h.imageList))
	mux.HandleFunc("POST /dashboard/api/images/pull", h.basic(h.imagePull))
	mux.HandleFunc("DELETE /dashboard/api/images", h.basic(h.imageRemove))
	mux.HandleFunc("POST /dashboard/api/images/prune", h.basic(h.imagePrune))

	// Snapshots.
	mux.HandleFunc("GET /dashboard/api/snapshots", h.basic(h.snapshotList))
	mux.HandleFunc("POST /dashboard/api/snapshots", h.basic(h.snapshotCreate))
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
