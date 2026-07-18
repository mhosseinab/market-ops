-- +goose Up
-- +goose StatementBegin
-- Owned catalog canonical entities (PRD §7.2 CAT-001, §15.1). Product, Variant,
-- Listing, and Owned Offer are SEPARATE canonical records, each keyed by a
-- stable DK native identifier that is UNIQUE within a marketplace account. The
-- uniqueness constraints are what make upserts identity-stable across repeated
-- and REORDERED payload replays (CAT-001, ACC-005 "zero duplicate canonical
-- records"): a replay in any order conflicts on the native key and updates in
-- place rather than inserting a duplicate.
--
-- These are current-state tables (upserted by sync). The append-only evidence is
-- the separate catalog_payload_snapshots table below.
CREATE TABLE products (
    id                     uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    marketplace_account_id uuid        NOT NULL REFERENCES marketplace_accounts (id) ON DELETE CASCADE,
    -- DK product_id — stable native identifier (PRD §7.2). Unique per account.
    native_product_id      bigint      NOT NULL,
    title                  text        NOT NULL DEFAULT '',
    brand_title            text        NOT NULL DEFAULT '',
    product_url            text        NOT NULL DEFAULT '',
    created_at             timestamptz NOT NULL DEFAULT now(),
    updated_at             timestamptz NOT NULL DEFAULT now(),
    UNIQUE (marketplace_account_id, native_product_id)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE variants (
    id                     uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    marketplace_account_id uuid        NOT NULL REFERENCES marketplace_accounts (id) ON DELETE CASCADE,
    product_id             uuid        NOT NULL REFERENCES products (id) ON DELETE CASCADE,
    -- DK variant id — stable native identifier. Unique per account.
    native_variant_id      bigint      NOT NULL,
    -- Denormalised DK product_id for reconciliation/joins without a lookup.
    native_product_id      bigint      NOT NULL,
    supplier_code          text        NOT NULL DEFAULT '',
    title                  text        NOT NULL DEFAULT '',
    created_at             timestamptz NOT NULL DEFAULT now(),
    updated_at             timestamptz NOT NULL DEFAULT now(),
    UNIQUE (marketplace_account_id, native_variant_id)
);
-- +goose StatementEnd

-- +goose StatementBegin
-- Marketplace listing presence for a variant (DK product_variant_id). Separate
-- canonical entity so a listing's marketplace identity is never conflated with
-- the seller's own variant (identity quarantine, CAT-001).
CREATE TABLE listings (
    id                     uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    marketplace_account_id uuid        NOT NULL REFERENCES marketplace_accounts (id) ON DELETE CASCADE,
    variant_id             uuid        NOT NULL REFERENCES variants (id) ON DELETE CASCADE,
    -- DK product_variant_id — the marketplace-facing listing identity.
    native_listing_id      bigint      NOT NULL,
    selling_channel        text        NOT NULL DEFAULT '',
    product_url            text        NOT NULL DEFAULT '',
    created_at             timestamptz NOT NULL DEFAULT now(),
    updated_at             timestamptz NOT NULL DEFAULT now(),
    UNIQUE (marketplace_account_id, native_listing_id)
);
-- +goose StatementEnd

-- +goose StatementBegin
-- Owned offer for a variant. MONEY QUARANTINE (PRD §9.1, plan §4.7): the price
-- is preserved ONLY as raw evidence (verbatim text / value token / source unit),
-- never as authoritative Money. There is deliberately NO numeric money column,
-- no currency, and no exponent here: the DK source unit (IRR/Toman), exponent,
-- and rounding are validation-gated (Gate 0a) and unknown until confirmed, so no
-- code path may convert this to Money. price_raw_unit is '' when DK omits a unit
-- token (the ambiguous case → stays quarantined, never inferred).
CREATE TABLE owned_offers (
    id                     uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    marketplace_account_id uuid        NOT NULL REFERENCES marketplace_accounts (id) ON DELETE CASCADE,
    variant_id             uuid        NOT NULL REFERENCES variants (id) ON DELETE CASCADE,
    -- Keyed by the DK variant id so there is exactly one owned offer per variant.
    native_variant_id      bigint      NOT NULL,
    -- Raw price evidence (money.RawAmount fields), never promoted to Money.
    price_raw_text         text        NOT NULL DEFAULT '',
    price_raw_value        text        NOT NULL DEFAULT '',
    price_raw_unit         text        NOT NULL DEFAULT '',
    -- Stock counts (integers, not money). NULL when DK omits them.
    seller_stock           bigint,
    warehouse_stock        bigint,
    -- Reconciliation bookkeeping: the sync run that last observed this offer.
    -- A row whose last_seen_run_id is not the current run is drift (missing from
    -- the latest full fetch) — detected by the reconciliation pass (ACC-005).
    last_seen_run_id       uuid,
    last_seen_at           timestamptz,
    created_at             timestamptz NOT NULL DEFAULT now(),
    updated_at             timestamptz NOT NULL DEFAULT now(),
    UNIQUE (marketplace_account_id, native_variant_id)
);
-- +goose StatementEnd

-- +goose StatementBegin
-- APPEND-ONLY raw payload evidence (never-cut invariant, plan §4.7 / CLAUDE.md
-- "observations/audit are append-only"). Every synced variant item's raw JSON is
-- captured verbatim alongside the canonical upsert. There is NO updated_at and
-- NO UPDATE/DELETE query against this table: it is written once and only read.
CREATE TABLE catalog_payload_snapshots (
    id                     uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    marketplace_account_id uuid        NOT NULL REFERENCES marketplace_accounts (id) ON DELETE CASCADE,
    sync_run_id            uuid        NOT NULL,
    native_variant_id      bigint      NOT NULL,
    page                   integer     NOT NULL,
    payload                jsonb       NOT NULL,
    captured_at            timestamptz NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_catalog_snapshots_account_variant
    ON catalog_payload_snapshots (marketplace_account_id, native_variant_id, captured_at DESC);
-- +goose StatementEnd

-- +goose StatementBegin
-- Sync run progress + status. Resumable: next_page is the cursor an interrupted
-- initial import resumes from; the counters back the sync-status view the UI
-- reads (data persisted for a later UI/endpoint step, ACC-004/ACC-005). This is
-- current-state progress tracking (updated as the run advances), not evidence.
CREATE TABLE catalog_sync_runs (
    id                     uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    marketplace_account_id uuid        NOT NULL REFERENCES marketplace_accounts (id) ON DELETE CASCADE,
    kind                   text        NOT NULL CHECK (kind IN ('initial', 'incremental')),
    status                 text        NOT NULL CHECK (status IN ('running', 'completed', 'failed')) DEFAULT 'running',
    -- Resume cursor: the next page to fetch (1-based).
    next_page              integer     NOT NULL DEFAULT 1,
    pages_done             integer     NOT NULL DEFAULT 0,
    total_pages            integer     NOT NULL DEFAULT 0,
    total_rows             integer     NOT NULL DEFAULT 0,
    items_seen             integer     NOT NULL DEFAULT 0,
    records_inserted       integer     NOT NULL DEFAULT 0,
    records_updated        integer     NOT NULL DEFAULT 0,
    drift_count            integer     NOT NULL DEFAULT 0,
    error                  text        NOT NULL DEFAULT '',
    started_at             timestamptz NOT NULL DEFAULT now(),
    updated_at             timestamptz NOT NULL DEFAULT now(),
    completed_at           timestamptz
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_catalog_sync_runs_account_started
    ON catalog_sync_runs (marketplace_account_id, started_at DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE catalog_sync_runs;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE catalog_payload_snapshots;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE owned_offers;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE listings;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE variants;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE products;
-- +goose StatementEnd
