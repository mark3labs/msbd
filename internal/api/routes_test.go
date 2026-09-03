package api

// routes_test.go — locks in the URL space: the REST API lives entirely under
// /api/v1, the dashboard owns the root, and the two never shadow each other.
//
// These are pure mux-shape assertions: they run without /dev/kvm because an
// unauthorised request is rejected by the auth middleware before any handler
// touches the SDK.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newRoutedServer builds a Server with a static API key so every REST route
// answers 401 (registered) rather than 404 (missing) for an anonymous request.
func newRoutedServer() *Server {
	return NewServer(nil, "secret", nil)
}

// TestRESTRoutesAreUnderAPIV1 asserts every versioned endpoint is reachable at
// /api/v1 and NOT at the old bare /v1.
func TestRESTRoutesAreUnderAPIV1(t *testing.T) {
	h := newRoutedServer().Handler()

	// One representative route per verb/shape in the table.
	routes := [][2]string{
		{http.MethodGet, "/api/v1/version"},
		{http.MethodPost, "/api/v1/terminal-tickets"},
		{http.MethodPost, "/api/v1/sandboxes"},
		{http.MethodGet, "/api/v1/sandboxes"},
		{http.MethodGet, "/api/v1/sandboxes/sbx_1"},
		{http.MethodDelete, "/api/v1/sandboxes/sbx_1"},
		{http.MethodPost, "/api/v1/sandboxes/sbx_1/exec"},
		{http.MethodPost, "/api/v1/sandboxes/sbx_1/run"},
		{http.MethodGet, "/api/v1/sandboxes/sbx_1/terminal"},
		{http.MethodPost, "/api/v1/sandboxes/sbx_1/jobs"},
		{http.MethodPost, "/api/v1/sandboxes/sbx_1/files/read"},
		{http.MethodGet, "/api/v1/sandboxes/sbx_1/logs"},
		{http.MethodGet, "/api/v1/metrics"},
		{http.MethodGet, "/api/v1/volumes"},
		{http.MethodGet, "/api/v1/images"},
		{http.MethodGet, "/api/v1/snapshots"},
	}

	for _, rt := range routes {
		method, path := rt[0], rt[1]

		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest(method, path, nil))
		if rr.Code != http.StatusUnauthorized {
			t.Errorf("%s %s = %d, want 401 (route should exist and require auth)",
				method, path, rr.Code)
		}

		// The pre-refactor path must be gone, not silently aliased.
		old := strings.TrimPrefix(path, "/api")
		rr = httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest(method, old, nil))
		if rr.Code != http.StatusNotFound {
			t.Errorf("%s %s = %d, want 404 (legacy path must not be registered)",
				method, old, rr.Code)
		}
	}
}

// TestUnversionedRoutesStayAtRoot: ops and spec endpoints are deliberately NOT
// under /api/v1 — probes and scrapers hard-code them.
func TestUnversionedRoutesStayAtRoot(t *testing.T) {
	s := newRoutedServer()
	s.SetOpenAPI([]byte("openapi: 3.1.0\n"))
	h := s.Handler()

	cases := map[string]int{
		"/healthz":      http.StatusOK,
		"/readyz":       http.StatusOK,
		"/docs":         http.StatusOK,
		"/openapi.yaml": http.StatusOK,
		"/metrics":      http.StatusUnauthorized, // registered, but auth-gated
	}
	for path, want := range cases {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, path, nil))
		if rr.Code != want {
			t.Errorf("GET %s = %d, want %d", path, rr.Code, want)
		}
	}
}

// TestNoCatchAllRoute is the guard for the trap described in
// internal/dashboard/handlers.go: registering a bare "/" pattern would make
// every unmatched request match it, flattening ServeMux's 405 Method Not
// Allowed into a 404 for the whole API.
func TestNoCatchAllRoute(t *testing.T) {
	h := newRoutedServer().Handler()

	// A registered path with an unregistered method must still be a 405.
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodPut, "/api/v1/sandboxes", nil))
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("PUT /api/v1/sandboxes = %d, want 405 — a catch-all route was added",
			rr.Code)
	}

	// A genuinely unknown path is a 404, not the dashboard overview.
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/definitely/not/a/route", nil))
	if rr.Code != http.StatusNotFound {
		t.Errorf("GET /definitely/not/a/route = %d, want 404", rr.Code)
	}
}
