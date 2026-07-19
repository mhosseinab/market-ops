-- Event engine queries (PRD §7.4 EVT-001..005, §15.1, §16). Write disciplines:
--   * materiality_thresholds and event_relevance_feedback are APPEND-ONLY — there
--     is deliberately NO UPDATE or DELETE query here (versioned config / history).
--   * market_events is the §15.1 lifecycle record: RecordEvent is ONE atomic,
--     monotonic upsert that opens a new open row OR refreshes the existing one on a
--     dedup hit (EVT-003) without a race window (issue #68); Resolve/Expire advance
--     its lifecycle. There is no arbitrary UPDATE.

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

-- name: RecordEvent :one
-- Record a detected candidate in ONE atomic, monotonic, race-safe statement
-- (EVT-001/EVT-003, issue #68). This folds the old OpenEvent+UpdateOpenEvent pair
-- into a single upsert so there is NO window between "open" and "update" in which a
-- concurrent Resolve/Expire can move the row out of the open|updated predicate and
-- silently drop the occurrence (DEFECT B). Two outcomes, both driven by the ONE
-- partial unique index (uq_market_events_open_dedup, tenant-scoped per issue #67):
--
--   * No open|updated row for (account, dedup_key) ⇒ the INSERT succeeds and OPENS
--     a fresh event with state 'open'. A key freed by a prior Resolve/Expire opens
--     cleanly here (EVT-003 recurrence), because the partial index only covers
--     open|updated. The returned state 'open' is how the caller detects an OPEN.
--
--   * An open|updated row already exists ⇒ ON CONFLICT DO UPDATE refreshes it in
--     place (state 'updated', bumps evidence_update_count) — ZERO new rows, so the
--     Today feed still shows exactly one item (§16). The opening identity
--     (first_detected_at, dedup_key) is preserved.
--
-- MONOTONIC GUARD (DEFECT A): the DO UPDATE applies ONLY when the incoming evidence
-- instant is at least as new as the stored one
-- (market_events.last_evidence_at <= EXCLUDED.last_evidence_at). EXCLUDED.last_evidence_at
-- is the candidate's detection instant ($19, also written to last_evidence_at on
-- insert). A strictly-OLDER replay fails the guard, updates NO row, and RETURNING
-- yields no row — the service treats that as an idempotent no-op (the older replay is
-- correctly ignored, never an error, and can never move last_evidence_at / evidence /
-- severity backward). A same-instant re-detection (equal timestamp) still refreshes.
--
-- Exposure obeys EVT-005 on both paths via the table CHECK: an unknown exposure
-- passes exposure_known=false with a NULL mantissa; a fabricated number is rejected.
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
ON CONFLICT (marketplace_account_id, dedup_key) WHERE state IN ('open', 'updated')
DO UPDATE SET
    state                   = 'updated',
    severity                = EXCLUDED.severity,
    threshold_id            = EXCLUDED.threshold_id,
    threshold_version       = EXCLUDED.threshold_version,
    exposure_known          = EXCLUDED.exposure_known,
    exposure_mantissa       = EXCLUDED.exposure_mantissa,
    exposure_currency       = EXCLUDED.exposure_currency,
    exposure_exponent       = EXCLUDED.exposure_exponent,
    confidence_bp           = EXCLUDED.confidence_bp,
    urgency_bp              = EXCLUDED.urgency_bp,
    evidence_observation_id = EXCLUDED.evidence_observation_id,
    evidence_quality        = EXCLUDED.evidence_quality,
    evidence_ref            = EXCLUDED.evidence_ref,
    evidence_detail         = EXCLUDED.evidence_detail,
    last_evidence_at        = EXCLUDED.last_evidence_at,
    expires_at              = EXCLUDED.expires_at,
    evidence_update_count   = market_events.evidence_update_count + 1,
    updated_at              = now()
WHERE market_events.last_evidence_at <= EXCLUDED.last_evidence_at
RETURNING *;

-- name: GetOpenEventByDedupKey :one
-- TENANT-SCOPED (issue #67): the open row is looked up within the owning account,
-- so a dedup key never resolves another account's open event.
SELECT * FROM market_events
WHERE marketplace_account_id = $1 AND dedup_key = $2 AND state IN ('open', 'updated');

-- name: GetEvent :one
SELECT * FROM market_events WHERE id = $1;

-- name: GetEventForOrg :one
-- ORG-SCOPED detail read (issue #67, S8-AUTHZ-001): an event resolves ONLY when its
-- marketplace account belongs to the authenticated organization. A foreign event id
-- (owned by a DIFFERENT org) matches no row — identical to an unknown id — so the
-- caller cannot use possession of an event UUID as a cross-tenant existence oracle.
SELECT me.* FROM market_events me
JOIN marketplace_accounts a ON me.marketplace_account_id = a.id
WHERE me.id = $1 AND a.organization_id = $2;

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
-- TENANT-SCOPED (issue #67): the condition-clear resolves the open event of the
-- OWNING account only, so a clearance in one account can never resolve another
-- account's open event that happens to share a logical dedup key.
UPDATE market_events SET
    state       = 'resolved',
    resolved_at = $3,
    updated_at  = now()
WHERE marketplace_account_id = $1 AND dedup_key = $2 AND state IN ('open', 'updated');

-- name: InsertRelevanceFeedback :one
-- APPEND-ONLY relevance history (EVT-005). Each vote is a new row; a mute is a
-- feedback record, never a delete of the event.
INSERT INTO event_relevance_feedback (event_id, user_id, relevance, note)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: InsertRelevanceFeedbackForOrg :one
-- ORG-SCOPED append-only relevance write (issue #67, S8-AUTHZ-001). The row is
-- inserted ONLY when the target event's marketplace account belongs to the
-- authenticated organization: the INSERT ... SELECT yields ZERO rows for a foreign
-- (or unknown) event id, so a cross-tenant relevance vote is silently written to
-- nothing and the service surfaces a not-found — no existence oracle, still
-- append-only (EVT-005).
INSERT INTO event_relevance_feedback (event_id, user_id, relevance, note)
SELECT me.id, sqlc.narg(user_id), sqlc.arg(relevance), sqlc.arg(note)
FROM market_events me
JOIN marketplace_accounts a ON me.marketplace_account_id = a.id
WHERE me.id = sqlc.arg(event_id) AND a.organization_id = sqlc.arg(organization_id)
RETURNING *;

-- name: ListRelevanceFeedback :many
SELECT * FROM event_relevance_feedback
WHERE event_id = $1
ORDER BY created_at DESC;
