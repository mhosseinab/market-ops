package cost_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mhosseinab/market-ops/services/core/internal/cost"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
)

func newPool(t *testing.T) (*pgxpool.Pool, *db.Queries) {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set; skipping cost DB test")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("connect pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool, db.New(pool)
}

// seedVariant creates org+account+product+variant with the given supplier code.
func seedVariant(t *testing.T, q *db.Queries, supplierCode string) (account, variant uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	org, err := q.CreateOrganization(ctx, "cost-test-"+uuid.NewString())
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	acct, err := q.CreateMarketplaceAccount(ctx, db.CreateMarketplaceAccountParams{
		OrganizationID:  org.ID,
		NativeAccountID: "native-" + uuid.NewString(),
		DisplayName:     "Cost Seller",
	})
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	np := int64(uuid.New().ID())
	nv := int64(uuid.New().ID())
	prod, err := q.UpsertProduct(ctx, db.UpsertProductParams{
		MarketplaceAccountID: acct.ID,
		NativeProductID:      np,
		Title:                "Widget",
	})
	if err != nil {
		t.Fatalf("upsert product: %v", err)
	}
	v, err := q.UpsertVariant(ctx, db.UpsertVariantParams{
		MarketplaceAccountID: acct.ID,
		ProductID:            prod.ID,
		NativeVariantID:      nv,
		NativeProductID:      np,
		SupplierCode:         supplierCode,
		Title:                "Widget - Red",
	})
	if err != nil {
		t.Fatalf("upsert variant: %v", err)
	}
	return acct.ID, v.ID
}

