// The execution-side half of BULK-PROTOCOL DESIGN RECORD (a): releasing the durable
// (account, variant) EXECUTION reservation on a TERMINAL external result (issue #87,
// prior finding 2). The reservation's own semantics live in internal/reservation; this
// file is only the seam that binds them to the EXE-003 result set.
package execution

import (
	"context"
	"errors"
	"time"

	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/reservation"
)

// releaseReasonFor maps a DEFINITE EXE-003 external state onto its release reason.
//
// pending_reconciliation is deliberately absent: it is the fail-closed state for an
// UNKNOWN outcome, and it maps to reservation.ReasonPendingReconciliation, which
// reservation.Release REFUSES. So even if a future caller reached here with an unknown
// state, the release would be refused explicitly rather than silently granted — the
// invariant does not depend on this function's callers being careful.
func releaseReasonFor(state ExternalState) string {
	switch state {
	case StateAccepted:
		return reservation.ReasonAccepted
	case StateRejected:
		return reservation.ReasonRejected
	case StateFailed:
		return reservation.ReasonFailed
	default:
		return reservation.ReasonPendingReconciliation
	}
}

// releaseVariantReservation frees the card's (account, variant) reservation on the
// CALLER'S transaction, so the release commits atomically with the result that
// justifies it.
//
// ErrNotReleasable is TOLERATED and is not an error here: it means this card is not
// the current holder (an expiry takeover already displaced it), or the reservation was
// already released (a replayed terminal result). Both are correct, idempotent states —
// and, critically, tolerating them cannot weaken the guard, because Release is
// FROM-guarded on card_id in SQL: a displaced holder physically cannot free a
// reservation another card now holds.
//
// A reservation row that never existed is likewise tolerated: cards approved before
// this seam existed, and cards driven to Approved by paths that predate it, have none.
// Any OTHER error fails the caller — a swallowed store error would leave a variant
// stranded with no signal.
func (s *Service) releaseVariantReservation(ctx context.Context, q *db.Queries, card db.ApprovalCard, reason string) error {
	row, err := q.GetVariantForCard(ctx, card.ID)
	if err != nil {
		return err
	}
	err = reservation.Release(ctx, q, reservation.ReleaseRequest{
		Account: row.MarketplaceAccountID,
		Variant: row.VariantID,
		CardID:  card.ID,
		Reason:  reason,
		Now:     s.nowOrWall(),
	})
	if errors.Is(err, reservation.ErrNotReleasable) {
		return nil
	}
	return err
}

// nowOrWall is the service clock, falling back to wall time when none is injected.
func (s *Service) nowOrWall() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now().UTC()
}
