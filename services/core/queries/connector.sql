-- name: UpsertConnectorConnection :one
-- Establish or update the connection with sealed tokens (connect / refresh).
-- ORG-SCOPED (S8-AUTHZ-001): the INSERT ... SELECT only materialises a row when
-- the account belongs to the given organization, so a caller cannot create or
-- overwrite a connection for another organization's account. A foreign account
-- yields zero source rows: nothing is inserted, no conflict fires, and RETURNING
-- is empty (pgx.ErrNoRows) — the same fail-closed result as an unknown account.
INSERT INTO connector_connections (
    marketplace_account_id, connection_state,
    access_token_sealed, refresh_token_sealed,
    access_expires_at, refresh_expires_at, key_version, updated_at
)
SELECT ma.id, 'connected',
    sqlc.arg(access_token_sealed), sqlc.arg(refresh_token_sealed),
    sqlc.arg(access_expires_at), sqlc.arg(refresh_expires_at), sqlc.arg(key_version), now()
FROM marketplace_accounts ma
WHERE ma.id = sqlc.arg(marketplace_account_id)
  AND ma.organization_id = sqlc.arg(organization_id)
ON CONFLICT (marketplace_account_id) DO UPDATE SET
    connection_state     = 'connected',
    access_token_sealed  = EXCLUDED.access_token_sealed,
    refresh_token_sealed = EXCLUDED.refresh_token_sealed,
    access_expires_at    = EXCLUDED.access_expires_at,
    refresh_expires_at   = EXCLUDED.refresh_expires_at,
    key_version          = EXCLUDED.key_version,
    updated_at           = now()
RETURNING *;

-- name: GetConnectorConnection :one
-- ORG-SCOPED: only returns the row when the account belongs to the organization.
SELECT cc.* FROM connector_connections cc
JOIN marketplace_accounts ma ON ma.id = cc.marketplace_account_id
WHERE cc.marketplace_account_id = sqlc.arg(marketplace_account_id)
  AND ma.organization_id = sqlc.arg(organization_id);

-- name: DisconnectConnectorConnection :one
-- Sever the connection and purge sealed tokens (ACC-001). Idempotent.
-- ORG-SCOPED: a foreign account matches zero rows, so no mutation occurs and
-- RETURNING is empty — identical to disconnecting an unknown account.
UPDATE connector_connections cc SET
    connection_state     = 'disconnected',
    access_token_sealed  = NULL,
    refresh_token_sealed = NULL,
    access_expires_at    = NULL,
    refresh_expires_at   = NULL,
    key_version          = 0,
    updated_at           = now()
FROM marketplace_accounts ma
WHERE cc.marketplace_account_id = ma.id
  AND cc.marketplace_account_id = sqlc.arg(marketplace_account_id)
  AND ma.organization_id = sqlc.arg(organization_id)
RETURNING cc.*;

-- name: SeedConnectorCapability :exec
-- Insert a capability at 'unknown' if absent; leave an existing row untouched so
-- a prior probe result is not clobbered by a re-seed (capability-gating invariant).
-- ORG-SCOPED: seeds only when the account belongs to the organization.
INSERT INTO connector_capabilities (marketplace_account_id, capability, status)
SELECT ma.id, sqlc.arg(capability), 'unknown'
FROM marketplace_accounts ma
WHERE ma.id = sqlc.arg(marketplace_account_id)
  AND ma.organization_id = sqlc.arg(organization_id)
ON CONFLICT (marketplace_account_id, capability) DO NOTHING;

-- name: SetConnectorCapabilityStatus :one
-- Record a probe result. Only a probe calls this; status is set explicitly and
-- last_verified_at stamps when it was determined. ORG-SCOPED.
UPDATE connector_capabilities cc SET
    status           = sqlc.arg(status),
    detail           = sqlc.arg(detail),
    last_verified_at = sqlc.arg(last_verified_at),
    updated_at       = now()
FROM marketplace_accounts ma
WHERE cc.marketplace_account_id = ma.id
  AND cc.marketplace_account_id = sqlc.arg(marketplace_account_id)
  AND cc.capability = sqlc.arg(capability)
  AND ma.organization_id = sqlc.arg(organization_id)
RETURNING cc.*;

-- name: ResetConnectorCapability :exec
-- Return a capability to 'unknown' (disconnect). Clears last_verified_at so a
-- stale verification can never read as current. ORG-SCOPED: a foreign account
-- matches zero rows, so no capability of another organization is ever reset.
UPDATE connector_capabilities cc SET
    status           = 'unknown',
    detail           = NULL,
    last_verified_at = NULL,
    updated_at       = now()
FROM marketplace_accounts ma
WHERE cc.marketplace_account_id = ma.id
  AND cc.marketplace_account_id = sqlc.arg(marketplace_account_id)
  AND ma.organization_id = sqlc.arg(organization_id);

-- name: ListConnectorCapabilities :many
-- ORG-SCOPED: capability rows are visible only to the owning organization.
SELECT cc.* FROM connector_capabilities cc
JOIN marketplace_accounts ma ON ma.id = cc.marketplace_account_id
WHERE cc.marketplace_account_id = sqlc.arg(marketplace_account_id)
  AND ma.organization_id = sqlc.arg(organization_id)
ORDER BY cc.capability;
