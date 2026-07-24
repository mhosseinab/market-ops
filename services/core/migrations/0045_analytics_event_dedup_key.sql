-- +goose Up
-- Analytics event DEDUPLICATION key (issue #111 / PD-4 item 1; PRD §18 event
-- families, §4.6 event-deduplication never-cut).
--
-- WHY: §18 events are produced from lifecycle transitions whose delivery is
-- at-least-once (a River retry, a re-observed commit, a redelivered job). Without a
-- key, a retried emission writes a SECOND row for the SAME business fact and every
-- §18 dashboard (activation, WVRA, unit economics) silently over-counts. Event
-- deduplication is a never-cut invariant, so the guarantee must be STRUCTURAL — a
-- database constraint that holds for every writer, including a future out-of-band
-- one — not a best-effort check in one service.
--
-- SHAPE (mirrors the PROVEN notifications pattern from migration 0015:
-- UNIQUE (marketplace_account_id, dedup_key) + ON CONFLICT DO NOTHING):
--
--   * dedup_key is NULLABLE. analytics_events already holds rows written before this
--     key existed; they are historical facts and are NOT rewritten, and no fabricated
--     backfill value is invented for them (a synthesized key would assert a dedup
--     identity nobody measured). NULL means "written before the key existed".
--   * The unique index is PARTIAL (WHERE dedup_key IS NOT NULL) so those historical
--     rows are simply outside the constraint, while every NEW keyed row is covered.
--   * The index is ACCOUNT-SCOPED, never global. A key is only ever unique WITHIN one
--     marketplace account, exactly like notifications.dedup_key. A globally-unique key
--     would let one tenant's event SUPPRESS another tenant's event — cross-tenant data
--     loss — which the composite (marketplace_account_id, organization_id) foreign key
--     from migration 0036 exists to prevent on the write side.
--
-- APPEND-ONLY (§4.6): this migration adds NO update path. The insert query uses
-- ON CONFLICT DO NOTHING (never DO UPDATE), so a duplicate is SUPPRESSED, never
-- merged into or overwriting an existing row. analytics_events stays INSERT/SELECT.
--
-- Fresh database: a pure schema change (the table is empty, the index builds empty).

-- +goose StatementBegin
ALTER TABLE analytics_events
    ADD COLUMN dedup_key text;
-- +goose StatementEnd

-- +goose StatementBegin
COMMENT ON COLUMN analytics_events.dedup_key IS
    'Stable per-account deduplication key for the producing lifecycle transition (issue #111). NULL only for rows written before the key existed; every new row is keyed.';
-- +goose StatementEnd

-- +goose StatementBegin
-- Account-scoped PARTIAL unique index: the structural no-double-count guarantee and
-- the arbiter for the insert query's ON CONFLICT DO NOTHING.
CREATE UNIQUE INDEX idx_analytics_events_account_dedup_key
    ON analytics_events (marketplace_account_id, dedup_key)
    WHERE dedup_key IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- Reverse in dependency order: drop the index, then the column. Dropping the column
-- discards only the dedup keys (the events themselves are untouched), so up+down is a
-- clean round-trip on a fresh database.

-- +goose StatementBegin
DROP INDEX idx_analytics_events_account_dedup_key;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE analytics_events
    DROP COLUMN dedup_key;
-- +goose StatementEnd
