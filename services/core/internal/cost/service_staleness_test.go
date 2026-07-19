package cost_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mhosseinab/market-ops/services/core/internal/cost"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
)

// readStaleBoundary returns the stored stale_boundary for a variant (valid=false
// when SQL NULL), so tests can assert the sentinel-vs-NULL disambiguation directly.
func readStaleBoundary(t *testing.T, pool *pgxpool.Pool, variant uuid.UUID) pgtype.Timestamptz {
	t.Helper()
	var b pgtype.Timestamptz
	if err := pool.QueryRow(context.Background(),
		`SELECT stale_boundary FROM margin_readiness WHERE variant_id=$1`, variant).Scan(&b); err != nil {
		t.Fatalf("read stale_boundary: %v", err)
	}
	return b
}

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

// TestGetReadiness_PreFixNullBoundaryRowAges_Issue39 closes the migration-safety
// leak WITHOUT a destructive projection wipe. 0019 is plain additive, so any
// projection written by PRE-FIX code (or present when the migration ran) has
// stale_boundary NULL. New code never writes NULL (a real boundary or the sentinel),
// so a stored NULL means EXACTLY "never computed under the freshness-aware path" — an
// UNDETERMINABLE horizon. Serving it from cache would keep a Complete row fresh
// indefinitely past its required component's stale_after (§4.6 quarantine-over-
// inference: an undeterminable horizon must resolve to Stale, never be inferred
// fresh). GetReadiness therefore RECOMPUTES a NULL-boundary row instead of serving it.
//
// This seeds a pre-fix Complete row (stale_boundary NULL) for a variant whose
// required COGS is already past its stale_after, then asserts GetReadiness rebuilds
// to Stale with NO intervening cost write. RED before the NULL→recompute branch (the
// seeded Complete row is served and stays complete); GREEN after.
func TestGetReadiness_PreFixNullBoundaryRowAges_Issue39(t *testing.T) {
	pool, q := newPool(t)
	base := time.Date(2026, 6, 4, 9, 0, 0, 0, time.UTC)
	staleInPast := base.Add(-time.Hour)
	now := base
	svc := cost.NewService(pool).WithClock(func() time.Time { return now })
	ctx := context.Background()
	account, variant := seedVariant(t, q, "SKU-PREFIX-39")

	// Required cost data whose COGS review-by instant is ALREADY past at `now`, plus a
	// fresh authoritative commission, so a real recompute derives Stale (not Missing).
	if _, err := q.InsertCostProfileVersion(ctx, db.InsertCostProfileVersionParams{
		MarketplaceAccountID: account, VariantID: variant, Component: string(cost.ComponentCOGS),
		AmountMantissa: 100, AmountCurrency: "IRR", AmountExponent: 0,
		RawText: "100", RawValue: "100",
		EffectiveFrom: base.Add(-2 * time.Hour),
		StaleAfter:    pgtype.Timestamptz{Time: staleInPast, Valid: true},
		Source:        cost.SourceSingleValue,
	}); err != nil {
		t.Fatalf("seed stale cogs: %v", err)
	}
	seedConnectorCommission(t, q, account, variant, 10, base.Add(-2*time.Hour), nil)

	// Seed a stale-unaware Complete projection row with stale_boundary NULL — exactly
	// what pre-fix code / a row present at migration time looks like.
	if _, err := pool.Exec(ctx,
		`INSERT INTO margin_readiness (variant_id, marketplace_account_id, state, missing_components, stale_components, computed_at, stale_boundary)
		 VALUES ($1, $2, 'complete', '[]'::jsonb, '[]'::jsonb, $3, NULL)`,
		variant, account, base); err != nil {
		t.Fatalf("seed pre-fix complete row: %v", err)
	}

	// The read must NOT serve the NULL-boundary row from cache; it recomputes and ages
	// closed to Stale.
	r, err := svc.GetReadiness(ctx, variant)
	if err != nil {
		t.Fatalf("readiness: %v", err)
	}
	if r.State != string(cost.StateStale) {
		t.Fatalf("pre-fix NULL-boundary Complete row state = %q, want stale (undeterminable horizon fails closed)", r.State)
	}
	// Recompute persisted a real (non-NULL) boundary, so the leak does not recur.
	if b := readStaleBoundary(t, pool, variant); !b.Valid {
		t.Fatalf("stale_boundary still NULL after recompute; want a non-NULL horizon")
	}
}

// TestRecompute_NothingAges_WritesSentinel_Issue39 is the sentinel half of the NULL
// disambiguation: when every required component is present, fresh, and carries NO
// review-by instant, recompute stores the far-future sentinel (a NON-NULL value), not
// NULL. A subsequent read then serves the cached Complete row (now is never at/after
// the sentinel) rather than recomputing, so the NULL→recompute path is reserved
// strictly for never-computed rows and there is no per-read churn for un-ageable SKUs.
func TestRecompute_NothingAges_WritesSentinel_Issue39(t *testing.T) {
	pool, q := newPool(t)
	base := time.Date(2026, 6, 5, 9, 0, 0, 0, time.UTC)
	now := base
	svc := cost.NewService(pool).WithClock(func() time.Time { return now })
	ctx := context.Background()
	account, variant := seedVariant(t, q, "SKU-SENTINEL-39")

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

	// Nothing carries a review-by instant → stored horizon is the far-future sentinel,
	// non-NULL, well beyond any real clock.
	b := readStaleBoundary(t, pool, variant)
	if !b.Valid {
		t.Fatalf("stale_boundary is NULL; want the non-NULL far-future sentinel")
	}
	if b.Time.Year() < 9999 {
		t.Fatalf("stale_boundary = %v; want the far-future sentinel (year 9999)", b.Time)
	}

	// A read far in the future still serves Complete from cache (sentinel never ages).
	now = base.Add(50 * 365 * 24 * time.Hour)
	r, err := svc.GetReadiness(ctx, variant)
	if err != nil {
		t.Fatalf("readiness: %v", err)
	}
	if r.State != string(cost.StateComplete) {
		t.Fatalf("sentinel-horizon state = %q, want complete (never ages)", r.State)
	}
}
