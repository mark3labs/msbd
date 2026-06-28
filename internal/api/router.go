package api

// router.go — HTTP surface. Uses stdlib http.ServeMux (Go 1.22+ method+path
// patterns), no external router. Middleware: panic recovery, bearer auth.

import (
	"encoding/json"
	"log"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/mark3labs/msbd/internal/core"
)

// Server holds dependencies for the HTTP handlers.
type Server struct {
	svc      *core.Service
	apiKey   string
	ready    func() error // readiness probe (FFI loaded + /dev/kvm openable)
	prebaked bool         // reported in /v1/capabilities
}

func NewServer(svc *core.Service, apiKey string, ready func() error) *Server {
	return &Server{svc: svc, apiKey: apiKey, ready: ready}
}

// SetPrebaked configures the prebaked_image flag reported by /v1/capabilities.
// Set true when MSBD_DEFAULT_IMAGE already ships kit + ssh + toolchain so
// shipagent skips ensureKit/ensureSSHClient.
func (s *Server) SetPrebaked(v bool) *Server { s.prebaked = v; return s }

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
	mux.HandleFunc("GET /v1/capabilities", s.auth(s.handleCapabilities))

	// Lifecycle.
	mux.HandleFunc("POST /v1/sandboxes", s.auth(s.handleCreate))
	mux.HandleFunc("GET /v1/sandboxes", s.auth(s.handleList))
	mux.HandleFunc("GET /v1/sandboxes/{id}", s.auth(s.handleGet))
	mux.HandleFunc("DELETE /v1/sandboxes/{id}", s.auth(s.handleDelete))
	mux.HandleFunc("POST /v1/sandboxes/{id}/stop", s.auth(s.handleStop))
	mux.HandleFunc("POST /v1/sandboxes/{id}/start", s.auth(s.handleStart))

	// Exec / Run.
	mux.HandleFunc("POST /v1/sandboxes/{id}/exec", s.auth(s.handleExec))
	mux.HandleFunc("POST /v1/sandboxes/{id}/run", s.auth(s.handleRun))

	// Async jobs.
	mux.HandleFunc("POST /v1/sandboxes/{id}/jobs", s.auth(s.handleLaunch))
	mux.HandleFunc("GET /v1/sandboxes/{id}/jobs/{job}", s.auth(s.handlePoll))

	// File IO.
	mux.HandleFunc("POST /v1/sandboxes/{id}/files/read", s.auth(s.handleReadFile))
	mux.HandleFunc("POST /v1/sandboxes/{id}/files/write", s.auth(s.handleWriteFile))

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
		log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start).Round(time.Millisecond))
	})
}

func recoverMW(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("panic: %v\n%s", rec, debug.Stack())
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
