package store

// cache.go — the bearer-token verification cache.
//
// Without it every authenticated REST request would be a database round trip,
// and — worse — every request with a WRONG token would be one too, handing an
// attacker a cheap amplification lever. KeyCache memoises both outcomes for a
// short TTL, so a hot key costs a map lookup and a flood of garbage tokens
// costs one query per distinct token per window.

import (
	"context"
	"sync"
	"time"
)

const (
	// verifyTTL bounds how long a cached decision — positive or negative — is
	// reused, and is therefore the REVOCATION LATENCY for a key revoked by
	// another process (`msbd keys revoke`). A revoke through the dashboard is
	// instant, because that path calls Invalidate directly.
	//
	// Kept deliberately small: the query it saves is one indexed lookup on a
	// tiny table, so this cache exists to absorb request bursts and token
	// floods, not to avoid an expensive operation. Trading seconds of "a
	// revoked key still works" for microseconds of database time would be a
	// bad bargain.
	verifyTTL = 5 * time.Second
	// touchInterval throttles last_used_at writes for a busy key, so an
	// authenticated read never becomes a database write on the hot path.
	touchInterval = time.Minute
	// maxEntries caps memory under a token-flood; past it the cache is dropped
	// wholesale, which is cheap and self-healing.
	maxEntries = 4096
)

type cacheEntry struct {
	keyID     int64
	ok        bool
	expires   time.Time
	lastTouch time.Time
}

// KeyCache wraps a Store with a small TTL cache for token verification. The
// zero value is not usable; build one with NewKeyCache. A nil *KeyCache is
// valid and always reports false, so callers can hold one unconditionally.
type KeyCache struct {
	store *Store
	mu    sync.Mutex
	m     map[string]*cacheEntry

	// anyVal memoises "does the store hold any key at all?", which the router
	// consults on EVERY request to decide whether auth is required — including
	// unauthenticated ones, so it must not be a live query.
	anyMu    sync.Mutex
	anyVal   bool
	anyUntil time.Time
}

// NewKeyCache builds a verification cache over the store. A nil store yields a
// nil cache (no store configured → no store-backed keys).
func NewKeyCache(s *Store) *KeyCache {
	if s == nil {
		return nil
	}
	return &KeyCache{store: s, m: make(map[string]*cacheEntry)}
}

// AuthConfigured reports whether the store holds any API key at all. The router
// uses it to decide whether the REST API requires a bearer token, so it flips
// the API from open to authenticated the moment the first key is created —
// including by `msbd keys create` in another process, which is why this is a
// cached query rather than a boot-time snapshot.
//
// It counts EVERY key, not just live ones: see store.CountAPIKeys for why
// revoking the last key must not reopen the server. And it fails CLOSED — if
// the store cannot be read we assume keys exist, so a database hiccup can never
// silently un-authenticate the API.
func (c *KeyCache) AuthConfigured(ctx context.Context) bool {
	if c == nil {
		return false
	}
	now := time.Now()
	c.anyMu.Lock()
	defer c.anyMu.Unlock()
	if now.Before(c.anyUntil) {
		return c.anyVal
	}
	n, err := c.store.CountAPIKeys(ctx)
	c.anyVal = err != nil || n > 0
	c.anyUntil = now.Add(verifyTTL)
	return c.anyVal
}

// Valid reports whether raw is a live store-backed API key.
//
// The cache is keyed by the token HASH, not the token, so a heap dump or a
// debugger view of a running msbd never contains usable credentials.
func (c *KeyCache) Valid(ctx context.Context, raw string) bool {
	if c == nil || raw == "" {
		return false
	}
	k := HashToken(raw)
	now := time.Now()

	c.mu.Lock()
	if e, hit := c.m[k]; hit && now.Before(e.expires) {
		ok, id, needsTouch := e.ok, e.keyID, e.ok && now.Sub(e.lastTouch) >= touchInterval
		if needsTouch {
			e.lastTouch = now
		}
		c.mu.Unlock()
		if needsTouch {
			_ = c.store.TouchAPIKey(ctx, id)
		}
		return ok
	}
	c.mu.Unlock()

	key, err := c.store.VerifyAPIKey(ctx, raw)
	ok := err == nil && key != nil

	e := &cacheEntry{ok: ok, expires: now.Add(verifyTTL)}
	if ok {
		e.keyID = key.ID
		e.lastTouch = now
		_ = c.store.TouchAPIKey(ctx, key.ID)
	}

	c.mu.Lock()
	if len(c.m) >= maxEntries {
		c.m = make(map[string]*cacheEntry, 64)
	}
	c.m[k] = e
	c.mu.Unlock()
	return ok
}

// Invalidate drops every cached decision. Called after a key is created,
// revoked or deleted in-process so the change takes effect immediately rather
// than after positiveTTL.
func (c *KeyCache) Invalidate() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.m = make(map[string]*cacheEntry, 64)
	c.mu.Unlock()
	c.anyMu.Lock()
	c.anyUntil = time.Time{}
	c.anyMu.Unlock()
}
