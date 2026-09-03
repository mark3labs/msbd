package api

// router.go — HTTP surface. Uses stdlib http.ServeMux (Go 1.22+ method+path
// patterns), no external router. Middleware: panic recovery, bearer auth.

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/charmbracelet/log"

	"github.com/mark3labs/msbd/internal/core"
	"github.com/mark3labs/msbd/internal/dashboard"
	"github.com/mark3labs/msbd/internal/store"
)

// Body size limits. control-plane JSON is small; file-write endpoints carry
// base64 payloads and get a larger, configurable ceiling.
const (
	defaultMaxBodyBytes int64 = 1 << 20  // 1 MiB for control-plane requests
	defaultMaxFileBytes int64 = 64 << 20 // 64 MiB for file/volume writes
)

// Server holds dependencies for the HTTP handlers.
type Server struct {
	svc      *core.Service
	apiKeys  []string        // static bearer tokens from --api-key / MSBD_API_KEY
	keys     *store.KeyCache // persisted bearer tokens (nil when no store)
	ready    func() error    // readiness probe (FFI loaded + /dev/kvm openable)
	openapi  []byte          // raw openapi.yaml served at /openapi.yaml + /docs
	dash     *dashboard.Handler
	maxBody  int64
	maxFile  int64
	startedT time.Time
}

func NewServer(svc *core.Service, apiKey string, ready func() error) *Server {
	return &Server{
		svc:      svc,
		apiKeys:  splitKeys(apiKey),
		ready:    ready,
		maxBody:  defaultMaxBodyBytes,
		maxFile:  defaultMaxFileBytes,
		startedT: time.Now(),
	}
}

// splitKeys parses a comma-separated bearer-token list (enables zero-downtime
// rotation: run with the old + new key, migrate clients, then drop the old).
func splitKeys(s string) []string {
	out := make([]string, 0, 1)
	for k := range strings.SplitSeq(s, ",") {
		if k = strings.TrimSpace(k); k != "" {
			out = append(out, k)
		}
	}
	return out
}

// SetStore attaches the persisted auth store, enabling store-backed API keys
// ALONGSIDE any static --api-key value. Static keys keep working untouched, so
// adopting the store is opt-in and never breaks an existing deployment.
func (s *Server) SetStore(st *store.Store) *Server {
	s.keys = store.NewKeyCache(st)
	return s
}

// Keys exposes the verification cache so callers can invalidate it after a key
// is created or revoked in-process (the dashboard does this, so the change is
// immediate rather than eventually-consistent).
func (s *Server) Keys() *store.KeyCache { return s.keys }

// authRequired reports whether requests must carry a bearer token: either a
// static one is configured, or the store holds at least one key. Neither means
// the API is open, which is the pre-existing dev-mode behaviour.
func (s *Server) authRequired(ctx context.Context) bool {
	return len(s.apiKeys) > 0 || s.keys.AuthConfigured(ctx)
}

// SetDashboard mounts the optional web dashboard at the root of the URL space
// (/, /sandboxes, /volumes, …). The REST API lives under /api/v1, so the two
// never collide.
func (s *Server) SetDashboard(d *dashboard.Handler) *Server {
	s.dash = d
	return s
}

