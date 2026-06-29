package api

// router.go — HTTP surface. Uses stdlib http.ServeMux (Go 1.22+ method+path
// patterns), no external router. Middleware: panic recovery, bearer auth.

import (
	"encoding/json"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/charmbracelet/log"

	"github.com/mark3labs/msbd/internal/core"
)

// Server holds dependencies for the HTTP handlers.
type Server struct {
	svc     *core.Service
	apiKey  string
	ready   func() error // readiness probe (FFI loaded + /dev/kvm openable)
	openapi []byte       // raw openapi.yaml served at /openapi.yaml + /docs
}

func NewServer(svc *core.Service, apiKey string, ready func() error) *Server {
	return &Server{svc: svc, apiKey: apiKey, ready: ready}
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
	mux.HandleFunc("GET /v1/version", s.auth(s.handleVersion))

	// API docs (unauthenticated; the spec is not a secret).
	if len(s.openapi) > 0 {
		mux.HandleFunc("GET /docs", s.handleDocs)
		mux.HandleFunc("GET /openapi.yaml", s.handleOpenAPI)
	}

	// Lifecycle.
	mux.HandleFunc("POST /v1/sandboxes", s.auth(s.handleCreate))
	mux.HandleFunc("GET /v1/sandboxes", s.auth(s.handleList))
	mux.HandleFunc("GET /v1/sandboxes/{id}", s.auth(s.handleGet))
	mux.HandleFunc("GET /v1/sandboxes/{id}/inspect", s.auth(s.handleInspect))
	mux.HandleFunc("DELETE /v1/sandboxes/{id}", s.auth(s.handleDelete))
	mux.HandleFunc("POST /v1/sandboxes/{id}/stop", s.auth(s.handleStop))
	mux.HandleFunc("POST /v1/sandboxes/{id}/start", s.auth(s.handleStart))

	// Exec / Run.
	mux.HandleFunc("POST /v1/sandboxes/{id}/exec", s.auth(s.handleExec))
	mux.HandleFunc("POST /v1/sandboxes/{id}/run", s.auth(s.handleRun))

	// Async jobs.
	mux.HandleFunc("POST /v1/sandboxes/{id}/jobs", s.auth(s.handleLaunch))
	mux.HandleFunc("GET /v1/sandboxes/{id}/jobs/{job}", s.auth(s.handlePoll))
	mux.HandleFunc("POST /v1/sandboxes/{id}/jobs/{job}/stdin", s.auth(s.handleJobStdin))
	mux.HandleFunc("POST /v1/sandboxes/{id}/jobs/{job}/signal", s.auth(s.handleJobSignal))

	// File IO.
	mux.HandleFunc("POST /v1/sandboxes/{id}/files/read", s.auth(s.handleReadFile))
	mux.HandleFunc("POST /v1/sandboxes/{id}/files/write", s.auth(s.handleWriteFile))
	mux.HandleFunc("POST /v1/sandboxes/{id}/files/list", s.auth(s.handleFileList))
	mux.HandleFunc("POST /v1/sandboxes/{id}/files/stat", s.auth(s.handleFileStat))
	mux.HandleFunc("POST /v1/sandboxes/{id}/files/exists", s.auth(s.handleFileExists))
	mux.HandleFunc("POST /v1/sandboxes/{id}/files/mkdir", s.auth(s.handleFileMkdir))
	mux.HandleFunc("POST /v1/sandboxes/{id}/files/remove", s.auth(s.handleFileRemove))
	mux.HandleFunc("POST /v1/sandboxes/{id}/files/copy", s.auth(s.handleFileCopy))
	mux.HandleFunc("POST /v1/sandboxes/{id}/files/rename", s.auth(s.handleFileRename))
	mux.HandleFunc("POST /v1/sandboxes/{id}/files/copy-from-host", s.auth(s.handleFileCopyFromHost))
	mux.HandleFunc("POST /v1/sandboxes/{id}/files/copy-to-host", s.auth(s.handleFileCopyToHost))

	// Metrics.
	mux.HandleFunc("GET /v1/metrics", s.auth(s.handleMetricsAll))
	mux.HandleFunc("GET /v1/sandboxes/{id}/metrics", s.auth(s.handleMetrics))

	// Logs.
	mux.HandleFunc("GET /v1/sandboxes/{id}/logs", s.auth(s.handleLogs))

	// Volumes.
	mux.HandleFunc("POST /v1/volumes", s.auth(s.handleVolumeCreate))
	mux.HandleFunc("GET /v1/volumes", s.auth(s.handleVolumeList))
	mux.HandleFunc("GET /v1/volumes/{name}", s.auth(s.handleVolumeGet))
	mux.HandleFunc("DELETE /v1/volumes/{name}", s.auth(s.handleVolumeDelete))
	mux.HandleFunc("POST /v1/volumes/{name}/files/read", s.auth(s.handleVolumeReadFile))
	mux.HandleFunc("POST /v1/volumes/{name}/files/write", s.auth(s.handleVolumeWriteFile))
	mux.HandleFunc("POST /v1/volumes/{name}/files/mkdir", s.auth(s.handleVolumeMkdir))
	mux.HandleFunc("POST /v1/volumes/{name}/files/remove", s.auth(s.handleVolumeRemoveFile))
	mux.HandleFunc("POST /v1/volumes/{name}/files/exists", s.auth(s.handleVolumeExists))

	// Images.
	mux.HandleFunc("GET /v1/images", s.auth(s.handleImageList))
	mux.HandleFunc("GET /v1/images/inspect", s.auth(s.handleImageInspect))
	mux.HandleFunc("DELETE /v1/images", s.auth(s.handleImageRemove))
	mux.HandleFunc("POST /v1/images/prune", s.auth(s.handleImagePrune))

	// Snapshots.
	mux.HandleFunc("POST /v1/snapshots", s.auth(s.handleSnapshotCreate))
	mux.HandleFunc("GET /v1/snapshots", s.auth(s.handleSnapshotList))
	mux.HandleFunc("GET /v1/snapshots/{name}", s.auth(s.handleSnapshotGet))
	mux.HandleFunc("DELETE /v1/snapshots/{name}", s.auth(s.handleSnapshotDelete))
	mux.HandleFunc("POST /v1/snapshots/{name}/verify", s.auth(s.handleSnapshotVerify))
	mux.HandleFunc("POST /v1/snapshots/export", s.auth(s.handleSnapshotExport))
	mux.HandleFunc("POST /v1/snapshots/import", s.auth(s.handleSnapshotImport))
	mux.HandleFunc("POST /v1/snapshots/reindex", s.auth(s.handleSnapshotReindex))

	return recoverMW(logMW(mux))
}

// auth wraps a handler with constant-ish bearer-token checking.
func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.apiKey == "" { // no key configured → open (dev only)
			next(w, r)
			return
		}
		tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if subtleEqual(tok, s.apiKey) {
			next(w, r)
			return
		}
		writeErr(w, http.StatusUnauthorized, "unauthorized", "invalid or missing bearer token")
	}
}

func logMW(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"dur", time.Since(start).Round(time.Millisecond))
	})
}

func recoverMW(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Error("panic recovered", "err", rec, "stack", string(debug.Stack()))
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

func decode(r *http.Request, v any) error {
	defer func() { _ = r.Body.Close() }()
	return json.NewDecoder(r.Body).Decode(v)
}

// subtleEqual is a length-checked constant-time-ish compare.
func subtleEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var diff byte
	for i := 0; i < len(a); i++ {
		diff |= a[i] ^ b[i]
	}
	return diff == 0
}
