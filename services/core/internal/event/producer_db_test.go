package event_test

import (
	"context"
	"strconv"
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

// appendObservation writes one append-only in-stock observation row for a
// competitor offer identity with a raw (quarantined) price token.
func appendObservation(t *testing.T, q *db.Queries, account, target uuid.UUID, nv int64, offer, rawValue string, at time.Time) {
	t.Helper()
	appendObsAvail(t, q, account, target, nv, offer, rawValue, "in_stock", at)
}

// appendObsAvail writes one append-only observation row with an explicit
// availability status (so a test can seed a would-be suppression transition).
func appendObsAvail(t *testing.T, q *db.Queries, account, target uuid.UUID, nv int64, offer, rawValue, avail string, at time.Time) {
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
		AvailabilityStatus:   avail,
		Quality:              "supported",
		FreshnessDeadline:    at.Add(6 * time.Hour),
		DedupKey:             offer + ":" + rawValue + ":" + avail + ":" + at.Format(time.RFC3339Nano),
		SchemaValid:          true,
		IdentityValid:        true,
		Confidence:           "partially_verified",
		ParsingWarnings:      []byte("[]"),
	})
	if err != nil {
		t.Fatalf("insert observation: %v", err)
	}
}

// ownedSellerID returns the account's AUTHORITATIVE owned DK seller identity
// (owned_seller_id, issue #212) — the validated decimal Seller.ID an owned offer
// observation carries as native_seller_id. The owned-offer exclusion compares
// against this, not the free-form native_account_id handle.
func ownedSellerID(t *testing.T, q *db.Queries, account uuid.UUID) string {
	t.Helper()
	acct, err := q.GetMarketplaceAccount(context.Background(), account)
	if err != nil {
		t.Fatalf("get account: %v", err)
	}
	return acct.OwnedSellerID.String
}

// transitionsForTarget filters a source's transitions down to those for one target.
func transitionsForTarget(all []event.Transition, target uuid.UUID) []event.Transition {
	var out []event.Transition
	for _, tr := range all {
		if tr.CompetitorPrice != nil && tr.CompetitorPrice.Target == target {
			out = append(out, tr)
		}
	}
	return out
}

// TestObservationSourceExcludesOwnedOffer is the BLOCKER 2 correctness proof: the
// account's OWN offer, if observed (Route C emits every matching-variant offer,
// unfiltered — observer.go buildCaptures), must NEVER drive a competitor_price
// event (EVT-001 type-2 is a COMPETITOR price movement). A genuine competitor with
// the same movement still fires.
func TestObservationSourceExcludesOwnedOffer(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account, _, target, nv := seedTarget(t, pool, q)
	owned := ownedSellerID(t, q, account)

	base := time.Now().UTC().Add(-30 * time.Minute)
	// The account's OWN offer moved price — must NOT become a competitor event.
	appendObservation(t, q, account, target, nv, owned, "1000000", base)
	appendObservation(t, q, account, target, nv, owned, "1300000", base.Add(5*time.Minute))
	// A genuine competitor moved price — MUST become a competitor event.
	appendObservation(t, q, account, target, nv, "rival-1", "2000000", base.Add(time.Minute))
	appendObservation(t, q, account, target, nv, "rival-1", "2600000", base.Add(6*time.Minute))

	all, err := event.NewObservationSource(pool).Transitions(ctx)
	if err != nil {
		t.Fatalf("transitions: %v", err)
	}
	got := transitionsForTarget(all, target)
	if len(got) != 1 {
		t.Fatalf("want exactly 1 competitor transition (owned excluded), got %d: %+v", len(got), got)
	}
	if got[0].CompetitorPrice.OfferIdentity == owned {
		t.Fatalf("the account's OWN offer must never drive a competitor_price event")
	}
	if got[0].CompetitorPrice.OfferIdentity != "rival-1" {
		t.Fatalf("competitor transition offer = %q, want rival-1", got[0].CompetitorPrice.OfferIdentity)
	}
}

// TestObservationSourceEmitsOnlyCompetitorPrice is the BLOCKER 1 fail-closed proof
// for the explicitly-planned stubs: even when the seeded data WOULD trigger a
// winning-state / seller-count / suppression-boundary / contribution-floor signal,
// the source emits ONLY competitor-price transitions and ZERO of the four dormant
// types (they are wired dormant pending their downstream prerequisites).
func TestObservationSourceEmitsOnlyCompetitorPrice(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account, _, target, nv := seedTarget(t, pool, q)

	base := time.Now().UTC().Add(-30 * time.Minute)
	// A would-be suppression/boundary transition on the owned offer: in_stock → unavailable.
	owned := ownedSellerID(t, q, account)
	appendObsAvail(t, q, account, target, nv, owned, "1000000", "in_stock", base)
	appendObsAvail(t, q, account, target, nv, owned, "1000000", "unavailable", base.Add(4*time.Minute))
	// A would-be seller-count increase: three distinct competing sellers appear.
	appendObservation(t, q, account, target, nv, "rival-1", "2000000", base.Add(time.Minute))
	appendObservation(t, q, account, target, nv, "rival-2", "2100000", base.Add(2*time.Minute))
	appendObservation(t, q, account, target, nv, "rival-3", "2200000", base.Add(3*time.Minute))
	// A genuine competitor price movement (the ONE leg that is sourced).
	appendObservation(t, q, account, target, nv, "rival-1", "2600000", base.Add(7*time.Minute))

	all, err := event.NewObservationSource(pool).Transitions(ctx)
	if err != nil {
		t.Fatalf("transitions: %v", err)
	}
	dormant := map[event.Type]int{}
	competitor := 0
	for _, tr := range all {
		// Scope to this target's transitions.
		if tr.CompetitorPrice != nil && tr.CompetitorPrice.Target != target {
			continue
		}
		switch tr.Type {
		case event.TypeCompetitorPrice:
			if tr.CompetitorPrice != nil && tr.CompetitorPrice.Target == target {
				competitor++
			}
		case event.TypeWinningState, event.TypeSellerCount,
			event.TypeSuppressionBoundary, event.TypeContributionFloor:
			dormant[tr.Type]++
		}
	}
	for _, ty := range []event.Type{
		event.TypeWinningState, event.TypeSellerCount,
		event.TypeSuppressionBoundary, event.TypeContributionFloor,
	} {
		if dormant[ty] != 0 {
			t.Errorf("dormant leg %s must emit ZERO transitions from the source, got %d", ty, dormant[ty])
		}
	}
	if competitor < 1 {
		t.Fatalf("the sourced competitor-price leg must still emit for rival-1, got %d", competitor)
	}
}

