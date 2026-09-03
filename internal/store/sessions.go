package store

// sessions.go — dashboard browser sessions.
//
// The dashboard authenticates with an opaque, random, server-side session id in
// a cookie rather than a signed token: revocation then means DELETE, with no
// key rotation, no clock skew and no JWT footguns. Sessions cascade away when
// their user is deleted (schema FK) and when their password changes
// (SetPassword calls DeleteUserSessions).

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"time"
)

// DefaultSessionTTL is how long a dashboard login stays valid.
const DefaultSessionTTL = 12 * time.Hour

// Session is a live dashboard login.
type Session struct {
	ID        string
	UserID    int64
	CreatedAt time.Time
	ExpiresAt time.Time
	UserAgent string
	IP        string
}

// newSessionID mints 256 bits of CSPRNG output as the cookie value.
func newSessionID() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate session id: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// CreateSession starts a login for userID. ttl <= 0 uses DefaultSessionTTL.
func (s *Store) CreateSession(ctx context.Context, userID int64, ttl time.Duration, userAgent, ip string) (*Session, error) {
	if ttl <= 0 {
		ttl = DefaultSessionTTL
	}
	id, err := newSessionID()
	if err != nil {
		return nil, err
	}
	now := time.Now()
	exp := now.Add(ttl)
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO sessions (id, user_id, created_at, expires_at, user_agent, ip)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		id, userID, now.Unix(), exp.Unix(), truncate(userAgent, 256), truncate(ip, 64)); err != nil {
		return nil, fmt.Errorf("create session: %w", err)
	}
	// Opportunistically drop expired rows so the table can't grow without
	// bound on a long-lived daemon that never restarts.
	_, _ = s.SweepSessions(ctx)
	return &Session{
		ID:        id,
		UserID:    userID,
		CreatedAt: now,
		ExpiresAt: exp,
		UserAgent: userAgent,
		IP:        ip,
	}, nil
}

// LookupSession resolves a cookie value to its session and owning user. An
// expired session is deleted and reported as ErrNotFound, so a stale cookie
// behaves exactly like an absent one.
func (s *Store) LookupSession(ctx context.Context, id string) (*Session, *User, error) {
	if id == "" {
		return nil, nil, ErrNotFound
	}
	var (
		sess    Session
		u       User
		created int64
		expires int64
		uc, um  int64
		last    sql.NullInt64
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT s.id, s.user_id, s.created_at, s.expires_at, s.user_agent, s.ip,
		        u.id, u.username, u.role, u.created_at, u.updated_at, u.last_login_at
		   FROM sessions s JOIN users u ON u.id = s.user_id
		  WHERE s.id = ?`, id,
	).Scan(&sess.ID, &sess.UserID, &created, &expires, &sess.UserAgent, &sess.IP,
		&u.ID, &u.Username, &u.Role, &uc, &um, &last)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, ErrNotFound
	}
	if err != nil {
		return nil, nil, err
	}
	sess.CreatedAt = time.Unix(created, 0)
	sess.ExpiresAt = time.Unix(expires, 0)
	if time.Now().After(sess.ExpiresAt) {
		_ = s.DeleteSession(ctx, id)
		return nil, nil, ErrNotFound
	}
	u.CreatedAt = time.Unix(uc, 0)
	u.UpdatedAt = time.Unix(um, 0)
	u.LastLoginAt = fromUnix(last)
	return &sess, &u, nil
}

// DeleteSession ends one login (sign out).
func (s *Store) DeleteSession(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, id)
	return err
}

// DeleteUserSessions ends every login for a user (password change, forced
// sign-out).
func (s *Store) DeleteUserSessions(ctx context.Context, userID int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, userID)
	return err
}

// SweepSessions removes expired rows and reports how many were deleted.
func (s *Store) SweepSessions(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM sessions WHERE expires_at <= ?`, time.Now().Unix())
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
