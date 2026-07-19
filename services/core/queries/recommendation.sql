-- Recommendation queries (PRD §7.5 PRC-001/002). recommendations is APPEND-ONLY
-- within a lineage: a new version is a new row whose version is the greatest in
-- the lineage plus one (computed in SQL, never floated). There is NO UPDATE/DELETE
-- here — the "current" recommendation is the greatest version per lineage.

-- name: InsertRecommendation :one
INSERT INTO recommendations (
    marketplace_account_id, variant_id, lineage_id, version, event_id, objective,
    current_price_mantissa, current_price_currency, current_price_exponent,
    proposed_price_available, proposed_price_mantissa, proposed_price_currency, proposed_price_exponent, proposed_price_reason,
    current_contribution_available, current_contribution_mantissa, current_contribution_currency, current_contribution_exponent, current_contribution_reason,
    proposed_contribution_available, proposed_contribution_mantissa, proposed_contribution_currency, proposed_contribution_exponent, proposed_contribution_reason,
    allowed_range_available, allowed_range_min_mantissa, allowed_range_max_mantissa, allowed_range_currency, allowed_range_exponent, allowed_range_reason,
    readiness, evidence_quality, evidence_observation_id, evidence_refs, evidence_as_of,
    cost_profile_version, policy_version, context_version, parameter_version,
    inputs, assumptions, blockers, approvable, simulation, expires_at
) VALUES (
    $1, $2, $3,
    (SELECT COALESCE(MAX(version), 0) + 1 FROM recommendations WHERE lineage_id = $3),
    $4, $5,
    $6, $7, $8,
    $9, $10, $11, $12, $13,
    $14, $15, $16, $17, $18,
    $19, $20, $21, $22, $23,
    $24, $25, $26, $27, $28, $29,
    $30, $31, $32, $33, $34,
    $35, $36, $37, $38,
    $39, $40, $41, $42, $43, $44
)
RETURNING *;

-- name: GetRecommendation :one
SELECT * FROM recommendations WHERE id = $1;

-- name: GetRecommendationForAccount :one
-- Tenant-scoped recommendation fetch (issue #102): resolves a recommendation ONLY
-- when it belongs to the caller's marketplace account. A recommendation owned by
-- another account matches no row, so a foreign recommendation is indistinguishable
-- from a missing one (no existence oracle) and is never disclosed.
SELECT * FROM recommendations WHERE id = $1 AND marketplace_account_id = $2;

-- name: GetCurrentRecommendation :one
-- The greatest-version row for a lineage (the current recommendation).
SELECT * FROM recommendations
WHERE lineage_id = $1
ORDER BY version DESC
LIMIT 1;

-- name: ListRecommendationsForVariant :many
SELECT * FROM recommendations
WHERE marketplace_account_id = $1 AND variant_id = $2
ORDER BY created_at DESC;
