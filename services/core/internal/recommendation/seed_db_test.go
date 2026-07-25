package recommendation_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/money"
	"github.com/mhosseinab/market-ops/services/core/internal/recommendation"
)

// Shared DB fixtures for the selection-set / actions / edit-price suites.

// persistRecommendation persists a fresh recommendation for account/variant and
// returns its persisted id. Reuses baseValidInput (recommendation_test.go) so
// the recommendation is approvable, with a proposed contribution available.
func persistRecommendation(t *testing.T, svc *recommendation.Service, account, variant uuid.UUID) uuid.UUID {
	t.Helper()
	in := baseValidInput(t)
	in.AccountID = account
	in.VariantID = variant
	in.EventID = uuid.Nil
	rec := recommendation.Assemble(in)
	row, err := svc.Persist(context.Background(), uuid.New(), rec)
	if err != nil {
		t.Fatalf("persist recommendation: %v", err)
	}
	return row.ID
}

// persistRecommendationWithContribution is persistRecommendation but with a
// caller-chosen proposed contribution — used to construct a cross-currency
// aggregate-impact mismatch across two members of one bulk preview.
func persistRecommendationWithContribution(t *testing.T, svc *recommendation.Service, account, variant uuid.UUID, contribution money.Money) uuid.UUID {
	t.Helper()
	in := baseValidInput(t)
	in.AccountID = account
	in.VariantID = variant
	in.EventID = uuid.Nil
	in.Policy.Proposed.Contribution = contribution
	rec := recommendation.Assemble(in)
	row, err := svc.Persist(context.Background(), uuid.New(), rec)
	if err != nil {
		t.Fatalf("persist recommendation: %v", err)
	}
	return row.ID
}

// seedSecondVariant adds another variant to an EXISTING account (seedVariant in
// service_db_test.go always mints a fresh account too; this lets a test build
// a multi-member bulk preview within one account).
func seedSecondVariant(t *testing.T, q *db.Queries, account uuid.UUID) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	nativeProduct := int64(uuid.New().ID())
	nativeVariant := int64(uuid.New().ID())
	prod, err := q.UpsertProduct(ctx, db.UpsertProductParams{
		MarketplaceAccountID: account,
		NativeProductID:      nativeProduct,
		Title:                "Widget 2",
	})
	if err != nil {
		t.Fatalf("upsert product: %v", err)
	}
	v, err := q.UpsertVariant(ctx, db.UpsertVariantParams{
		MarketplaceAccountID: account,
		ProductID:            prod.ID,
		NativeVariantID:      nativeVariant,
		NativeProductID:      nativeProduct,
		SupplierCode:         "SKU-" + uuid.NewString()[:8],
		Title:                "Widget 2 - Blue",
	})
	if err != nil {
		t.Fatalf("upsert variant: %v", err)
	}
	return v.ID
}

// persistRecommendationUnavailableContribution persists a recommendation whose
// proposed contribution is UNAVAILABLE (the policy engine produced no proposal),
// so its ProposedContributionAvailable column is false — the #141 fixture.
func persistRecommendationUnavailableContribution(t *testing.T, svc *recommendation.Service, account, variant uuid.UUID) uuid.UUID {
	t.Helper()
	in := baseValidInput(t)
	in.AccountID = account
	in.VariantID = variant
	in.EventID = uuid.Nil
	in.Policy.Proposed = nil // no proposal ⇒ proposed contribution is Unavailable
	rec := recommendation.Assemble(in)
	row, err := svc.Persist(context.Background(), uuid.New(), rec)
	if err != nil {
		t.Fatalf("persist recommendation (unavailable contribution): %v", err)
	}
	return row.ID
}