// TestOwnedSellerIdentityContract PINS the corrected owned-vs-competitor identity
// contract (issue #212). The owned-offer exclusion now compares an observation's
// native_seller_id against the account's AUTHORITATIVE, validated owned_seller_id
// (the decimal DK Seller.ID bound by provisioning/sync), NOT the free-form
// native_account_id handle. Route C writes native_seller_id as strconv.FormatInt of
// the DK Seller.ID (routec/parser.go), so the exclusion is exact when owned_seller_id
// is populated.
//
// This proves BOTH directions: (A) owned_seller_id bound -> the owned offer is
// EXCLUDED and a genuine competitor still fires; (B) owned_seller_id ABSENT/empty ->
// the account's owned identity is unresolved, so the source FAILS CLOSED (quarantines
// the account and emits ZERO competitor transitions) instead of leaking the owned
// price change as a spurious competitor movement. Fail-closed is the never-cut
// behavior (quarantine-over-inference, §4.6) — the previous silent leak is fixed.
func TestOwnedSellerIdentityContract(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()

	// ---- Scenario A: owned_seller_id BOUND -> owned offer EXCLUDED. ----
	accountA, _, targetA, nvA := seedTarget(t, pool, q)
	ownedDecimal := ownedSellerID(t, q, accountA) // decimal Seller.ID provisioned by the seed
	if _, err := strconv.ParseInt(ownedDecimal, 10, 64); err != nil {
		t.Fatalf("precondition: seeded owned_seller_id %q must be a decimal Seller.ID", ownedDecimal)
	}

	baseA := time.Now().UTC().Add(-30 * time.Minute)
	// Owned offer, as Route C emits it (native_seller_id = decimal Seller.ID), moves price.
	appendObservation(t, q, accountA, targetA, nvA, ownedDecimal, "1000000", baseA)
	appendObservation(t, q, accountA, targetA, nvA, ownedDecimal, "1300000", baseA.Add(5*time.Minute))
	// A genuine competitor moves price and MUST fire.
	appendObservation(t, q, accountA, targetA, nvA, "rival-1", "2000000", baseA.Add(time.Minute))
	appendObservation(t, q, accountA, targetA, nvA, "rival-1", "2600000", baseA.Add(6*time.Minute))

	allA, err := event.NewObservationSource(pool).Transitions(ctx)
	if err != nil {
		t.Fatalf("transitions A: %v", err)
	}
	gotA := transitionsForTarget(allA, targetA)
	if len(gotA) != 1 {
		t.Fatalf("owned_seller_id bound: want exactly 1 competitor transition (owned excluded), got %d: %+v", len(gotA), gotA)
	}
	if gotA[0].CompetitorPrice.OfferIdentity == ownedDecimal {
		t.Fatalf("owned_seller_id bound: the owned decimal Seller.ID must be excluded, not surfaced as a competitor")
	}

	// ---- Scenario B: owned_seller_id ABSENT -> fail closed (account quarantined). ----
	accountB, _, targetB, nvB := seedTarget(t, pool, q)
	// Clear the authoritative identity: an unresolved owned seller.
	setOwnedSeller(t, q, accountB, "")
	if got := ownedSellerID(t, q, accountB); got != "" {
		t.Fatalf("precondition: owned_seller_id must be cleared, got %q", got)
	}
	// Even a distinct competitor is present — but the whole account must fail closed
	// because the owned identity is unresolved (an owned offer could masquerade as a
	// competitor and there is no safe way to distinguish it).
	ownedDecimalB := strconv.FormatInt(int64(uuid.New().ID()), 10)
	baseB := time.Now().UTC().Add(-30 * time.Minute)
	appendObservation(t, q, accountB, targetB, nvB, ownedDecimalB, "1000000", baseB)
	appendObservation(t, q, accountB, targetB, nvB, ownedDecimalB, "1300000", baseB.Add(5*time.Minute))
	appendObservation(t, q, accountB, targetB, nvB, "rival-9", "2000000", baseB.Add(time.Minute))
	appendObservation(t, q, accountB, targetB, nvB, "rival-9", "2600000", baseB.Add(6*time.Minute))

	allB, err := event.NewObservationSource(pool).Transitions(ctx)
	if err != nil {
		t.Fatalf("transitions B: %v", err)
	}
	gotB := transitionsForTarget(allB, targetB)
	if len(gotB) != 0 {
		t.Fatalf("owned_seller_id absent: the account must FAIL CLOSED and emit ZERO competitor transitions "+
			"(quarantine-over-inference), got %d: %+v", len(gotB), gotB)
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
