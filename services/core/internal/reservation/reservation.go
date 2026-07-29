// Package reservation owns the durable (account, variant) EXECUTION reservation:
// BULK-PROTOCOL DESIGN RECORD (a), issue #87, prior finding 2.
//
// PRD refs: §7.5 EXE-002 (one execution record per action), EXE-003 (an UNKNOWN
// external result parks in pending_reconciliation and is NEVER inferred as
// success/failure), §4.6 (idempotency, reconciliation, quarantine over inference).
//
// WHY IT EXISTS. #90 established the SELECTION reservation — binding a
// (lineage, version) pair under the per-lineage lock fixes exactly which members and
// dispositions an operator authorized — and each member is then authorized through its
// own §8.4 confirm with its own card-id-unique execution intent. Those guards bound
// ONE CARD. They do not bound TWO CARDS on ONE VARIANT: two separate one-member
// selection lineages for sibling offers could each enqueue an action for the same owned
// variant, because one-executable-per-variant was only REQUEST-LOCAL. This package is
// the durable, cross-request bound the record assigned to #87.
//
// THE THREE PINNED RULES, none of which may be weakened:
//   - keyed on (account, variant);
//   - ACQUIRED BEFORE DISPATCH — before any execution intent is enqueued, on the SAME
//     transaction that commits the authorization, so an authorization can never exist
//     without its reservation;
//   - RELEASED ON A TERMINAL EXTERNAL RESULT.
//
// WHAT "TERMINAL" MEANS HERE, AND WHY. `pending_reconciliation` is EXE-003's
// fail-closed state for an UNKNOWN outcome. It does NOT release. Releasing on it would
// infer "no write is in flight" from "we do not know whether the write landed", and
// admit a second concurrent write to a variant whose first write may in fact have been
// accepted by the marketplace — the precise harm this reservation exists to prevent.
// Release happens only on a DEFINITE result (accepted / rejected / failed), when the
// action is recommend-only (no external write exists at all), or via the bounded,
// AUDITED expiry takeover below.
//
// APPEND-ONLY (§4.6). execution_variant_reservations is a NEW table with a genuine
// release/expiry lifecycle, so it carries mutable release state; no UPDATE is
// introduced on observations, actions, audit records, or outcome_windows. Its HISTORY
// is append-only: every acquire, release, and takeover is an immutable
// execution_reservation_events row, so the lifecycle is reconstructable without the
// mutable projection (AUD-001).
//
// MONEY (§9.1): this package touches no monetary value. Every field is an identity or
// an instant.
package reservation

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mhosseinab/market-ops/services/core/internal/db"
)

// Window is the bounded liveness window a reservation is held for before it may be
// TAKEN OVER by a new acquirer.
//
// It is a LIVENESS bound, not a business rule and not a marketplace parameter: without
// it, a writer that crashes between acquiring and reporting a result would strand its
// variant forever, and no approval on that variant could ever execute again. It is
// deliberately far longer than any plausible in-flight write plus its reconciliation
// latency, so a takeover means "the holder is genuinely gone", never "the holder is
// slow". A takeover is always audited (see Acquire) — it is an incident, not a silent
// recovery.
const Window = 30 * time.Minute

// ErrVariantReserved is returned when a DIFFERENT card holds a LIVE, unexpired
// reservation on the variant. It is the fail-closed outcome of prior finding 2: the
// second authorization does not proceed to dispatch.
var ErrVariantReserved = errors.New("reservation: variant already has an in-flight execution")

// ErrNotReleasable is returned when a release names a card that is not the current
// holder, or a reservation that is already released, or a reason that does not
// represent a DEFINITE external result. Nothing is changed.
var ErrNotReleasable = errors.New("reservation: not releasable by this card/reason")

