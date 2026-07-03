package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mark3labs/msbd/internal/core"
)

func TestBearerToken(t *testing.T) {
	cases := map[string]string{
		"Bearer abc":  "abc",
		"bearer abc":  "abc", // case-insensitive scheme
		"BEARER  abc": "abc",
		"abc":         "", // no scheme → rejected (no lax bare-token accept)
		"Basic abc":   "",
		"":            "",
	}
	for hdr, want := range cases {
		r := httptest.NewRequest("GET", "/", nil)
		if hdr != "" {
			r.Header.Set("Authorization", hdr)
		}
		if got := bearerToken(r); got != want {
			t.Errorf("bearerToken(%q) = %q, want %q", hdr, got, want)
		}
	}
}

func TestTokenOKMultipleKeys(t *testing.T) {
	s := &Server{apiKeys: splitKeys("old , new ,")}
	if len(s.apiKeys) != 2 {
		t.Fatalf("splitKeys = %v", s.apiKeys)
	}
	if !s.tokenOK("old") || !s.tokenOK("new") {
		t.Fatal("both rotation keys should be accepted")
	}
	if s.tokenOK("") || s.tokenOK("wrong") {
		t.Fatal("bad token accepted")
	}
}

func TestDecodeBodyLimitAnd413(t *testing.T) {
	s := &Server{maxBody: 16, maxFile: 1 << 20}
	big := strings.NewReader(`{"cmd":"` + strings.Repeat("x", 1000) + `"}`)
	r := httptest.NewRequest("POST", "/", big)
	w := httptest.NewRecorder()
	var v ExecRequestDTO
	if s.decodeBody(w, r, &v, s.maxBody) {
		t.Fatal("expected decodeBody to fail on oversize body")
	}
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", w.Code)
	}
}

func TestDecodeRejectsUnknownFields(t *testing.T) {
	s := &Server{maxBody: 1 << 20}
	r := httptest.NewRequest("POST", "/", strings.NewReader(`{"timeout_seconds":5}`))
	w := httptest.NewRecorder()
	var v ExecRequestDTO
	if s.decodeBody(w, r, &v, s.maxBody) {
		t.Fatal("expected unknown field to be rejected")
	}
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestWriteCreateErrStatuses(t *testing.T) {
	cases := []struct {
		err  error
		want int
	}{
		{core.ErrCapacity, http.StatusInsufficientStorage},
		{core.ErrInvalidParams, http.StatusBadRequest},
		{core.ErrNotFound, http.StatusNotFound},
	}
	for _, c := range cases {
		w := httptest.NewRecorder()
		writeCreateErr(w, c.err)
		if w.Code != c.want {
			t.Errorf("writeCreateErr(%v) = %d, want %d", c.err, w.Code, c.want)
		}
	}
}

func TestNotFoundOrForbidden(t *testing.T) {
	w := httptest.NewRecorder()
	notFoundOr(w, core.ErrForbidden)
	if w.Code != http.StatusForbidden {
		t.Fatalf("forbidden mapped to %d, want 403", w.Code)
	}
}
