package store

// users.go — dashboard accounts. Passwords are bcrypt-hashed; the plaintext is
// never stored, logged, or returned.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"
)

// Roles. Admins can do everything the dashboard offers, including managing
// users and API keys; viewers get read-only access (every mutating dashboard
// action is refused).
const (
	RoleAdmin  = "admin"
	RoleViewer = "viewer"
)

// bcryptCost is deliberately above the library default (10). Logins are rare
// and interactive, so ~250ms of hashing is invisible to the user and expensive
// for an offline cracker.
const bcryptCost = 12

// hashCost returns the cost to hash new passwords with.
//
// Under `go test` it drops to bcrypt's minimum. The tests exercise the hashing
// LOGIC, not the work factor, and at cost 12 the handful of accounts they
// create cost about a minute of CI time per run (far worse under -race) for
// exactly zero additional coverage. testing.Testing() is false in every real
// binary, so a shipped msbd always uses bcryptCost.
//
// The cost is encoded in each stored hash, so mixed-cost databases verify fine
// and a future bump needs no migration.
func hashCost() int {
	if testing.Testing() {
		return bcrypt.MinCost
	}
	return bcryptCost
}

// Password policy. Long enough to matter, capped because bcrypt silently
// truncates input beyond 72 bytes — accepting a longer one would mean a
// password whose tail is ignored.
const (
	MinPasswordLen = 8
	MaxPasswordLen = 72
)

// User is a dashboard account. It never carries the password hash.
type User struct {
	ID          int64
	Username    string
	Role        string
	CreatedAt   time.Time
	UpdatedAt   time.Time
	LastLoginAt time.Time // zero = never signed in
}

// IsAdmin reports whether the user may perform mutating/administrative actions.
func (u *User) IsAdmin() bool { return u != nil && u.Role == RoleAdmin }

// ValidateUsername enforces a conservative character set: usernames appear in
// URLs, logs and audit output, so we keep them boring on purpose.
func ValidateUsername(name string) error {
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		return errors.New("username is required")
	case utf8.RuneCountInString(name) > 64:
		return errors.New("username must be at most 64 characters")
	}
	for _, r := range name {
		ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '.' || r == '-' || r == '_' || r == '@'
		if !ok {
			return fmt.Errorf("username may only contain letters, digits and . - _ @ (got %q)", r)
		}
	}
	return nil
}

// ValidatePassword enforces the length policy described above.
func ValidatePassword(pw string) error {
	switch {
	case len(pw) < MinPasswordLen:
		return fmt.Errorf("password must be at least %d characters", MinPasswordLen)
	case len(pw) > MaxPasswordLen:
		return fmt.Errorf("password must be at most %d bytes (bcrypt limit)", MaxPasswordLen)
	}
	return nil
}

// NormalizeRole validates and canonicalises a role string. An empty role means
// admin, so `msbd users add alice` does the obvious thing.
func NormalizeRole(role string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "", RoleAdmin:
		return RoleAdmin, nil
	case RoleViewer:
		return RoleViewer, nil
	default:
		return "", fmt.Errorf("invalid role %q (want %s or %s)", role, RoleAdmin, RoleViewer)
	}
}

// CreateUser adds an account. Returns ErrExists if the username is taken.
func (s *Store) CreateUser(ctx context.Context, username, password, role string) (*User, error) {
	username = strings.TrimSpace(username)
	if err := ValidateUsername(username); err != nil {
		return nil, err
	}
	if err := ValidatePassword(password); err != nil {
		return nil, err
	}
	role, err := NormalizeRole(role)
	if err != nil {
		return nil, err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), hashCost())
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}
	now := time.Now().Unix()
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO users (username, password_hash, role, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?)`,
		username, string(hash), role, now, now)
	if err != nil {
		if isUnique(err) {
			return nil, fmt.Errorf("user %q: %w", username, ErrExists)
		}
		return nil, fmt.Errorf("create user: %w", err)
	}
	id, _ := res.LastInsertId()
	return &User{
		ID:        id,
		Username:  username,
		Role:      role,
		CreatedAt: time.Unix(now, 0),
		UpdatedAt: time.Unix(now, 0),
	}, nil
}

const userCols = `id, username, role, created_at, updated_at, last_login_at`

func scanUser(sc interface{ Scan(...any) error }) (*User, error) {
	var (
		u    User
		c, m int64
		last sql.NullInt64
	)
	if err := sc.Scan(&u.ID, &u.Username, &u.Role, &c, &m, &last); err != nil {
		return nil, err
	}
	u.CreatedAt = time.Unix(c, 0)
	u.UpdatedAt = time.Unix(m, 0)
	u.LastLoginAt = fromUnix(last)
	return &u, nil
}

// GetUser looks a user up by (case-insensitive) username.
func (s *Store) GetUser(ctx context.Context, username string) (*User, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+userCols+` FROM users WHERE username = ? COLLATE NOCASE`,
		strings.TrimSpace(username))
	u, err := scanUser(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("user %q: %w", username, ErrNotFound)
	}
	return u, err
}

// ListUsers returns every account, oldest first.
func (s *Store) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+userCols+` FROM users ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []User{}
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *u)
	}
	return out, rows.Err()
}

