package watchlist_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/mhosseinab/market-ops/services/core/internal/audit"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/watchlist"
)

// TestWatchlistAddCapIsRaceSafeAcrossConcurrentVariants is the issue #136
// regression: the cap check must be ATOMIC with the insert. An account seeded to
// MaxEntries-1 has exactly one free slot; N goroutines racing to add DISTINCT
// confirmed variants must, in aggregate, add at most one — the account can never
// transiently exceed MaxEntries. Before the fix (cap counted OUTSIDE the insert
// tx) two concurrent adds both observed count < cap and both inserted → 51.
//
// DB-gated: requires real concurrency against Postgres (CI postgres:18); skips
// locally without DATABASE_URL.
func TestWatchlistAddCapIsRaceSafeAcrossConcurrentVariants(t *testing.T) {
	pool, q := newPool(t)
	account := seedAccount(t, q)
	svc := watchlist.NewService(pool)
	actor := audit.Actor{ID: "owner@x.io", Role: "owner", Surface: "screens"}

	// Fill to exactly one slot below the cap via direct SQL (CountWatchlistEntries
	// counts rows regardless of provenance — same technique as the cap test).
	for i := 0; i < watchlist.MaxEntries-1; i++ {
		v := seedVariant(t, q, account)
		if _, err := q.InsertWatchlistEntry(context.Background(), db.InsertWatchlistEntryParams{
			MarketplaceAccountID: account,
			VariantID:            v,
			AddedBy:              "seed",
		}); err != nil {
			t.Fatalf("seed watchlist entry %d: %v", i, err)
		}
	}

	const racers = 10
	variants := make([]uuid.UUID, racers)
	for i := range variants {
		variants[i] = seedConfirmedVariant(t, q, account)
	}

	var (
		wg        sync.WaitGroup
		mu        sync.Mutex
		succeeded int
		capErrors int
	)
	start := make(chan struct{})
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(variant uuid.UUID) {
			defer wg.Done()
			<-start
			_, err := svc.Add(context.Background(), account, variant, actor)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				succeeded++
			case errors.Is(err, watchlist.ErrCapExceeded):
				capErrors++
			default:
				t.Errorf("unexpected Add error: %v", err)
			}
		}(variants[i])
	}
	close(start)
	wg.Wait()

	// Exactly one racer takes the last slot; the rest are cap-rejected.
	if succeeded != 1 {
		t.Fatalf("succeeded = %d, want exactly 1 (only one free slot)", succeeded)
	}
	if capErrors != racers-1 {
		t.Fatalf("capErrors = %d, want %d", capErrors, racers-1)
	}

	count, err := q.CountWatchlistEntries(context.Background(), account)
	if err != nil {
		t.Fatalf("CountWatchlistEntries: %v", err)
	}
	if count > int64(watchlist.MaxEntries) {
		t.Fatalf("final count = %d, EXCEEDS MaxEntries = %d (TOCTOU race)", count, watchlist.MaxEntries)
	}
	if count != int64(watchlist.MaxEntries) {
		t.Fatalf("final count = %d, want exactly %d", count, watchlist.MaxEntries)
	}
}

// TestWatchlistAddSameVariantIdempotentUnderConcurrency proves the new
// per-account serialization does not break same-variant idempotency: N
// goroutines racing to add the SAME confirmed variant yield exactly one entry
// and exactly one audit row, with no error.
func TestWatchlistAddSameVariantIdempotentUnderConcurrency(t *testing.T) {
	pool, q := newPool(t)
	account := seedAccount(t, q)
	variant := seedConfirmedVariant(t, q, account)
	svc := watchlist.NewService(pool)
	actor := audit.Actor{ID: "owner@x.io", Role: "owner", Surface: "screens"}

	const racers = 10
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if _, err := svc.Add(context.Background(), account, variant, actor); err != nil {
				t.Errorf("idempotent concurrent Add: %v", err)
			}
		}()
	}
	close(start)
	wg.Wait()

	entries, err := svc.List(context.Background(), account)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want exactly 1 (idempotent)", len(entries))
	}

	var auditCount int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_records WHERE marketplace_account_id = $1 AND event_type = 'watchlist_change'`,
		account).Scan(&auditCount); err != nil {
		t.Fatalf("query audit count: %v", err)
	}
	if auditCount != 1 {
		t.Fatalf("audit rows = %d, want exactly 1", auditCount)
	}
}
