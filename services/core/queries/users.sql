-- name: CreateUser :one
INSERT INTO users (organization_id, email, role)
VALUES ($1, $2, $3)
RETURNING *;

-- name: GetUser :one
SELECT * FROM users
WHERE id = $1;

-- name: GetUserByEmail :one
-- Login identifier lookup. Emails are unique per organization; in P0 the beta
-- runs one organization, so this resolves the login user. A duplicate email
-- across organizations would return the earliest-created row deterministically.
SELECT * FROM users
WHERE email = $1
ORDER BY created_at, id
LIMIT 1;

-- name: ListUsersByOrganization :many
SELECT * FROM users
WHERE organization_id = $1
ORDER BY created_at, id;
