-- Observation store queries (PRD §7.3, §10.3, §16). Two write disciplines:
--   * observations + observation_dedup are APPEND-ONLY — there is deliberately NO
--     UPDATE or DELETE query here (never-cut invariant).
--   * observation_targets and observed_offers are current-state projections;
--     observed_offers is the derived current view, swept and upserted, never the
--     evidence of record.

-- name: InsertObservationTarget :one
-- Create ONE observation target. The BEFORE INSERT trigger rejects this unless
-- identity_id is an active Confirmed mapping (OBS-001) — the negative test drives
-- exactly this path with an unconfirmed identity and asserts the raise.
INSERT INTO observation_targets (
    marketplace_account_id, identity_id, variant_id, native_variant_id,
    native_product_id, tier, cadence_seconds, freshness_deadline_seconds
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: CreateObservationTargetsFromConfirmed :many
-- OBS-001 auto-create: a target for EXACTLY the account's active Confirmed
-- mappings that do not yet have one. NeedsReview/Rejected/Obsolete are excluded by
-- construction, and the trigger is a second guard. Idempotent via ON CONFLICT.
INSERT INTO observation_targets (
    marketplace_account_id, identity_id, variant_id, native_variant_id,
    native_product_id, tier, cadence_seconds, freshness_deadline_seconds
)
SELECT mpi.marketplace_account_id, mpi.id, mpi.variant_id, mpi.native_variant_id,
       mpi.native_product_id, $2, $3, $4
FROM market_product_identities mpi
WHERE mpi.marketplace_account_id = $1
  AND mpi.state = 'confirmed'
  AND mpi.active = true
ON CONFLICT (identity_id) DO NOTHING
RETURNING *;

-- name: ListObservationTargets :many
SELECT * FROM observation_targets
WHERE marketplace_account_id = $1 AND active = true
ORDER BY native_variant_id;

-- name: DeactivateObservationTargetsForIdentity :many
-- OBS-001 carry-forward from S13: when a Confirmed identity is REOPENED
-- (NeedsReview/Rejected/Obsolete) its observation target must stop producing
-- executable observations — an identity that has left the executable set can no
-- longer own a live target. This DEACTIVATES (never deletes) the target so the
-- append-only observation history and its provenance stay intact; the scheduler
-- and executable-path queries filter on active = true, so a deactivated target is
-- silently dropped from every fetch and recommendation. Idempotent: a re-delivery
-- of the reopen event finds the target already inactive and changes nothing.
-- OBS-007: this disables only the dependent capability; it never relabels the
-- target's already-stored observations as current.
UPDATE observation_targets SET
    active     = false,
    updated_at = now()
WHERE identity_id = $1 AND active = true
RETURNING *;

-- name: CountActiveTargetsForIdentity :one
-- Test/introspection helper: how many ACTIVE targets an identity still owns.
SELECT count(*) FROM observation_targets
WHERE identity_id = $1 AND active = true;

-- name: GetObservationTarget :one
SELECT * FROM observation_targets WHERE id = $1;

-- name: ListActiveTargetsByTier :many
-- Route C scheduler enumeration (S14, OBS-005/§10.2): every ACTIVE target in a
-- cadence tier, across all accounts, in a stable order. A target deactivated by
-- identity reopen (DeactivateObservationTargetsForIdentity) is excluded here, so
-- a reopened identity stops being fetched. Ordered by account then native id so
-- per-account planning/capping is a simple contiguous group.
SELECT * FROM observation_targets
WHERE tier = $1 AND active = true
ORDER BY marketplace_account_id, native_variant_id;

-- name: ClaimDedupKey :many
-- OBS-008 atomic dedup. A returned row = first sighting (accept the observation);
-- an empty result = replay (dedup — create no duplicate current offer). The key
-- carries the route, so a different route observing the same value is a distinct
-- claim and is retained: route provenance is never collapsed away.
INSERT INTO observation_dedup (dedup_key, target_id, route, offer_identity)
VALUES ($1, $2, $3, $4)
ON CONFLICT (dedup_key) DO NOTHING
RETURNING *;

-- name: InsertObservation :one
-- APPEND-ONLY evidence write (OBS-002). Every field of the evidence envelope is
-- required; the domain rejects incomplete evidence before reaching here.
INSERT INTO observations (
    captured_at, target_id, marketplace_account_id, native_variant_id,
    native_seller_id, offer_identity, route, sub_route, parser_version,
    connector_version, source_url, source_type, evidence_ref, raw_fixture_ref,
    price_raw_text, price_raw_value, price_raw_unit,
    list_price_raw_text, list_price_raw_value, list_price_raw_unit,
    availability_status, stock_signal, quality, freshness_deadline, dedup_key,
    schema_valid, identity_valid, confidence, parsing_warnings
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14,
    $15, $16, $17, $18, $19, $20, $21, $22, $23, $24, $25, $26, $27, $28, $29
)
RETURNING id, captured_at;

-- name: ListObservationsByTarget :many
-- Append-only evidence, newest first (bounded by the caller).
SELECT * FROM observations
WHERE target_id = $1
ORDER BY captured_at DESC
LIMIT $2;

-- name: ListInWindowRouteValues :many
-- OBS-003/§16 cross-route analysis from APPEND-ONLY evidence. For one offer,
-- returns the LATEST observation per route that is STILL IN WINDOW at :now
-- (freshness_deadline > now) — i.e. the routes whose evidence is currently fresh,
-- with the value each currently attests. The domain derives Verified (a DIFFERENT
-- route agreeing within window), Conflicted (a different route disagreeing within
-- window), and recent history (a prior in-window sighting) from this — never from
-- a retained string set that has no per-route freshness.
SELECT DISTINCT ON (route)
    route, price_raw_value, price_raw_unit, availability_status, freshness_deadline, captured_at
FROM observations
WHERE target_id = $1 AND offer_identity = $2 AND freshness_deadline > $3
ORDER BY route, captured_at DESC;

-- name: GetObservedOffer :one
SELECT * FROM observed_offers
WHERE target_id = $1 AND offer_identity = $2;

-- name: UpsertObservedOffer :one
-- Derived current view (OBS-008). One row per (target, offer identity); the latest
-- accepted observation's fields, quality, freshness deadline, and the corroborating
-- route-provenance set (computed by the domain and passed verbatim). Re-opening a
-- previously closed offer clears ended_at.
INSERT INTO observed_offers (
    target_id, marketplace_account_id, offer_identity, native_variant_id,
    native_seller_id, price_raw_text, price_raw_value, price_raw_unit,
    list_price_raw_text, list_price_raw_value, list_price_raw_unit,
    availability_status, stock_signal, quality, captured_at, freshness_deadline,
    routes, last_observation_id, ended_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, NULL
)
ON CONFLICT (target_id, offer_identity) DO UPDATE SET
    native_seller_id     = EXCLUDED.native_seller_id,
    price_raw_text       = EXCLUDED.price_raw_text,
    price_raw_value      = EXCLUDED.price_raw_value,
    price_raw_unit       = EXCLUDED.price_raw_unit,
    list_price_raw_text  = EXCLUDED.list_price_raw_text,
    list_price_raw_value = EXCLUDED.list_price_raw_value,
    list_price_raw_unit  = EXCLUDED.list_price_raw_unit,
    availability_status  = EXCLUDED.availability_status,
    stock_signal         = EXCLUDED.stock_signal,
    quality              = EXCLUDED.quality,
    captured_at          = EXCLUDED.captured_at,
    freshness_deadline   = EXCLUDED.freshness_deadline,
    routes               = EXCLUDED.routes,
    last_observation_id  = EXCLUDED.last_observation_id,
    ended_at             = NULL,
    updated_at           = now()
RETURNING *;

-- name: MarkObservedOfferConflicted :one
-- §16 "Routes disagree → Conflicted; block". A newer disagreeing value must NOT
-- silently overwrite the current offer: the price/availability of record are LEFT
-- INTACT and only the quality is set to 'conflicted' (which blocks recommend and
-- execute in the §10.3 matrix). The disagreeing capture is still retained as
-- append-only evidence; last_observation_id points at it for traceability.
UPDATE observed_offers SET
    quality             = 'conflicted',
    last_observation_id = $2,
    updated_at          = now()
WHERE target_id = $1 AND offer_identity = $3
RETURNING *;

-- name: CloseObservedOffer :one
-- §16 offer disappearance: close the current offer with an END TIME. The last raw
-- price is left intact — it is NEVER converted to a zero price. Availability
-- becomes 'disappeared' and quality 'unavailable'.
UPDATE observed_offers SET
    availability_status = 'disappeared',
    quality             = 'unavailable',
    ended_at            = $2,
    last_observation_id = $3,
    updated_at          = now()
WHERE target_id = $1 AND offer_identity = $4
RETURNING *;

-- name: MarkExpiredObservedOffersStale :execrows
-- OBS-004 expiry sweep on the derived current view: any live offer past its
-- freshness deadline becomes Stale (renders age-only, never satisfies a
-- current-data gate — that decision is in the domain). Closed offers are left as
-- Unavailable. Evidence rows are untouched (append-only).
UPDATE observed_offers SET
    quality    = 'stale',
    updated_at = now()
WHERE marketplace_account_id = $1
  AND ended_at IS NULL
  AND freshness_deadline < $2
  AND quality NOT IN ('stale', 'unavailable');

-- name: ListObservedOffers :many
SELECT * FROM observed_offers
WHERE marketplace_account_id = $1
ORDER BY updated_at DESC;
