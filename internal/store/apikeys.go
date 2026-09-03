package store

// apikeys.go — REST bearer tokens.
//
// The raw token is shown EXACTLY once, at creation. Only sha256(token) is
// stored, so a leaked database yields no usable credentials, and only the
// non-secret prefix is kept for display.

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	// TokenPrefix namespaces msbd tokens so they are recognisable in logs and
	// scannable by secret-detection tooling.
	TokenPrefix = "msbd_"
	// tokenBytes is the entropy behind each key: 256 bits.
	tokenBytes = 32
	// displayPrefixLen is how much of the token is retained for identification.
	// Long enough to be unambiguous in a list, far too short to brute-force.
	displayPrefixLen = len(TokenPrefix) + 8
)

// APIKey is the metadata for a bearer token. It never carries the secret.
type APIKey struct {
	ID         int64
	Name       string
	Prefix     string
	CreatedAt  time.Time
	ExpiresAt  time.Time // zero = never expires
	LastUsedAt time.Time // zero = never used
	RevokedAt  time.Time // zero = live
	CreatedBy  string
}

// Revoked reports whether the key was explicitly revoked.
func (k *APIKey) Revoked() bool { return !k.RevokedAt.IsZero() }

// Expired reports whether the key is past its expiry.
func (k *APIKey) Expired() bool { return !k.ExpiresAt.IsZero() && time.Now().After(k.ExpiresAt) }

// Active reports whether the key would be accepted right now.
func (k *APIKey) Active() bool { return !k.Revoked() && !k.Expired() }

// Status is the display state: active | revoked | expired.
func (k *APIKey) Status() string {
	switch {
	case k.Revoked():
		return "revoked"
	case k.Expired():
		return "expired"
	default:
		return "active"
	}
}

// NewToken mints a fresh random bearer token.
func NewToken() (string, error) {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	return TokenPrefix + base64.RawURLEncoding.EncodeToString(b), nil
}

// HashToken is the one-way function protecting stored keys. sha256 (not bcrypt)
// is correct here: the input is already full-entropy random, so there is
// nothing to slow down a dictionary attack against, and every authenticated
// request would otherwise pay a KDF.
func HashToken(tok string) string {
	sum := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(sum[:])
}

// tokenDisplayPrefix is the identifying, non-secret head of a token.
func tokenDisplayPrefix(tok string) string {
	if len(tok) <= displayPrefixLen {
		return tok
	}
	return tok[:displayPrefixLen]
}

// CreateAPIKey mints a key and returns it together with the raw token. The raw
// token is the caller's ONLY chance to see it.
func (s *Store) CreateAPIKey(ctx context.Context, name string, ttl time.Duration, createdBy string) (*APIKey, string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, "", errors.New("key name is required")
	}
	if len(name) > 128 {
		return nil, "", errors.New("key name must be at most 128 characters")
	}
	if ttl < 0 {
		return nil, "", errors.New("key ttl must not be negative")
	}

	tok, err := NewToken()
	if err != nil {
		return nil, "", err
	}
	var expires time.Time
	if ttl > 0 {
		expires = time.Now().Add(ttl)
	}
	now := time.Now()
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO api_keys (name, prefix, token_hash, created_at, expires_at, created_by)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		name, tokenDisplayPrefix(tok), HashToken(tok), now.Unix(), toUnix(expires), createdBy)
	if err != nil {
		return nil, "", fmt.Errorf("create api key: %w", err)
	}
	id, _ := res.LastInsertId()
	return &APIKey{
		ID:        id,
		Name:      name,
		Prefix:    tokenDisplayPrefix(tok),
		CreatedAt: now,
		ExpiresAt: expires,
		CreatedBy: createdBy,
	}, tok, nil
}

const keyCols = `id, name, prefix, created_at, expires_at, last_used_at, revoked_at, created_by`

func scanKey(sc interface{ Scan(...any) error }) (*APIKey, error) {
	var (
		k                   APIKey
		created             int64
		expires, used, revd sql.NullInt64
	)
	if err := sc.Scan(&k.ID, &k.Name, &k.Prefix, &created, &expires, &used, &revd, &k.CreatedBy); err != nil {
		return nil, err
	}
	k.CreatedAt = time.Unix(created, 0)
	k.ExpiresAt = fromUnix(expires)
	k.LastUsedAt = fromUnix(used)
	k.RevokedAt = fromUnix(revd)
	return &k, nil
}

// ListAPIKeys returns every key (including revoked/expired ones), newest last.
func (s *Store) ListAPIKeys(ctx context.Context) ([]APIKey, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+keyCols+` FROM api_keys ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list api keys: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []APIKey{}
	for rows.Next() {
		k, err := scanKey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *k)
	}
	return out, rows.Err()
}

// CountAPIKeys reports how many key rows exist, live or not.
//
// This — not the active count — is what decides whether the API requires
// authentication. Using "active" would mean revoking the last key silently
// reopens the server to the world, turning a security action into a security
// hole. Once a key has ever been created, msbd stays authenticated; the way
// back to an open server is to DELETE every key (`msbd keys rm`), which is
// explicit and hard to do by accident.
func (s *Store) CountAPIKeys(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM api_keys`).Scan(&n)
	return n, err
}

