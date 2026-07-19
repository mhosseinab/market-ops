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

-- name: SetOwnedSellerID :one
-- Bind (or clear, with NULL) the account's AUTHORITATIVE owned DK seller identity
-- (issue #212). Populated by account provisioning/sync (S10) from the DK seller
-- profile; the column CHECK rejects a non-decimal value. The market-event
-- ObservationSource excludes the account's OWN offer by comparing an observation's
-- native_seller_id against THIS validated id — never the free-form native_account_id
-- handle. A NULL owned_seller_id is an unresolved identity and the source fails
-- closed (quarantines the account) rather than guessing.
UPDATE marketplace_accounts SET
    owned_seller_id = $2,
    updated_at      = now()
WHERE id = $1
RETURNING *;

-- name: ListMarketplaceAccountIDs :many
-- Every marketplace account id, in a stable order — the per-account fan-out for
-- platform passes (e.g. the daily briefing job, CHAT-010).
SELECT id FROM marketplace_accounts
ORDER BY created_at, id;
