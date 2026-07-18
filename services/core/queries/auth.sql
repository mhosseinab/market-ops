-- name: UpsertUserCredential :exec
-- Set or rotate a user's argon2id password hash. Current-state upsert: a
-- password change replaces the hash in place.
INSERT INTO user_credentials (user_id, password_hash)
VALUES ($1, $2)
ON CONFLICT (user_id) DO UPDATE SET
    password_hash = EXCLUDED.password_hash,
    updated_at    = now();

-- name: GetUserCredential :one
SELECT * FROM user_credentials
WHERE user_id = $1;

-- name: CreateSession :one
-- Open a server-side session. token_hash is the SHA-256 of the opaque cookie
-- token; the raw token never reaches the database.
INSERT INTO sessions (token_hash, user_id, expires_at)
VALUES ($1, $2, $3)
RETURNING *;

-- name: GetSessionUser :one
-- Resolve a live session to its principal (user + role + organization). Rows
-- at/after expiry are excluded, so an expired cookie fails closed.
SELECT s.token_hash, s.expires_at,
       u.id AS user_id, u.organization_id, u.email, u.role
FROM sessions s
JOIN users u ON u.id = s.user_id
WHERE s.token_hash = $1 AND s.expires_at > now();

-- name: DeleteSession :exec
-- Close a single session (logout). Idempotent: deleting an absent row is a no-op.
DELETE FROM sessions
WHERE token_hash = $1;

-- name: DeleteSessionsForUser :exec
-- Revoke every session for a user (role change / password rotation).
DELETE FROM sessions
WHERE user_id = $1;

-- name: DeleteExpiredSessions :exec
-- Sweep expired sessions.
DELETE FROM sessions
WHERE expires_at <= now();
