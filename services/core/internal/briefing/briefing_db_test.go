package briefing_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mhosseinab/market-ops/services/core/internal/briefing"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/event"
	"github.com/mhosseinab/market-ops/services/core/internal/money"
)

func newPool(t *testing.T) (*pgxpool.Pool, *db.Queries) {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set; skipping briefing DB test")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("connect pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool, db.New(pool)
}

// seedAccountWithVariants creates one account and n variants under it, so several
// distinct events can be opened for the SAME account (distinct dedup keys).
func seedAccountWithVariants(t *testing.T, q *db.Queries, n int) (account uuid.UUID, variants []uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	org, err := q.CreateOrganization(ctx, "brief-test-"+uuid.NewString())
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	acct, err := q.CreateMarketplaceAccount(ctx, db.CreateMarketplaceAccountParams{
		OrganizationID:  org.ID,
		NativeAccountID: "native-" + uuid.NewString(),
		DisplayName:     "Brief Seller",
	})
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	for i := 0; i < n; i++ {
		nativeProduct := int64(uuid.New().ID())
		nativeVariant := int64(uuid.New().ID())
		prod, err := q.UpsertProduct(ctx, db.UpsertProductParams{
			MarketplaceAccountID: acct.ID,
			NativeProductID:      nativeProduct,
			Title:                "Widget",
		})
		if err != nil {
			t.Fatalf("upsert product: %v", err)
		}
		v, err := q.UpsertVariant(ctx, db.UpsertVariantParams{
			MarketplaceAccountID: acct.ID,
			ProductID:            prod.ID,
			NativeVariantID:      nativeVariant,
			NativeProductID:      nativeProduct,
			SupplierCode:         "SKU-" + uuid.NewString()[:8],
			Title:                "Widget - V",
		})
		if err != nil {
			t.Fatalf("upsert variant: %v", err)
		}
		variants = append(variants, v.ID)
	}
	return acct.ID, variants
}

func openLostWinning(t *testing.T, svc *event.Service, account, variant uuid.UUID, exposure int64, now time.Time) {
	t.Helper()
	m, err := money.New(exposure, "IRR", 0)
	if err != nil {
		t.Fatalf("money: %v", err)
	}
	c, ok := event.DetectWinningState(event.WinningStateInput{
		Variant:    variant,
		WasWinning: true,
		IsWinning:  false,
		Exposure:   event.KnownExposure(m),
		Evidence:   event.Evidence{Quality: event.QualityVerified, Ref: "r"},
		Now:        now,
		TTL:        time.Hour,
	})
	if !ok {
		t.Fatal("detector did not produce a candidate")
	}
	if _, err := svc.RecordFor(context.Background(), account, c); err != nil {
		t.Fatalf("record event: %v", err)
	}
}

// TestBriefingEventsEqualTodayFeed is the CHAT-010 never-cut determinism test: a
// generated briefing's event ids AND order EQUAL the Today feed for the same
// account, because the briefing reuses the SAME event.Rank the feed uses. It also
// proves generation is idempotent per business day (no duplicate on re-run).
func TestBriefingEventsEqualTodayFeed(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account, variants := seedAccountWithVariants(t, q, 3)
	eventSvc := event.NewService(pool)
	now := time.Now().UTC()

	// Three events with distinct exposures ⇒ a non-trivial deterministic order.
	openLostWinning(t, eventSvc, account, variants[0], 5_000, now)
	openLostWinning(t, eventSvc, account, variants[1], 20_000, now)
	openLostWinning(t, eventSvc, account, variants[2], 12_000, now)

	today, err := eventSvc.Today(ctx, account)
	if err != nil {
		t.Fatalf("today: %v", err)
	}
	if len(today) != 3 {
		t.Fatalf("today has %d events, want 3", len(today))
	}

	bsvc := briefing.NewService(pool, eventSvc)
	created, err := bsvc.GenerateForAccount(ctx, account)
	if err != nil {
		t.Fatalf("generate briefing: %v", err)
	}
	if !created {
		t.Fatal("first generation must create the briefing")
	}

	b, err := bsvc.Get(ctx, account, bsvc.BusinessDay())
	if err != nil {
		t.Fatalf("get briefing: %v", err)
	}
	if len(b.Events) != len(today) {
		t.Fatalf("briefing has %d events, want %d (== Today)", len(b.Events), len(today))
	}
	for i, e := range b.Events {
		if e.EventID != today[i].Event.ID {
			t.Fatalf("briefing[%d] id = %v, Today = %v — order/ids diverged (CHAT-010 breach)", i, e.EventID, today[i].Event.ID)
		}
		if int(e.Rank) != today[i].Rank {
			t.Fatalf("briefing[%d] rank = %d, Today rank = %d", i, e.Rank, today[i].Rank)
		}
	}

	// Idempotent per business day: a re-run creates nothing and leaves one briefing.
	created2, err := bsvc.GenerateForAccount(ctx, account)
	if err != nil {
		t.Fatalf("regenerate: %v", err)
	}
	if created2 {
		t.Fatal("second same-day generation must be a no-op (idempotent per business day)")
	}
	b2, err := bsvc.Get(ctx, account, bsvc.BusinessDay())
	if err != nil {
		t.Fatalf("get after regen: %v", err)
	}
	if len(b2.Events) != len(today) {
		t.Fatalf("after regen briefing has %d events, want %d (no duplication)", len(b2.Events), len(today))
	}
}
