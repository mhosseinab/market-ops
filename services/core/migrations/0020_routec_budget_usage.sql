-- +goose Up
-- +goose StatementBegin
-- Route C durable per-account daily budget usage (PRD §17.3 cost controls,
-- OBS-006, issue #48). The in-process, mutex-guarded budget could not satisfy the
-- route's HARD marketplace safety ceilings: it did not survive a restart, and two
-- scheduler executions in separate processes each held their own in-memory count
-- and collectively overshot the request/byte limit. This table makes the daily
-- total DURABLE and the reserve ATOMIC.
--
-- One row per (marketplace account, timezone-defined window bucket key). The
-- window_key is UTC-truncated to the operating window by the observer's injected
-- clock — exactly the deterministic, clock-driven boundary the in-memory budget
-- used (no wall-clock nondeterminism). RESET is by bucket-key ROLLOVER: a new
-- window is simply a new (account_id, window_key) row; the previous window's row
-- is left untouched (never rewritten, never DELETEd on the hot path), so history
-- is append-only-safe. Counters accrue within the CURRENT window's row only.
--
-- The LIMITS (RequestBudget/ByteBudget) are NOT stored here — they stay
-- config-driven and are supplied as the predicate bound on every atomic reserve,
-- so an operator can retune the envelope without a migration.
--
-- OBS-007: this table governs FETCHING budget only; it never relabels stored
-- observations. Exhausting the budget skips fetches — values age out normally.
CREATE TABLE route_budget_usage (
    account_id    uuid        NOT NULL REFERENCES marketplace_accounts (id) ON DELETE CASCADE,
    -- window_key is the UTC-truncated operating-window bucket. A new window is a
    -- new key => a fresh row with full headroom; rollover needs no reset job.
    window_key    timestamptz NOT NULL,
    -- Accrued spend within this window. requests_used is bumped by the atomic
    -- conditional reserve; bytes_used is reconciled by an atomic increment after
    -- each fetch. Non-negative by construction.
    requests_used integer     NOT NULL DEFAULT 0 CHECK (requests_used >= 0),
    bytes_used    bigint      NOT NULL DEFAULT 0 CHECK (bytes_used >= 0),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    -- The composite PK is the ON CONFLICT arbiter AND the row lock: the atomic
    -- reserve is a single INSERT ... ON CONFLICT DO UPDATE ... WHERE predicate on
    -- exactly one row, so concurrent workers racing the last unit serialize on
    -- this key and admit at most the remaining headroom.
    PRIMARY KEY (account_id, window_key)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE route_budget_usage;
-- +goose StatementEnd
