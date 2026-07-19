package catalog_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mhosseinab/market-ops/services/core/internal/catalog"
	"github.com/mhosseinab/market-ops/services/core/internal/connector"
)

// rawDKSource drives the syncer through the REAL connector variants validation
// (DKClient.FetchVariantsPage) against a server returning a hand-crafted body,
// so the invalid-payload behavior is exercised end to end (connector rejection →
// syncer fail-closed), not through a hand-rolled fake error.
type rawDKSource struct {
	dk    *connector.DKClient
	token string
}

func (r rawDKSource) FetchVariantsPage(ctx context.Context, page, size int) (connector.VariantPage, error) {
	return r.dk.FetchVariantsPage(ctx, r.token, page, size)
}

// rawSource returns a Source backed by a real DKClient whose /open-api/v1/variants
// endpoint returns body verbatim with a 200.
func rawSource(t *testing.T, body string) catalog.Source {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	dk, err := connector.NewDKClient(srv.URL, nil)
	if err != nil {
		t.Fatalf("dk client: %v", err)
	}
	return rawDKSource{dk: dk, token: "tok"}
}

// replayServer returns a Source backed by a real DKClient whose
// /open-api/v1/variants endpoint returns a DIFFERENT verbatim body per requested
// `page` query param, so a per-page replay/truncation scenario can be driven end
// to end through the real connector. A page with no mapped body yields 500 (the
// test only exercises the mapped pages).
func replayServer(t *testing.T, bodies map[int]string) catalog.Source {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		body, ok := bodies[page]
		if !ok {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	dk, err := connector.NewDKClient(srv.URL, nil)
	if err != nil {
		t.Fatalf("dk client: %v", err)
	}
	return rawDKSource{dk: dk, token: "tok"}
}

// zeroIDRows counts any canonical row or payload snapshot carrying a native id of
// 0 for the account — the identity a malformed `{}` item would project.
func zeroIDRows(t *testing.T, pool *pgxpool.Pool, account uuid.UUID) (prod, variant, listing, offer, snap int64) {
	t.Helper()
	ctx := context.Background()
	q := func(sql string) int64 {
		var n int64
		if err := pool.QueryRow(ctx, sql, account).Scan(&n); err != nil {
			t.Fatalf("count zero-id: %v", err)
		}
		return n
	}
	prod = q(`SELECT count(*) FROM products WHERE marketplace_account_id=$1 AND native_product_id=0`)
	variant = q(`SELECT count(*) FROM variants WHERE marketplace_account_id=$1 AND native_variant_id=0`)
	listing = q(`SELECT count(*) FROM listings WHERE marketplace_account_id=$1 AND native_listing_id=0`)
	offer = q(`SELECT count(*) FROM owned_offers WHERE marketplace_account_id=$1 AND native_variant_id=0`)
	snap = q(`SELECT count(*) FROM catalog_payload_snapshots WHERE marketplace_account_id=$1 AND native_variant_id=0`)
	return
}

// TestSyncerRejectsInvalidEmptyEnvelopePayload proves a bare `{}` page 1 (issue
// #7 case 1) fails closed: the run stays retryable at page 1, records the error,
// completes no reconciliation, and records no drift against a pre-seeded offer.
func TestSyncerRejectsInvalidEmptyEnvelopePayload(t *testing.T) {
	pool, q := newPool(t)
	account := seedAccount(t, q)
	ctx := context.Background()

	// Seed one existing owned offer via a normal initial import.
	seed := catalog.NewSyncer(pool, newFakeSource([]connector.VariantItem{item(100, 1000, 1, 111000)}, 2), 2)
	runImport(t, seed, account, catalog.KindInitial)
	if _, _, _, o, _ := counts(t, q, account); o != 1 {
		t.Fatalf("seed offers=%d, want 1", o)
	}

	// Incremental run whose page 1 returns `{}`.
	s := catalog.NewSyncer(pool, rawSource(t, `{}`), 1)
	runID, err := s.Start(ctx, account, catalog.KindIncremental)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := s.Resume(ctx, account, runID); err == nil {
		t.Fatal("expected semantic decode error on `{}` page 1")
	}

	run, _ := q.GetCatalogSyncRun(ctx, runID)
	if run.Status != "running" {
		t.Fatalf("status=%s, want running (retryable)", run.Status)
	}
	if run.NextPage != 1 {
		t.Fatalf("next_page=%d, want 1 (cursor unchanged)", run.NextPage)
	}
	if run.Error == "" {
		t.Fatal("expected the error recorded on the run")
	}
	if run.DriftCount != 0 {
		t.Fatalf("drift_count=%d, want 0 (rejected empty page never reconciles)", run.DriftCount)
	}
	// The pre-seeded offer is untouched; no zero-id rows exist.
	if _, _, _, o, _ := counts(t, q, account); o != 1 {
		t.Fatalf("offers=%d after rejected page, want 1 retained", o)
	}
	if p, v, l, o, sn := zeroIDRows(t, pool, account); p+v+l+o+sn != 0 {
		t.Fatalf("zero-id rows present: products=%d variants=%d listings=%d offers=%d snapshots=%d", p, v, l, o, sn)
	}
}

// TestSyncerRejectsInvalidZeroIdentityPayload proves a one-empty-item page (issue
// #7 case 3) fails the page transaction BEFORE any catalog write, so no canonical
// row or payload snapshot with a native id of 0 is ever created.
func TestSyncerRejectsInvalidZeroIdentityPayload(t *testing.T) {
	pool, q := newPool(t)
	account := seedAccount(t, q)
	ctx := context.Background()

	body := `{"data":{"items":[{}],"pager":{"page":1,"total_pages":1,"total_rows":1}}}`
	s := catalog.NewSyncer(pool, rawSource(t, body), 1)
	runID, err := s.Start(ctx, account, catalog.KindInitial)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := s.Resume(ctx, account, runID); err == nil {
		t.Fatal("expected identity validation error on zero-id item")
	}

	run, _ := q.GetCatalogSyncRun(ctx, runID)
	if run.Status != "running" {
		t.Fatalf("status=%s, want running (retryable)", run.Status)
	}
	if run.NextPage != 1 {
		t.Fatalf("next_page=%d, want 1 (page committed nothing)", run.NextPage)
	}
	if run.Error == "" {
		t.Fatal("expected the error recorded on the run")
	}
	// No canonical rows at all, and specifically none with a native id of 0.
	if p, v, l, o, sn := counts(t, q, account); p+v+l+o+sn != 0 {
		t.Fatalf("rejected page wrote rows: products=%d variants=%d listings=%d offers=%d snapshots=%d", p, v, l, o, sn)
	}
	if p, v, l, o, sn := zeroIDRows(t, pool, account); p+v+l+o+sn != 0 {
		t.Fatalf("zero-id rows present: products=%d variants=%d listings=%d offers=%d snapshots=%d", p, v, l, o, sn)
	}
}

// TestSyncerRejectsPagerPageMismatch drives issue #197 case 1 (replayed/cached
// page) through the REAL connector + Syncer.Resume: the syncer requests page 2 but
// DK returns a coherent copy of page 1 (echoed page=1). The connector rejects the
// page mismatch, so the run stays retryable at next_page=2, commits nothing, and
// never reconciles a pre-seeded offer as drift.
func TestSyncerRejectsPagerPageMismatch(t *testing.T) {
	pool, q := newPool(t)
	account := seedAccount(t, q)
	ctx := context.Background()

	// Seed two pages of size 2 (4 rows) via a normal initial import so page 2 is a
	// real cursor position and an existing offer set can be checked for false drift.
	four := []connector.VariantItem{
		item(100, 1000, 1, 111000),
		item(100, 1001, 2, 222000),
		item(101, 1002, 3, 333000),
		item(102, 1003, 4, 444000),
	}
	seed := catalog.NewSyncer(pool, newFakeSource(four, 2), 2)
	runImport(t, seed, account, catalog.KindInitial)
	if _, _, _, o, _ := counts(t, q, account); o != 4 {
		t.Fatalf("seed offers=%d, want 4", o)
	}

	// Incremental run, page size 2. Page 1 fetch is a valid copy of page 1; page 2
	// fetch REPLAYS page 1 (echoed page=1) — the mismatch must be rejected.
	body := replayServer(t, map[int]string{
		1: `{"data":{"items":[{"product_id":100,"id":1000,"product_variant_id":1},{"product_id":100,"id":1001,"product_variant_id":2}],"pager":{"page":1,"total_pages":2,"total_rows":4}}}`,
		2: `{"data":{"items":[{"product_id":100,"id":1000,"product_variant_id":1},{"product_id":100,"id":1001,"product_variant_id":2}],"pager":{"page":1,"total_pages":2,"total_rows":4}}}`,
	})
	s := catalog.NewSyncer(pool, body, 2)
	runID, err := s.Start(ctx, account, catalog.KindIncremental)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := s.Resume(ctx, account, runID); err == nil {
		t.Fatal("expected page-mismatch rejection on replayed page 2")
	}

	run, _ := q.GetCatalogSyncRun(ctx, runID)
	if run.Status != "running" {
		t.Fatalf("status=%s, want running (retryable)", run.Status)
	}
	if run.NextPage != 2 {
		t.Fatalf("next_page=%d, want 2 (cursor unchanged at the rejected page)", run.NextPage)
	}
	if run.Error == "" {
		t.Fatal("expected the error recorded on the run")
	}
	if run.DriftCount != 0 {
		t.Fatalf("drift_count=%d, want 0 (rejected page never reconciles)", run.DriftCount)
	}
	// The seeded offers are intact; the incremental run must NOT report the second
	// page's rows as drift, and must NOT complete.
	if _, _, _, o, _ := counts(t, q, account); o != 4 {
		t.Fatalf("offers=%d after rejected replay, want 4 retained", o)
	}
}

// TestSyncerRejectsTruncatedPage drives issue #197 case 2 (truncated single-page
// response) through the REAL connector + Syncer.Resume: page 1 claims total_rows=50
// on a single page but carries one valid item. The connector rejects the
// cardinality mismatch, so the initial import stays retryable at page 1, commits
// nothing, and does not complete a partial import.
func TestSyncerRejectsTruncatedPage(t *testing.T) {
	pool, q := newPool(t)
	account := seedAccount(t, q)
	ctx := context.Background()

	body := `{"data":{"items":[{"product_id":100,"id":1000,"product_variant_id":1}],"pager":{"page":1,"total_pages":1,"total_rows":50}}}`
	s := catalog.NewSyncer(pool, rawSource(t, body), 50)
	runID, err := s.Start(ctx, account, catalog.KindInitial)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := s.Resume(ctx, account, runID); err == nil {
		t.Fatal("expected cardinality rejection on truncated page 1")
	}

	run, _ := q.GetCatalogSyncRun(ctx, runID)
	if run.Status != "running" {
		t.Fatalf("status=%s, want running (retryable)", run.Status)
	}
	if run.NextPage != 1 {
		t.Fatalf("next_page=%d, want 1 (page committed nothing)", run.NextPage)
	}
	if run.Error == "" {
		t.Fatal("expected the error recorded on the run")
	}
	// A truncated page must complete NO import: no canonical rows or snapshots.
	if p, v, l, o, sn := counts(t, q, account); p+v+l+o+sn != 0 {
		t.Fatalf("rejected truncated page wrote rows: products=%d variants=%d listings=%d offers=%d snapshots=%d", p, v, l, o, sn)
	}
}
