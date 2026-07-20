-- +goose Up
-- Optimistic-concurrency version for L3 guardrail writes (issue #101). Two Owners
-- editing the same guardrails must not silently overwrite one another (a lost
-- update): every write carries the version it read, and the version-guarded
-- upsert (queries/guardrail.sql) commits only if that version is still current,
-- bumping it by one. A stale write matches no row and is a SAFE conflict, never a
-- lost update (§4.6 approval/versioning family). Additive column; existing rows
-- default to 0 and take their first versioned write at expectedVersion=0.
-- +goose StatementBegin
ALTER TABLE guardrail_settings
    ADD COLUMN version bigint NOT NULL DEFAULT 0;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE guardrail_settings
    DROP COLUMN version;
-- +goose StatementEnd
