package catalog_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mhosseinab/market-ops/services/core/internal/catalog"
	"github.com/mhosseinab/market-ops/services/core/internal/connector"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
)

// newPool connects to DATABASE_URL (schema applied via task db:reset). Skips when
// unset so the suite still runs where no Postgres is provisioned.
func newPool(t *testing.T) (*pgxpool.Pool, *db.Queries) {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set; skipping catalog DB test")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("connect pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool, db.New(pool)
}

func seedAccount(t *testing.T, q *db.Queries) uuid.UUID {
	t.Helper()
	_, account := seedOrgAccount(t, q)
	return account
}

// seedOrgAccount creates an organization + marketplace account and returns both.
// The org id is needed wherever a real connector call is made, since connector
// reads/writes are org-scoped (S8-AUTHZ-001).
func seedOrgAccount(t *testing.T, q *db.Queries) (org, account uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	o, err := q.CreateOrganization(ctx, "catalog-test-"+uuid.NewString())
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	acct, err := q.CreateMarketplaceAccount(ctx, db.CreateMarketplaceAccountParams{
		OrganizationID:  o.ID,
		NativeAccountID: "native-" + uuid.NewString(),
		DisplayName:     "Catalog Seller",
	})
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	return o.ID, acct.ID
}

// item builds a connector.VariantItem with the given native ids and a verbatim
// price token, plus a raw JSON snapshot body.
func item(productID, variantID, listingID, price int64) connector.VariantItem {
	stock := int64(7)
	return connector.VariantItem{
		NativeProductID: productID,
		NativeVariantID: variantID,
		NativeListingID: listingID,
		ProductTitle:    fmt.Sprintf("product %d", productID),
		VariantTitle:    fmt.Sprintf("variant %d", variantID),
		SupplierCode:    fmt.Sprintf("SUP-%d", variantID),
		ProductURL:      fmt.Sprintf("https://demo.digikala.com/product/dkp-%d/", productID),
		SellingChannel:  "digikala",
		PriceRawValue:   fmt.Sprintf("%d", price),
		SellerStock:     &stock,
		Raw:             json.RawMessage(fmt.Sprintf(`{"id":%d,"price_sale":%d}`, variantID, price)),
	}
}

// baseItems: 3 products, 5 variants/listings/offers.
func baseItems() []connector.VariantItem {
	return []connector.VariantItem{
		item(100, 1000, 1, 111000),
		item(100, 1001, 2, 222000),
		item(101, 1002, 3, 333000),
		item(102, 1003, 4, 444000),
		item(102, 1004, 5, 555000),
	}
}

// fakeSource paginates a fixed ordered item list. failOnce injects an error the
// first time a given 1-based page is fetched, to simulate a pagination fault.
type fakeSource struct {
	items    []connector.VariantItem
	pageSize int
	failOnce map[int]bool
	seen     map[int]int // page -> fetch count
}

func newFakeSource(items []connector.VariantItem, pageSize int) *fakeSource {
	return &fakeSource{items: items, pageSize: pageSize, failOnce: map[int]bool{}, seen: map[int]int{}}
}

func (f *fakeSource) FetchVariantsPage(_ context.Context, page, _ int) (connector.VariantPage, error) {
	f.seen[page]++
	if f.failOnce[page] && f.seen[page] == 1 {
		return connector.VariantPage{}, fmt.Errorf("injected pagination fault on page %d", page)
	}
	total := (len(f.items) + f.pageSize - 1) / f.pageSize
	if total == 0 {
		total = 1
	}
	start := (page - 1) * f.pageSize
	end := start + f.pageSize
	var slice []connector.VariantItem
	if start < len(f.items) {
		if end > len(f.items) {
			end = len(f.items)
		}
		slice = f.items[start:end]
	}
	return connector.VariantPage{Items: slice, Page: page, TotalPages: total, TotalRows: len(f.items)}, nil
}

