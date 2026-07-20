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

	// Mark the failing run terminal before starting the next one. A retryable Resume
	// leaves the run 'running' (in-flight); the account may hold at most one in-flight
	// run (uq_catalog_sync_runs_inflight, issue #76), so a fresh run can only be
	// created once the prior one is terminal — exactly what happens in production when
	// River exhausts a run's retries. This durable transition does not touch the live
	// telemetry streak (already recorded above).
	if _, err := q.FailCatalogSyncRun(ctx, db.FailCatalogSyncRunParams{
		ID: failRun.ID, Error: "retries exhausted (seam test)",
	}); err != nil {
		t.Fatalf("fail run: %v", err)
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

// TestSyncStreak_MultiRetrySingleRun_LiveEqualsDurable is the blocker-1 core
// (issue #146): ONE run that fails across MULTIPLE River retry attempts must
// advance the streak by exactly 1 on the LIVE path, and the value a fresh tracker
// re-derives from the SAME durable history via SeedFromDurableState must equal it
// — no false page from retries (a), no collapse-to-1 that silences an active page
// on restart (b). A post-restart retry of the same run must not double-count.
func TestSyncStreak_MultiRetrySingleRun_LiveEqualsDurable(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	_, account := seedOrgAccount(t, q)

	tel := catalog.NewSyncTelemetry(nil)
	run, err := q.CreateCatalogSyncRun(ctx, db.CreateCatalogSyncRunParams{
		MarketplaceAccountID: account, Kind: string(catalog.KindIncremental),
	})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}

	// River retries the SAME run three times; each Resume fails on the failing source.
	failing := catalog.NewSyncer(pool, failingSource{}, 2).WithTelemetry(tel)
	for i := 0; i < 3; i++ {
		if err := failing.Resume(ctx, account, run.ID); err == nil {
			t.Fatalf("attempt %d: expected the failing sync to error", i+1)
		}
	}
	if got := tel.StreakFor(account); got != 1 {
		t.Fatalf("one run failing across 3 retries must yield streak 1 (not a false page), got %d", got)
	}

	// Restart: a fresh tracker seeded from the SAME durable history must re-derive
	// the identical value — no collapse that would silence an active streak.
	fresh := catalog.NewSyncTelemetry(nil)
	if err := fresh.SeedFromDurableState(ctx, pool); err != nil {
		t.Fatalf("seed from durable state: %v", err)
	}
	if got := fresh.StreakFor(account); got != 1 {
		t.Fatalf("re-derived streak must equal the live value (1), got %d", got)
	}

	// A further retry of the SAME run after restart must not double-count on top of
	// the seeded value.
	failingAfter := catalog.NewSyncer(pool, failingSource{}, 2).WithTelemetry(fresh)
	if err := failingAfter.Resume(ctx, account, run.ID); err == nil {
		t.Fatal("expected the post-restart retry to error")
	}
	if got := fresh.StreakFor(account); got != 1 {
		t.Fatalf("a post-restart retry of an already-counted run must not double-count, got %d", got)
	}
}