// Handler builds the routed http.Handler with middleware applied.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Health (unauthenticated).
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		if s.ready != nil {
			if err := s.ready(); err != nil {
				writeErr(w, http.StatusServiceUnavailable, "not_ready", err.Error())
				return
			}
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ready"))
	})

	// Meta.
	mux.HandleFunc("GET /api/v1/version", s.auth(s.handleVersion))

	// Prometheus-style operational metrics (auth-gated like everything else).
	mux.HandleFunc("GET /metrics", s.auth(s.handleProm))

	// Short-lived single-use terminal tickets (for browser WS clients).
	mux.HandleFunc("POST /api/v1/terminal-tickets", s.auth(s.handleTerminalTicket))

	// API docs (unauthenticated; the spec is not a secret).
	if len(s.openapi) > 0 {
		mux.HandleFunc("GET /docs", s.handleDocs)
		mux.HandleFunc("GET /openapi.yaml", s.handleOpenAPI)
	}

	// Web dashboard, mounted at the root (optional; gated by its own auth).
	if s.dash != nil {
		s.dash.Mount(mux)
	}

	// Lifecycle.
	mux.HandleFunc("POST /api/v1/sandboxes", s.auth(s.handleCreate))
	mux.HandleFunc("GET /api/v1/sandboxes", s.auth(s.handleList))
	mux.HandleFunc("GET /api/v1/sandboxes/{id}", s.auth(s.handleGet))
	mux.HandleFunc("GET /api/v1/sandboxes/{id}/inspect", s.auth(s.handleInspect))
	mux.HandleFunc("DELETE /api/v1/sandboxes/{id}", s.auth(s.handleDelete))
	mux.HandleFunc("POST /api/v1/sandboxes/{id}/stop", s.auth(s.handleStop))
	mux.HandleFunc("POST /api/v1/sandboxes/{id}/start", s.auth(s.handleStart))

	// Exec / Run.
	mux.HandleFunc("POST /api/v1/sandboxes/{id}/exec", s.auth(s.handleExec))
	mux.HandleFunc("POST /api/v1/sandboxes/{id}/run", s.auth(s.handleRun))

	// Interactive terminal (WebSocket upgrade). Bearer auth via header or
	// ?key= query param (browsers can't set headers on a WS upgrade).
	mux.HandleFunc("GET /api/v1/sandboxes/{id}/terminal", s.authWS(s.handleTerminal))

	// Async jobs.
	mux.HandleFunc("POST /api/v1/sandboxes/{id}/jobs", s.auth(s.handleLaunch))
	mux.HandleFunc("GET /api/v1/sandboxes/{id}/jobs/{job}", s.auth(s.handlePoll))
	mux.HandleFunc("POST /api/v1/sandboxes/{id}/jobs/{job}/stdin", s.auth(s.handleJobStdin))
	mux.HandleFunc("POST /api/v1/sandboxes/{id}/jobs/{job}/signal", s.auth(s.handleJobSignal))

	// File IO.
	mux.HandleFunc("POST /api/v1/sandboxes/{id}/files/read", s.auth(s.handleReadFile))
	mux.HandleFunc("POST /api/v1/sandboxes/{id}/files/write", s.auth(s.handleWriteFile))
	mux.HandleFunc("POST /api/v1/sandboxes/{id}/files/list", s.auth(s.handleFileList))
	mux.HandleFunc("POST /api/v1/sandboxes/{id}/files/stat", s.auth(s.handleFileStat))
	mux.HandleFunc("POST /api/v1/sandboxes/{id}/files/exists", s.auth(s.handleFileExists))
	mux.HandleFunc("POST /api/v1/sandboxes/{id}/files/mkdir", s.auth(s.handleFileMkdir))
	mux.HandleFunc("POST /api/v1/sandboxes/{id}/files/remove", s.auth(s.handleFileRemove))
	mux.HandleFunc("POST /api/v1/sandboxes/{id}/files/copy", s.auth(s.handleFileCopy))
	mux.HandleFunc("POST /api/v1/sandboxes/{id}/files/rename", s.auth(s.handleFileRename))
	mux.HandleFunc("POST /api/v1/sandboxes/{id}/files/copy-from-host", s.auth(s.handleFileCopyFromHost))
	mux.HandleFunc("POST /api/v1/sandboxes/{id}/files/copy-to-host", s.auth(s.handleFileCopyToHost))

	// Metrics.
	mux.HandleFunc("GET /api/v1/metrics", s.auth(s.handleMetricsAll))
	mux.HandleFunc("GET /api/v1/sandboxes/{id}/metrics", s.auth(s.handleMetrics))

	// Logs.
	mux.HandleFunc("GET /api/v1/sandboxes/{id}/logs", s.auth(s.handleLogs))

	// Volumes.
	mux.HandleFunc("POST /api/v1/volumes", s.auth(s.handleVolumeCreate))
	mux.HandleFunc("GET /api/v1/volumes", s.auth(s.handleVolumeList))
	mux.HandleFunc("GET /api/v1/volumes/{name}", s.auth(s.handleVolumeGet))
	mux.HandleFunc("DELETE /api/v1/volumes/{name}", s.auth(s.handleVolumeDelete))
	mux.HandleFunc("POST /api/v1/volumes/{name}/files/read", s.auth(s.handleVolumeReadFile))
	mux.HandleFunc("POST /api/v1/volumes/{name}/files/write", s.auth(s.handleVolumeWriteFile))
	mux.HandleFunc("POST /api/v1/volumes/{name}/files/mkdir", s.auth(s.handleVolumeMkdir))
	mux.HandleFunc("POST /api/v1/volumes/{name}/files/remove", s.auth(s.handleVolumeRemoveFile))
	mux.HandleFunc("POST /api/v1/volumes/{name}/files/exists", s.auth(s.handleVolumeExists))

	// Images.
	mux.HandleFunc("GET /api/v1/images", s.auth(s.handleImageList))
	mux.HandleFunc("POST /api/v1/images/pull", s.auth(s.handleImagePull))
	mux.HandleFunc("GET /api/v1/images/inspect", s.auth(s.handleImageInspect))
	mux.HandleFunc("DELETE /api/v1/images", s.auth(s.handleImageRemove))
	mux.HandleFunc("POST /api/v1/images/prune", s.auth(s.handleImagePrune))

	// Snapshots.
	mux.HandleFunc("POST /api/v1/snapshots", s.auth(s.handleSnapshotCreate))
	mux.HandleFunc("GET /api/v1/snapshots", s.auth(s.handleSnapshotList))
	mux.HandleFunc("GET /api/v1/snapshots/{name}", s.auth(s.handleSnapshotGet))
	mux.HandleFunc("DELETE /api/v1/snapshots/{name}", s.auth(s.handleSnapshotDelete))
	mux.HandleFunc("POST /api/v1/snapshots/{name}/verify", s.auth(s.handleSnapshotVerify))
	mux.HandleFunc("POST /api/v1/snapshots/export", s.auth(s.handleSnapshotExport))
	mux.HandleFunc("POST /api/v1/snapshots/import", s.auth(s.handleSnapshotImport))
	mux.HandleFunc("POST /api/v1/snapshots/reindex", s.auth(s.handleSnapshotReindex))

	return recoverMW(logMW(mux))
}