func countCostProfiles(t *testing.T, pool *pgxpool.Pool, variant uuid.UUID) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM cost_profiles WHERE variant_id=$1`, variant).Scan(&n); err != nil {
		t.Fatalf("count cost_profiles: %v", err)
	}
	return n
}

// TestPreviewDoesNotCommit is the CST-001 core invariant: a preview writes NO
// cost profile — the value only lands after an explicit commit.
func TestPreviewDoesNotCommit(t *testing.T) {
	pool, q := newPool(t)
	svc := cost.NewService(pool)
	ctx := context.Background()
	account, variant := seedVariant(t, q, "SKU-PRE")

	csv := "sku,cogs\nSKU-PRE,1500\n"
	preview, err := svc.PreviewImport(ctx, cost.PreviewInput{Account: account, Filename: "costs.csv", Content: csv})
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if preview.Batch.Status != "preview" {
		t.Fatalf("batch status = %q, want preview", preview.Batch.Status)
	}
	if len(preview.Rows) != 1 || preview.Rows[0].Disposition != "accept" {
		t.Fatalf("unexpected rows: %+v", preview.Rows)
	}
	if n := countCostProfiles(t, pool, variant); n != 0 {
		t.Fatalf("preview committed %d cost profiles; want 0 (no commit before confirmation)", n)
	}

	// Commit confirms and lands exactly one version.
	res, err := svc.CommitImport(ctx, preview.Batch.ID, uuid.Nil)
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if res.CommittedRows != 1 || len(res.AffectedVariants) != 1 {
		t.Fatalf("commit result = %+v", res)
	}
	if n := countCostProfiles(t, pool, variant); n != 1 {
		t.Fatalf("after commit cost profiles = %d, want 1", n)
	}

	// Re-commit is refused (no silent double-commit).
	if _, err := svc.CommitImport(ctx, preview.Batch.ID, uuid.Nil); !errors.Is(err, cost.ErrBatchNotPreview) {
		t.Fatalf("re-commit err = %v, want ErrBatchNotPreview", err)
	}
}

// TestDuplicateRowsBlockCommit is §16: duplicate cost rows are a preview conflict
// and no commit succeeds until resolved.
func TestDuplicateRowsBlockCommit(t *testing.T) {
	pool, q := newPool(t)
	svc := cost.NewService(pool)
	ctx := context.Background()
	account, variant := seedVariant(t, q, "SKU-DUP")

	// Same (SKU, cogs) twice in the file → duplicate conflict.
	csv := "sku,cogs\nSKU-DUP,100\nSKU-DUP,200\n"
	preview, err := svc.PreviewImport(ctx, cost.PreviewInput{Account: account, Content: csv})
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if preview.Batch.DuplicateCount != 2 {
		t.Fatalf("duplicate count = %d, want 2", preview.Batch.DuplicateCount)
	}
	for _, r := range preview.Rows {
		if r.Disposition != "duplicate" || r.Reason == "" {
			t.Fatalf("row not a reasoned duplicate: %+v", r)
		}
	}
	if _, err := svc.CommitImport(ctx, preview.Batch.ID, uuid.Nil); !errors.Is(err, cost.ErrUnresolvedDuplicates) {
		t.Fatalf("commit err = %v, want ErrUnresolvedDuplicates", err)
	}
	if n := countCostProfiles(t, pool, variant); n != 0 {
		t.Fatalf("duplicate batch committed %d profiles; want 0", n)
	}
}

// TestPointInTimeVersionLookup is CST-002: the exact version in force at a
// timestamp is reproduced, never the current one.
func TestPointInTimeVersionLookup(t *testing.T) {
	pool, q := newPool(t)
	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	svc := cost.NewService(pool).WithClock(func() time.Time { return base })
	ctx := context.Background()
	account, variant := seedVariant(t, q, "SKU-PIT")

	t1 := base
	t2 := base.Add(48 * time.Hour)
	if _, err := svc.EnterSingleCost(ctx, cost.SingleCostInput{Account: account, VariantID: variant, Component: cost.ComponentCOGS, RawValue: "100", EffectiveFrom: t1}); err != nil {
		t.Fatalf("enter v1: %v", err)
	}
	if _, err := svc.EnterSingleCost(ctx, cost.SingleCostInput{Account: account, VariantID: variant, Component: cost.ComponentCOGS, RawValue: "250", EffectiveFrom: t2}); err != nil {
		t.Fatalf("enter v2: %v", err)
	}

	// Between t1 and t2 → version 1 (mantissa 100).
	mid, err := svc.CostProfileAt(ctx, variant, base.Add(24*time.Hour))
	if err != nil {
		t.Fatalf("pit mid: %v", err)
	}
	if len(mid) != 1 || mid[0].AmountMantissa != 100 || mid[0].Version != 1 {
		t.Fatalf("mid in-force = %+v, want version 1 mantissa 100", mid)
	}

	// After t2 → version 2 (mantissa 250).
	now, err := svc.CostProfileAt(ctx, variant, base.Add(72*time.Hour))
	if err != nil {
		t.Fatalf("pit now: %v", err)
	}
	if len(now) != 1 || now[0].AmountMantissa != 250 || now[0].Version != 2 {
		t.Fatalf("now in-force = %+v, want version 2 mantissa 250", now)
	}

	// Before t1 → no version in force.
	before, err := svc.CostProfileAt(ctx, variant, base.Add(-time.Hour))
	if err != nil {
		t.Fatalf("pit before: %v", err)
	}
	if len(before) != 0 {
		t.Fatalf("before first version in-force = %+v, want none", before)
	}
}

// TestReadinessAllFourStatesViaDB drives Missing → Complete → Stale → Partial
// through the real recompute path.
func TestReadinessAllFourStatesViaDB(t *testing.T) {
	pool, q := newPool(t)
	base := time.Date(2026, 4, 1, 9, 0, 0, 0, time.UTC)
	now := base
	svc := cost.NewService(pool).WithClock(func() time.Time { return now })
	ctx := context.Background()
	account, variant := seedVariant(t, q, "SKU-RDY")

	// Missing: no cost data yet.
	r, err := svc.GetReadiness(ctx, variant)
	if err != nil {
		t.Fatalf("readiness missing: %v", err)
	}
	if r.State != "missing" {
		t.Fatalf("state = %q, want missing", r.State)
	}

	// Complete: cogs + commission present, fresh.
	enter := func(comp cost.Component, raw string, staleAfter *time.Time) {
		if _, err := svc.EnterSingleCost(ctx, cost.SingleCostInput{
			Account: account, VariantID: variant, Component: comp, RawValue: raw,
			EffectiveFrom: now, StaleAfter: staleAfter,
		}); err != nil {
			t.Fatalf("enter %s: %v", comp, err)
		}
	}
	enter(cost.ComponentCOGS, "100", nil)
	enter(cost.ComponentCommission, "10", nil)
	r, _ = svc.GetReadiness(ctx, variant)
	if r.State != "complete" {
		t.Fatalf("state = %q, want complete", r.State)
	}

	// Stale: enter a new cogs version whose review-by instant is already past.
	past := now.Add(-time.Hour)
	enter(cost.ComponentCOGS, "120", &past)
	r, _ = svc.GetReadiness(ctx, variant)
	if r.State != "stale" {
		t.Fatalf("state = %q, want stale", r.State)
	}

	// Partial: fresh hard requirements + an account-required optional missing.
	// Re-enter a fresh cogs version (no stale_after) so staleness clears, then
	// require packaging by policy while it is absent.
	enter(cost.ComponentCOGS, "130", nil)
	if _, err := svc.SetAccountPolicy(ctx, account, "IRR", 0, []cost.Component{cost.ComponentPackaging}); err != nil {
		t.Fatalf("set policy: %v", err)
	}
	if _, err := svc.RecomputeReadiness(ctx, account, variant); err != nil {
		t.Fatalf("recompute: %v", err)
	}
	r, _ = svc.GetReadiness(ctx, variant)
	if r.State != "partial" {
		t.Fatalf("state = %q, want partial", r.State)
	}

	// Back to Complete once the required optional is provided.
	enter(cost.ComponentPackaging, "5", nil)
	r, _ = svc.GetReadiness(ctx, variant)
	if r.State != "complete" {
		t.Fatalf("state after packaging = %q, want complete", r.State)
	}
}
