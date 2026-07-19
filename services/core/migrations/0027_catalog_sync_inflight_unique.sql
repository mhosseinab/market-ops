-- +goose Up
-- Idempotency serialization point for catalog sync initiation (issue #76, PRD §9.1
-- never-cut). BEFORE this migration SyncCatalog guarded a duplicate in-flight run
-- with a TOCTOU: a plain latest-run SELECT followed by a SEPARATE INSERT, so two
-- concurrent POST /connector/catalog/sync for the same account could both read
-- "not in-flight" and both enqueue -> two in-flight runs + two external DK sync
-- passes. This partial unique index makes the DATABASE the serialization point: at
-- most one non-terminal (running/queued) run may exist per account at a time, so the
-- concurrent claim is decided atomically by the INSERT ... ON CONFLICT DO NOTHING
-- and the loser enqueues nothing.
--
-- The predicate includes 'queued' for forward-safety: 'queued' is a RESERVED sync
-- state advertised by the gateway contract/locale but not yet emitted by the enqueue
-- path (CreateCatalogSyncRun writes 'running'; the table CHECK does not yet allow
-- 'queued'). Covering it here means the index already serializes a future queued
-- state without a second migration.
-- +goose StatementBegin
CREATE UNIQUE INDEX uq_catalog_sync_runs_inflight
    ON catalog_sync_runs (marketplace_account_id)
    WHERE status IN ('running', 'queued');
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX uq_catalog_sync_runs_inflight;
-- +goose StatementEnd
