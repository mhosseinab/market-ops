-- name: CreateMarketplaceAccount :one
INSERT INTO marketplace_accounts (organization_id, native_account_id, display_name)
VALUES ($1, $2, $3)
RETURNING *;

-- name: GetMarketplaceAccount :one
SELECT * FROM marketplace_accounts
WHERE id = $1;

-- name: GetMarketplaceAccountByOrganization :one
SELECT * FROM marketplace_accounts
WHERE organization_id = $1;

-- name: GetOrgMarketplaceAccountID :one
-- Ownership guard: resolve an account id ONLY when it belongs to the given
-- organization. A foreign or unknown account id yields no row (pgx.ErrNoRows),
-- so possession of a UUID never grants cross-organization access (S8-AUTHZ-001).
SELECT id FROM marketplace_accounts
WHERE id = sqlc.arg(id) AND organization_id = sqlc.arg(organization_id);

-- name: GetMarketplaceAccountByNativeID :one
SELECT * FROM marketplace_accounts
WHERE native_account_id = $1;

-- name: ListMarketplaceAccountIDs :many
-- Every marketplace account id, in a stable order — the per-account fan-out for
-- platform passes (e.g. the daily briefing job, CHAT-010).
SELECT id FROM marketplace_accounts
ORDER BY created_at, id;
