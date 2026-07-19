-- Execution + recommend-only + write-verification queries (PRD §7.5 EXE-002..005).
-- Discipline:
--   * action_executions is the EXE-002 SINGLE record per action, keyed by the
--     stable idempotency_key (UNIQUE). Claiming is an INSERT ... ON CONFLICT DO
--     NOTHING: a duplicate request finds the existing row and performs NO second
--     external write. The external_state projection is advanced by a FROM-guarded
--     UPDATE (pending_reconciliation → terminal), never a blind overwrite.
--   * account_write_verification is the S35 write-verification flag; ABSENT/false
--     means writes are OFF (the read returns false when there is no row).

-- name: ClaimActionExecution :one
-- Claim the idempotency key. ON CONFLICT DO NOTHING makes this idempotent: a
-- second request for the same key returns NO row (the service then reads the
-- existing record and writes nothing external).
INSERT INTO action_executions (
    card_id, action_id, idempotency_key, mode, external_state, request_payload
) VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (idempotency_key) DO NOTHING
RETURNING *;

-- name: GetActionExecutionByKey :one
SELECT * FROM action_executions WHERE idempotency_key = $1;

-- name: GetActionExecutionByAction :one
SELECT * FROM action_executions WHERE action_id = $1;

-- name: GetActionExecution :one
SELECT * FROM action_executions WHERE id = $1;

-- name: RecordExecutionResult :one
-- Record the classified write result. FROM-guarded on the pending state so a
-- definitive result is written exactly once; a row already resolved to a terminal
-- state is not overwritten (idempotent result recording).
UPDATE action_executions
SET external_state = $2,
    external_ref = $3,
    response_payload = $4,
    reconciled_at = CASE WHEN $2 <> 'pending_reconciliation' THEN now() ELSE reconciled_at END,
    updated_at = now()
WHERE id = $1 AND external_state = 'pending_reconciliation'
RETURNING *;

-- name: ReconcileActionExecution :one
-- Resolve a pending_reconciliation record to a terminal state (post-write
-- read-back or periodic reconciliation, §16). FROM-guarded on pending so only an
-- unresolved record is reconciled; the retry endpoint refuses any record still
-- pending (EXE-003). The observed external ref (batch handle) is recorded verbatim.
UPDATE action_executions
SET external_state = $2,
    external_ref = $3,
    reconciled_at = now(),
    updated_at = now()
WHERE id = $1 AND external_state = 'pending_reconciliation'
RETURNING *;

-- name: InsertRecommendOnlyAction :one
INSERT INTO recommend_only_actions (
    card_id, action_id, marketplace_account_id, variant_id,
    approved_price_mantissa, approved_price_currency, approved_price_exponent,
    approved_at, window_expires_at, state
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, 'awaiting_external_execution')
ON CONFLICT (action_id) DO NOTHING
RETURNING *;

-- name: GetRecommendOnlyAction :one
SELECT * FROM recommend_only_actions WHERE action_id = $1;

-- name: SetRecommendOnlyState :one
-- Advance a recommend-only action to a terminal EXE-005 state. FROM-guarded on
-- the awaiting state so a resolved action is not re-resolved.
UPDATE recommend_only_actions
SET state = $2,
    matched_observation_at = $3,
    updated_at = now()
WHERE action_id = $1 AND state = 'awaiting_external_execution'
RETURNING *;

-- name: ListAwaitingRecommendOnlyForVariant :many
-- Awaiting recommend-only actions for a variant (the recommend-only matcher runs
-- these against fresh owned-price observations).
SELECT * FROM recommend_only_actions
WHERE variant_id = $1 AND state = 'awaiting_external_execution'
ORDER BY approved_at;

-- name: ListAwaitingRecommendOnlyActions :many
-- Every awaiting recommend-only action (the periodic matcher job iterates these,
-- matching each against fresh owned-price observations or lapsing it once its 24h
-- window has passed). Bounded batch to bound the job's work per pass.
SELECT * FROM recommend_only_actions
WHERE state = 'awaiting_external_execution'
ORDER BY approved_at
LIMIT $1;

-- name: ListActionExecutionsByAccount :many
-- Every write-mode action_executions row for an account (issue #106 unified action
-- projection), newest first. Scoped via the bound approval_cards row
-- (action_executions carries no account column of its own). A pure SELECT — the
-- common action API overlays these onto the account's approval cards.
SELECT ae.*
FROM action_executions ae
JOIN approval_cards ac ON ac.id = ae.card_id
WHERE ac.marketplace_account_id = $1
ORDER BY ae.created_at DESC
LIMIT $2;