// TestSyncStreak_SuffixBounded_LargeTailDoesNotIncreaseRows proves the issue #211
// bound: startup re-derivation reads only a bounded newest suffix per account, so a
// growing lifetime catalog_sync_runs history never inflates the seed read nor changes
// the derived streak. Recent runs (3 failures over a completed run => streak 3) are
// established first; then a large OLDER tail (> SeedSuffixBound rows) is appended.
// ListRecentCatalogSyncOutcomes(ctx, SeedSuffixBound) must return <= SeedSuffixBound
// rows for the account, and the re-derived streak must stay 3 as the tail grows.
// Skips without DATABASE_URL (defers the row-count assertion to CI postgres).
func TestSyncStreak_SuffixBounded_LargeTailDoesNotIncreaseRows(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	_, account := seedOrgAccount(t, q)

	bound := catalog.SeedSuffixBound

	// Recent history with explicit started_at so ordering is deterministic: an oldest
	// 'completed' run, then three newer 'failed' runs => newest-first walk yields 3.
	// make_interval takes integer/double arguments directly, so no fragile
	// `integer || text` cast is needed (that operator does not exist in Postgres).
	insertRun := func(status string, offsetSeconds int) {
		t.Helper()
		if _, err := pool.Exec(ctx,
			`INSERT INTO catalog_sync_runs (marketplace_account_id, kind, status, next_page, error, started_at)
			 VALUES ($1, 'incremental', $2, 1, $3, now() + make_interval(secs => $4))`,
			account, status, "seed test", offsetSeconds,
		); err != nil {
			t.Fatalf("insert recent run: %v", err)
		}
	}
	insertRun("completed", 1)
	insertRun("failed", 2)
	insertRun("failed", 3)
	insertRun("failed", 4)

	// appendOlderTail inserts n 'failed' runs OLDER than the recent history (negative
	// hour offsets), so they never affect the streak but do grow lifetime history.
	appendOlderTail := func(n int) {
		t.Helper()
		if _, err := pool.Exec(ctx,
			`INSERT INTO catalog_sync_runs (marketplace_account_id, kind, status, next_page, error, started_at)
			 SELECT $1, 'incremental', 'failed', 1, 'old tail', now() - make_interval(hours => g)
			 FROM generate_series(1, $2) g`,
			account, n,
		); err != nil {
			t.Fatalf("insert old tail: %v", err)
		}
	}

	deriveViaSeed := func() int64 {
		t.Helper()
		tel := catalog.NewSyncTelemetry(nil)
		if err := tel.SeedFromDurableState(ctx, pool); err != nil {
			t.Fatalf("seed from durable state: %v", err)
		}
		return tel.StreakFor(account)
	}
	rowsForAccount := func() int {
		t.Helper()
		rows, err := q.ListRecentCatalogSyncOutcomes(ctx, int32(bound))
		if err != nil {
			t.Fatalf("list recent outcomes: %v", err)
		}
		n := 0
		for _, r := range rows {
			if r.MarketplaceAccountID == account {
				n++
			}
		}
		return n
	}

	if got := deriveViaSeed(); got != 3 {
		t.Fatalf("pre-tail: derived streak should be 3, got %d", got)
	}

	// Grow lifetime history well past the bound. Once total rows exceed SeedSuffixBound
	// the read is capped: it returns exactly `bound` rows for the account, and adding
	// yet MORE older history does not increase that capped count — proving the read is
	// suffix-bounded, not proportional to lifetime history. The streak stays 3 (the
	// 'completed' run remains inside the newest suffix, so it is authoritative).
	appendOlderTail(bound + 50)
	afterFirstTail := rowsForAccount()
	if afterFirstTail > bound {
		t.Fatalf("suffix read must be bounded: got %d rows for account, want <= %d", afterFirstTail, bound)
	}
	if afterFirstTail != bound {
		t.Fatalf("with history beyond the bound the read must fill exactly %d rows, got %d", bound, afterFirstTail)
	}
	if got := deriveViaSeed(); got != 3 {
		t.Fatalf("post-tail: derived streak must stay 3 as history grows, got %d", got)
	}

	// More history still must NOT increase the bounded read (this is the core #211
	// property: startup cost is independent of lifetime history depth).
	appendOlderTail(bound + 50)
	afterSecondTail := rowsForAccount()
	if afterSecondTail != afterFirstTail {
		t.Fatalf("more lifetime history must not enlarge the bounded read: first=%d second=%d", afterFirstTail, afterSecondTail)
	}
	if got := deriveViaSeed(); got != 3 {
		t.Fatalf("post-second-tail: derived streak must still be 3, got %d", got)
	}
}

// failingSource always errors on fetch, driving a sync-attempt failure.
type failingSource struct{}

func (failingSource) FetchVariantsPage(_ context.Context, page, _ int) (connector.VariantPage, error) {
	return connector.VariantPage{}, &connector.VariantsPayloadError{Page: page, Reason: "seam test: forced failure"}
}
