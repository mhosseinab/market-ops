-- Market Product Identity queries (S11, CAT-002, §6.5 journey 4, §16).
-- market_product_identities is a current-state table (state transitions UPDATE in
-- place); the append-only history is market_product_identity_decisions and the
-- append-only event log is recommendation_invalidation_events.
--
-- IDENTITY QUARANTINE (never-cut, CAT-002): the executable-path queries below —
-- GetActiveConfirmedIdentityForVariant and ListConfirmedObservationTargets —
-- filter to state='confirmed' AND active=true. NeedsReview/Rejected/Obsolete
-- rows can therefore never be returned to a caller that drives a recommendation,
-- and the negative tests assert exactly that at this query layer.

-- name: CreateIdentityCandidate :one
-- Rule-based EXACT-native-id candidate (P0). Created in NeedsReview and active so
-- it appears in the review queue but cannot drive an executable path until a
-- human confirms it. Fuzzy suggestion is P0.5 and not created here.
INSERT INTO market_product_identities (
    marketplace_account_id, variant_id, native_variant_id, native_product_id,
    state, active, candidate_source, version
) VALUES ($1, $2, $3, $4, 'needs_review', true, 'exact_native_id', 1)
RETURNING *;

-- name: GetIdentity :one
SELECT * FROM market_product_identities WHERE id = $1;

-- name: ConfirmIdentity :one
-- Transition NeedsReview -> Confirmed. Guarded WHERE state='needs_review' so a
-- concurrent/duplicate confirm is a no-op (returns no row). The partial unique
-- index uq_mpi_one_active_confirmed_per_variant rejects a second active Confirmed
-- for the same variant, enforcing CAT-002 at commit time.
UPDATE market_product_identities SET
    state      = 'confirmed',
    active     = true,
    version    = version + 1,
    updated_at = now()
WHERE id = $1 AND state = 'needs_review'
RETURNING *;

-- name: RejectIdentity :one
-- Transition NeedsReview -> Rejected and deactivate. A rejected mapping never
-- feeds an executable path and frees the variant for a fresh candidate later.
UPDATE market_product_identities SET
    state      = 'rejected',
    active     = false,
    version    = version + 1,
    updated_at = now()
WHERE id = $1 AND state = 'needs_review'
RETURNING *;

-- name: DeferIdentity :one
-- Defer keeps the candidate in NeedsReview (still in the queue) and bumps the
-- version + updated_at so the defer is ordered in the append-only audit. It never
-- promotes the mapping to an executable state.
UPDATE market_product_identities SET
    version    = version + 1,
    updated_at = now()
WHERE id = $1 AND state = 'needs_review'
RETURNING *;

-- name: ReopenConfirmedIdentity :one
-- Reopen a Confirmed mapping on a merge/split/redirect/variant-conflict signal
-- (§16). Guarded WHERE state='confirmed' AND active so only a live Confirmed
-- mapping is reopened and a duplicate signal is a no-op. $2 is the target state
-- ('needs_review' to re-queue, or 'obsolete' when the product record is gone);
-- either way the mapping leaves the executable set. The caller emits the
-- recommendation-invalidation event in the same transaction.
UPDATE market_product_identities SET
    state      = $2,
    active     = false,
    version    = version + 1,
    updated_at = now()
WHERE id = $1 AND state = 'confirmed' AND active = true
RETURNING *;

-- name: InsertIdentityDecision :one
-- APPEND-ONLY audit row (who/when/evidence). The ONLY write path to this table is
-- INSERT; there is deliberately no UPDATE/DELETE query.
INSERT INTO market_product_identity_decisions (
    identity_id, marketplace_account_id, variant_id,
    decision, from_state, to_state, reason, evidence, decided_by
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- name: ListIdentityDecisions :many
SELECT * FROM market_product_identity_decisions
WHERE identity_id = $1
ORDER BY decided_at, id;

-- name: InsertRecommendationInvalidation :one
-- APPEND-ONLY event emit (§16). dedup_key carries the event-dedup invariant: a
-- unique-violation on re-emit is swallowed by the producer, so a retry never
-- double-expires downstream recommendations.
INSERT INTO recommendation_invalidation_events (
    marketplace_account_id, variant_id, identity_id, reason, dedup_key
) VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: ListRecommendationInvalidations :many
SELECT * FROM recommendation_invalidation_events
WHERE marketplace_account_id = $1
ORDER BY emitted_at DESC, id;

-- name: CountRecommendationInvalidationsForIdentity :one
SELECT count(*) FROM recommendation_invalidation_events WHERE identity_id = $1;

-- name: GetActiveConfirmedIdentityForVariant :one
-- EXECUTABLE-PATH query (CAT-002/OBS-001). Returns the variant's mapping ONLY
-- when it is Confirmed AND active. A NeedsReview/Rejected/Obsolete mapping yields
-- no row, so no executable recommendation can be built on an unconfirmed identity.
SELECT * FROM market_product_identities
WHERE variant_id = $1 AND state = 'confirmed' AND active = true;

-- name: ListConfirmedObservationTargets :many
-- EXECUTABLE-PATH query (OBS-001): the observation targets an account may create
-- are EXACTLY its active Confirmed mappings. NeedsReview/Rejected/Obsolete are
-- excluded by construction — no target exists for an unconfirmed identity.
SELECT * FROM market_product_identities
WHERE marketplace_account_id = $1 AND state = 'confirmed' AND active = true
ORDER BY native_variant_id;

-- name: ListNeedsReviewQueue :many
-- The Needs Review queue (journey 4): each pending candidate joined to its
-- variant/product so the row carries SKU (supplier_code), variant + product title,
-- and the native-id evidence a reviewer needs to confirm/reject/defer.
SELECT
    mpi.id                 AS identity_id,
    mpi.marketplace_account_id,
    mpi.variant_id,
    mpi.native_variant_id,
    mpi.native_product_id,
    mpi.candidate_source,
    mpi.version,
    mpi.created_at,
    mpi.updated_at,
    v.supplier_code,
    v.title                AS variant_title,
    p.title                AS product_title
FROM market_product_identities mpi
JOIN variants v ON v.id = mpi.variant_id
JOIN products p ON p.id = v.product_id
WHERE mpi.marketplace_account_id = $1 AND mpi.state = 'needs_review'
ORDER BY mpi.created_at, mpi.id;

-- name: ListVariantsWithoutActiveIdentity :many
-- Candidate-generation source (rule-based exact-native-id). Returns variants that
-- have no pending (NeedsReview) or Confirmed mapping, so generation is idempotent
-- and never stacks duplicate candidates. A previously rejected/obsolete variant
-- becomes eligible again for a fresh candidate.
SELECT v.id, v.native_variant_id, v.native_product_id, v.title, v.supplier_code
FROM variants v
WHERE v.marketplace_account_id = $1
  AND NOT EXISTS (
      SELECT 1 FROM market_product_identities mpi
      WHERE mpi.variant_id = v.id
        AND mpi.state IN ('needs_review', 'confirmed')
  )
ORDER BY v.native_variant_id;
