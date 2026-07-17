-- name: CreateOrganization :one
INSERT INTO organizations (name)
VALUES ($1)
RETURNING *;

-- name: GetOrganization :one
SELECT * FROM organizations
WHERE id = $1;

-- name: ListOrganizations :many
SELECT * FROM organizations
ORDER BY created_at, id;

-- name: RenameOrganization :one
UPDATE organizations
SET name = $2, updated_at = now()
WHERE id = $1
RETURNING *;
