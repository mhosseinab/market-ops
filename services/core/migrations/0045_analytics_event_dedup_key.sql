-- +goose Up
-- Analytics event DEDUPLICATION key (issue #111 / PD-4 item 1; PRD §18 event
-- families, §4.6 event-deduplication never-cut).
--
-- WHY: §18 events are produced from lifecycle transitions whose delivery is
-- at-least-once (a River retry, a re-observed commit, a redelivered job). Without a
-- key, a retried emission writes a SECOND row for the SAME business fact and every
-- §18 dashboard (activation, WVRA, unit economics) silently over-counts. Event
-- deduplication is a never-cut invariant, so the guarantee is STRUCTURAL for KEYED
-- rows — a database constraint that holds for every writer, including a future
-- out-of-band one.
--
-- EXACTLY WHAT THIS SCHEMA ENFORCES (state the guarantee precisely; do not overclaim):
--
--   1. Two rows in ONE account with the SAME non-null dedup_key cannot both exist —
--      enforced by the partial unique index below, for EVERY writer.
--   2. A row's dedup_key can never be the EMPTY string — enforced by the
--      analytics_events_dedup_key_nonempty CHECK below, for EVERY writer. '' is a
--      rejected value, never a deduplicated one.
--
-- WHAT IT DOES *NOT* ENFORCE — the residual and its owner:
--
--   * Nothing at the SCHEMA level requires a row to be keyed at all. An UNKEYED
--     (dedup_key IS NULL) row is outside the partial index, so N unkeyed rows for the
--     same business fact all persist and are NOT deduplicated. The only thing
--     preventing that is the SERVICE boundary: analytics.Emit rejects an unkeyed
--     event with ErrMissingDedupKey (internal/analytics/analytics.go), which binds
--     the Go core and nothing else. RESIDUAL OWNER: the Go core (internal/analytics).
--     A future out-of-band writer that inserts NULL is therefore NOT structurally
--     prevented — making the column NOT NULL is the follow-on hardening, deliberately
--     deferred here because it forecloses the nullability the shape below relies on.
--
-- SHAPE (mirrors the PROVEN notifications pattern from migration 0015:
-- UNIQUE (marketplace_account_id, dedup_key) + ON CONFLICT DO NOTHING):
--
--   * dedup_key is NULLABLE so that "unkeyed" is representable without inventing a
--     value: no fabricated backfill key is ever synthesized for a row (a synthesized
--     key would assert a dedup identity nobody measured). NULL means "written by a
--     path that supplied no key" — which, for the Go core, Emit makes unreachable.
--     (This migration is a pure schema change: S34, the first production deploy, is
--     still pending, so there are no existing rows to backfill or exempt.)
--   * The unique index is PARTIAL (WHERE dedup_key IS NOT NULL) so unkeyed rows are
--     simply outside the constraint, while every KEYED row is covered.
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
    'Stable per-account deduplication key for the producing lifecycle transition (issue #111). Non-null values are structurally unique per account and never empty. NULL means the writing path supplied no key: such rows are OUTSIDE the partial unique index and are NOT deduplicated. analytics.Emit in the Go core never writes NULL (ErrMissingDedupKey); no other writer is structurally prevented from doing so.';
-- +goose StatementEnd

-- +goose StatementBegin
-- The EMPTY key is REJECTED, never deduplicated. '' IS NOT NULL, so without this
-- CHECK an empty key would fall INSIDE the partial unique index below: the first ''
-- row would win the account's single '' slot and every later '' row — a DIFFERENT
-- business fact — would be silently suppressed as if it were a legitimate retry.
-- That is data loss wearing the deduplication invariant's uniform, and it is exactly
-- what an out-of-band writer (or a caller that simply omits the generated params
-- field, whose zero value is "") would produce. Absent means NULL (unkeyed, outside
-- the index); present means non-empty.
ALTER TABLE analytics_events
    ADD CONSTRAINT analytics_events_dedup_key_nonempty
    CHECK (dedup_key IS NULL OR length(dedup_key) > 0);
-- +goose StatementEnd

-- +goose StatementBegin
-- Account-scoped PARTIAL unique index: the structural no-double-count guarantee and
-- the arbiter for the insert query's ON CONFLICT DO NOTHING.
CREATE UNIQUE INDEX idx_analytics_events_account_dedup_key
    ON analytics_events (marketplace_account_id, dedup_key)
    WHERE dedup_key IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- Reverse in dependency order: drop the index and the CHECK, then the column.
--
-- ROLLBACK IS AN INCIDENT, NOT A ROUTINE OP (issue #111 review finding F5). On a
-- FRESH database this is a clean round-trip. On a DATA-BEARING one it is NOT
-- semantically reversible: dropping the column keeps every event row but DESTROYS
-- its dedup key, so the surviving rows become permanently UNKEYED. Re-applying Up
-- restores the column as NULL — the deduplication window for every pre-rollback
-- business fact is reopened, and a producer retry after the rollback writes a
-- SECOND row for a fact that was already recorded, with no suppression and no
-- analytics.events_deduplicated signal to reveal it. Treat a 0045 rollback on a
-- populated database as an incident with a manual re-key, never a routine
-- migration step.

-- +goose StatementBegin
DROP INDEX idx_analytics_events_account_dedup_key;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE analytics_events
    DROP CONSTRAINT analytics_events_dedup_key_nonempty;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE analytics_events
    DROP COLUMN dedup_key;
-- +goose StatementEnd
