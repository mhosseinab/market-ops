-- +goose Up
-- Freshness horizon for the margin-readiness projection (CST-003, issue #39).
--
-- margin_readiness is a stored current-state projection recomputed on cost-input
-- changes. Without a durable horizon, a Complete row can outlive the review-by
-- instant (stale_after) of a required component: no new input arrives, so nothing
-- recomputes, and stale COGS/commission silently passes the readiness gate.
--
-- stale_boundary is the EARLIEST stale_after among the required, present,
-- currently-non-stale components at recompute time — i.e. the wall-clock instant
-- at which this projection must next transition to Stale even with no new input.
-- NULL means no required component can age (no review-by instant in force), so the
-- cached row never expires by time alone. A freshness-aware read compares now()
-- against this column and recomputes at/after it, so the projection ages closed to
-- Stale on the first read past its horizon. It is derived data (recomputable from
-- cost_profiles), not evidence; margin_readiness stays an upserted projection.
-- +goose StatementBegin
ALTER TABLE margin_readiness
    ADD COLUMN stale_boundary timestamptz;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE margin_readiness
    DROP COLUMN stale_boundary;
-- +goose StatementEnd
