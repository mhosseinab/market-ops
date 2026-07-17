-- Approval card queries (PRD §7.5 APR-001, §8.4 state machine). Write discipline:
--   * approval_cards is APPEND-ONLY within a lineage (a price edit is a new
--     version). The `state` column is a CURRENT-state projection advanced by a
--     FROM-guarded UPDATE (a checked §8.4 transition, never a blind overwrite).
--   * approval_card_states is STRICTLY APPEND-ONLY: INSERT only, no UPDATE/DELETE.
--     It is the authoritative lifecycle history reconstructable for audit (AUD-001).

-- name: InsertApprovalCard :one
INSERT INTO approval_cards (
    recommendation_id, marketplace_account_id, lineage_id, version,
    action_id, parameter_version, context_version, policy_version, cost_profile_version,
    evidence_versions, idempotency_key, state,
    price_mantissa, price_currency, price_exponent, expires_at
) VALUES (
    $1, $2, $3,
    (SELECT COALESCE(MAX(version), 0) + 1 FROM approval_cards WHERE lineage_id = $3),
    $4, $5, $6, $7, $8,
    $9, $10, $11,
    $12, $13, $14, $15
)
RETURNING *;

-- name: GetApprovalCard :one
SELECT * FROM approval_cards WHERE id = $1;

-- name: GetCurrentApprovalCard :one
-- The greatest-version card for a lineage (the live card version).
SELECT * FROM approval_cards
WHERE lineage_id = $1
ORDER BY version DESC
LIMIT 1;

-- name: AdvanceApprovalCardState :one
-- FROM-guarded §8.4 transition on the current-state projection. The WHERE clause
-- is the optimistic guard: only a card still in from_state advances; a card that
-- already moved matches nothing and returns no row (the service treats that as a
-- rejected transition — no blind overwrite). The append-only history row is
-- inserted separately in the same transaction (AppendApprovalCardState).
UPDATE approval_cards
SET state = $3
WHERE id = $1 AND state = $2
RETURNING *;

-- name: AppendApprovalCardState :one
-- APPEND-ONLY §8.4 history. One row per state change; INSERT only.
INSERT INTO approval_card_states (
    card_id, card_version, from_state, to_state, reason
) VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: ListApprovalCardStates :many
-- The append-only lifecycle history for a card, in occurrence order (AUD-001).
SELECT * FROM approval_card_states
WHERE card_id = $1
ORDER BY occurred_at, id;

-- name: ListLiveCardsForVariant :many
-- Live (control-bearing or revalidating) cards for a variant. Used by the
-- identity-reopen consumer to expire dependent recommendations (§16): a reopened
-- mapping invalidates any card whose control could still authorize a write.
SELECT ac.* FROM approval_cards ac
JOIN recommendations r ON r.id = ac.recommendation_id
WHERE r.variant_id = $1
  AND ac.state IN ('draft', 'ready_for_review', 'awaiting_confirmation', 'approved', 'revalidating')
ORDER BY ac.created_at;
