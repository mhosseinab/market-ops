// The reconciliation-side half of BULK-PROTOCOL DESIGN RECORD (a) (issue #87, prior
// finding 2): resolving an UNKNOWN external result is the only path by which a
// pending_reconciliation write frees its (account, variant) execution reservation.
package reconcile

import (
	"context"
	"errors"
	"time"

	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/reservation"
)

// releaseVariantReservation frees a reconciled action's variant reservation on the
// CALLER'S transaction, so the release commits atomically with the terminal state.
//
// The reason is always ReasonReconciled: what makes the release legitimate is not
// which terminal state was reached but the fact that the outcome is now DETERMINED.
// EXE-003's whole posture is that an unknown result is never inferred — reconciliation
// is the seam that removes the unknown, and this is its downstream consequence.
//
// ErrNotReleasable is TOLERATED: it means an expiry takeover already displaced this
// card, or the reservation was already released, or none was ever taken (an action
// approved before this seam existed). All are correct, idempotent states, and
// tolerating them cannot weaken the guard because Release is FROM-guarded on card_id
// in SQL — a displaced holder physically cannot free a reservation another card holds.
// Any OTHER error fails the reconciliation rather than stranding a variant silently.
func releaseVariantReservation(ctx context.Context, q *db.Queries, card db.ApprovalCard, now time.Time) error {
	row, err := q.GetVariantForCard(ctx, card.ID)
	if err != nil {
		return err
	}
	err = reservation.Release(ctx, q, reservation.ReleaseRequest{
		Account: row.MarketplaceAccountID,
		Variant: row.VariantID,
		CardID:  card.ID,
		Reason:  reservation.ReasonReconciled,
		Now:     now,
	})
	if errors.Is(err, reservation.ErrNotReleasable) {
		return nil
	}
	return err
}
