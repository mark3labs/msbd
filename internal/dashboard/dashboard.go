package dashboard

// Package dashboard serves a self-contained web UI for managing everything the
// msbd REST API manages: sandboxes (lifecycle, exec/run, logs, metrics, files,
// terminal), volumes, images and snapshots — plus the persisted accounts and
// API keys that guard all of it.
//
// It is built with templ (server-rendered HTML), styled with templui components
// + Tailwind, and made reactive with Datastar (SSE-driven DOM patching). The
// package speaks only to core.Service and store.Store — it never imports the
// microsandbox SDK, preserving the cgo isolation boundary the api package keeps.
//
// Routing model: every section is a REAL page at its own URL (bookmarkable,
// refreshable, back/forward-able). The /dashboard/api/* endpoints are the SSE
// fragment/action surface used for in-page updates only.

import (
	"io/fs"
	"net/http"
	"sync"
	"time"

	"github.com/mark3labs/msbd/internal/core"
	"github.com/mark3labs/msbd/internal/store"
)

// Config controls dashboard mounting and authentication.
type Config struct {
	// Enabled mounts the dashboard routes when true.
	Enabled bool
	// User and Pass are the LEGACY single-account HTTP Basic credentials. They
	// remain supported so existing deployments keep working, but they are
	// superseded the moment a real account exists in the store.
	User string
	Pass string
	// Version is the msbd build version, shown in the shell header.
	Version string
	// SessionTTL is how long a dashboard login lasts; 0 uses the store default.
	SessionTTL time.Duration
	// APIKeyConfigured records that a static --api-key / MSBD_API_KEY is set.
	// Together with KeyCache it answers "is the REST API protected?", which
	// decides whether serving an UNAUTHENTICATED dashboard would be a way
	// around the bearer token.
	APIKeyConfigured bool
	// AllowInsecure overrides that safety refusal (--dashboard-allow-insecure).
	AllowInsecure bool
	// KeyCache, when set, is invalidated after an API key is created, revoked
	// or deleted here, so the change takes effect on the REST API immediately
	// instead of after the cache TTL. It also reports whether any stored key
	// exists, for the refusal above.
	KeyCache *store.KeyCache
}

// BasicAuthEnabled reports whether the legacy HTTP Basic credentials are
// configured. It does NOT mean Basic auth is active — a store account takes
// precedence (see authMode).
func (c Config) BasicAuthEnabled() bool { return c.User != "" || c.Pass != "" }

// Handler renders and serves the dashboard.
type Handler struct {
	svc   *core.Service
	store *store.Store
	cfg   Config

	// users memoises the account count so the per-request auth-mode check is
	// not a database query.
	users userCounter

	// cancelled records jobs the user explicitly cancelled from the Run panel.
	// The runtime reports a SIGKILLed job as a plain exit 0, which would render
	// as a green "success" tick; this lets the streaming handler label it
	// "cancelled" instead. Entries are consumed by the stream that owns the job.
	cancelMu  sync.Mutex
	cancelled map[string]bool
}

