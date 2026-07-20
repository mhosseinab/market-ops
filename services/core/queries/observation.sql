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

-- name: ClaimDedupKey :one
-- OBS-008 atomic dedup with evidence-hash comparison (issue #44). Returns EXACTLY
-- one row describing the outcome of the claim:
--   * inserted = true  → first sighting: the row was freshly inserted with the
--                        incoming evidence_hash (accept the observation).
--   * inserted = false → the key already existed: the row carries the STORED
--                        (original) evidence_hash, so the caller can compare it to
--                        the incoming hash. Equal ⇒ a true replay (idempotent
--                        no-op); UNEQUAL ⇒ a material conflict (fail closed, record
--                        it, never overwrite the authoritative current offer).
-- The key carries the route, so a different route observing the same value is a
-- distinct claim and is retained: route provenance is never collapsed away.
WITH ins AS (
    INSERT INTO observation_dedup (dedup_key, target_id, route, offer_identity, evidence_hash)
    VALUES ($1, $2, $3, $4, $5)
    ON CONFLICT (dedup_key) DO NOTHING
    RETURNING dedup_key, evidence_hash
)
SELECT dedup_key, evidence_hash, true AS inserted FROM ins
UNION ALL
SELECT d.dedup_key, d.evidence_hash, false AS inserted
    FROM observation_dedup d
    WHERE d.dedup_key = $1 AND NOT EXISTS (SELECT 1 FROM ins);

-- name: InsertDedupConflict :one
-- APPEND-ONLY dedup conflict record (issue #44). Written when a capture collides on
-- the dedup key but carries a materially different evidence envelope (its canonical
-- evidence hash differs from the stored one). Preserves BOTH hashes and the full
-- conflicting envelope (raw tokens, money quarantine) so the dropped evidence is
-- auditable — the second capture is never silently lost. INSERT-only.
INSERT INTO observation_dedup_conflicts (
    dedup_key, target_id, marketplace_account_id, route, offer_identity,
    stored_evidence_hash, conflicting_evidence_hash, conflicting_observation_id,
    conflicting_envelope
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- name: ListDedupConflictsByTarget :many
-- Append-only dedup conflicts for a target, newest first (review/introspection).
SELECT * FROM observation_dedup_conflicts
WHERE target_id = $1
ORDER BY detected_at DESC
LIMIT $2;

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

-- name: ListUnconsumedObservationsByTarget :many
-- Durable forward drain for the market-event producer (issue #212). Returns the
-- target's append-only observations that lie STRICTLY AFTER each stream's durable
-- consumer cursor, oldest-first per stream. A stream is (native_seller_id,
-- offer_identity); the LEFT JOIN gives the stream's cursor (NULL when never
-- consumed) and the (captured_at, id) tie-break makes "after" deterministic for
-- equal timestamps. Ordered by (native_seller_id, offer_identity, captured_at, id)
-- so the caller groups by stream and walks each in captured order; LIMIT bounds the
-- per-pass work and the cursor provides continuation across passes (no fixed
-- latest-N window). Seller identity is part of the stream key, so a reused offer
-- identity across two sellers is TWO streams and is never paired.
-- The owned seller's own stream is excluded here (sqlc.arg(owned_seller)) so it
-- never consumes the per-pass page budget — EVT-001 type 2 is a COMPETITOR movement,
-- and the caller has already validated owned_seller as an authoritative decimal id.
SELECT o.id, o.captured_at, o.native_seller_id, o.offer_identity,
       o.price_raw_value, o.price_raw_unit, o.quality, o.evidence_ref
FROM observations o
LEFT JOIN observation_consumer_cursors c
       ON c.target_id = o.target_id
      AND c.native_seller_id = o.native_seller_id
      AND c.offer_identity = o.offer_identity
WHERE o.target_id = sqlc.arg(target_id)
  AND o.native_seller_id <> sqlc.arg(owned_seller)
  AND (
        c.last_captured_at IS NULL
     OR o.captured_at > c.last_captured_at
     OR (o.captured_at = c.last_captured_at AND o.id > c.last_observation_id)
      )
ORDER BY o.native_seller_id, o.offer_identity, o.captured_at, o.id
LIMIT sqlc.arg(page_limit);

-- name: ListObservationCursorsByTarget :many
-- Every durable stream cursor for a target, so the producer can seed each stream's
-- pairing anchor ("before") from the last consumed observation's raw value.
SELECT * FROM observation_consumer_cursors
WHERE target_id = $1;

-- name: AdvanceObservationCursor :exec
-- Advance (or create) a stream's durable consumer position. MONOTONIC: an existing
-- cursor moves forward only when the incoming (captured_at, observation_id) is
-- strictly greater, so an out-of-order or replayed advance never rewinds the
-- stream. Called inside the SAME transaction as the event write + ledger insert so
-- the position, the event, and the ingestion-idempotency record commit atomically.
INSERT INTO observation_consumer_cursors (
    target_id, marketplace_account_id, native_seller_id, offer_identity,
    last_observation_id, last_captured_at, last_price_raw_value
) VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (target_id, native_seller_id, offer_identity) DO UPDATE SET
    last_observation_id  = EXCLUDED.last_observation_id,
    last_captured_at     = EXCLUDED.last_captured_at,
    last_price_raw_value = EXCLUDED.last_price_raw_value,
    updated_at           = now()
WHERE (EXCLUDED.last_captured_at, EXCLUDED.last_observation_id)
    > (observation_consumer_cursors.last_captured_at, observation_consumer_cursors.last_observation_id);

-- name: GetObservationForAccount :one
-- ACCOUNT-SCOPED single-observation load for the S15 event-evidence derivation
-- (#70, evidence-quality never-cut §4.6). The event boundary must DERIVE its quality
-- and provenance from a real, account-bound observation rather than trust a caller-
-- supplied token, so RecordFor loads the cited observation inside the SAME account-
-- scoped transaction as the event write and copies the quality/ref AS-IS. The predicate
-- is (id, marketplace_account_id): a random or foreign-account id resolves to NO row
-- (fail closed, no cross-tenant existence oracle). observations is PARTITIONED with PK
-- (id, captured_at) so id is not independently unique across partitions; LIMIT 1
-- returns the single logical row (id is a gen_random_uuid, unique in practice).
SELECT id, marketplace_account_id, target_id, quality, freshness_deadline,
       captured_at, evidence_ref
FROM observations
WHERE id = $1 AND marketplace_account_id = $2
LIMIT 1;

-- name: ListInWindowRouteValues :many
-- OBS-003/§16 cross-route analysis from APPEND-ONLY evidence. For one offer,
-- returns the LATEST observation per route that is STILL IN WINDOW at :now
-- (freshness_deadline > now) — i.e. the routes whose evidence is currently fresh,
-- with the value each currently attests. The domain derives Verified (a DIFFERENT
-- route agreeing within window), Conflicted (a different route disagreeing within
-- window), and recent history (a prior in-window sighting) from this — never from
-- a retained string set that has no per-route freshness.
-- Only schema_valid rows qualify (#154): a capture from an UNKNOWN/retired/malformed
-- parser is persisted append-only with schema_valid=false and MUST NOT contribute
-- qualifying history or corroboration to any other capture — "unknown never enables".
SELECT DISTINCT ON (route)
    route, price_raw_value, price_raw_unit, availability_status, freshness_deadline, captured_at
FROM observations
WHERE target_id = $1 AND offer_identity = $2 AND freshness_deadline > $3
  AND schema_valid = true
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

-- name: DowngradeObservedOffersForDrift :execrows
-- §10.4 parser-drift stop rule on the derived current view: when Route C detects
-- drift for a target (parse failure, failed canary, product-identity mismatch, or
-- an already-paused guard), the target's LIVE current offers must stop reading as
-- current before any consumer sees them. Each live offer is downgraded so it can no
-- longer satisfy the current-data gate: to 'unavailable' when it carries no usable
-- value (a disappeared offer), else to 'stale' (renders age-only). Mirrors
-- PausedQuality (Stale if had value, else Unavailable). This touches ONLY the
-- derived projection — the append-only observations evidence table is never
-- modified. Idempotent and one-directional: offers already stale/unavailable/
-- conflicted are excluded, so a re-run is a no-op and a more-restrictive state is
-- never loosened.
UPDATE observed_offers SET
    quality    = CASE WHEN availability_status = 'disappeared'
                      THEN 'unavailable' ELSE 'stale' END,
    updated_at = now()
WHERE target_id = $1
  AND ended_at IS NULL
  AND quality NOT IN ('stale', 'unavailable', 'conflicted');

-- name: ListObservedOffers :many
SELECT * FROM observed_offers
WHERE marketplace_account_id = $1
ORDER BY updated_at DESC;

-- name: ListConflictedObservedOffers :many
-- Cross-route conflicted Observed Offers (PD-3 item 8, S37 Market conflict
-- banner — §16 "routes disagree → Conflicted; block"). The price of record is
-- untouched; only the quality state signals the conflict.
SELECT * FROM observed_offers
WHERE marketplace_account_id = $1 AND quality = 'conflicted'
ORDER BY updated_at DESC;