// CountUsers reports how many accounts exist. The dashboard uses this to decide
// whether store-backed login is active at all.
func (s *Store) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

// countAdminsExcept counts admins other than the given user id.
func (s *Store) countAdminsExcept(ctx context.Context, id int64) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM users WHERE role = ? AND id <> ?`, RoleAdmin, id).Scan(&n)
	return n, err
}

// SetPassword changes a user's password and invalidates their existing browser
// sessions — a password change is how you evict a compromised login, so leaving
// old cookies valid would defeat the point.
func (s *Store) SetPassword(ctx context.Context, username, password string) error {
	if err := ValidatePassword(password); err != nil {
		return err
	}
	u, err := s.GetUser(ctx, username)
	if err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), hashCost())
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	if _, err := s.db.ExecContext(ctx,
		`UPDATE users SET password_hash = ?, updated_at = ? WHERE id = ?`,
		string(hash), time.Now().Unix(), u.ID); err != nil {
		return fmt.Errorf("set password: %w", err)
	}
	return s.DeleteUserSessions(ctx, u.ID)
}

// SetRole changes a user's role, refusing to demote the last admin.
func (s *Store) SetRole(ctx context.Context, username, role string) error {
	role, err := NormalizeRole(role)
	if err != nil {
		return err
	}
	u, err := s.GetUser(ctx, username)
	if err != nil {
		return err
	}
	if u.Role == RoleAdmin && role != RoleAdmin {
		others, err := s.countAdminsExcept(ctx, u.ID)
		if err != nil {
			return err
		}
		if others == 0 {
			return ErrLastAdmin
		}
	}
	_, err = s.db.ExecContext(ctx,
		`UPDATE users SET role = ?, updated_at = ? WHERE id = ?`,
		role, time.Now().Unix(), u.ID)
	return err
}

// DeleteUser removes an account (and, by cascade, its sessions). It refuses to
// delete the last admin so the dashboard can never become unreachable.
func (s *Store) DeleteUser(ctx context.Context, username string) error {
	u, err := s.GetUser(ctx, username)
	if err != nil {
		return err
	}
	if u.Role == RoleAdmin {
		others, err := s.countAdminsExcept(ctx, u.ID)
		if err != nil {
			return err
		}
		if others == 0 {
			return ErrLastAdmin
		}
	}
	_, err = s.db.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, u.ID)
	return err
}

// Authenticate verifies a username/password pair and stamps last_login_at.
//
// A missing user still costs one bcrypt comparison (against a fixed dummy hash)
// so the response time doesn't reveal whether the account exists.
func (s *Store) Authenticate(ctx context.Context, username, password string) (*User, error) {
	var (
		hash string
		u    User
		c, m int64
		last sql.NullInt64
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT id, username, role, created_at, updated_at, last_login_at, password_hash
		   FROM users WHERE username = ? COLLATE NOCASE`,
		strings.TrimSpace(username),
	).Scan(&u.ID, &u.Username, &u.Role, &c, &m, &last, &hash)

	if errors.Is(err, sql.ErrNoRows) {
		_ = bcrypt.CompareHashAndPassword([]byte(dummyHash), []byte(password))
		return nil, ErrInvalidCredentials
	}
	if err != nil {
		return nil, err
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		return nil, ErrInvalidCredentials
	}

	now := time.Now()
	if _, err := s.db.ExecContext(ctx,
		`UPDATE users SET last_login_at = ? WHERE id = ?`, now.Unix(), u.ID); err != nil {
		return nil, err
	}
	u.CreatedAt = time.Unix(c, 0)
	u.UpdatedAt = time.Unix(m, 0)
	u.LastLoginAt = now
	return &u, nil
}

// dummyHash is a valid bcrypt digest (cost 12) of an unguessable string, used
// only to equalise the timing of a failed lookup with a failed comparison.
const dummyHash = "$2a$12$eImiTXuWVxfM37uY4JANjQ.dJdrqZ9jL0kM3aVoQhcVGvKLbG1a3G"
