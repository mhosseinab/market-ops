package cost_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mhosseinab/market-ops/services/core/internal/cost"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
)

// seedConnectorCommission inserts an AUTHORITATIVE (connector-sourced) commission
// cost-profile version directly through the db layer. There is no service method
// that writes source 'connector' (connector ingestion is a later step); tests use
// this to represent official-connector commission provenance (§9.2).
func seedConnectorCommission(t *testing.T, q *db.Queries, account, variant uuid.UUID, mantissa int64, effectiveFrom time.Time, staleAfter *time.Time) {
	t.Helper()
	sa := pgtype.Timestamptz{}
	if staleAfter != nil {
		sa = pgtype.Timestamptz{Time: *staleAfter, Valid: true}
	}
	if _, err := q.InsertCostProfileVersion(context.Background(), db.InsertCostProfileVersionParams{
		MarketplaceAccountID: account,
		VariantID:            variant,
		Component:            string(cost.ComponentCommission),
		AmountMantissa:       mantissa,
		AmountCurrency:       "IRR",
		AmountExponent:       0,
		RawText:              "connector",
		RawValue:             "connector",
		RawUnit:              "",
		EffectiveFrom:        effectiveFrom,
		StaleAfter:           sa,
		Source:               cost.SourceConnector,
	}); err != nil {
		t.Fatalf("seed connector commission: %v", err)
	}
}

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
	return countRows(t, pool, `SELECT count(*) FROM cost_profiles WHERE variant_id=$1`, variant)
}

func countSkuRequirements(t *testing.T, pool *pgxpool.Pool, variant uuid.UUID) int {
	t.Helper()
	return countRows(t, pool, `SELECT count(*) FROM sku_cost_requirements WHERE variant_id=$1`, variant)
}

func countMarginReadiness(t *testing.T, pool *pgxpool.Pool, variant uuid.UUID) int {
	t.Helper()
	return countRows(t, pool, `SELECT count(*) FROM margin_readiness WHERE variant_id=$1`, variant)
}

func countRows(t *testing.T, pool *pgxpool.Pool, query string, arg uuid.UUID) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), query, arg).Scan(&n); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	return n
}

// TestEnterSingleCostRejectsCrossAccountVariant is the tenant-isolation invariant
// (§4.6, issue #37): a single-value cost write submitted under account A for a
// variant owned by account B must fail closed with a distinct mismatch error and
// write NOTHING — no cost_profiles row, no margin_readiness row for that variant.
func TestEnterSingleCostRejectsCrossAccountVariant(t *testing.T) {
	pool, q := newPool(t)
	svc := cost.NewService(pool)
	ctx := context.Background()

	accountA, _ := seedVariant(t, q, "SKU-A")
	_, variantB := seedVariant(t, q, "SKU-B")

	_, err := svc.EnterSingleCost(ctx, cost.SingleCostInput{
		Account:   accountA,
		VariantID: variantB,
		Component: cost.ComponentCOGS,
		RawValue:  "100",
	})
	if !errors.Is(err, cost.ErrAccountVariantMismatch) {
		t.Fatalf("EnterSingleCost cross-account err = %v, want ErrAccountVariantMismatch", err)
	}
	if n := countCostProfiles(t, pool, variantB); n != 0 {
		t.Fatalf("cross-account write created %d cost profiles for variant B; want 0", n)
	}
	if n := countMarginReadiness(t, pool, variantB); n != 0 {
		t.Fatalf("cross-account write created %d readiness rows for variant B; want 0", n)
	}
}

