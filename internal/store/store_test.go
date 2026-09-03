package store

import (
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(MemoryPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestOpenIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := DBPath(dir)

	s1, err := Open(path)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	if _, err := s1.CreateUser(t.Context(), "alice", "hunter2hunter2", RoleAdmin); err != nil {
		t.Fatalf("create user: %v", err)
	}
	_ = s1.Close()

	// Re-opening must re-run migrations harmlessly and keep the data.
	s2, err := Open(path)
	if err != nil {
		t.Fatalf("second open: %v", err)
	}
	defer func() { _ = s2.Close() }()
	if _, err := s2.GetUser(t.Context(), "alice"); err != nil {
		t.Fatalf("user did not survive reopen: %v", err)
	}
}

// TestDatabaseFileIsPrivate — the file holds password and token hashes; a
// world-readable mode would leak them to every account on the host.
func TestDatabaseFileIsPrivate(t *testing.T) {
	path := DBPath(t.TempDir())
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode&0o077 != 0 {
		t.Errorf("db mode = %04o, want no group/other access", mode)
	}
}

func TestUserLifecycle(t *testing.T) {
	s := testStore(t)
	ctx := t.Context()

	u, err := s.CreateUser(ctx, "alice", "correct horse", RoleAdmin)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if u.Role != RoleAdmin {
		t.Errorf("role = %q", u.Role)
	}

	// Usernames are case-insensitive for uniqueness AND lookup.
	if _, err := s.CreateUser(ctx, "ALICE", "another password", RoleViewer); !errors.Is(err, ErrExists) {
		t.Errorf("duplicate (differing case) = %v, want ErrExists", err)
	}
	if _, err := s.GetUser(ctx, "AlIcE"); err != nil {
		t.Errorf("case-insensitive lookup failed: %v", err)
	}

	if _, err := s.Authenticate(ctx, "alice", "correct horse"); err != nil {
		t.Errorf("authenticate: %v", err)
	}
	if _, err := s.Authenticate(ctx, "alice", "wrong"); !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("bad password = %v, want ErrInvalidCredentials", err)
	}
	// A missing user must be indistinguishable from a bad password.
	if _, err := s.Authenticate(ctx, "nobody", "whatever"); !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("missing user = %v, want ErrInvalidCredentials", err)
	}

	if err := s.SetPassword(ctx, "alice", "a brand new password"); err != nil {
		t.Fatalf("set password: %v", err)
	}
	if _, err := s.Authenticate(ctx, "alice", "correct horse"); err == nil {
		t.Error("old password still works after change")
	}
	if _, err := s.Authenticate(ctx, "alice", "a brand new password"); err != nil {
		t.Errorf("new password rejected: %v", err)
	}
}

func TestPasswordPolicy(t *testing.T) {
	s := testStore(t)
	if _, err := s.CreateUser(t.Context(), "bob", "short", RoleAdmin); err == nil {
		t.Error("short password accepted")
	}
	// bcrypt silently ignores bytes past 72; accepting a longer password would
	// mean the tail the user typed is not actually part of the secret.
	if _, err := s.CreateUser(t.Context(), "bob", strings.Repeat("x", 100), RoleAdmin); err == nil {
		t.Error("over-length password accepted")
	}
}

func TestUsernameValidation(t *testing.T) {
	for _, bad := range []string{"", "  ", "has space", "sneaky/../path", "a<b>"} {
		if err := ValidateUsername(bad); err == nil {
			t.Errorf("ValidateUsername(%q) accepted", bad)
		}
	}
	for _, ok := range []string{"alice", "a.b-c_d", "ops@example.com", "User1"} {
		if err := ValidateUsername(ok); err != nil {
			t.Errorf("ValidateUsername(%q) = %v", ok, err)
		}
	}
}

