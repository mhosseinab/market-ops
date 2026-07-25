-- BULK-PROTOCOL DESIGN RECORD (a) — DURABLE (account, variant) EXECUTION RESERVATION
-- (issue #87, prior finding 2; PRD §7.5 EXE-002 "one execution record per action",
-- §4.6 idempotency + reconciliation).
--
-- What #90 established: binding a (lineage, version) pair under the per-lineage lock
-- is the SELECTION reservation, and each member is authorized through its own §8.4
-- confirm with its own card-id-unique execution intent. What it did NOT establish,
-- and this owns: a durable EXECUTION reservation spanning the window between
-- authorization and terminal external result, so two different selection sets — or a
-- bulk and an individual confirmation — cannot hold concurrent in-flight writes for
-- the SAME owned variant. Card-level guards bound one CARD; they do not bound two
-- cards on one variant.
--
-- Lifecycle: ACQUIRED BEFORE DISPATCH, RELEASED ON A TERMINAL EXTERNAL RESULT.
--
-- FAIL-CLOSED on an unknown result: `pending_reconciliation` (EXE-003's fail-closed
-- state for an UNKNOWN outcome) does NOT release. Releasing it would infer "no write
-- is in flight" from "we do not know whether the write landed", and permit a second
-- concurrent write on a variant whose first write may have been accepted at the
-- marketplace. Quarantine over inference (§4.6). Release happens only when the
-- outcome is DEFINITE (accepted / rejected / failed), when the action is
-- recommend-only (no external write exists), or via a bounded, AUDITED expiry
-- takeover.
--
-- MONEY: no monetary column here. Every value is an identity or a timestamp.

-- name: GetVariantReservationForUpdate :one
-- The current holder of (account, variant), row-locked. Absent ⇒ pgx.ErrNoRows: the
-- variant is unreserved. Taking the row lock FIRST is what makes the acquire decision
-- atomic against a concurrent acquirer: two confirmations racing on one variant
-- serialize here, and the loser observes the winner's LIVE reservation rather than a
-- stale "unreserved" read.
SELECT * FROM execution_variant_reservations
WHERE marketplace_account_id = $1 AND variant_id = $2
FOR UPDATE;

-- name: GetVariantReservation :one
-- Unlocked read of the current holder (diagnostics / assertions).
SELECT * FROM execution_variant_reservations
WHERE marketplace_account_id = $1 AND variant_id = $2;

-- name: InsertVariantReservation :one
-- First acquire for a variant that has never been reserved. A concurrent acquirer
-- that inserted first raises a unique violation on the primary key, which the caller
-- reports as HELD — never as a silent success.
INSERT INTO execution_variant_reservations (
    marketplace_account_id, variant_id, card_id, action_id, acquired_at, expires_at
) VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: TakeOverVariantReservation :one
-- Acquire a variant whose existing reservation is NO LONGER LIVE, FROM-guarded so the
-- guard is evaluated by the DATABASE and not by a read the caller performed earlier.
-- Exactly three conditions release the row to a new acquirer:
--
--   1. released_at IS NOT NULL — a terminal external result already released it;
--   2. expires_at <= @now — the bounded window lapsed (an AUDITED takeover; the
--      caller appends an `expired_takeover` event naming the displaced holder, so a
--      takeover is never a silent recovery, §4.6);
--   3. card_id = @card_id — the SAME card re-acquiring. Idempotent by construction: a
--      replayed confirmation of one card must not deadlock against its own live
--      reservation.
--
-- Any other state matches NO row, returns pgx.ErrNoRows, and the caller fails closed
-- with "held". This is a mutable projection on a NEW table with a genuine lifecycle —
-- no UPDATE is introduced on observations, actions, audit records, or outcome_windows.
UPDATE execution_variant_reservations
SET card_id     = @card_id,
    action_id   = @action_id,
    acquired_at = @acquired_at,
    expires_at  = @expires_at,
    released_at = NULL,
    release_reason = ''
WHERE marketplace_account_id = @marketplace_account_id
  AND variant_id = @variant_id
  AND (released_at IS NOT NULL OR expires_at <= @now OR card_id = @card_id)
RETURNING *;

-- name: ReleaseVariantReservation :one
-- Release the reservation HELD BY A SPECIFIC CARD on a terminal external result.
-- FROM-guarded on card_id so a late release from a displaced holder can never free a
-- reservation a DIFFERENT card has since acquired. Already-released rows match no row
-- (released_at IS NULL predicate), so a duplicate release is a no-op rather than a
-- rewrite of the release reason — the first release is the historical fact.
UPDATE execution_variant_reservations
SET released_at = @released_at,
    release_reason = @release_reason
WHERE marketplace_account_id = @marketplace_account_id
  AND variant_id = @variant_id
  AND card_id = @card_id
  AND released_at IS NULL
RETURNING *;

-- name: InsertReservationEvent :one
-- APPEND-ONLY provenance of one reservation lifecycle transition (§4.6). The mutable
-- projection above can be reconstructed entirely from these rows, so the audit trail
-- never depends on it (AUD-001). `reason` is a stable, NON-LOCALIZED diagnostic key —
-- never Persian copy, never marketplace free text.
INSERT INTO execution_reservation_events (
    marketplace_account_id, variant_id, card_id, action_id, event_type,
    prior_card_id, reason, occurred_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: ListReservationEventsForVariant :many
-- The append-only lifecycle of one variant's reservations, oldest first.
SELECT * FROM execution_reservation_events
WHERE marketplace_account_id = $1 AND variant_id = $2
ORDER BY occurred_at, created_at, id;

-- name: GetVariantForCard :one
-- The (account, variant) a card's reservation is keyed on, resolved from the card's
-- OWN recommendation — never from a request field. A card and its recommendation are
-- account-bound by migration 0025's composite FK, so this pair is authoritative.
SELECT r.variant_id, c.marketplace_account_id
FROM approval_cards c
JOIN recommendations r ON r.id = c.recommendation_id
WHERE c.id = $1;
