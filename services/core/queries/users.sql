-- name: CreateUser :one
-- Email is stored in its canonical (normalized) form via email_canonical() —
-- the SINGLE SQL canonicalizer (migration 0034) that reproduces Go's
-- internal/normalize.Email (full Unicode White_Space trim + case-fold) EXACTLY.
-- The same expression backs the users_email_canonical_key unique index and the
-- GetUserByEmail lookup, so write, uniqueness, and login share one definition
-- with no divergence. Normalizing in SQL guarantees write-time canonicalization
-- for every caller, not just those that remembered to normalize first.
INSERT INTO users (organization_id, email, role)
VALUES (sqlc.arg(organization_id), email_canonical(sqlc.arg(email)), sqlc.arg(role))
RETURNING *;

-- name: GetUser :one
SELECT * FROM users
WHERE id = $1;

-- name: GetUserByEmail :one
-- Login identifier lookup (issue #12, #201). Both sides run through the SAME
-- email_canonical() the write path and unique index use, so the lookup can never
-- diverge from storage/uniqueness (the divergence between Go TrimSpace and SQL
-- 1-arg btrim that let a padded id resolve another org's row is closed). Wrapping
-- the argument too makes the DB the enforcement authority even if the caller's
-- pre-normalization ever drifted. Normalized email is GLOBALLY unique (the
-- users_email_canonical_key functional index), so this resolves at most one
-- principal — and therefore exactly one organization — using that unique index,
-- deterministically, with no LIMIT 1 tie-break masking an ambiguous match.
SELECT * FROM users
WHERE email_canonical(email) = email_canonical($1);

-- name: ListUsersByOrganization :many
SELECT * FROM users
WHERE organization_id = $1
ORDER BY created_at, id;
