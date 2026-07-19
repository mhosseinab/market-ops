-- Event engine queries (PRD §7.4 EVT-001..005, §15.1, §16). Write disciplines:
--   * materiality_thresholds and event_relevance_feedback are APPEND-ONLY — there
--     is deliberately NO UPDATE or DELETE query here (versioned config / history).
--   * market_events is the §15.1 lifecycle record: OpenEvent inserts a new open
--     row; UpdateOpenEvent mutates the SAME open record on a dedup hit (EVT-003);
--     Resolve/Expire advance its lifecycle. There is no arbitrary UPDATE.

-- name: InsertMaterialityThreshold :one
-- APPEND-ONLY versioned materiality config (EVT-002). A new version for a
-- (category, event_type) is a new row with its own effective_from; prior versions
-- are never mutated, so an event that stored a threshold_id reproduces its knobs.
INSERT INTO materiality_thresholds (
    marketplace_account_id, category, event_type, version,
    move_bp, seller_count_delta, challenge_margin_bp, effective_from, created_by
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- name: GetMaterialityThresholdAsOf :one
-- Point-in-time in-force threshold (EVT-002): the greatest effective_from <= asof
-- for (account, category, event_type). This is the version a detector fires
-- against and stores on the event, so the trigger is reproducible.
SELECT * FROM materiality_thresholds
WHERE marketplace_account_id = $1
  AND category = $2
  AND event_type = $3
  AND effective_from <= $4
ORDER BY effective_from DESC, version DESC
LIMIT 1;

-- name: GetMaterialityThreshold :one
SELECT * FROM materiality_thresholds WHERE id = $1;

-- name: ListMaterialityThresholds :many
SELECT * FROM materiality_thresholds
WHERE marketplace_account_id = $1
ORDER BY category, event_type, version DESC;

-- name: OpenEvent :one
-- Open a NEW market event (EVT-001). The partial unique index
-- (uq_market_events_open_dedup) guarantees at most one open|updated row per
-- dedup_key: ON CONFLICT DO NOTHING means a concurrent/duplicate open collides
-- and returns NO row, so the caller falls back to UpdateOpenEvent — a duplicate
-- NEVER creates a second events row (EVT-003, §16). Exposure obeys EVT-005: an
-- unknown exposure passes exposure_known=false with NULL mantissa (the CHECK
-- rejects a fabricated number).
INSERT INTO market_events (
    marketplace_account_id, variant_id, target_id, event_type, severity, state,
    dedup_key, threshold_id, threshold_version,
    exposure_known, exposure_mantissa, exposure_currency, exposure_exponent,
    confidence_bp, urgency_bp,
    evidence_observation_id, evidence_quality, evidence_ref, evidence_detail,
    first_detected_at, last_evidence_at, expires_at
) VALUES (
    $1, $2, $3, $4, $5, 'open',
    $6, $7, $8,
    $9, $10, $11, $12,
    $13, $14,
    $15, $16, $17, $18,
    $19, $19, $20
)
ON CONFLICT (dedup_key) WHERE state IN ('open', 'updated') DO NOTHING
RETURNING *;

-- name: UpdateOpenEvent :one
-- EVT-003 / §16: a duplicate detection UPDATES the open record in place — it
-- refreshes the evidence, factors, exposure, severity, and expiry, marks the row
-- 'updated', and bumps evidence_update_count. It produces ZERO new events rows,
-- so the Today feed still shows exactly one item. The dedup_key and the opening
-- identity are preserved. Exposure still obeys EVT-005 via the table CHECK.
UPDATE market_events SET
    state                   = 'updated',
    severity                = $2,
    threshold_id            = $3,
    threshold_version       = $4,
    exposure_known          = $5,
    exposure_mantissa       = $6,
    exposure_currency       = $7,
    exposure_exponent       = $8,
    confidence_bp           = $9,
    urgency_bp              = $10,
    evidence_observation_id = $11,
    evidence_quality        = $12,
    evidence_ref            = $13,
    evidence_detail         = $14,
    last_evidence_at        = $15,
    expires_at              = $16,
    evidence_update_count   = evidence_update_count + 1,
    updated_at              = now()
WHERE dedup_key = $1 AND state IN ('open', 'updated')
RETURNING *;

-- name: GetOpenEventByDedupKey :one
SELECT * FROM market_events
WHERE dedup_key = $1 AND state IN ('open', 'updated');

-- name: GetEvent :one
SELECT * FROM market_events WHERE id = $1;

-- name: ListOpenEvents :many
-- Today feed source (EVT-004): every open|updated event for the account. Ordering
-- here is stable but NOT the ranking — the domain computes the deterministic
-- exposure×confidence×urgency rank over these rows so all three factors are
-- exposed. Newest evidence first gives a stable base order.
SELECT * FROM market_events
WHERE marketplace_account_id = $1 AND state IN ('open', 'updated')
ORDER BY last_evidence_at DESC, id;

-- name: ResolveEvent :one
-- Lifecycle transition (§15.1): the triggering condition cleared, so the event is
-- resolved. This leaves the partial-unique predicate, freeing the dedup_key so a
-- genuinely new future occurrence can open a fresh event.
UPDATE market_events SET
    state       = 'resolved',
    resolved_at = $2,
    updated_at  = now()
WHERE id = $1 AND state IN ('open', 'updated')
RETURNING *;

-- name: ExpireStaleEvents :execrows
-- Lifecycle expiry sweep (§15.1): open|updated events past their expiry deadline
-- become 'expired'. Like resolution this frees the dedup_key. Evidence is left
-- intact; expiry is a lifecycle transition, not a delete.
UPDATE market_events SET
    state      = 'expired',
    updated_at = now()
WHERE marketplace_account_id = $1
  AND state IN ('open', 'updated')
  AND expires_at < $2;

-- name: ExpireStaleEventsAll :execrows
-- DURABLE, ACCOUNT-WIDE expiry sweep (§15.1, issue #66): every open|updated event
-- past its expiry deadline across ALL accounts becomes 'expired'. This is the query
-- the runtime producer pass drives so a stale alert cannot stay actionable-looking
-- indefinitely — a read-time filter alone would NOT free the dedup_key, so the row
-- must actually leave open|updated. Freeing the key lets a genuinely new future
-- occurrence open a fresh event (EVT-003). Idempotent: a sweep with nothing due
-- affects zero rows, and a resolved/expired row is untouched (never resurrected).
UPDATE market_events SET
    state      = 'expired',
    updated_at = now()
WHERE state IN ('open', 'updated')
  AND expires_at < $1;

-- name: ResolveOpenEventByDedupKey :execrows
-- Type-aware CONDITION-CLEAR (§15.1, issue #66): when a detector reports its
-- triggering condition no longer holds, the runtime producer resolves the single
-- open|updated event for that dedup identity. Scoping on state IN ('open','updated')
-- makes it MONOTONIC and idempotent — a replay of the same clearance (the event
-- already resolved/expired, or none ever opened) affects zero rows and can never
-- resurrect a terminal event. Resolving frees the dedup_key just like ResolveEvent.
UPDATE market_events SET
    state       = 'resolved',
    resolved_at = $2,
    updated_at  = now()
WHERE dedup_key = $1 AND state IN ('open', 'updated');

-- name: InsertRelevanceFeedback :one
-- APPEND-ONLY relevance history (EVT-005). Each vote is a new row; a mute is a
-- feedback record, never a delete of the event.
INSERT INTO event_relevance_feedback (event_id, user_id, relevance, note)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: ListRelevanceFeedback :many
SELECT * FROM event_relevance_feedback
WHERE event_id = $1
ORDER BY created_at DESC;
