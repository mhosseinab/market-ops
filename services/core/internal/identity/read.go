package identity

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mhosseinab/market-ops/services/core/internal/db"
)

// QueueItem is one Needs Review row (journey 4 step 1): the pending candidate
// plus the SKU / variant title / product title / native-id evidence a reviewer
// needs. It is the read model behind GET /identity/needs-review.
type QueueItem struct {
	IdentityID      uuid.UUID
	VariantID       uuid.UUID
	NativeVariantID int64
	NativeProductID int64
	SupplierCode    string
	VariantTitle    string
	ProductTitle    string
	CandidateSource string
	Version         int32
}

// NeedsReviewQueue returns the account's pending candidates (journey 4). It never
// includes Confirmed/Rejected/Obsolete mappings — only the queue.
func (s *Service) NeedsReviewQueue(ctx context.Context, account uuid.UUID) ([]QueueItem, error) {
	rows, err := db.New(s.pool).ListNeedsReviewQueue(ctx, account)
	if err != nil {
		return nil, fmt.Errorf("identity: list needs-review queue: %w", err)
	}
	items := make([]QueueItem, 0, len(rows))
	for _, r := range rows {
		items = append(items, QueueItem{
			IdentityID:      r.IdentityID,
			VariantID:       r.VariantID,
			NativeVariantID: r.NativeVariantID,
			NativeProductID: r.NativeProductID,
			SupplierCode:    r.SupplierCode,
			VariantTitle:    r.VariantTitle,
			ProductTitle:    r.ProductTitle,
			CandidateSource: r.CandidateSource,
			Version:         r.Version,
		})
	}
	return items, nil
}

// ActiveConfirmedIdentity returns the variant's mapping ONLY when it is Confirmed
// and active — the executable-path lookup (CAT-002/OBS-001). It returns
// (_, false, nil) for a variant whose mapping is NeedsReview/Rejected/Obsolete or
// absent, so no executable recommendation can be built on an unconfirmed
// identity. This is the in-code entry point the negative tests exercise.
func (s *Service) ActiveConfirmedIdentity(ctx context.Context, variantID uuid.UUID) (db.MarketProductIdentity, bool, error) {
	m, err := db.New(s.pool).GetActiveConfirmedIdentityForVariant(ctx, variantID)
	if errors.Is(err, pgx.ErrNoRows) {
		return db.MarketProductIdentity{}, false, nil
	}
	if err != nil {
		return db.MarketProductIdentity{}, false, fmt.Errorf("identity: active confirmed lookup: %w", err)
	}
	return m, true, nil
}

// ObservationTargets returns the account's executable observation targets: its
// active Confirmed mappings, and nothing else (OBS-001 — no target exists for an
// unconfirmed identity). S13/S14 create observation targets from exactly this
// set.
func (s *Service) ObservationTargets(ctx context.Context, account uuid.UUID) ([]db.MarketProductIdentity, error) {
	targets, err := db.New(s.pool).ListConfirmedObservationTargets(ctx, account)
	if err != nil {
		return nil, fmt.Errorf("identity: list observation targets: %w", err)
	}
	return targets, nil
}

// Decisions returns the append-only decision history for a mapping in order.
func (s *Service) Decisions(ctx context.Context, identityID uuid.UUID) ([]db.MarketProductIdentityDecision, error) {
	rows, err := db.New(s.pool).ListIdentityDecisions(ctx, identityID)
	if err != nil {
		return nil, fmt.Errorf("identity: list decisions: %w", err)
	}
	return rows, nil
}
