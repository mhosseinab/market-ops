package catalog_test

import (
	"context"
	"testing"

	"github.com/mhosseinab/market-ops/services/core/internal/catalog"
	"github.com/mhosseinab/market-ops/services/core/internal/connector"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
)

// TestSyncStreak_ProducerSeam_FailThenSucceed proves the producer seam: a Syncer
// wired to the shared tracker advances the §20.1 consecutive-failure streak on a
// failed sync attempt and resets it to zero on a successful completion (issue #146).
// Skips without DATABASE_URL, matching the other catalog DB tests.
func TestSyncStreak_ProducerSeam_FailThenSucceed(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	org, account := seedOrgAccount(t, q)

	tel := catalog.NewSyncTelemetry(nil)

	// A failing source (always errors on fetch) => the attempt fails => streak 1.
	failRun, err := q.CreateCatalogSyncRun(ctx, db.CreateCatalogSyncRunParams{
		MarketplaceAccountID: account, Kind: string(catalog.KindIncremental),
	})
	if err != nil {
		t.Fatalf("create failing run: %v", err)
	}
	failing := catalog.NewSyncer(pool, failingSource{}, 2).WithTelemetry(tel)
	if err := failing.Resume(ctx, account, failRun.ID); err == nil {
		t.Fatal("expected the failing sync to return an error")
	}
	if got := tel.StreakFor(account); got != 1 {
		t.Fatalf("after one failed sync the streak should be 1, got %d", got)
	}

	// A succeeding source => the run completes => streak resets to 0.
	okRun, err := q.CreateCatalogSyncRun(ctx, db.CreateCatalogSyncRunParams{
		MarketplaceAccountID: account, Kind: string(catalog.KindIncremental),
	})
	if err != nil {
		t.Fatalf("create ok run: %v", err)
	}
	ok := catalog.NewSyncer(pool, newFakeSource(baseItems(), 2), 2).WithTelemetry(tel)
	if err := ok.Resume(ctx, account, okRun.ID); err != nil {
		t.Fatalf("expected the succeeding sync to complete: %v", err)
	}
	if got := tel.StreakFor(account); got != 0 {
		t.Fatalf("a successful sync must reset the streak to 0, got %d", got)
	}
	_ = org
}

// TestSyncStreak_SeedFromDurableState proves restart re-derivation from durable
// sync-run state: after two failed runs recorded in the DB, a fresh tracker seeded
// from durable state reports a streak of 2, so a process restart does not silently
// zero a real streak (issue #146 acceptance test 6).
func TestSyncStreak_SeedFromDurableState(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	_, account := seedOrgAccount(t, q)

	for i := 0; i < 2; i++ {
		run, err := q.CreateCatalogSyncRun(ctx, db.CreateCatalogSyncRunParams{
			MarketplaceAccountID: account, Kind: string(catalog.KindIncremental),
		})
		if err != nil {
			t.Fatalf("create run: %v", err)
		}
		if _, err := q.FailCatalogSyncRun(ctx, db.FailCatalogSyncRunParams{
			ID: run.ID, Error: "DK 503 during sync fetch",
		}); err != nil {
			t.Fatalf("fail run: %v", err)
		}
	}

	tel := catalog.NewSyncTelemetry(nil)
	if err := tel.SeedFromDurableState(ctx, pool); err != nil {
		t.Fatalf("seed from durable state: %v", err)
	}
	if got := tel.StreakFor(account); got != 2 {
		t.Fatalf("re-derived streak should be 2 after two durable failed runs, got %d", got)
	}
}

// failingSource always errors on fetch, driving a sync-attempt failure.
type failingSource struct{}

func (failingSource) FetchVariantsPage(_ context.Context, page, _ int) (connector.VariantPage, error) {
	return connector.VariantPage{}, &connector.VariantsPayloadError{Page: page, Reason: "seam test: forced failure"}
}
