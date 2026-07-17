package event_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mhosseinab/market-ops/services/core/internal/cost"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/event"
	"github.com/mhosseinab/market-ops/services/core/internal/money"
)

// newPool connects to DATABASE_URL (schema applied via `task db:reset`). Skips
// when unset so the suite still runs where no Postgres is provisioned.
func newPool(t *testing.T) (*pgxpool.Pool, *db.Queries) {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set; skipping event DB test")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("connect pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool, db.New(pool)
}

// seedVariant creates org+account+product+variant with fresh native ids.
func seedVariant(t *testing.T, q *db.Queries) (account, variant uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	org, err := q.CreateOrganization(ctx, "evt-test-"+uuid.NewString())
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	acct, err := q.CreateMarketplaceAccount(ctx, db.CreateMarketplaceAccountParams{
		OrganizationID:  org.ID,
		NativeAccountID: "native-" + uuid.NewString(),
		DisplayName:     "Evt Seller",
	})
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
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
		Title:                "Widget - Red",
	})
	if err != nil {
		t.Fatalf("upsert variant: %v", err)
	}
	return acct.ID, v.ID
}

func irrDB(t *testing.T, mantissa int64) money.Money {
	t.Helper()
	m, err := money.New(mantissa, "IRR", 0)
	if err != nil {
		t.Fatalf("money: %v", err)
	}
	return m
}