// Release reasons. They are stable, NON-LOCALIZED diagnostic keys (§4.6 localization
// boundary: no Persian copy is ever a diagnostic identifier).
const (
	// ReasonAccepted / ReasonRejected / ReasonFailed are the DEFINITE EXE-003 results.
	ReasonAccepted = "external_result_accepted"
	ReasonRejected = "external_result_rejected"
	ReasonFailed   = "external_result_failed"
	// ReasonRecommendOnly — the action was tracked in recommend-only mode (EXE-005);
	// no external write exists, so nothing can be in flight.
	ReasonRecommendOnly = "recommend_only"
	// ReasonReconciled — reconciliation RESOLVED a previously unknown outcome into a
	// definite one. This is the only path by which a pending_reconciliation write ever
	// releases its variant.
	ReasonReconciled = "reconciled"
	// ReasonGateBlocked — the §8.4 revalidation gate blocked the action at
	// Revalidating and drove the card to the TERMINAL Invalidated state (FIX-CYCLE-1
	// FINDING F3). It is definite in the strongest possible sense: the block happens
	// BEFORE the write is attempted, so no external write exists and nothing can be in
	// flight. It is NOT an inference about an unknown outcome — the outcome is
	// "no attempt was made", which is why it may release where
	// ReasonPendingReconciliation may not.
	ReasonGateBlocked = "gate_blocked_no_write"
	// ReasonPendingReconciliation is DELIBERATELY NOT RELEASABLE. It exists as a named
	// constant so a caller that tries to release on an UNKNOWN outcome is refused
	// explicitly (ErrNotReleasable) rather than silently succeeding — the failure mode
	// is visible in code and asserted by test, not left to a caller's discipline.
	ReasonPendingReconciliation = "external_result_pending_reconciliation"
)

// releasable reports whether a reason represents a DEFINITE outcome. An unknown
// outcome never releases (quarantine over inference, §4.6).
func releasable(reason string) bool {
	switch reason {
	case ReasonAccepted, ReasonRejected, ReasonFailed, ReasonRecommendOnly, ReasonReconciled, ReasonGateBlocked:
		return true
	default:
		return false
	}
}

// Request is one acquire attempt. Account and Variant are resolved from the CARD's own
// recommendation by the caller — never from request input.
type Request struct {
	Account  uuid.UUID
	Variant  uuid.UUID
	CardID   uuid.UUID
	ActionID uuid.UUID
	Now      time.Time
}

// ReleaseRequest is one release attempt by the CURRENT holder.
type ReleaseRequest struct {
	Account uuid.UUID
	Variant uuid.UUID
	CardID  uuid.UUID
	// ActionID is the releasing card's APR-001 action id (FIX-CYCLE-1 FINDING F5).
	// Release used to hard-code uuid.Nil onto the append-only
	// execution_reservation_events row, so exactly the transition that CLOSES the
	// in-flight window was the one transition not attributable to an action — while
	// `acquired` and `expired_takeover` both carried it. CLAUDE.md requires the action
	// id to propagate so an approval control can be reconstructed from telemetry
	// alone, and migration 0049 declares action_id as reservation provenance. A Nil
	// value is rejected by the events table's CHECK, so the omission fails closed and
	// loud instead of silently writing a zeroed ledger row.
	ActionID uuid.UUID
	Reason   string
	Now      time.Time
}

