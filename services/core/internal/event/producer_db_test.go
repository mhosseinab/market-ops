package event_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/event"
	"github.com/mhosseinab/market-ops/services/core/internal/money"
)

// seedTarget creates org+account+product+variant+confirmed identity+observation
// target and returns the ids the producer's ObservationSource walks.
func seedTarget(t *testing.T, pool *pgxpool.Pool, q *db.Queries) (account, variant, target uuid.UUID, nativeVariant int64) {
	t.Helper()
	ctx := context.Background()
	account, variant = seedVariant(t, q)
	// native ids denormalised onto the target/observations.
	nativeVariant = int64(uuid.New().ID())
	nativeProduct := int64(uuid.New().ID())

	var identityID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO market_product_identities
		    (marketplace_account_id, variant_id, native_variant_id, native_product_id, state, active)
		VALUES ($1,$2,$3,$4,'confirmed',true)
		RETURNING id`, account, variant, nativeVariant, nativeProduct).Scan(&identityID); err != nil {
		t.Fatalf("insert confirmed identity: %v", err)
	}
	tgt, err := q.InsertObservationTarget(ctx, db.InsertObservationTargetParams{
		MarketplaceAccountID:     account,
		IdentityID:               identityID,
		VariantID:                variant,
		NativeVariantID:          nativeVariant,
		NativeProductID:          nativeProduct,
		Tier:                     "standard",
		CadenceSeconds:           3600,
		FreshnessDeadlineSeconds: 21600,
	})
	if err != nil {
		t.Fatalf("insert target: %v", err)
	}
	return account, variant, tgt.ID, nativeVariant
}

// appendObservation writes one append-only observation row for a competitor offer
// identity with a raw (quarantined) price token.
func appendObservation(t *testing.T, q *db.Queries, account, target uuid.UUID, nv int64, offer, rawValue string, at time.Time) {
	t.Helper()
	_, err := q.InsertObservation(context.Background(), db.InsertObservationParams{
		CapturedAt:           at,
		TargetID:             target,
		MarketplaceAccountID: account,
		NativeVariantID:      nv,
		NativeSellerID:       offer,
		OfferIdentity:        offer,
		Route:                "route_c",
		ParserVersion:        "p1",
		SourceType:           "public-web-endpoint",
		EvidenceRef:          "fixture://evt-prod",
		PriceRawText:         rawValue + " IRR",
		PriceRawValue:        rawValue,
		PriceRawUnit:         "IRR",
		AvailabilityStatus:   "in_stock",
		Quality:              "supported",
		FreshnessDeadline:    at.Add(6 * time.Hour),
		DedupKey:             offer + ":" + rawValue + ":" + at.Format(time.RFC3339Nano),
		SchemaValid:          true,
		IdentityValid:        true,
		Confidence:           "partially_verified",
		ParsingWarnings:      []byte("[]"),
	})
	if err != nil {
		t.Fatalf("insert observation: %v", err)
	}
}

// TestObservationSourceProducesCompetitorPriceEvent is the full cross-boundary
// acceptance test: committed observations → ObservationSource → producer →
// detector → market_events row → Today response, WITHOUT a direct RecordFor call.
func TestObservationSourceProducesCompetitorPriceEvent(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account, _, target, nv := seedTarget(t, pool, q)
	svc := event.NewService(pool)

	// A competitor-price materiality threshold must be in force (EVT-002).
	if _, err := svc.SetThreshold(ctx, event.ThresholdParams{
		Account: account, Category: "*", Type: event.TypeCompetitorPrice, Version: 1,
		MoveBp: money.NewBasisPoints(1000), EffectiveFrom: time.Now().UTC().Add(-time.Hour),
	}); err != nil {
		t.Fatalf("set threshold: %v", err)
	}

	base := time.Now().UTC().Add(-30 * time.Minute)
	appendObservation(t, q, account, target, nv, "seller-1", "1000000", base)
	appendObservation(t, q, account, target, nv, "seller-1", "1300000", base.Add(10*time.Minute))

	producer := event.NewProducer(svc, event.NewObservationSource(pool), nil)
	m, err := producer.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	// Global metrics may include other accounts sharing this test DB; the
	// account-scoped Today assertion below is the precise guarantee.
	if m.Produced < 1 {
		t.Fatalf("want >=1 produced competitor-price event, got produced=%d scanned=%d dormant=%d errors=%d",
			m.Produced, m.Scanned, m.Dormant, m.Errors)
	}

	feed, err := svc.Today(ctx, account)
	if err != nil {
		t.Fatalf("today: %v", err)
	}
	if len(feed) != 1 {
		t.Fatalf("Today must surface exactly the produced event, got %d", len(feed))
	}
	if feed[0].Event.EventType != string(event.TypeCompetitorPrice) {
		t.Fatalf("Today item type = %s, want competitor_price", feed[0].Event.EventType)
	}
}

// TestObservationSourceReplayNoDuplicate is the durability/idempotency never-cut:
// a simulated restart (a fresh source+producer over the SAME committed data)
// re-derives the transition but produces ZERO duplicate Today items (EVT-003).
func TestObservationSourceReplayNoDuplicate(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account, variant, target, nv := seedTarget(t, pool, q)
	svc := event.NewService(pool)
	if _, err := svc.SetThreshold(ctx, event.ThresholdParams{
		Account: account, Category: "*", Type: event.TypeCompetitorPrice, Version: 1,
		MoveBp: money.NewBasisPoints(1000), EffectiveFrom: time.Now().UTC().Add(-time.Hour),
	}); err != nil {
		t.Fatalf("set threshold: %v", err)
	}
	base := time.Now().UTC().Add(-30 * time.Minute)
	appendObservation(t, q, account, target, nv, "seller-1", "1000000", base)
	appendObservation(t, q, account, target, nv, "seller-1", "1300000", base.Add(10*time.Minute))

	// Pass A: first producer instance opens the event (metrics may include other
	// accounts in the shared DB; the variant-scoped row count below is precise).
	first, err := event.NewProducer(svc, event.NewObservationSource(pool), nil).RunOnce(ctx)
	if err != nil || first.Produced < 1 {
		t.Fatalf("pass A: produced=%d err=%v", first.Produced, err)
	}
	// Pass B: a NEW producer instance (simulated restart) over the same durable
	// observations must NOT lose the committed input and must NOT open a NEW event
	// — every restart replay is idempotent through RecordFor dedup.
	second, err := event.NewProducer(svc, event.NewObservationSource(pool), nil).RunOnce(ctx)
	if err != nil {
		t.Fatalf("pass B: %v", err)
	}
	if second.Produced != 0 {
		t.Fatalf("restart replay must open ZERO new events, got produced=%d", second.Produced)
	}

	var rows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM market_events WHERE variant_id=$1`, variant).Scan(&rows); err != nil {
		t.Fatalf("count: %v", err)
	}
	if rows != 1 {
		t.Fatalf("a restart replay must produce ZERO new events rows; found %d", rows)
	}
	feed, _ := svc.Today(ctx, account)
	if len(feed) != 1 {
		t.Fatalf("Today must still show exactly one item after replay, got %d", len(feed))
	}
}
