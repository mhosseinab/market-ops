package execution

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mhosseinab/market-ops/services/core/internal/audit"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/recommendation"
)

// recommendOnlyContext builds a write-DISABLED context (default OFF) so Execute
// records an awaiting recommend-only action.
func recommendOnlyContext(card db.ApprovalCard, variant uuid.UUID) RevalidationContext {
	rc := enabledContext(card, 1)
	rc.Enablement = WriteEnablement{} // OFF
	rc.VariantID = variant
	return rc
}

// fixedSource is an injected OwnedPriceSource returning observations ONLY for a
// specific variant (so a shared DB's other awaiting actions are never matched).
type fixedSource struct {
	variant uuid.UUID
	obs     []OwnedPriceObservation
}

func (f fixedSource) OwnedPrices(_ context.Context, variant uuid.UUID, _ time.Time) ([]OwnedPriceObservation, error) {
	if variant != f.variant {
		return nil, nil
	}
	return f.obs, nil
}

// TestRecommendOnlyReconciler_MatchWithinWindow proves the WIRED EXE-005 matcher
// advances an awaiting action to ExternallyExecuted on a ≤24h owned-price match.
func TestRecommendOnlyReconciler_MatchWithinWindow(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	card, _ := seedApprovedCard(t, pool, q)
	variant := variantOfCard(t, pool, card)

	exec := NewService(pool, recommendation.NewService(pool), unknownWriter{}, fakeResolver{ctx: recommendOnlyContext(card, variant)})
	if _, err := exec.Execute(ctx, card.ID, audit.Actor{ID: "owner-1"}); err != nil {
		t.Fatalf("execute (recommend-only): %v", err)
	}
	ro, err := q.GetRecommendOnlyAction(ctx, card.ActionID)
	if err != nil {
		t.Fatalf("get recommend-only: %v", err)
	}

	// An owned-price observation equal to the approved price, within 24h — scoped
	// to THIS variant so unrelated awaiting actions in the shared DB never match.
	obs := OwnedPriceObservation{Price: mustMoney(t, ro.ApprovedPriceMantissa, ro.ApprovedPriceCurrency, int8(ro.ApprovedPriceExponent)), ObservedAt: ro.ApprovedAt.Add(time.Hour)}
	rec := NewRecommendOnlyReconciler(pool, fixedSource{variant: variant, obs: []OwnedPriceObservation{obs}}).
		WithClock(func() time.Time { return ro.ApprovedAt.Add(2 * time.Hour) })

	if _, err := rec.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	after, _ := q.GetRecommendOnlyAction(ctx, card.ActionID)
	if after.State != string(StateExternallyExecuted) {
		t.Fatalf("state = %q; want externally_executed", after.State)
	}
	if !after.MatchedObservationAt.Valid {
		t.Fatalf("externally_executed row missing matched observation instant")
	}
}

// TestRecommendOnlyReconciler_LapsesAfterWindow proves the WIRED matcher lapses an
// awaiting action once its 24h window passes with no match (the default dark
// behaviour: the no-owned-price source yields no match).
func TestRecommendOnlyReconciler_LapsesAfterWindow(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	card, _ := seedApprovedCard(t, pool, q)
	variant := variantOfCard(t, pool, card)

	exec := NewService(pool, recommendation.NewService(pool), unknownWriter{}, fakeResolver{ctx: recommendOnlyContext(card, variant)})
	if _, err := exec.Execute(ctx, card.ID, audit.Actor{ID: "owner-1"}); err != nil {
		t.Fatalf("execute (recommend-only): %v", err)
	}
	ro, _ := q.GetRecommendOnlyAction(ctx, card.ActionID)

	// Default source (no owned prices), clock past the 24h window.
	rec := NewRecommendOnlyReconciler(pool, nil).
		WithClock(func() time.Time { return ro.ApprovedAt.Add(25 * time.Hour) })
	sum, err := rec.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if sum.Lapsed < 1 {
		t.Fatalf("lapsed = %d; want at least 1 (summary %+v)", sum.Lapsed, sum)
	}
	after, _ := q.GetRecommendOnlyAction(ctx, card.ActionID)
	if after.State != string(StateLapsed) {
		t.Fatalf("state = %q; want lapsed", after.State)
	}
}