// Acquire takes the (account, variant) execution reservation for a card, on the
// CALLER'S transaction — so it commits atomically with the authorization it guards and
// a rollback of that authorization releases it automatically.
//
// It succeeds in exactly three cases, and appends an append-only provenance event for
// each:
//
//  1. the variant is unreserved;
//  2. the current holder is the SAME card (idempotent: a replayed confirmation of one
//     card must not deadlock against its own live reservation — §4.6 idempotency);
//  3. the current holder is RELEASED, or its bounded Window has LAPSED. A lapsed
//     takeover appends an `expired_takeover` event NAMING the displaced holder, so it
//     is observable and attributable — a fallback that engages without an emitted,
//     audited event is always a bug (§4.6).
//
// Otherwise it returns ErrVariantReserved and changes NOTHING: the live holder is never
// displaced by a competing authorization.
func Acquire(ctx context.Context, q *db.Queries, r Request) error {
	expires := r.Now.Add(Window)

	// Row-lock the current holder FIRST. Two acquirers racing on one variant serialize
	// here, so the loser observes the winner's LIVE reservation rather than a stale
	// "unreserved" read.
	held, err := q.GetVariantReservationForUpdate(ctx, db.GetVariantReservationForUpdateParams{
		MarketplaceAccountID: r.Account,
		VariantID:            r.Variant,
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		// Unreserved. A concurrent acquirer that inserted between the locking read and
		// this insert raises a unique violation, which is reported as HELD — never as a
		// silent success.
		if _, err := q.InsertVariantReservation(ctx, db.InsertVariantReservationParams{
			MarketplaceAccountID: r.Account,
			VariantID:            r.Variant,
			CardID:               r.CardID,
			ActionID:             r.ActionID,
			AcquiredAt:           r.Now,
			ExpiresAt:            expires,
		}); err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
				return ErrVariantReserved
			}
			return err
		}
		return appendEvent(ctx, q, r, "acquired", uuid.Nil, "first_acquire")

	case err != nil:
		return err
	}

	live := !held.ReleasedAt.Valid && held.ExpiresAt.After(r.Now)
	if live && held.CardID != r.CardID {
		// PRIOR FINDING 2: a different card already has an in-flight write on this
		// owned variant. Fail closed BEFORE dispatch.
		return ErrVariantReserved
	}

	eventType, reason := "acquired", "reacquire"
	prior := uuid.Nil
	switch {
	case held.CardID == r.CardID:
		reason = "idempotent_reacquire"
	case !held.ReleasedAt.Valid: // implies lapsed, since `live` is false here.
		eventType, reason, prior = "expired_takeover", "holder_window_lapsed", held.CardID
	}

	// FROM-guarded in SQL: the release/expiry/same-card predicate is re-evaluated by
	// the DATABASE, so the decision cannot rest on a read that went stale.
	if _, err := q.TakeOverVariantReservation(ctx, db.TakeOverVariantReservationParams{
		MarketplaceAccountID: r.Account,
		VariantID:            r.Variant,
		CardID:               r.CardID,
		ActionID:             r.ActionID,
		AcquiredAt:           r.Now,
		ExpiresAt:            expires,
		Now:                  r.Now,
	}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrVariantReserved
		}
		return err
	}
	return appendEvent(ctx, q, r, eventType, prior, reason)
}

// Release frees the reservation held by a card on a DEFINITE external result, on the
// caller's transaction so it commits atomically with the result it reports.
//
// It refuses (ErrNotReleasable, changing nothing) when the reason is not a definite
// outcome — notably ReasonPendingReconciliation, EXE-003's UNKNOWN state — or when the
// caller is not the current holder, so a late release from a DISPLACED holder can never
// free a reservation another card has since acquired.
func Release(ctx context.Context, q *db.Queries, r ReleaseRequest) error {
	if !releasable(r.Reason) {
		return ErrNotReleasable
	}
	if _, err := q.ReleaseVariantReservation(ctx, db.ReleaseVariantReservationParams{
		MarketplaceAccountID: r.Account,
		VariantID:            r.Variant,
		CardID:               r.CardID,
		ReleasedAt:           pgtype.Timestamptz{Time: r.Now, Valid: true},
		ReleaseReason:        r.Reason,
	}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotReleasable
		}
		return err
	}
	if _, err := q.InsertReservationEvent(ctx, db.InsertReservationEventParams{
		MarketplaceAccountID: r.Account,
		VariantID:            r.Variant,
		CardID:               r.CardID,
		ActionID:             r.ActionID,
		EventType:            "released",
		Reason:               r.Reason,
		OccurredAt:           r.Now,
	}); err != nil {
		return err
	}
	return nil
}

// appendEvent writes one immutable lifecycle row. A failure fails the whole acquire —
// a reservation without its provenance is exactly the unobservable state §4.6 forbids.
func appendEvent(ctx context.Context, q *db.Queries, r Request, eventType string, prior uuid.UUID, reason string) error {
	var priorCard pgtype.UUID
	if prior != uuid.Nil {
		priorCard = pgtype.UUID{Bytes: prior, Valid: true}
	}
	_, err := q.InsertReservationEvent(ctx, db.InsertReservationEventParams{
		MarketplaceAccountID: r.Account,
		VariantID:            r.Variant,
		CardID:               r.CardID,
		ActionID:             r.ActionID,
		EventType:            eventType,
		PriorCardID:          priorCard,
		Reason:               reason,
		OccurredAt:           r.Now,
	})
	return err
}

// uniqueViolation is PostgreSQL's SQLSTATE for a unique/primary-key conflict.
const uniqueViolation = "23505"