// New builds a dashboard Handler over the given service and state store. A nil
// store disables login/session auth and the Settings section, leaving the
// legacy Basic-auth (or open) behaviour intact.
func New(svc *core.Service, cfg Config, st *store.Store) *Handler {
	return &Handler{svc: svc, store: st, cfg: cfg, cancelled: map[string]bool{}}
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

// settingsEnabled reports whether account/key management is available. It needs
// a store to manage anything at all.
func (h *Handler) settingsEnabled() bool { return h.store != nil }

// Mount registers every dashboard route on the provided mux.
//
// Three guards are in play and the choice matters:
//   - guardPage      full documents; unauthenticated → redirect to the login form
//   - guardAPI       SSE reads; unauthenticated → 401 (never a redirect, which
//     Datastar would happily patch into the page)
//   - guardWrite     guardAPI + admin role; viewers get 403
func (h *Handler) Mount(mux *http.ServeMux) {
	// Static assets are deliberately UNAUTHENTICATED: the login page needs its
	// stylesheet before anyone has signed in, and these are embedded, immutable
	// CSS/JS with no information in them.
	sub, _ := fs.Sub(assetFS, "assets")
	assets := http.StripPrefix("/dashboard/assets/", http.FileServer(http.FS(sub)))
	mux.Handle("GET /dashboard/assets/", assets)

	// ---- Authentication (unauthenticated by necessity) ----
	mux.HandleFunc("GET /dashboard/login", h.pageLogin)
	mux.HandleFunc("POST /dashboard/login", h.doLogin)
	mux.HandleFunc("POST /dashboard/logout", h.doLogout)

	// ---- Pages (full documents, one per section) ----
	mux.HandleFunc("GET /dashboard", h.guardPage(h.pageOverview))
	mux.HandleFunc("GET /dashboard/", h.guardPage(h.pageOverview))
	mux.HandleFunc("GET /dashboard/sandboxes", h.guardPage(h.pageSandboxes))
	mux.HandleFunc("GET /dashboard/sandboxes/{id}", h.guardPage(h.pageSandboxDetail))
	mux.HandleFunc("GET /dashboard/volumes", h.guardPage(h.pageVolumes))
	mux.HandleFunc("GET /dashboard/images", h.guardPage(h.pageImages))
	mux.HandleFunc("GET /dashboard/snapshots", h.guardPage(h.pageSnapshots))

	// Terminal (standalone page; connects to the existing WS terminal endpoint).
	// A terminal is a root shell, so it is a write action, not a read.
	mux.HandleFunc("GET /dashboard/terminal/{id}", h.guardPage(h.handleTerminalPage))
	mux.HandleFunc("POST /dashboard/api/sandboxes/{id}/terminal-ticket", h.guardWrite(h.terminalTicket))

	// ---- Datastar API (SSE fragments + actions) ----
	// Overview.
	mux.HandleFunc("GET /dashboard/api/overview", h.guardAPI(h.overviewFragment))

	// Sandboxes.
	mux.HandleFunc("GET /dashboard/api/sandboxes/table", h.guardAPI(h.sandboxTable))
	mux.HandleFunc("POST /dashboard/api/sandboxes", h.guardWrite(h.sandboxCreate))
	mux.HandleFunc("POST /dashboard/api/sandboxes/{id}/start", h.guardWrite(h.sandboxStart))
	mux.HandleFunc("POST /dashboard/api/sandboxes/{id}/stop", h.guardWrite(h.sandboxStop))
	mux.HandleFunc("DELETE /dashboard/api/sandboxes/{id}", h.guardWrite(h.sandboxDelete))
	mux.HandleFunc("POST /dashboard/api/sandboxes/{id}/run", h.guardWrite(h.sandboxRun))
	mux.HandleFunc("POST /dashboard/api/sandboxes/{id}/jobs/{job}/cancel", h.guardWrite(h.sandboxJobCancel))
	mux.HandleFunc("GET /dashboard/api/sandboxes/{id}/logs", h.guardAPI(h.sandboxLogs))
	mux.HandleFunc("GET /dashboard/api/sandboxes/{id}/logs/download", h.guardAPI(h.sandboxLogsDownload))
	mux.HandleFunc("GET /dashboard/api/sandboxes/{id}/metrics", h.guardAPI(h.sandboxMetricsStream))

	// Sandbox files. Listing/viewing/downloading are reads even though the
	// browser sends the directory as a POST body.
	mux.HandleFunc("POST /dashboard/api/sandboxes/{id}/files", h.guardAPI(h.filesList))
	mux.HandleFunc("GET /dashboard/api/sandboxes/{id}/files/view", h.guardAPI(h.filesView))
	mux.HandleFunc("POST /dashboard/api/sandboxes/{id}/files/save", h.guardWrite(h.filesSave))
	mux.HandleFunc("GET /dashboard/api/sandboxes/{id}/files/download", h.guardAPI(h.filesDownload))
	mux.HandleFunc("POST /dashboard/api/sandboxes/{id}/files/upload", h.guardWrite(h.filesUpload))
	mux.HandleFunc("POST /dashboard/api/sandboxes/{id}/files/mkdir", h.guardWrite(h.filesMkdir))
	mux.HandleFunc("DELETE /dashboard/api/sandboxes/{id}/files", h.guardWrite(h.filesRemove))

	// Volumes.
	mux.HandleFunc("GET /dashboard/api/volumes/table", h.guardAPI(h.volumeTable))
	mux.HandleFunc("POST /dashboard/api/volumes", h.guardWrite(h.volumeCreate))
	mux.HandleFunc("DELETE /dashboard/api/volumes/{name}", h.guardWrite(h.volumeDelete))

	// Images.
	mux.HandleFunc("GET /dashboard/api/images/table", h.guardAPI(h.imageTable))
	mux.HandleFunc("GET /dashboard/api/images/inspect", h.guardAPI(h.imageInspect))
	mux.HandleFunc("POST /dashboard/api/images/pull", h.guardWrite(h.imagePull))
	mux.HandleFunc("DELETE /dashboard/api/images", h.guardWrite(h.imageRemove))
	mux.HandleFunc("POST /dashboard/api/images/prune", h.guardWrite(h.imagePrune))

	// Snapshots.
	mux.HandleFunc("GET /dashboard/api/snapshots/table", h.guardAPI(h.snapshotTable))
	mux.HandleFunc("POST /dashboard/api/snapshots", h.guardWrite(h.snapshotCreate))
	mux.HandleFunc("POST /dashboard/api/snapshots/{name}/verify", h.guardWrite(h.snapshotVerify))
	mux.HandleFunc("DELETE /dashboard/api/snapshots/{name}", h.guardWrite(h.snapshotDelete))

	// ---- Settings: accounts and API keys (admin only) ----
	if !h.settingsEnabled() {
		return
	}
	mux.HandleFunc("GET /dashboard/settings/keys", h.guardAdminPage(h.pageKeys))
	mux.HandleFunc("GET /dashboard/settings/users", h.guardAdminPage(h.pageUsers))

	mux.HandleFunc("GET /dashboard/api/keys/table", h.guardWrite(h.keyTable))
	mux.HandleFunc("POST /dashboard/api/keys", h.guardWrite(h.keyCreate))
	mux.HandleFunc("POST /dashboard/api/keys/{id}/revoke", h.guardWrite(h.keyRevoke))
	mux.HandleFunc("DELETE /dashboard/api/keys/{id}", h.guardWrite(h.keyDelete))

	mux.HandleFunc("GET /dashboard/api/users/table", h.guardWrite(h.userTable))
	mux.HandleFunc("POST /dashboard/api/users", h.guardWrite(h.userCreate))
	// The target user travels in the signals, not the path: one dialog is shared
	// by every row.
	mux.HandleFunc("POST /dashboard/api/users/password", h.guardWrite(h.userPassword))
	mux.HandleFunc("POST /dashboard/api/users/{name}/role", h.guardWrite(h.userRole))
	mux.HandleFunc("DELETE /dashboard/api/users/{name}", h.guardWrite(h.userDelete))

	// Self-service: any signed-in account, including a viewer, may change its
	// OWN password. Gating this on admin would leave viewers unable to rotate a
	// credential they were handed.
	mux.HandleFunc("POST /dashboard/api/account/password", h.guardAPI(h.accountPassword))
}