// TestLastAdminIsProtected is the lockout guard: deleting or demoting the only
// admin would leave the dashboard permanently unreachable.
func TestLastAdminIsProtected(t *testing.T) {
	s := testStore(t)
	ctx := t.Context()

	if _, err := s.CreateUser(ctx, "root", "rootrootroot", RoleAdmin); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateUser(ctx, "view", "viewviewview", RoleViewer); err != nil {
		t.Fatal(err)
	}

	if err := s.DeleteUser(ctx, "root"); !errors.Is(err, ErrLastAdmin) {
		t.Errorf("delete last admin = %v, want ErrLastAdmin", err)
	}
	if err := s.SetRole(ctx, "root", RoleViewer); !errors.Is(err, ErrLastAdmin) {
		t.Errorf("demote last admin = %v, want ErrLastAdmin", err)
	}

	// A viewer is never load-bearing.
	if err := s.DeleteUser(ctx, "view"); err != nil {
		t.Errorf("delete viewer: %v", err)
	}
	// With a second admin present, the first becomes removable.
	if _, err := s.CreateUser(ctx, "root2", "rootrootroot2", RoleAdmin); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteUser(ctx, "root"); err != nil {
		t.Errorf("delete admin with a spare: %v", err)
	}
}

func TestAPIKeyLifecycle(t *testing.T) {
	s := testStore(t)
	ctx := t.Context()

	key, raw, err := s.CreateAPIKey(ctx, "ci-runner", 0, "alice")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !strings.HasPrefix(raw, TokenPrefix) {
		t.Errorf("token %q missing %q prefix", raw, TokenPrefix)
	}
	if !strings.HasPrefix(raw, key.Prefix) {
		t.Errorf("stored prefix %q is not a prefix of the token", key.Prefix)
	}
	if key.Status() != "active" {
		t.Errorf("status = %q", key.Status())
	}

	got, err := s.VerifyAPIKey(ctx, raw)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if got.ID != key.ID {
		t.Errorf("verify returned key %d, want %d", got.ID, key.ID)
	}
	if _, err := s.VerifyAPIKey(ctx, raw+"x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("tampered token = %v, want ErrNotFound", err)
	}

	// Revoked keys stop authenticating but stay listed for the audit trail.
	if _, err := s.RevokeAPIKey(ctx, key.Prefix); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, err := s.VerifyAPIKey(ctx, raw); !errors.Is(err, ErrNotFound) {
		t.Errorf("revoked key still verifies")
	}
	keys, err := s.ListAPIKeys(ctx)
	if err != nil || len(keys) != 1 {
		t.Fatalf("list = %v, %v", keys, err)
	}
	if keys[0].Status() != "revoked" {
		t.Errorf("status after revoke = %q", keys[0].Status())
	}
}

// TestRawTokenIsNotRecoverable is the core secrecy property: nothing in the
// database can be turned back into a working bearer token.
func TestRawTokenIsNotRecoverable(t *testing.T) {
	s := testStore(t)
	_, raw, err := s.CreateAPIKey(t.Context(), "k", 0, "")
	if err != nil {
		t.Fatal(err)
	}
	var dump strings.Builder
	rows, err := s.db.Query(`SELECT id, name, prefix, token_hash, created_by FROM api_keys`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id int64
		var name, prefix, hash, by string
		if err := rows.Scan(&id, &name, &prefix, &hash, &by); err != nil {
			t.Fatal(err)
		}
		// Append each column separately: concatenating first would allocate an
		// intermediate string on every row for no reason.
		for _, col := range []string{name, prefix, hash, by} {
			dump.WriteString(col)
		}
	}
	if strings.Contains(dump.String(), strings.TrimPrefix(raw, TokenPrefix)) {
		t.Fatal("the raw token is stored in the database")
	}
}

func TestAPIKeyExpiry(t *testing.T) {
	s := testStore(t)
	ctx := t.Context()

	if _, _, err := s.CreateAPIKey(ctx, "short-lived", -1*time.Second, ""); err == nil {
		t.Fatal("negative ttl should be rejected")
	}

	k, raw, err := s.CreateAPIKey(ctx, "short-lived", time.Millisecond, "")
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond) // expiry has 1s resolution
	if _, err := s.VerifyAPIKey(ctx, raw); !errors.Is(err, ErrNotFound) {
		t.Error("expired key still verifies")
	}
	got, err := s.GetAPIKey(ctx, k.Prefix)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status() != "expired" {
		t.Errorf("status = %q, want expired", got.Status())
	}
	n, err := s.CountActiveAPIKeys(ctx)
	if err != nil || n != 0 {
		t.Errorf("active count = %d (%v), want 0", n, err)
	}
}