func counts(t *testing.T, q *db.Queries, account uuid.UUID) (prod, variant, listing, offer, snap int64) {
	t.Helper()
	ctx := context.Background()
	var err error
	if prod, err = q.CountProducts(ctx, account); err != nil {
		t.Fatal(err)
	}
	if variant, err = q.CountVariants(ctx, account); err != nil {
		t.Fatal(err)
	}
	if listing, err = q.CountListings(ctx, account); err != nil {
		t.Fatal(err)
	}
	if offer, err = q.CountOwnedOffers(ctx, account); err != nil {
		t.Fatal(err)
	}
	if snap, err = q.CountCatalogSnapshots(ctx, account); err != nil {
		t.Fatal(err)
	}
	return
}

func runImport(t *testing.T, s *catalog.Syncer, account uuid.UUID, kind catalog.Kind) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	runID, err := s.Start(ctx, account, kind)
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	if err := s.Resume(ctx, account, runID); err != nil {
		t.Fatalf("resume run: %v", err)
	}
	return runID
}

// TestInitialImportZeroDuplicatesOnReorderedReplay is the CAT-001/ACC-005 core:
// a repeated AND reordered payload replay preserves identity and creates zero
// duplicate canonical records, while raw snapshots accumulate (append-only).
func TestInitialImportZeroDuplicatesOnReorderedReplay(t *testing.T) {
	pool, q := newPool(t)
	account := seedAccount(t, q)
	ctx := context.Background()

	// First import: pages of size 2 over 5 items.
	s1 := catalog.NewSyncer(pool, newFakeSource(baseItems(), 2), 2)
	runA := runImport(t, s1, account, catalog.KindInitial)

	prod, variant, listing, offer, snap := counts(t, q, account)
	if prod != 3 || variant != 5 || listing != 5 || offer != 5 {
		t.Fatalf("after import: products=%d variants=%d listings=%d offers=%d, want 3/5/5/5", prod, variant, listing, offer)
	}
	if snap != 5 {
		t.Fatalf("snapshots=%d, want 5", snap)
	}
	runARow, _ := q.GetCatalogSyncRun(ctx, runA)
	if runARow.Status != "completed" || runARow.DriftCount != 0 {
		t.Fatalf("runA status=%s drift=%d, want completed/0", runARow.Status, runARow.DriftCount)
	}

	// Reordered replay: reverse the item order AND use a different page size, so
	// both intra-page order and page boundaries differ from the first run.
	reordered := baseItems()
	for i, j := 0, len(reordered)-1; i < j; i, j = i+1, j-1 {
		reordered[i], reordered[j] = reordered[j], reordered[i]
	}
	s2 := catalog.NewSyncer(pool, newFakeSource(reordered, 3), 3)
	runImport(t, s2, account, catalog.KindInitial)

	prod2, variant2, listing2, offer2, snap2 := counts(t, q, account)
	if prod2 != 3 || variant2 != 5 || listing2 != 5 || offer2 != 5 {
		t.Fatalf("after reordered replay: products=%d variants=%d listings=%d offers=%d, want 3/5/5/5 (zero duplicates)", prod2, variant2, listing2, offer2)
	}
	if snap2 != 10 {
		t.Fatalf("snapshots=%d after replay, want 10 (append-only accumulation)", snap2)
	}
}