// auth wraps a handler with constant-time bearer-token checking.
func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.authRequired(r.Context()) { // no key configured → open (dev only)
			next(w, r)
			return
		}
		if s.tokenOK(r.Context(), bearerToken(r)) {
			next(w, r)
			return
		}
		s.unauthorized(w)
	}
}

// authWS is auth for WebSocket upgrade endpoints: it accepts the bearer token
// from the Authorization header, a ?key= query param (browsers can't set
// headers on a WS handshake), OR a short-lived single-use ?ticket= bound to the
// sandbox {id} in the path — the preferred path for browsers, so the long-lived
// key never enters the URL / proxy logs / page source.
func (s *Server) authWS(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.authRequired(r.Context()) { // no key configured → open (dev only)
			next(w, r)
			return
		}
		if tkt := r.URL.Query().Get("ticket"); tkt != "" {
			if s.svc.RedeemTerminalTicket(tkt, r.PathValue("id")) {
				next(w, r)
				return
			}
		}
		tok := bearerToken(r)
		if tok == "" {
			tok = r.URL.Query().Get("key")
		}
		if s.tokenOK(r.Context(), tok) {
			next(w, r)
			return
		}
		s.unauthorized(w)
	}
}

// bearerToken extracts the token from an "Authorization: Bearer <token>" header.
// The scheme match is case-insensitive per RFC 9110; a header without the
// Bearer scheme yields "" (we do NOT accept a bare token as the scheme laxness
// masks client bugs).
func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) < 7 || !strings.EqualFold(h[:7], "Bearer ") {
		return ""
	}
	return strings.TrimSpace(h[7:])
}

// tokenOK reports whether tok matches a static configured key (constant time,
// no early exit) or a live store-backed key.
//
// Static keys are checked first and without a database round trip, so the
// single-key deployment stays exactly as fast as before the store existed. The
// store lookup is memoised by KeyCache — including negative results, so a flood
// of bogus tokens can't be used to hammer SQLite.
func (s *Server) tokenOK(ctx context.Context, tok string) bool {
	if tok == "" {
		return false
	}
	ok := false
	for _, k := range s.apiKeys {
		if subtle.ConstantTimeCompare([]byte(tok), []byte(k)) == 1 {
			ok = true
		}
	}
	if ok {
		return true
	}
	return s.keys.Valid(ctx, tok)
}