// CountActiveAPIKeys reports how many keys would currently authenticate. The
// server logs this at boot so "every key is expired" is never a silent state.
func (s *Store) CountActiveAPIKeys(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM api_keys
		  WHERE revoked_at IS NULL AND (expires_at IS NULL OR expires_at > ?)`,
		time.Now().Unix()).Scan(&n)
	return n, err
}

// VerifyAPIKey resolves a raw bearer token to its key record, returning
// ErrNotFound when the token is unknown, revoked or expired. Callers must treat
// all three identically — the distinction is for logs, not for clients.
//
// The lookup is a single indexed equality match on the hash, so an attacker
// learns nothing from timing.
func (s *Store) VerifyAPIKey(ctx context.Context, raw string) (*APIKey, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, ErrNotFound
	}
	row := s.db.QueryRowContext(ctx,
		`SELECT `+keyCols+` FROM api_keys WHERE token_hash = ?`, HashToken(raw))
	k, err := scanKey(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if !k.Active() {
		return nil, ErrNotFound
	}
	return k, nil
}

// TouchAPIKey records that a key was just used. Callers should invoke this only
// on an auth-cache miss: doing it per request would turn every authenticated
// read into a database write.
func (s *Store) TouchAPIKey(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE api_keys SET last_used_at = ? WHERE id = ?`, time.Now().Unix(), id)
	return err
}

// findKey resolves a human-supplied reference to exactly one key. Accepted, in
// order of specificity: a numeric id, the display prefix ("msbd_a1b2c3d4" or
// the bare body), or the key's name.
//
// Name matching is included because that is what people actually type —
// `msbd keys revoke ci-runner` should not have to be `msbd keys revoke
// msbd_a1b2c3d4`. Names are not unique, so a collision is reported as
// ErrAmbiguous rather than silently revoking whichever row came first.
func (s *Store) findKey(ctx context.Context, ref string) (*APIKey, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, fmt.Errorf("key reference: %w", ErrNotFound)
	}
	if id, err := strconv.ParseInt(ref, 10, 64); err == nil {
		row := s.db.QueryRowContext(ctx, `SELECT `+keyCols+` FROM api_keys WHERE id = ?`, id)
		k, err := scanKey(row)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("api key %q: %w", ref, ErrNotFound)
		}
		return k, err
	}

	matches, err := s.keysMatching(ctx,
		`SELECT `+keyCols+` FROM api_keys WHERE prefix = ? OR prefix = ?`,
		ref, TokenPrefix+ref)
	if err != nil {
		return nil, err
	}
	if len(matches) == 0 {
		matches, err = s.keysMatching(ctx, `SELECT `+keyCols+` FROM api_keys WHERE name = ?`, ref)
		if err != nil {
			return nil, err
		}
	}

	switch len(matches) {
	case 0:
		return nil, fmt.Errorf("api key %q: %w", ref, ErrNotFound)
	case 1:
		return &matches[0], nil
	default:
		return nil, fmt.Errorf("api key %q: %w (use the id or the msbd_… prefix from `msbd keys list`)",
			ref, ErrAmbiguous)
	}
}

// keysMatching runs a key query and collects the rows.
func (s *Store) keysMatching(ctx context.Context, query string, args ...any) ([]APIKey, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var found []APIKey
	for rows.Next() {
		k, err := scanKey(rows)
		if err != nil {
			return nil, err
		}
		found = append(found, *k)
	}
	return found, rows.Err()
}

// GetAPIKey resolves a reference to a key record.
func (s *Store) GetAPIKey(ctx context.Context, ref string) (*APIKey, error) {
	return s.findKey(ctx, ref)
}

// RevokeAPIKey disables a key without deleting it, so the audit trail (who
// created it, when it was last used) survives. Revoking twice is a no-op.
func (s *Store) RevokeAPIKey(ctx context.Context, ref string) (*APIKey, error) {
	k, err := s.findKey(ctx, ref)
	if err != nil {
		return nil, err
	}
	if k.Revoked() {
		return k, nil
	}
	now := time.Now()
	if _, err := s.db.ExecContext(ctx,
		`UPDATE api_keys SET revoked_at = ? WHERE id = ?`, now.Unix(), k.ID); err != nil {
		return nil, err
	}
	k.RevokedAt = now
	return k, nil
}

// DeleteAPIKey removes a key row entirely.
func (s *Store) DeleteAPIKey(ctx context.Context, ref string) (*APIKey, error) {
	k, err := s.findKey(ctx, ref)
	if err != nil {
		return nil, err
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM api_keys WHERE id = ?`, k.ID); err != nil {
		return nil, err
	}
	return k, nil
}