// TestInterruptedImportResumes proves the initial import is resumable: after
// processing only some pages it continues from the persisted cursor to a
// consistent, complete, duplicate-free result.
func TestInterruptedImportResumes(t *testing.T) {
	pool, q := newPool(t)
	account := seedAccount(t, q)
	ctx := context.Background()

	s := catalog.NewSyncer(pool, newFakeSource(baseItems(), 2), 2)
	runID, err := s.Start(ctx, account, catalog.KindInitial)
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	// Interruption: process just one page, then stop.
	if err := s.ResumeN(ctx, account, runID, 1); err != nil {
		t.Fatalf("resumeN: %v", err)
	}
	partial, _ := q.GetCatalogSyncRun(ctx, runID)
	if partial.Status != "running" {
		t.Fatalf("after partial: status=%s, want running", partial.Status)
	}
	if partial.NextPage != 2 {
		t.Fatalf("after partial: next_page=%d, want 2 (resume cursor)", partial.NextPage)
	}
	if _, v, _, _, _ := counts(t, q, account); v != 2 {
		t.Fatalf("after 1 page: variants=%d, want 2", v)
	}

	// Resume to completion.
	if err := s.Resume(ctx, account, runID); err != nil {
		t.Fatalf("resume: %v", err)
	}
	done, _ := q.GetCatalogSyncRun(ctx, runID)
	if done.Status != "completed" {
		t.Fatalf("after resume: status=%s, want completed", done.Status)
	}
	if _, v, _, o, _ := counts(t, q, account); v != 5 || o != 5 {
		t.Fatalf("after resume: variants=%d offers=%d, want 5/5 (no duplicates, no skips)", v, o)
	}
}

// TestPaginationFaultThenResume proves a transient pagination fault leaves the
// run resumable (status running, cursor unchanged) and a retry completes with
// zero duplicates — not a skipped page or a failed run.
func TestPaginationFaultThenResume(t *testing.T) {
	pool, q := newPool(t)
	account := seedAccount(t, q)
	ctx := context.Background()

	src := newFakeSource(baseItems(), 2)
	src.failOnce[2] = true // fault on the second page, once.
	s := catalog.NewSyncer(pool, src, 2)

	runID, err := s.Start(ctx, account, catalog.KindInitial)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := s.Resume(ctx, account, runID); err == nil {
		t.Fatal("expected pagination-fault error on first resume")
	}
	faulted, _ := q.GetCatalogSyncRun(ctx, runID)
	if faulted.Status != "running" {
		t.Fatalf("after fault: status=%s, want running (retryable)", faulted.Status)
	}
	if faulted.NextPage != 2 || faulted.Error == "" {
		t.Fatalf("after fault: next_page=%d error=%q, want cursor=2 and recorded error", faulted.NextPage, faulted.Error)
	}

	// Retry: the fault is one-shot, so the second attempt completes.
	if err := s.Resume(ctx, account, runID); err != nil {
		t.Fatalf("resume after fault: %v", err)
	}
	done, _ := q.GetCatalogSyncRun(ctx, runID)
	if done.Status != "completed" {
		t.Fatalf("after retry: status=%s, want completed", done.Status)
	}
	if _, v, _, _, _ := counts(t, q, account); v != 5 {
		t.Fatalf("after retry: variants=%d, want 5 (no duplicates)", v)
	}
}

// TestIncrementalReconciliationDetectsDrift proves the incremental sync's
// reconciliation pass flags an owned offer that vanished from the latest full
// fetch (drift), without deleting it.
func TestIncrementalReconciliationDetectsDrift(t *testing.T) {
	pool, q := newPool(t)
	account := seedAccount(t, q)
	ctx := context.Background()

	// Full import of all 5 variants.
	s1 := catalog.NewSyncer(pool, newFakeSource(baseItems(), 2), 2)
	runImport(t, s1, account, catalog.KindInitial)

	// Incremental run where one variant (1004) has disappeared.
	shrunk := baseItems()[:4]
	s2 := catalog.NewSyncer(pool, newFakeSource(shrunk, 2), 2)
	runB := runImport(t, s2, account, catalog.KindIncremental)

	row, _ := q.GetCatalogSyncRun(ctx, runB)
	if row.DriftCount != 1 {
		t.Fatalf("drift_count=%d, want 1 (variant 1004 missing from latest fetch)", row.DriftCount)
	}
	// The offer is retained, not deleted (reconciliation reports, never destroys).
	if _, _, _, o, _ := counts(t, q, account); o != 5 {
		t.Fatalf("offers=%d after incremental, want 5 retained", o)
	}
}
