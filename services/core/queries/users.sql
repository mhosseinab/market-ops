-- name: CreateUser :one
-- Email is stored in its canonical (normalized) form: trimmed and case-folded,
-- matching internal/normalize.Email and the global UNIQUE index on lower(email).
-- Normalizing in SQL guarantees write-time canonicalization for every caller,
-- not just those that remembered to normalize first.
INSERT INTO users (organization_id, email, role)
VALUES (sqlc.arg(organization_id), lower(btrim(sqlc.arg(email))), sqlc.arg(role))
RETURNING *;

-- name: GetUser :one
SELECT * FROM users
WHERE id = $1;

-- name: GetUserByEmail :one
-- Login identifier lookup (issue #12). Normalized email is GLOBALLY unique (see
-- the UNIQUE index on lower(email)), so this resolves at most one principal —
-- and therefore exactly one organization. The caller passes an already-normalized
-- email (internal/normalize.Email); matching on lower(email) uses that unique
-- functional index and is deterministic, with no LIMIT 1 tie-break masking an
-- ambiguous match.
SELECT * FROM users
WHERE lower(email) = $1;

-- name: ListUsersByOrganization :many
SELECT * FROM users
WHERE organization_id = $1
ORDER BY created_at, id;
