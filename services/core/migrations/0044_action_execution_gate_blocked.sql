-- +goose Up
-- +goose StatementBegin
-- Gate-blocked recovery marker on action_executions (issue #105 fix cycle 1,
-- EXE-001/EXE-003, never-cut §4.6).
--
-- A card a crash left in Executing with NO execution record has NOT written
-- externally (the durable claim is taken BEFORE the write). When the EXE-001 gate
-- FAILS on resume, the card must fail closed to PendingReconciliation AND become
-- visible + drainable to reconciliation. To do that we now write a gate-blocked
-- action_executions row in the SAME transaction as the Executing→PendingReconciliation
-- advance, so ReconcilePending, the OPS-002 operations queue
-- (ListPendingReconciliationByAccount), and the execution.pending_reconciliation_*
-- backlog gauges (AggregatePendingReconciliation) all enumerate it. Without this row
-- the parked card was an operations-invisible zombie that could never drain.
--
-- gate_blocked distinguishes that no-write marker from a genuine "wrote, result
-- unknown" pending record. A genuine pending write MAY reconcile to Accepted (the
-- write may have landed); a gate_blocked marker had NO external write, so it can
-- ONLY reconcile to Failed — reconciliation must never INFER a success from a write
-- that never happened (EXE-003). ReconcilePending enforces that: it refuses an
-- Accepted resolution for a gate_blocked record and leaves it visibly pending.
--
-- Additive: DEFAULT false, so every existing and every genuine (claimed) write row
-- is gate_blocked = false. No Money, no float; a boolean marker only.
ALTER TABLE action_executions
    ADD COLUMN gate_blocked boolean NOT NULL DEFAULT false;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE action_executions
    DROP COLUMN gate_blocked;
-- +goose StatementEnd
