-- +goose Up
-- Cost profiles, CSV import, and margin readiness (PRD §7.2 CST-001..003, §9.2,
-- §16). MONEY QUARANTINE / MONEY CORRECTNESS (PRD §9.1): cost values are
-- seller-entered in the account's CONFIGURED currency, so they ARE representable
-- as an authoritative money.Money (currency known) — stored here as the exact
-- integer (mantissa, currency, exponent) triple, never a float. The RAW entered
-- text/value/unit is preserved separately (raw_* columns) as evidence, never
-- conflated with the Money. These values are deliberately EXCLUDED from every
-- executable path until S16 (contribution/policy) + S35 (verified parameters);
-- nothing in S12 wires them into an approve/execute path.

-- +goose StatementBegin
-- Per-account cost policy. Two decisions live here so neither is hardcoded in
-- logic (LOC-001 locale-neutral core, §9.2 "by account policy"):
--   * entry_currency/entry_exponent: the currency + canonical exponent the seller
--     enters costs in. This is the ENTRY representation only — NOT the gated Toman
--     display transform (that stays disabled until the S35 region probes).
--   * required_optional_components: which of the P0-optional components
--     (packaging/ads/returns) THIS account requires. Their absence demotes margin
--     readiness to Partial (§9.2 "missing component ... may make readiness Partial
--     by account policy"). Read from here; never a hardcoded component branch.
CREATE TABLE account_cost_policies (
    marketplace_account_id       uuid        PRIMARY KEY REFERENCES marketplace_accounts (id) ON DELETE CASCADE,
    entry_currency               text        NOT NULL DEFAULT 'IRR',
    entry_exponent               smallint    NOT NULL DEFAULT 0,
    required_optional_components jsonb       NOT NULL DEFAULT '[]'::jsonb,
    created_at                   timestamptz NOT NULL DEFAULT now(),
    updated_at                   timestamptz NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
-- Per-SKU applicability of the "required when applicable to the listing"
-- components (§9.2: fulfillment/shipping/promotion). A component listed here is
-- required for THIS listing, so its absence prevents Complete readiness. Default
-- (no row) ⇒ none applicable. Populated by the listing/fulfilment sync in a later
-- step; readiness reads it as data, never a hardcoded rule.
CREATE TABLE sku_cost_requirements (
    variant_id             uuid        PRIMARY KEY REFERENCES variants (id) ON DELETE CASCADE,
    marketplace_account_id uuid        NOT NULL REFERENCES marketplace_accounts (id) ON DELETE CASCADE,
    applicable_components  jsonb       NOT NULL DEFAULT '[]'::jsonb,
    updated_at             timestamptz NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
-- CSV import batch (CST-001). A batch is created in 'preview' and holds its row
-- dispositions; NO cost value commits until the preview is explicitly confirmed
-- (the two-step preview→commit contract makes "no row commits before preview
-- confirmation" structural). A batch with any unresolved duplicate conflict
-- (§16) cannot be committed. The disposition counts back the preview cards.
CREATE TABLE cost_import_batches (
    id                     uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    marketplace_account_id uuid        NOT NULL REFERENCES marketplace_accounts (id) ON DELETE CASCADE,
    filename               text        NOT NULL DEFAULT '',
    status                 text        NOT NULL DEFAULT 'preview'
                                       CHECK (status IN ('preview', 'committed', 'cancelled')),
    accept_count           integer     NOT NULL DEFAULT 0,
    reject_count           integer     NOT NULL DEFAULT 0,
    duplicate_count        integer     NOT NULL DEFAULT 0,
    created_by             uuid        REFERENCES users (id) ON DELETE SET NULL,
    created_at             timestamptz NOT NULL DEFAULT now(),
    committed_at           timestamptz
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_cost_import_batches_account
    ON cost_import_batches (marketplace_account_id, created_at DESC);
-- +goose StatementEnd

-- +goose StatementBegin
-- One preview row per (file line, cost component). Each row carries its raw
-- captured cells (verbatim, plus the digit-normalized numeric token — LOC-007),
-- the resolved variant (or NULL when the SKU did not resolve), the parsed Money
-- triple when acceptable, a disposition, and — for every non-accept — a stated
-- machine reason (CST-001 "every rejected row has a reason"; §16 duplicate rows
-- are a 'duplicate' conflict). The CHECK enforces the reason invariant in the
-- schema so no code path can commit a reject/duplicate row without a reason.
CREATE TABLE cost_import_rows (
    id                  uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    batch_id            uuid        NOT NULL REFERENCES cost_import_batches (id) ON DELETE CASCADE,
    row_number          integer     NOT NULL,
    raw_sku             text        NOT NULL DEFAULT '',
    component           text        NOT NULL,
    raw_value           text        NOT NULL DEFAULT '',
    normalized_value    text        NOT NULL DEFAULT '',
    raw_unit            text        NOT NULL DEFAULT '',
    resolved_variant_id uuid        REFERENCES variants (id) ON DELETE SET NULL,
    amount_mantissa     bigint,
    amount_currency     text        NOT NULL DEFAULT '',
    amount_exponent     smallint    NOT NULL DEFAULT 0,
    disposition         text        NOT NULL
                                    CHECK (disposition IN ('accept', 'reject', 'duplicate')),
    reason              text        NOT NULL DEFAULT '',
    created_at          timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT cost_import_row_reason_required
        CHECK (disposition = 'accept' OR reason <> '')
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_cost_import_rows_batch
    ON cost_import_rows (batch_id, row_number);
-- +goose StatementEnd

-- +goose StatementBegin
-- APPEND-ONLY, component-versioned, effective-dated cost profiles (CST-002). A
-- new value for a component NEVER updates a prior row: it inserts the NEXT version
-- with its own effective_from. The version "in force at time T" for a component is
-- the row with the greatest effective_from <= T — so effective_to is DERIVED at
-- read time and never stored, which keeps this table strictly append-only and
-- lets a historical recommendation reproduce the EXACT version active when it was
-- generated. There is deliberately no updated_at and no UPDATE/DELETE query.
--
-- MONEY: (amount_mantissa, amount_currency, amount_exponent) is the exact
-- money.Money triple (currency known ⇒ representable). raw_* is the seller's
-- entered evidence, preserved separately from the Money (§9.1). stale_after is an
-- optional review-by instant: when set and in the past, the in-force value is
-- STALE for readiness (data-driven, not a hardcoded max-age branch).
CREATE TABLE cost_profiles (
    id                     uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    marketplace_account_id uuid        NOT NULL REFERENCES marketplace_accounts (id) ON DELETE CASCADE,
    variant_id             uuid        NOT NULL REFERENCES variants (id) ON DELETE CASCADE,
    component              text        NOT NULL
                                       CHECK (component IN ('cogs', 'commission', 'fulfillment', 'shipping', 'packaging', 'promotion', 'ads', 'returns')),
    version                integer     NOT NULL,
    amount_mantissa        bigint      NOT NULL,
    amount_currency        text        NOT NULL,
    amount_exponent        smallint    NOT NULL,
    raw_text               text        NOT NULL DEFAULT '',
    raw_value              text        NOT NULL DEFAULT '',
    raw_unit               text        NOT NULL DEFAULT '',
    effective_from         timestamptz NOT NULL,
    stale_after            timestamptz,
    source                 text        NOT NULL
                                       CHECK (source IN ('csv_import', 'single_value', 'connector')),
    import_batch_id        uuid        REFERENCES cost_import_batches (id) ON DELETE SET NULL,
    created_by             uuid        REFERENCES users (id) ON DELETE SET NULL,
    created_at             timestamptz NOT NULL DEFAULT now(),
    UNIQUE (variant_id, component, version)
);
-- +goose StatementEnd

-- +goose StatementBegin
-- Point-in-time lookup index (CST-002): the in-force version per component at a
-- timestamp is DISTINCT ON (component) ORDER BY effective_from DESC.
CREATE INDEX idx_cost_profiles_pit
    ON cost_profiles (variant_id, component, effective_from DESC, version DESC);
-- +goose StatementEnd

-- +goose StatementBegin
-- Derived margin-readiness projection per SKU (CST-003). Recomputed on any input
-- change (a committed import, a single-value entry). The four states are closed:
-- complete | partial | stale | missing. Only 'complete' drives an executable
-- recommendation (enforced downstream in S16/S17); 'partial' may show analysis but
-- exposes no approval control; 'stale'/'missing' block. missing_components and
-- stale_components name the blocking components for the UI blocker chips. This is a
-- current-state projection (upserted), NOT evidence.
CREATE TABLE margin_readiness (
    variant_id             uuid        PRIMARY KEY REFERENCES variants (id) ON DELETE CASCADE,
    marketplace_account_id uuid        NOT NULL REFERENCES marketplace_accounts (id) ON DELETE CASCADE,
    state                  text        NOT NULL
                                       CHECK (state IN ('complete', 'partial', 'stale', 'missing')),
    missing_components     jsonb       NOT NULL DEFAULT '[]'::jsonb,
    stale_components       jsonb       NOT NULL DEFAULT '[]'::jsonb,
    computed_at            timestamptz NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_margin_readiness_account
    ON margin_readiness (marketplace_account_id, state);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE margin_readiness;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE cost_profiles;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE cost_import_rows;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE cost_import_batches;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE sku_cost_requirements;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE account_cost_policies;
-- +goose StatementEnd
