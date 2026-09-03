// Package store is msbd's persisted state: dashboard users, API keys and
// browser sessions.
//
// It is deliberately the ONLY package that speaks SQL, and it knows nothing
// about HTTP or the microsandbox SDK — `api` and `dashboard` consume it the
// same way they consume `core`. The backing engine is SQLite through
// modernc.org/sqlite, a PURE-GO driver: msbd already pays a cgo tax for libkrun
// and the whole point of the daemon is to quarantine that to one binary, so the
// auth store must not add a second C toolchain dependency.
//
// Scale assumptions: a handful of users and keys on a single host. Reads on the
// hot path (bearer-token verification) are cached by the caller; everything
// here is a plain, unoptimised query.
package store

import (
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite" // pure-Go SQLite driver, registered as "sqlite"
)

//go:embed schema/*.sql
var schemaFS embed.FS

// Sentinel errors. Callers map these to exit codes (CLI) or HTTP statuses.
var (
	// ErrNotFound is returned when a user / key / session does not exist.
	ErrNotFound = errors.New("not found")
	// ErrExists is returned when a unique constraint would be violated.
	ErrExists = errors.New("already exists")
	// ErrInvalidCredentials is the deliberately vague authentication failure:
	// it does NOT distinguish "no such user" from "wrong password", so the
	// login form can't be used to enumerate accounts.
	ErrInvalidCredentials = errors.New("invalid username or password")
	// ErrLastAdmin guards against locking everyone out of the dashboard by
	// deleting or demoting the only remaining admin.
	ErrLastAdmin = errors.New("cannot remove the last admin user")
	// ErrAmbiguous is returned when an api-key reference matches several keys.
	ErrAmbiguous = errors.New("reference matches more than one key")
)

// Store owns the SQLite handle and every query against it.
type Store struct {
	db   *sql.DB
	path string
}

// MemoryPath is the DSN-ish sentinel for an in-process database (tests).
const MemoryPath = ":memory:"

// Open opens (creating if needed) the database at path and applies migrations.
// The parent directory is created with 0700 and the file itself is chmod'd to
// 0600 — it holds password hashes and API-key hashes, so it must not be
// world-readable even on a single-tenant host.
func Open(path string) (*Store, error) {
	dsn, memory := buildDSN(path)
	if !memory {
		dir := filepath.Dir(path)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("create data dir %s: %w", dir, err)
		}
	}

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}

	// An in-memory database lives inside a single connection; more than one
	// pooled conn would silently hand out separate, empty databases. On disk we
	// allow a small pool — WAL plus a busy timeout makes that safe, and the
	// dashboard does a session lookup per request.
	if memory {
		db.SetMaxOpenConns(1)
	} else {
		db.SetMaxOpenConns(4)
	}
	db.SetMaxIdleConns(2)
	db.SetConnMaxIdleTime(5 * time.Minute)

	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("open store: %w", err)
	}

	s := &Store{db: db, path: path}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if !memory {
		// Best effort: an existing file created by an older/looser umask gets
		// tightened, but a permission error here must not stop the daemon.
		_ = os.Chmod(path, 0o600)
	}
	return s, nil
}

// buildDSN turns a filesystem path into a modernc.org/sqlite DSN with the
// pragmas msbd needs, and reports whether the database is in-memory.
//
//   - journal_mode=WAL      readers don't block the writer (dashboard + CLI)
//   - busy_timeout=5000     wait rather than fail when the CLI writes mid-request
//   - foreign_keys=1        sessions actually cascade when a user is deleted
//   - synchronous=NORMAL    the durability/latency tradeoff WAL is designed for
func buildDSN(path string) (dsn string, memory bool) {
	pragmas := url.Values{}
	pragmas.Add("_pragma", "busy_timeout(5000)")
	pragmas.Add("_pragma", "foreign_keys(1)")
	pragmas.Add("_pragma", "journal_mode(WAL)")
	pragmas.Add("_pragma", "synchronous(NORMAL)")

	if path == "" || path == MemoryPath || strings.HasPrefix(path, ":memory:") {
		return "file::memory:?" + pragmas.Encode(), true
	}
	return "file:" + path + "?" + pragmas.Encode(), false
}

// Close releases the database handle.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// Path is the on-disk location of the database (empty for in-memory stores).
func (s *Store) Path() string {
	if s == nil {
		return ""
	}
	return s.path
}

// ---------------------------------------------------------------------------
// migrations
// ---------------------------------------------------------------------------

// migrate applies every embedded schema/*.sql file not yet recorded in
// schema_migrations, in lexical (= numeric prefix) order, each in its own
// transaction. Migrations are append-only: never edit a shipped file, add a
// new one.
func (s *Store) migrate() error {
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		name       TEXT PRIMARY KEY,
		applied_at INTEGER NOT NULL
	)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	applied := map[string]bool{}
	rows, err := s.db.Query(`SELECT name FROM schema_migrations`)
	if err != nil {
		return fmt.Errorf("read schema_migrations: %w", err)
	}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			_ = rows.Close()
			return err
		}
		applied[n] = true
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	_ = rows.Close()

	names, err := migrationNames()
	if err != nil {
		return err
	}
	for _, name := range names {
		if applied[name] {
			continue
		}
		body, err := schemaFS.ReadFile("schema/" + name)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", name, err)
		}
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(string(body)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply migration %s: %w", name, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations (name, applied_at) VALUES (?, ?)`,
			name, time.Now().Unix()); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record migration %s: %w", name, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %s: %w", name, err)
		}
	}
	return nil
}

func migrationNames() ([]string, error) {
	entries, err := fs.ReadDir(schemaFS, "schema")
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out, nil
}

// ---------------------------------------------------------------------------
// time helpers — timestamps are stored as Unix seconds, absent as NULL
// ---------------------------------------------------------------------------

func toUnix(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.Unix()
}

func fromUnix(n sql.NullInt64) time.Time {
	if !n.Valid || n.Int64 == 0 {
		return time.Time{}
	}
	return time.Unix(n.Int64, 0)
}

// isUnique reports whether err is a UNIQUE-constraint violation. The modernc
// driver does not export a typed constraint error, so this matches on the
// message — narrowly, so an unrelated failure is not swallowed as "exists".
func isUnique(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToUpper(err.Error())
	return strings.Contains(msg, "UNIQUE CONSTRAINT FAILED") ||
		strings.Contains(msg, "SQLITE_CONSTRAINT_UNIQUE")
}
