-- +goose Up
-- +goose StatementBegin
-- DK connector state (PRD §15.2, ACC-001/ACC-003). One connection row per
-- marketplace account. Tokens are stored ONLY as AES-256-GCM sealed blobs
-- (nonce||ciphertext); plaintext tokens are never written. key_version tags the
-- encryption key so a rotation can find rows sealed under an older key. This is
-- current-state (not append-only): connect/refresh/disconnect update in place.
CREATE TABLE connector_connections (
    id                       uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    marketplace_account_id   uuid        NOT NULL UNIQUE REFERENCES marketplace_accounts (id) ON DELETE CASCADE,
    -- Fixed state set: a severed connection can drive nothing (ACC-001).
    connection_state         text        NOT NULL CHECK (connection_state IN ('connected', 'disconnected')) DEFAULT 'disconnected',
    -- Sealed token material. NULL while disconnected; never plaintext.
    access_token_sealed      bytea,
    refresh_token_sealed     bytea,
    access_expires_at        timestamptz,
    refresh_expires_at       timestamptz,
    -- Encryption key identifier for rotation; 0 means "no token sealed yet".
    key_version              integer     NOT NULL DEFAULT 0,
    created_at               timestamptz NOT NULL DEFAULT now(),
    updated_at               timestamptz NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
-- Per-capability status + last-verified time (ACC-001). Every capability starts
-- Unknown; nothing flips to 'supported' without a probe result. Current-state
-- table (upserted by probes), not append-only.
CREATE TABLE connector_capabilities (
    id                       uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    marketplace_account_id   uuid        NOT NULL REFERENCES marketplace_accounts (id) ON DELETE CASCADE,
    -- One of the nine §15.2 capability keys. Widening is a deliberate migration.
    capability               text        NOT NULL CHECK (capability IN (
        'catalog_read', 'owned_offer_read', 'stock_read', 'buybox_read',
        'boundary_read', 'commission_read', 'sales_context_read',
        'price_write', 'change_feed'
    )),
    -- Fixed status set (§15.2). Default 'unknown' encodes the never-cut
    -- capability-gating invariant at the storage layer.
    status                   text        NOT NULL CHECK (status IN ('unknown', 'supported', 'unsupported', 'degraded')) DEFAULT 'unknown',
    -- Recovery-oriented reason for a non-supported status (ACC-003).
    detail                   text,
    -- NULL until the first probe runs; a historical value never reads as current.
    last_verified_at         timestamptz,
    created_at               timestamptz NOT NULL DEFAULT now(),
    updated_at               timestamptz NOT NULL DEFAULT now(),
    UNIQUE (marketplace_account_id, capability)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE connector_capabilities;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE connector_connections;
-- +goose StatementEnd
