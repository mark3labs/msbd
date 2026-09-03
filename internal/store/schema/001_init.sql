-- 001_init.sql — users, API keys and browser sessions.
--
-- Timestamps are Unix seconds (INTEGER); "absent" is NULL, never 0.
-- Append-only: to change this schema add 002_*.sql, never edit this file.

CREATE TABLE users (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  -- COLLATE NOCASE: usernames are case-insensitive for both uniqueness and
  -- lookup, so "Admin" can't shadow "admin" at the login form.
  username      TEXT    NOT NULL UNIQUE COLLATE NOCASE,
  password_hash TEXT    NOT NULL,          -- bcrypt
  role          TEXT    NOT NULL DEFAULT 'admin',  -- 'admin' | 'viewer'
  created_at    INTEGER NOT NULL,
  updated_at    INTEGER NOT NULL,
  last_login_at INTEGER
);

CREATE TABLE api_keys (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  name         TEXT    NOT NULL,
  -- prefix is the leading, non-secret slice of the token, kept so the UI and
  -- CLI can identify a key ("msbd_a1b2c3d…") without ever storing the secret.
  prefix       TEXT    NOT NULL,
  -- token_hash is sha256(token) hex. NOT bcrypt: the token is already 256 bits
  -- of CSPRNG output, so a slow KDF would only tax every authenticated request.
  token_hash   TEXT    NOT NULL UNIQUE,
  created_at   INTEGER NOT NULL,
  expires_at   INTEGER,                    -- NULL = never expires
  last_used_at INTEGER,
  revoked_at   INTEGER,                    -- NULL = live
  created_by   TEXT    NOT NULL DEFAULT ''
);

CREATE INDEX idx_api_keys_prefix ON api_keys(prefix);

CREATE TABLE sessions (
  id         TEXT    PRIMARY KEY,          -- the opaque cookie value
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  created_at INTEGER NOT NULL,
  expires_at INTEGER NOT NULL,
  user_agent TEXT    NOT NULL DEFAULT '',
  ip         TEXT    NOT NULL DEFAULT ''
);

CREATE INDEX idx_sessions_user    ON sessions(user_id);
CREATE INDEX idx_sessions_expires ON sessions(expires_at);
