-- name: UpsertConnectorConnection :one
-- Establish or update the connection with sealed tokens (connect / refresh).
INSERT INTO connector_connections (
    marketplace_account_id, connection_state,
    access_token_sealed, refresh_token_sealed,
    access_expires_at, refresh_expires_at, key_version, updated_at
) VALUES ($1, 'connected', $2, $3, $4, $5, $6, now())
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
SELECT * FROM connector_connections
WHERE marketplace_account_id = $1;

-- name: DisconnectConnectorConnection :one
-- Sever the connection and purge sealed tokens (ACC-001). Idempotent.
UPDATE connector_connections SET
    connection_state     = 'disconnected',
    access_token_sealed  = NULL,
    refresh_token_sealed = NULL,
    access_expires_at    = NULL,
    refresh_expires_at   = NULL,
    key_version          = 0,
    updated_at           = now()
WHERE marketplace_account_id = $1
RETURNING *;

-- name: SeedConnectorCapability :exec
-- Insert a capability at 'unknown' if absent; leave an existing row untouched so
-- a prior probe result is not clobbered by a re-seed (capability-gating invariant).
INSERT INTO connector_capabilities (marketplace_account_id, capability, status)
VALUES ($1, $2, 'unknown')
ON CONFLICT (marketplace_account_id, capability) DO NOTHING;

-- name: SetConnectorCapabilityStatus :one
-- Record a probe result. Only a probe calls this; status is set explicitly and
-- last_verified_at stamps when it was determined.
UPDATE connector_capabilities SET
    status           = $3,
    detail           = $4,
    last_verified_at = $5,
    updated_at       = now()
WHERE marketplace_account_id = $1 AND capability = $2
RETURNING *;

-- name: ResetConnectorCapability :exec
-- Return a capability to 'unknown' (disconnect). Clears last_verified_at so a
-- stale verification can never read as current.
UPDATE connector_capabilities SET
    status           = 'unknown',
    detail           = NULL,
    last_verified_at = NULL,
    updated_at       = now()
WHERE marketplace_account_id = $1;

-- name: ListConnectorCapabilities :many
SELECT * FROM connector_capabilities
WHERE marketplace_account_id = $1
ORDER BY capability;
