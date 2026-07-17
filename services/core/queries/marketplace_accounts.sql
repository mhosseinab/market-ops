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

-- name: GetMarketplaceAccountByNativeID :one
SELECT * FROM marketplace_accounts
WHERE native_account_id = $1;
