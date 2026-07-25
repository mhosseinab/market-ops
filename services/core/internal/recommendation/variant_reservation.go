// The approval-side half of BULK-PROTOCOL DESIGN RECORD (a): acquiring the durable
// (account, variant) EXECUTION reservation BEFORE dispatch (issue #87, prior finding
// 2). The reservation's own acquire/release/expiry semantics live in
// internal/reservation; this file is only the seam that binds them to the §8.4
// confirmation.
package recommendation

import (
	"context"
	"time"

	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/reservation"
)

// reserveVariantForCard acquires the (account, variant) execution reservation for a
// card that has just committed Approved, on the CALLER'S transaction.
//
// The (account, variant) pair is resolved from the CARD'S OWN recommendation
// (GetVariantForCard) — never from request input. A card and its recommendation are
// account-bound by migration 0025's composite FK, so the pair is authoritative and a
// caller cannot redirect the reservation onto another tenant's variant.
//
// A resolution failure is returned, never swallowed: dispatching without a reservation
// is precisely the state prior finding 2 describes, so "we could not determine the
// variant" must fail the confirmation rather than silently skip the guard.
func (s *Service) reserveVariantForCard(ctx context.Context, q *db.Queries, card db.ApprovalCard, now time.Time) error {
	row, err := q.GetVariantForCard(ctx, card.ID)
	if err != nil {
		return err
	}
	return reservation.Acquire(ctx, q, reservation.Request{
		Account:  row.MarketplaceAccountID,
		Variant:  row.VariantID,
		CardID:   card.ID,
		ActionID: card.ActionID,
		Now:      now,
	})
}