// TestSetSkuApplicableRejectsCrossAccountVariant asserts SKU-applicability writes
// also resolve the variant's owning account and reject a mismatched supplied
// account, persisting no sku_cost_requirements row (issue #37).
func TestSetSkuApplicableRejectsCrossAccountVariant(t *testing.T) {
	pool, q := newPool(t)
	svc := cost.NewService(pool)
	ctx := context.Background()

	accountA, _ := seedVariant(t, q, "SKU-APP-A")
	_, variantB := seedVariant(t, q, "SKU-APP-B")

	_, err := svc.SetSkuApplicable(ctx, accountA, variantB, []cost.Component{cost.ComponentPackaging})
	if !errors.Is(err, cost.ErrAccountVariantMismatch) {
		t.Fatalf("SetSkuApplicable cross-account err = %v, want ErrAccountVariantMismatch", err)
	}
	if n := countSkuRequirements(t, pool, variantB); n != 0 {
		t.Fatalf("cross-account applicability wrote %d sku_cost_requirements for variant B; want 0", n)
	}
}

// TestCostWritesAcceptMatchingAccountVariant proves the ownership guard does not
// break the happy path: a matching account+variant pair still succeeds.
func TestCostWritesAcceptMatchingAccountVariant(t *testing.T) {
	pool, q := newPool(t)
	svc := cost.NewService(pool)
	ctx := context.Background()

	account, variant := seedVariant(t, q, "SKU-OK")

	if _, err := svc.EnterSingleCost(ctx, cost.SingleCostInput{
		Account: account, VariantID: variant, Component: cost.ComponentCOGS, RawValue: "100",
	}); err != nil {
		t.Fatalf("matching EnterSingleCost: %v", err)
	}
	if n := countCostProfiles(t, pool, variant); n != 1 {
		t.Fatalf("matching write cost profiles = %d, want 1", n)
	}
	if _, err := svc.SetSkuApplicable(ctx, account, variant, []cost.Component{cost.ComponentPackaging}); err != nil {
		t.Fatalf("matching SetSkuApplicable: %v", err)
	}
	if n := countSkuRequirements(t, pool, variant); n != 1 {
		t.Fatalf("matching applicability sku_cost_requirements = %d, want 1", n)
	}
}

// TestEnterSingleCostRejectsPercentValue is the #40 seam completion at the
// service boundary: a percent token entered via single-value entry is not Money;
// EnterSingleCost must reject it with the stable ErrPercentNotMoney code and
// commit NO cost profile, whether the percent is typed with Persian or Latin
// digits (§9.1: percentages never coerce into Money).
func TestEnterSingleCostRejectsPercentValue(t *testing.T) {
	pool, q := newPool(t)
	svc := cost.NewService(pool)
	ctx := context.Background()

	account, variant := seedVariant(t, q, "SKU-PCT")

	for _, raw := range []string{"۱۰٪", "10%"} {
		_, err := svc.EnterSingleCost(ctx, cost.SingleCostInput{
			Account: account, VariantID: variant, Component: cost.ComponentCOGS, RawValue: raw,
		})
		if !errors.Is(err, cost.ErrPercentNotMoney) {
			t.Fatalf("EnterSingleCost(%q) err = %v, want ErrPercentNotMoney", raw, err)
		}
	}
	if n := countCostProfiles(t, pool, variant); n != 0 {
		t.Fatalf("percent rejection committed cost profiles = %d, want 0", n)
	}
}