func (s *Server) unauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="msbd"`)
	writeErr(w, http.StatusUnauthorized, "unauthorized", "invalid or missing bearer token")
}

// statusRecorder captures the response status for structured logging while
// preserving http.Hijacker (required by the WebSocket upgrade) and http.Flusher
// (required by SSE endpoints).
type statusRecorder struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (sr *statusRecorder) WriteHeader(code int) {
	if !sr.wrote {
		sr.status = code
		sr.wrote = true
	}
	sr.ResponseWriter.WriteHeader(code)
}

func (sr *statusRecorder) Write(b []byte) (int, error) {
	if !sr.wrote {
		sr.status = http.StatusOK
		sr.wrote = true
	}
	return sr.ResponseWriter.Write(b)
}

// Unwrap exposes the underlying ResponseWriter so http.ResponseController and
// the gorilla WebSocket Hijack()/Flush() reach the real connection.
func (sr *statusRecorder) Unwrap() http.ResponseWriter { return sr.ResponseWriter }

// Hijack delegates to the underlying connection so the WebSocket terminal
// upgrade (gorilla type-asserts http.Hijacker directly) keeps working through
// the logging middleware.
func (sr *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hj, ok := sr.ResponseWriter.(http.Hijacker); ok {
		return hj.Hijack()
	}
	return nil, nil, http.ErrNotSupported
}

// Flush delegates to the underlying writer so SSE (dashboard Datastar) streams
// keep flushing through the middleware.
func (sr *statusRecorder) Flush() {
	if fl, ok := sr.ResponseWriter.(http.Flusher); ok {
		fl.Flush()
	}
}

func logMW(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Health probes are high-frequency and low-value: don't spam Info.
		if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" {
			next.ServeHTTP(w, r)
			return
		}
		rid := r.Header.Get("X-Request-Id")
		if rid == "" {
			rid = newRequestID()
		}
		w.Header().Set("X-Request-Id", rid)
		sr := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()
		next.ServeHTTP(sr, r)
		globalReqStats.observe(sr.status)
		// r.Pattern (Go 1.23+) is the ServeMux pattern that matched, filled in by
		// the mux on this same request value, so it is readable here on the way
		// back out. Logging the route template next to the concrete path makes
		// route shadowing diagnosable in production: the dashboard owns the root
		// of the URL space, so "which pattern actually handled this?" is a real
		// question. Empty means nothing matched (a 404 from the mux itself).
		route := r.Pattern
		if route == "" {
			route = "(unmatched)"
		}
		log.Info("request",
			"id", rid,
			"method", r.Method,
			"path", r.URL.Path,
			"route", route,
			"status", sr.status,
			"remote", clientIP(r),
			"dur", time.Since(start).Round(time.Millisecond))
	})
}

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i > 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	if i := strings.LastIndexByte(r.RemoteAddr, ':'); i > 0 {
		return r.RemoteAddr[:i]
	}
	return r.RemoteAddr
}

func recoverMW(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Error("panic recovered",
					"id", w.Header().Get("X-Request-Id"),
					"path", r.URL.Path,
					"err", rec, "stack", string(debug.Stack()))
				writeErr(w, http.StatusInternalServerError, "panic", "internal error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// ---------------------------------------------------------------------------
// JSON helpers
// ---------------------------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, ErrorBody{Error: ErrorDetail{Code: code, Message: msg}})
}

// decode reads a JSON body with a size cap and rejects unknown fields so a
// typo'd field (e.g. "timeout_seconds" for "timeout_secs") surfaces as a 400
// instead of a silent no-op. It does NOT have access to the ResponseWriter, so
// the size cap is enforced by decodeLimited via http.MaxBytesReader upstream.
func decode(r *http.Request, v any) error {
	defer func() { _ = r.Body.Close() }()
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	// Reject trailing garbage after the first JSON value.
	if dec.More() {
		return errors.New("unexpected trailing data after JSON body")
	}
	return nil
}

// decodeBody caps the request body at limit bytes (413 on overflow), then
// decodes JSON with unknown-field rejection. Returns false and writes the error
// response when decoding fails, so handlers can `if !decodeBody(...) { return }`.
func (s *Server) decodeBody(w http.ResponseWriter, r *http.Request, v any, limit int64) bool {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	if err := decode(r, v); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			writeErr(w, http.StatusRequestEntityTooLarge, "payload_too_large",
				"request body exceeds limit")
			return false
		}
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return false
	}
	return true
}

func newRequestID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "req"
	}
	return "req_" + hex.EncodeToString(b[:])
}