-- name: ListRecommendOnlyActionsByAccount :many
-- Every recommend-only action for an account (issue #106 unified action
-- projection), newest first. recommend_only_actions carries its own account
-- column, so no join is needed. A pure SELECT.
SELECT * FROM recommend_only_actions
WHERE marketplace_account_id = $1
ORDER BY approved_at DESC
LIMIT $2;

-- name: GetCurrentExecutionContext :one
-- Server-side re-resolution for the Revalidating gate (EXE-001): the account,
-- variant, and native variant id for a card's recommendation, PLUS the CURRENT
-- (greatest-version) cost/policy/context/parameter versions in the recommendation
-- lineage. Comparing these to the card's BOUND versions catches an out-of-band
-- version change that never passed through a state-machine invalidation.
SELECT
    base.marketplace_account_id AS account_id,
    base.variant_id             AS variant_id,
    v.native_variant_id         AS native_variant_id,
    cur.cost_profile_version    AS current_cost_profile_version,
    cur.policy_version          AS current_policy_version,
    cur.context_version         AS current_context_version,
    cur.parameter_version       AS current_parameter_version
FROM recommendations base
JOIN variants v ON v.id = base.variant_id
JOIN LATERAL (
    SELECT cost_profile_version, policy_version, context_version, parameter_version
    FROM recommendations
    WHERE lineage_id = base.lineage_id
    ORDER BY version DESC
    LIMIT 1
) cur ON true
WHERE base.id = $1;

-- name: ListPendingReconciliationByAccount :many
-- The account's action_executions still in pending_reconciliation (PD-3 item 8,
-- S37 Operations queue) — an unknown external write result that must resolve
-- before any retry (EXE-003, never inferred). Scoped via the bound
-- approval_cards row (action_executions carries no account column of its own).
SELECT ae.*
FROM action_executions ae
JOIN approval_cards ac ON ac.id = ae.card_id
WHERE ac.marketplace_account_id = $1 AND ae.external_state = 'pending_reconciliation'
ORDER BY ae.created_at
LIMIT $2;

-- name: AggregatePendingReconciliation :many
-- Per-account aggregate of the DURABLE pending_reconciliation set (EXE-003, §20.1):
-- the current count of parked-unknown write results and the OLDEST park instant.
-- This is the authoritative backlog measurement — the same durable rows
-- ListPendingReconciliationByAccount renders in the Operations queue — read LIVE by
-- the execution.pending_reconciliation_current / _oldest_age_seconds observable
-- gauges. Because it reads current state, a resolved item simply leaves the set
-- (count drops, never negative), an unrelated terminal result cannot cancel a still-
-- pending item, and a process restart re-reads the same rows (no in-memory counter
-- to zero). Grouped by the bound approval_cards account (action_executions carries
-- no account column of its own), a bounded label. Pure SELECT — no UPDATE/DELETE, no
-- Money arithmetic; the age is derived in Go as plain time subtraction on
-- oldest_created_at.
SELECT
    ac.marketplace_account_id AS account_id,
    count(*)                  AS pending_count,
    min(ae.created_at)::timestamptz AS oldest_created_at
FROM action_executions ae
JOIN approval_cards ac ON ac.id = ae.card_id
WHERE ae.external_state = 'pending_reconciliation'
GROUP BY ac.marketplace_account_id;

-- name: GetWriteVerification :one
SELECT * FROM account_write_verification WHERE marketplace_account_id = $1;

-- name: IsWriteVerified :one
-- The S35 write-verification flag for an account. Returns false when there is no
-- row (writes OFF by default) — the two-key write gate's second key.
SELECT EXISTS (
    SELECT 1 FROM account_write_verification
    WHERE marketplace_account_id = $1 AND verified
) AS verified;

-- name: UpsertWriteVerification :one
-- Set the S35 write-verification flag (called only by the gated S35 probe path,
-- never at runtime). Present for completeness of the seam.
INSERT INTO account_write_verification (
    marketplace_account_id, region_code, verified, parameter_contract_version, verified_at, note
) VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (marketplace_account_id) DO UPDATE
SET region_code = EXCLUDED.region_code,
    verified = EXCLUDED.verified,
    parameter_contract_version = EXCLUDED.parameter_contract_version,
    verified_at = EXCLUDED.verified_at,
    note = EXCLUDED.note,
    updated_at = now()
RETURNING *;