// TestEnterSingleCostUnknownVariant keeps the not-found path distinct from the
// mismatch path (unknown variant ⇒ 404 ErrVariantNotFound, not a tenant breach).
func TestEnterSingleCostUnknownVariant(t *testing.T) {
	pool, q := newPool(t)
	svc := cost.NewService(pool)
	ctx := context.Background()

	account, _ := seedVariant(t, q, "SKU-UNK")
	_, err := svc.EnterSingleCost(ctx, cost.SingleCostInput{
		Account: account, VariantID: uuid.New(), Component: cost.ComponentCOGS, RawValue: "100",
	})
	if !errors.Is(err, cost.ErrVariantNotFound) {
		t.Fatalf("unknown variant err = %v, want ErrVariantNotFound", err)
	}
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

// TestCommissionProvenanceGatesCompleteViaDB is the issue #38 regression guard at
// the DB seam (PRD §9.2, §16, CST-003): a SKU with fresh COGS plus a seller-entered
// commission — via BOTH single-value entry (source single_value) and CSV import
// (source csv_import) — stays blocked (Missing), because seller-supplied commission
// is not authoritative. The seller commission is still STORED as evidence. The same
// SKU becomes Complete only after an authoritative (connector) commission is present.
func TestCommissionProvenanceGatesCompleteViaDB(t *testing.T) {
	pool, q := newPool(t)
	base := time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC)
	now := base
	svc := cost.NewService(pool).WithClock(func() time.Time { return now })
	ctx := context.Background()

	// --- single-value seller commission path ---
	account, variant := seedVariant(t, q, "SKU-PROV-SV")
	if _, err := svc.EnterSingleCost(ctx, cost.SingleCostInput{
		Account: account, VariantID: variant, Component: cost.ComponentCOGS, RawValue: "100", EffectiveFrom: now,
	}); err != nil {
		t.Fatalf("enter cogs: %v", err)
	}
	if _, err := svc.EnterSingleCost(ctx, cost.SingleCostInput{
		Account: account, VariantID: variant, Component: cost.ComponentCommission, RawValue: "10", EffectiveFrom: now,
	}); err != nil {
		t.Fatalf("enter seller commission: %v", err)
	}
	// Seller commission is stored as evidence (append-only version exists)...
	if n := countCostProfiles(t, pool, variant); n != 2 {
		t.Fatalf("cost profiles = %d, want 2 (cogs + seller commission stored)", n)
	}
	// ...but does NOT satisfy the hard requirement: SKU stays blocked.
	r, err := svc.GetReadiness(ctx, variant)
	if err != nil {
		t.Fatalf("readiness: %v", err)
	}
	if r.State != "missing" {
		t.Fatalf("single-value seller commission state = %q, want missing", r.State)
	}
	// Authoritative connector commission unblocks → Complete (later effective_from
	// so it is the in-force version).
	seedConnectorCommission(t, q, account, variant, 10, now, nil)
	if _, err := svc.RecomputeReadiness(ctx, account, variant); err != nil {
		t.Fatalf("recompute: %v", err)
	}
	r, _ = svc.GetReadiness(ctx, variant)
	if r.State != "complete" {
		t.Fatalf("after connector commission state = %q, want complete", r.State)
	}

	// --- CSV import seller commission path (source csv_import) ---
	account2, variant2 := seedVariant(t, q, "SKU-PROV-CSV")
	csv := "sku,cogs,commission\nSKU-PROV-CSV,100,10\n"
	preview, err := svc.PreviewImport(ctx, cost.PreviewInput{Account: account2, Content: csv})
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if _, err := svc.CommitImport(ctx, preview.Batch.ID, uuid.Nil); err != nil {
		t.Fatalf("commit: %v", err)
	}
	r2, err := svc.GetReadiness(ctx, variant2)
	if err != nil {
		t.Fatalf("readiness csv: %v", err)
	}
	if r2.State != "missing" {
		t.Fatalf("csv seller commission state = %q, want missing", r2.State)
	}
	seedConnectorCommission(t, q, account2, variant2, 10, now, nil)
	if _, err := svc.RecomputeReadiness(ctx, account2, variant2); err != nil {
		t.Fatalf("recompute csv: %v", err)
	}
	r2, _ = svc.GetReadiness(ctx, variant2)
	if r2.State != "complete" {
		t.Fatalf("csv after connector commission state = %q, want complete", r2.State)
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
	// Commission must be authoritative (connector-derived) to satisfy its hard
	// requirement (§9.2, issue #38); seller entry alone leaves the SKU blocked.
	seedConnectorCommission(t, q, account, variant, 10, now, nil)
	// The direct seed bypasses the service recompute; trigger it explicitly.
	if _, err := svc.RecomputeReadiness(ctx, account, variant); err != nil {
		t.Fatalf("recompute after connector commission: %v", err)
	}
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
