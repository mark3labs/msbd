package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mark3labs/msbd/internal/core"
	"github.com/mark3labs/msbd/internal/store"
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
	ctx := t.Context()
	s := &Server{apiKeys: splitKeys("old , new ,")}
	if len(s.apiKeys) != 2 {
		t.Fatalf("splitKeys = %v", s.apiKeys)
	}
	if !s.tokenOK(ctx, "old") || !s.tokenOK(ctx, "new") {
		t.Fatal("both rotation keys should be accepted")
	}
	if s.tokenOK(ctx, "") || s.tokenOK(ctx, "wrong") {
		t.Fatal("bad token accepted")
	}
}

// TestStoreBackedKeysAuthenticate covers the persisted-key path: a key created
// in the store must authenticate exactly like a static --api-key one, and
// revoking it must stop it (once the verification cache is invalidated).
func TestStoreBackedKeysAuthenticate(t *testing.T) {
	ctx := t.Context()
	st, err := store.Open(store.MemoryPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	s := &Server{}
	s.SetStore(st)

	// No keys yet: the API is open, matching the pre-store dev behaviour.
	if s.authRequired(ctx) {
		t.Error("an empty store must not switch the API to authenticated")
	}

	key, raw, err := st.CreateAPIKey(ctx, "ci", 0, "test")
	if err != nil {
		t.Fatal(err)
	}
	s.Keys().Invalidate()

	if !s.authRequired(ctx) {
		t.Error("creating a key must switch the API to authenticated")
	}
	if !s.tokenOK(ctx, raw) {
		t.Error("store-backed key rejected")
	}
	if s.tokenOK(ctx, raw+"x") {
		t.Error("tampered token accepted")
	}

	if _, err := st.RevokeAPIKey(ctx, key.Prefix); err != nil {
		t.Fatal(err)
	}
	s.Keys().Invalidate()
	if s.tokenOK(ctx, raw) {
		t.Error("revoked key still authenticates")
	}

	// Revoking the LAST key must NOT reopen the server: that would turn a
	// security action into a security hole. Auth stays on until every key row
	// is explicitly deleted.
	if !s.authRequired(ctx) {
		t.Error("revoking the only key reopened the API")
	}
	if _, err := st.DeleteAPIKey(ctx, key.Prefix); err != nil {
		t.Fatal(err)
	}
	s.Keys().Invalidate()
	if s.authRequired(ctx) {
		t.Error("deleting every key should return the API to open (dev) mode")
	}
}

// TestStaticAndStoreKeysCoexist is the no-forced-migration guarantee: adopting
// the store must not invalidate an existing MSBD_API_KEY deployment.
func TestStaticAndStoreKeysCoexist(t *testing.T) {
	ctx := t.Context()
	st, err := store.Open(store.MemoryPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	s := &Server{apiKeys: splitKeys("legacy-env-key")}
	s.SetStore(st)
	_, raw, err := st.CreateAPIKey(ctx, "new", 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if !s.tokenOK(ctx, "legacy-env-key") {
		t.Error("static key stopped working once a store was attached")
	}
	if !s.tokenOK(ctx, raw) {
		t.Error("store key rejected alongside a static key")
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