func TestFindKeyByIDAndPrefix(t *testing.T) {
	s := testStore(t)
	ctx := t.Context()
	k, _, err := s.CreateAPIKey(ctx, "ci-runner", 0, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{
		"1",
		k.Prefix,
		strings.TrimPrefix(k.Prefix, TokenPrefix), // bare body
		"ci-runner", // the name is what people type
	} {
		if _, err := s.GetAPIKey(ctx, ref); err != nil {
			t.Errorf("GetAPIKey(%q) = %v", ref, err)
		}
	}
	if _, err := s.GetAPIKey(ctx, "msbd_zzzzzzzz"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown ref = %v, want ErrNotFound", err)
	}
}

// TestAmbiguousKeyNameIsRefused — names are not unique, so revoking by a
// duplicated name must error instead of picking one arbitrarily.
func TestAmbiguousKeyNameIsRefused(t *testing.T) {
	s := testStore(t)
	ctx := t.Context()
	if _, _, err := s.CreateAPIKey(ctx, "dup", 0, ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.CreateAPIKey(ctx, "dup", 0, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetAPIKey(ctx, "dup"); !errors.Is(err, ErrAmbiguous) {
		t.Errorf("duplicate name = %v, want ErrAmbiguous", err)
	}
	// The id still resolves it unambiguously.
	if _, err := s.GetAPIKey(ctx, "2"); err != nil {
		t.Errorf("id lookup = %v", err)
	}
}

func TestSessionLifecycle(t *testing.T) {
	s := testStore(t)
	ctx := t.Context()

	u, err := s.CreateUser(ctx, "alice", "correct horse", RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := s.CreateSession(ctx, u.ID, time.Hour, "curl/8", "10.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	_, got, err := s.LookupSession(ctx, sess.ID)
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if got.Username != "alice" {
		t.Errorf("session resolved to %q", got.Username)
	}

	if err := s.DeleteSession(ctx, sess.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.LookupSession(ctx, sess.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("deleted session = %v, want ErrNotFound", err)
	}
}

// TestExpiredSessionIsRejected — a stale cookie must behave exactly like no
// cookie at all, and must not linger in the table.
func TestExpiredSessionIsRejected(t *testing.T) {
	s := testStore(t)
	ctx := t.Context()
	u, _ := s.CreateUser(ctx, "alice", "correct horse", RoleAdmin)

	sess, err := s.CreateSession(ctx, u.ID, time.Millisecond, "", "")
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond)
	if _, _, err := s.LookupSession(ctx, sess.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("expired session = %v, want ErrNotFound", err)
	}
}

// TestPasswordChangeEndsSessions — changing a password is how an operator
// evicts a compromised login, so existing cookies must stop working.
func TestPasswordChangeEndsSessions(t *testing.T) {
	s := testStore(t)
	ctx := t.Context()
	u, _ := s.CreateUser(ctx, "alice", "correct horse", RoleAdmin)
	sess, _ := s.CreateSession(ctx, u.ID, time.Hour, "", "")

	if err := s.SetPassword(ctx, "alice", "a brand new password"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.LookupSession(ctx, sess.ID); !errors.Is(err, ErrNotFound) {
		t.Error("session survived a password change")
	}
}

// TestDeletingUserEndsSessions covers the ON DELETE CASCADE, which only works
// because Open() turns foreign keys on (they are OFF by default in SQLite).
func TestDeletingUserEndsSessions(t *testing.T) {
	s := testStore(t)
	ctx := t.Context()
	if _, err := s.CreateUser(ctx, "root", "rootrootroot", RoleAdmin); err != nil {
		t.Fatal(err)
	}
	u, _ := s.CreateUser(ctx, "alice", "correct horse", RoleAdmin)
	sess, _ := s.CreateSession(ctx, u.ID, time.Hour, "", "")

	if err := s.DeleteUser(ctx, "alice"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.LookupSession(ctx, sess.ID); !errors.Is(err, ErrNotFound) {
		t.Error("session survived user deletion (foreign keys off?)")
	}
}

func TestKeyCache(t *testing.T) {
	s := testStore(t)
	ctx := t.Context()
	c := NewKeyCache(s)

	_, raw, err := s.CreateAPIKey(ctx, "k", 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if !c.Valid(ctx, raw) {
		t.Fatal("fresh key rejected")
	}
	if c.Valid(ctx, "msbd_nope") {
		t.Error("garbage token accepted")
	}
	if c.Valid(ctx, "") {
		t.Error("empty token accepted")
	}

	// Revocation only takes effect after Invalidate (or the TTL) — that is the
	// documented tradeoff, so pin it rather than let it drift silently.
	if _, err := s.RevokeAPIKey(ctx, "1"); err != nil {
		t.Fatal(err)
	}
	if !c.Valid(ctx, raw) {
		t.Error("cache should still serve the pre-revocation decision")
	}
	c.Invalidate()
	if c.Valid(ctx, raw) {
		t.Error("revoked key still valid after Invalidate")
	}
}

// TestNilKeyCacheIsSafe lets callers hold a cache unconditionally when no store
// is configured.
func TestNilKeyCacheIsSafe(t *testing.T) {
	var c *KeyCache
	if c.Valid(t.Context(), "anything") {
		t.Error("nil cache authenticated a token")
	}
	c.Invalidate() // must not panic
	if NewKeyCache(nil) != nil {
		t.Error("NewKeyCache(nil) should be nil")
	}
}

// TestConcurrentAccess exercises the pool and the cache mutex together.
// Meaningful under -race, which CI runs.
func TestConcurrentAccess(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(DBPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	ctx := t.Context()

	_, raw, err := s.CreateAPIKey(ctx, "k", 0, "")
	if err != nil {
		t.Fatal(err)
	}
	c := NewKeyCache(s)

	var wg sync.WaitGroup
	for i := range 16 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for range 20 {
				if !c.Valid(ctx, raw) {
					t.Error("valid key rejected under concurrency")
					return
				}
				if _, err := s.ListAPIKeys(ctx); err != nil {
					t.Errorf("list: %v", err)
					return
				}
			}
		}(i)
	}
	wg.Wait()
}

func TestDBPath(t *testing.T) {
	if got := DBPath("/var/lib/msbd"); got != "/var/lib/msbd/"+DBFileName {
		t.Errorf("DBPath(dir) = %q", got)
	}
	// An explicit file wins, so --data-dir can name one directly.
	if got := DBPath("/tmp/custom.db"); got != "/tmp/custom.db" {
		t.Errorf("DBPath(file) = %q", got)
	}
	if DBPath("") == "" {
		t.Error("DBPath(\"\") should fall back to the default dir")
	}
}

func TestNormalizeRole(t *testing.T) {
	for in, want := range map[string]string{"": RoleAdmin, "admin": RoleAdmin, "ADMIN": RoleAdmin, "viewer": RoleViewer} {
		got, err := NormalizeRole(in)
		if err != nil || got != want {
			t.Errorf("NormalizeRole(%q) = %q, %v", in, got, err)
		}
	}
	if _, err := NormalizeRole("root"); err == nil {
		t.Error("unknown role accepted")
	}
}

func TestMigrationsAreOrdered(t *testing.T) {
	names, err := migrationNames()
	if err != nil {
		t.Fatal(err)
	}
	if len(names) == 0 {
		t.Fatal("no migrations embedded")
	}
	for i := 1; i < len(names); i++ {
		if names[i-1] >= names[i] {
			t.Errorf("migrations out of order: %q before %q", names[i-1], names[i])
		}
	}
}

// TestRevokingTheLastKeyDoesNotReopenTheServer is the fail-safe that separates
// "no keys were ever configured" (dev mode, open) from "every key was revoked"
// (locked down). Getting this backwards would turn `msbd keys revoke` — a
// security action — into a way to accidentally expose the whole daemon.
func TestRevokingTheLastKeyDoesNotReopenTheServer(t *testing.T) {
	s := testStore(t)
	ctx := t.Context()
	c := NewKeyCache(s)

	if c.AuthConfigured(ctx) {
		t.Fatal("an empty store must not require auth")
	}

	k, _, err := s.CreateAPIKey(ctx, "only", 0, "")
	if err != nil {
		t.Fatal(err)
	}
	c.Invalidate()
	if !c.AuthConfigured(ctx) {
		t.Fatal("creating a key must require auth")
	}

	if _, err := s.RevokeAPIKey(ctx, "only"); err != nil {
		t.Fatal(err)
	}
	c.Invalidate()
	if !c.AuthConfigured(ctx) {
		t.Error("revoking the last key reopened the server")
	}

	// Deleting every row IS the explicit way back to an open server.
	if _, err := s.DeleteAPIKey(ctx, k.Prefix); err != nil {
		t.Fatal(err)
	}
	c.Invalidate()
	if c.AuthConfigured(ctx) {
		t.Error("deleting every key should return to open mode")
	}
}
