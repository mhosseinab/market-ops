package cost_test

import (
	"context"
	"testing"
	"time"

	"github.com/mhosseinab/market-ops/services/core/internal/cost"
)

// TestGetReadiness_AgesIntoStale_Issue39 is the exact issue #39 Verify: a stored
// Complete projection whose earliest required component carries stale_after=T must
// transition to Stale no later than T on a freshness-aware read, WITH NO intervening
// write. Reads before T stay Complete; the first read at/after T is Stale.
//
// Boundary semantics match recompute (stale = !stale_after.After(now)): a read at
// exactly T is already Stale (at/after), never a one-tick grace.
func TestGetReadiness_AgesIntoStale_Issue39(t *testing.T) {
	pool, q := newPool(t)
	base := time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC)
	staleAt := base.Add(time.Hour)
	now := base
	svc := cost.NewService(pool).WithClock(func() time.Time { return now })
	ctx := context.Background()
	account, variant := seedVariant(t, q, "SKU-AGE-39")

	// Complete at base: fresh COGS with a future review-by instant (T) plus an
	// authoritative connector commission with no review-by instant.
	if _, err := svc.EnterSingleCost(ctx, cost.SingleCostInput{
		Account: account, VariantID: variant, Component: cost.ComponentCOGS,
		RawValue: "100", EffectiveFrom: base, StaleAfter: &staleAt,
	}); err != nil {
		t.Fatalf("enter cogs: %v", err)
	}
	seedConnectorCommission(t, q, account, variant, 10, base, nil)
	if _, err := svc.RecomputeReadiness(ctx, account, variant); err != nil {
		t.Fatalf("recompute: %v", err)
	}

	// Read before T: still Complete.
	now = staleAt.Add(-time.Minute)
	r, err := svc.GetReadiness(ctx, variant)
	if err != nil {
		t.Fatalf("readiness before T: %v", err)
	}
	if r.State != string(cost.StateComplete) {
		t.Fatalf("state before T = %q, want complete", r.State)
	}

	// First read AT T: Stale, without any intervening write.
	now = staleAt
	r, err = svc.GetReadiness(ctx, variant)
	if err != nil {
		t.Fatalf("readiness at T: %v", err)
	}
	if r.State != string(cost.StateStale) {
		t.Fatalf("state at exactly T = %q, want stale (at/after semantics, no intervening write)", r.State)
	}
}

// TestGetReadiness_FailsClosed_Issue39 is the fail-closed negative: a read well past
// the earliest freshness boundary never remains Complete/Ready — it must age to Stale
// with no write. A cached-Complete row is not served past its own horizon.
func TestGetReadiness_FailsClosed_Issue39(t *testing.T) {
	pool, q := newPool(t)
	base := time.Date(2026, 6, 2, 9, 0, 0, 0, time.UTC)
	staleAt := base.Add(time.Hour)
	now := base
	svc := cost.NewService(pool).WithClock(func() time.Time { return now })
	ctx := context.Background()
	account, variant := seedVariant(t, q, "SKU-FAILCLOSED-39")

	if _, err := svc.EnterSingleCost(ctx, cost.SingleCostInput{
		Account: account, VariantID: variant, Component: cost.ComponentCOGS,
		RawValue: "100", EffectiveFrom: base, StaleAfter: &staleAt,
	}); err != nil {
		t.Fatalf("enter cogs: %v", err)
	}
	seedConnectorCommission(t, q, account, variant, 10, base, nil)
	if _, err := svc.RecomputeReadiness(ctx, account, variant); err != nil {
		t.Fatalf("recompute: %v", err)
	}

	// Well past the boundary and no new input: the cached Complete row must not be
	// served — the read fails closed to Stale.
	now = staleAt.Add(365 * 24 * time.Hour)
	r, err := svc.GetReadiness(ctx, variant)
	if err != nil {
		t.Fatalf("readiness far future: %v", err)
	}
	if r.State != string(cost.StateStale) {
		t.Fatalf("state far past boundary = %q, want stale (fail closed, never Complete)", r.State)
	}
}

// TestGetReadiness_NoBoundaryNeverAges_Issue39 keeps the happy path green: a Complete
// projection whose required components carry NO review-by instant has no freshness
// horizon (boundary NULL), so time alone never ages it. The cached row is served
// and stays Complete no matter how far the clock advances.
func TestGetReadiness_NoBoundaryNeverAges_Issue39(t *testing.T) {
	pool, q := newPool(t)
	base := time.Date(2026, 6, 3, 9, 0, 0, 0, time.UTC)
	now := base
	svc := cost.NewService(pool).WithClock(func() time.Time { return now })
	ctx := context.Background()
	account, variant := seedVariant(t, q, "SKU-NOBOUND-39")

	if _, err := svc.EnterSingleCost(ctx, cost.SingleCostInput{
		Account: account, VariantID: variant, Component: cost.ComponentCOGS,
		RawValue: "100", EffectiveFrom: base, StaleAfter: nil,
	}); err != nil {
		t.Fatalf("enter cogs: %v", err)
	}
	seedConnectorCommission(t, q, account, variant, 10, base, nil)
	if _, err := svc.RecomputeReadiness(ctx, account, variant); err != nil {
		t.Fatalf("recompute: %v", err)
	}

	now = base.Add(10 * 365 * 24 * time.Hour)
	r, err := svc.GetReadiness(ctx, variant)
	if err != nil {
		t.Fatalf("readiness: %v", err)
	}
	if r.State != string(cost.StateComplete) {
		t.Fatalf("state with no review-by instant = %q, want complete (never ages)", r.State)
	}
}