// TestDedupUpdatesOpenRecordZeroDuplicates is the EVT-003 / §16 never-cut test:
// a repeated detection UPDATES the one open event and creates ZERO duplicate
// Today items. It asserts the structural guarantee end to end through the service.
func TestDedupUpdatesOpenRecordZeroDuplicates(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account, variant := seedVariant(t, q)
	svc := event.NewService(pool)
	now := time.Now().UTC()

	first, _ := event.DetectWinningState(event.WinningStateInput{
		Variant: variant, WasWinning: true, IsWinning: false,
		Exposure: event.UnknownExposure(),
		Evidence: event.Evidence{Quality: event.QualitySupported, Ref: "r1"},
		Now:      now, TTL: time.Hour,
	})
	r1, err := svc.RecordFor(ctx, account, first)
	if err != nil {
		t.Fatalf("record first: %v", err)
	}
	if r1.Deduped {
		t.Fatal("first detection must OPEN a new event, not dedup")
	}

	// A repeated detection of the SAME condition (same dedup key), later evidence.
	second, _ := event.DetectWinningState(event.WinningStateInput{
		Variant: variant, WasWinning: true, IsWinning: false,
		Exposure: event.UnknownExposure(),
		Evidence: event.Evidence{Quality: event.QualityVerified, Ref: "r2"},
		Now:      now.Add(10 * time.Minute), TTL: time.Hour,
	})
	r2, err := svc.RecordFor(ctx, account, second)
	if err != nil {
		t.Fatalf("record dup: %v", err)
	}
	if !r2.Deduped {
		t.Fatal("a repeated detection must DEDUP (update the open record)")
	}
	if r2.Event.ID != r1.Event.ID {
		t.Fatalf("dedup must update the SAME record: %v vs %v", r2.Event.ID, r1.Event.ID)
	}
	if r2.Event.EvidenceUpdateCount != 1 {
		t.Errorf("evidence_update_count = %d, want 1", r2.Event.EvidenceUpdateCount)
	}
	if r2.Event.EvidenceRef != "r2" || r2.Event.State != string(event.LifecycleUpdated) {
		t.Errorf("dedup must refresh evidence and mark 'updated': ref=%s state=%s", r2.Event.EvidenceRef, r2.Event.State)
	}

	// ZERO duplicate Today items: exactly one open event for the account.
	feed, err := svc.Today(ctx, account)
	if err != nil {
		t.Fatalf("today: %v", err)
	}
	if len(feed) != 1 {
		t.Fatalf("dedup must yield exactly ONE Today item, got %d", len(feed))
	}

	// A raw row count confirms no second events row was inserted.
	var rows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM market_events WHERE variant_id=$1`, variant).Scan(&rows); err != nil {
		t.Fatalf("count: %v", err)
	}
	if rows != 1 {
		t.Fatalf("a duplicate event must produce ZERO new events rows; found %d", rows)
	}
}

// TestUnknownExposurePersistsAsNull is the EVT-005 persistence negative test: an
// unknown exposure is stored with a NULL mantissa (the DB CHECK forbids a
// fabricated number) and never surfaces as 0.
func TestUnknownExposurePersistsAsNull(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account, variant := seedVariant(t, q)
	svc := event.NewService(pool)
	now := time.Now().UTC()

	cand, _ := event.DetectWinningState(event.WinningStateInput{
		Variant: variant, WasWinning: true, IsWinning: false,
		Exposure: event.UnknownExposure(),
		Evidence: event.Evidence{Quality: event.QualityUnverified, Ref: "r"},
		Now:      now, TTL: time.Hour,
	})
	res, err := svc.RecordFor(ctx, account, cand)
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	if res.Event.ExposureKnown {
		t.Fatal("unknown exposure must persist exposure_known=false")
	}
	if res.Event.ExposureMantissa.Valid {
		t.Fatal("unknown exposure must persist a NULL mantissa — never a fabricated number (EVT-005)")
	}
	feed, _ := svc.Today(ctx, account)
	if len(feed) != 1 || feed[0].Factors.ExposureKnown {
		t.Fatal("Today feed must expose the exposure as unknown")
	}
	if feed[0].Score != nil {
		t.Fatal("unknown-exposure event must have a nil composite score")
	}
}

// TestKnownExposureFloorEvent proves the contribution-floor detector (type 5)
// records a KNOWN Money exposure derived from margin, ranked with a real score.
func TestKnownExposureFloorEvent(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account, variant := seedVariant(t, q)
	svc := event.NewService(pool)
	now := time.Now().UTC()

	cand, ok, err := event.DetectContributionFloor(event.ContributionFloorInput{
		Variant: variant, Readiness: cost.StateComplete, HasContribution: true,
		Contribution: irrDB(t, 60), Floor: irrDB(t, 100),
		Evidence: event.Evidence{Quality: event.QualityVerified, Ref: "r"},
		Now:      now, TTL: time.Hour,
	})
	if err != nil || !ok {
		t.Fatalf("floor detect: ok=%v err=%v", ok, err)
	}
	res, err := svc.RecordFor(ctx, account, cand)
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	if !res.Event.ExposureKnown || !res.Event.ExposureMantissa.Valid || res.Event.ExposureMantissa.Int64 != 40 {
		t.Fatalf("floor exposure must be a known 40 shortfall, got known=%v mant=%v",
			res.Event.ExposureKnown, res.Event.ExposureMantissa)
	}
	feed, _ := svc.Today(ctx, account)
	if len(feed) != 1 || feed[0].Score == nil {
		t.Fatal("a known-exposure event must carry a composite score in the feed")
	}
}

// TestThresholdVersionReproducibility proves EVT-002: an event records the exact
// threshold version that fired it, resolvable point-in-time.
func TestThresholdVersionReproducibility(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account, variant := seedVariant(t, q)
	svc := event.NewService(pool)
	base := time.Now().UTC().Add(-24 * time.Hour)

	// Two versions of the competitor-price threshold, effective at different times.
	_, err := svc.SetThreshold(ctx, event.ThresholdParams{
		Account: account, Category: "*", Type: event.TypeCompetitorPrice, Version: 1,
		MoveBp: money.NewBasisPoints(1000), EffectiveFrom: base,
	})
	if err != nil {
		t.Fatalf("set v1: %v", err)
	}
	_, err = svc.SetThreshold(ctx, event.ThresholdParams{
		Account: account, Category: "*", Type: event.TypeCompetitorPrice, Version: 2,
		MoveBp: money.NewBasisPoints(500), EffectiveFrom: base.Add(12 * time.Hour),
	})
	if err != nil {
		t.Fatalf("set v2: %v", err)
	}

	// As of just after v1 (before v2), the in-force version is 1 (1000bp).
	early, err := svc.ThresholdAsOf(ctx, account, "*", event.TypeCompetitorPrice, base.Add(time.Hour))
	if err != nil {
		t.Fatalf("asof early: %v", err)
	}
	if early.Version != 1 || early.MoveBp.Value() != 1000 {
		t.Fatalf("early threshold = v%d/%dbp, want v1/1000bp", early.Version, early.MoveBp.Value())
	}
	// As of now, the in-force version is 2 (500bp).
	late, err := svc.ThresholdAsOf(ctx, account, "*", event.TypeCompetitorPrice, time.Now().UTC())
	if err != nil {
		t.Fatalf("asof late: %v", err)
	}
	if late.Version != 2 || late.MoveBp.Value() != 500 {
		t.Fatalf("late threshold = v%d, want v2/500bp", late.Version)
	}

	// An event that fired against the late version records exactly that version.
	cand, _ := event.DetectCompetitorPrice(event.CompetitorPriceInput{
		Variant: variant, OfferIdentity: "s1", Unit: "IRR",
		PrevValue: "1000000", CurrValue: "1060000",
		Exposure: event.UnknownExposure(),
		Evidence: event.Evidence{Quality: event.QualitySupported, Ref: "r"},
		Now:      time.Now().UTC(), TTL: time.Hour, Threshold: late,
	})
	res, err := svc.RecordFor(ctx, account, cand)
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	if !res.Event.ThresholdVersion.Valid || res.Event.ThresholdVersion.Int32 != 2 {
		t.Fatalf("event must reproduce threshold version 2, got %v", res.Event.ThresholdVersion)
	}
}

// TestResolveFreesDedupKey proves the lifecycle (§15.1): once resolved, a new
// occurrence of the same condition opens a FRESH event rather than deduping onto
// the closed one.
func TestResolveFreesDedupKey(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account, variant := seedVariant(t, q)
	svc := event.NewService(pool)
	now := time.Now().UTC()

	cand, _ := event.DetectWinningState(event.WinningStateInput{
		Variant: variant, WasWinning: true, IsWinning: false,
		Exposure: event.UnknownExposure(),
		Evidence: event.Evidence{Quality: event.QualitySupported, Ref: "r"},
		Now:      now, TTL: time.Hour,
	})
	r1, err := svc.RecordFor(ctx, account, cand)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := svc.Resolve(ctx, r1.Event.ID); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	// The same condition recurs — with the open key freed, this OPENS a new event.
	r2, err := svc.RecordFor(ctx, account, cand)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if r2.Deduped || r2.Event.ID == r1.Event.ID {
		t.Fatal("after resolution a recurrence must open a NEW event, not dedup onto the resolved one")
	}
}

// TestExpireSweep proves the §15.1 expiry lifecycle transition drops an event out
// of the Today feed.
func TestExpireSweep(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account, variant := seedVariant(t, q)
	past := time.Now().UTC().Add(-2 * time.Hour)
	svc := event.NewService(pool).WithClock(func() time.Time { return time.Now().UTC() })

	cand, _ := event.DetectWinningState(event.WinningStateInput{
		Variant: variant, WasWinning: true, IsWinning: false,
		Exposure: event.UnknownExposure(),
		Evidence: event.Evidence{Quality: event.QualitySupported, Ref: "r"},
		Now:      past, TTL: time.Hour, // expires 1h in the past
	})
	if _, err := svc.RecordFor(ctx, account, cand); err != nil {
		t.Fatalf("record: %v", err)
	}
	n, err := svc.ExpireStale(ctx, account)
	if err != nil {
		t.Fatalf("expire: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 event expired, got %d", n)
	}
	feed, _ := svc.Today(ctx, account)
	if len(feed) != 0 {
		t.Fatalf("expired event must leave the Today feed, got %d", len(feed))
	}
}

// TestRelevanceFeedbackAppendOnly proves EVT-005 feedback is stored and history is
// append-only (each vote is a new row).
func TestRelevanceFeedbackAppendOnly(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account, variant := seedVariant(t, q)
	svc := event.NewService(pool)
	now := time.Now().UTC()

	cand, _ := event.DetectWinningState(event.WinningStateInput{
		Variant: variant, WasWinning: true, IsWinning: false,
		Exposure: event.UnknownExposure(),
		Evidence: event.Evidence{Quality: event.QualitySupported, Ref: "r"},
		Now:      now, TTL: time.Hour,
	})
	res, err := svc.RecordFor(ctx, account, cand)
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	if _, err := svc.RecordRelevance(ctx, res.Event.ID, uuid.Nil, "not_relevant", "noise"); err != nil {
		t.Fatalf("feedback 1: %v", err)
	}
	if _, err := svc.RecordRelevance(ctx, res.Event.ID, uuid.Nil, "muted", ""); err != nil {
		t.Fatalf("feedback 2: %v", err)
	}
	hist, err := svc.ListRelevance(ctx, res.Event.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(hist) != 2 {
		t.Fatalf("append-only feedback: expected 2 rows, got %d", len(hist))
	}
}
