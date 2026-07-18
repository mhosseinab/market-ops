-- +goose Up
-- S37 consolidated PD-3 gateway endpoints (dk-p0-product-decisions.md): guardrail
-- persistence and the EXT-007 priority watchlist. Two tables:
--   * guardrail_settings — ONE row per account (upserted), the L3 commercial
--     guardrails (contribution floor, movement cap, cooldown, strategy
--     enablement). A write is Owner-only and appends an AUD-001 audit record
--     ATOMICALLY with the mutation (same transaction) — see the
--     audit_records.event_type extension below.
--   * watchlist_entries — the EXT-007 priority watchlist. One row per
--     (account, variant); the SERVER enforces a cap (PRD EXT-007 "Server
--     enforces cap") and every accepted add is audited atomically with the
--     insert, same pattern as guardrail_settings.
--
-- MONEY (PRD §9.1, never-cut): the contribution floor is the exact
-- (mantissa, currency, exponent) triple, NEVER a float.

-- +goose StatementBegin
CREATE TABLE guardrail_settings (
    marketplace_account_id      uuid        PRIMARY KEY REFERENCES marketplace_accounts (id) ON DELETE CASCADE,
    contribution_floor_mantissa bigint      NOT NULL,
    contribution_floor_currency text        NOT NULL,
    contribution_floor_exponent smallint    NOT NULL,
    movement_cap_basis_points   bigint      NOT NULL DEFAULT 500,
    cooldown_seconds            bigint      NOT NULL DEFAULT 3600,
    strategy                    text        NOT NULL CHECK (strategy IN ('hold', 'match', 'undercut')),
    strategy_enabled            boolean     NOT NULL DEFAULT false,
    updated_by                  text        NOT NULL DEFAULT '',
    updated_at                  timestamptz NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
-- EXT-007 priority watchlist. One row per (account, variant) — an add is
-- idempotent (ON CONFLICT DO NOTHING in the query), never a duplicate entry.
CREATE TABLE watchlist_entries (
    id                     uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    marketplace_account_id uuid        NOT NULL REFERENCES marketplace_accounts (id) ON DELETE CASCADE,
    variant_id             uuid        NOT NULL REFERENCES variants (id) ON DELETE CASCADE,
    added_by               text        NOT NULL DEFAULT '',
    created_at             timestamptz NOT NULL DEFAULT now(),
    UNIQUE (marketplace_account_id, variant_id)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_watchlist_entries_account ON watchlist_entries (marketplace_account_id, created_at DESC);
-- +goose StatementEnd

-- +goose StatementBegin
-- Extend the AUD-001 audit_records event_type CHECK with the two new governance
-- events (same 0014 pattern that added 'level2_proposal'). Both are synthetic
-- action_id rows (a fresh gen_random_uuid() minted by the write, not a
-- marketplace action) — the transcript-independent AUD-001 trail this way covers
-- every state-changing operation, not only the marketplace action plane.
ALTER TABLE audit_records DROP CONSTRAINT audit_records_event_type_check;
ALTER TABLE audit_records ADD CONSTRAINT audit_records_event_type_check CHECK (event_type IN (
    'confirmation', 'revalidation_blocked', 'execution_started',
    'external_result', 'reconciliation', 'recommend_only', 'terminal', 'level2_proposal',
    'guardrail_change', 'watchlist_change'));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Reversing this migration removes the two new event_type values, so any audit
-- rows using them must go first (mirrors 0014's own down teardown discipline).
DELETE FROM audit_records WHERE event_type IN ('guardrail_change', 'watchlist_change');
ALTER TABLE audit_records DROP CONSTRAINT audit_records_event_type_check;
ALTER TABLE audit_records ADD CONSTRAINT audit_records_event_type_check CHECK (event_type IN (
    'confirmation', 'revalidation_blocked', 'execution_started',
    'external_result', 'reconciliation', 'recommend_only', 'terminal', 'level2_proposal'));
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX idx_watchlist_entries_account;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE watchlist_entries;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE guardrail_settings;
-- +goose StatementEnd
