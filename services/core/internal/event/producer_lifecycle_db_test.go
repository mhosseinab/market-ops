package event_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mhosseinab/market-ops/services/core/internal/cost"
	"github.com/mhosseinab/market-ops/services/core/internal/event"
	"github.com/mhosseinab/market-ops/services/core/internal/money"
)

// lifecycleDedupKey mirrors the domain's dedup identity contract exactly
// ("<type>:<variant>" with an optional ":<scope>" suffix) so a test can open an
// event whose key matches the transition the producer will clear.
func lifecycleDedupKey(ty event.Type, variant uuid.UUID, scope string) string {
	key := string(ty) + ":" + variant.String()
	if scope != "" {
		key += ":" + scope
	}
	return key
}

// openLifecycleEvent records one open event of a given type/scope directly (no
// source), so a lifecycle test can then drive the producer's expiry sweep or
// condition-clear against a known open row.
func openLifecycleEvent(t *testing.T, svc *event.Service, account, variant, target uuid.UUID, ty event.Type, scope string, expiresAt time.Time) string {
	t.Helper()
	key := lifecycleDedupKey(ty, variant, scope)
	cand := event.Candidate{
		Type:       ty,
		Variant:    variant,
		Target:     target,
		DedupKey:   key,
		Severity:   event.SeverityWarning,
		Exposure:   event.UnknownExposure(),
		Confidence: money.NewBasisPoints(8000),
		Urgency:    money.NewBasisPoints(6000),
		Evidence:   event.Evidence{Quality: event.QualitySupported, Ref: "fixture://evt-lifecycle"},
		DetectedAt: expiresAt.Add(-time.Hour),
		ExpiresAt:  expiresAt,
	}
	if _, err := svc.RecordFor(context.Background(), account, cand); err != nil {
		t.Fatalf("open %s event: %v", ty, err)
	}
	return key
}

func eventStateByKey(t *testing.T, pool *pgxpool.Pool, key string) string {
	t.Helper()
	var state string
	if err := pool.QueryRow(context.Background(),
		`SELECT state FROM market_events WHERE dedup_key=$1 ORDER BY updated_at DESC LIMIT 1`, key).Scan(&state); err != nil {
		t.Fatalf("query state for %q: %v", key, err)
	}
	return state
}

func countByKey(t *testing.T, pool *pgxpool.Pool, key string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM market_events WHERE dedup_key=$1`, key).Scan(&n); err != nil {
		t.Fatalf("count for %q: %v", key, err)
	}
	return n
}

// TestProducerExpirySweepExcludesFromTodayAndIsMonotonic is the issue #66 expiry
// acceptance test: the FIRST producer RUN at/after expires_at excludes the event
// from Today WITHOUT any test calling ExpireStale directly, actually transitions the
// row to 'expired' (freeing the dedup key — a read-time filter alone would not), and
// a repeated sweep is a monotonic no-op that never resurrects the row.
func TestProducerExpirySweepExcludesFromTodayAndIsMonotonic(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account, variant, target, _ := seedTarget(t, pool, q)
	svc := event.NewService(pool)

	base := time.Now().UTC()
	key := openLifecycleEvent(t, svc, account, variant, target, event.TypeCompetitorPrice, "rival-expire", base.Add(time.Minute))

	// Before expiry the event is actionable on Today.
	if feed, err := svc.Today(ctx, account); err != nil || len(feed) != 1 {
		t.Fatalf("pre-expiry Today must show the event: len=%d err=%v", len(feed), err)
	}

	// A producer whose clock is PAST the deadline. Its source yields nothing — the
	// exclusion must come from the durable sweep the RUN performs, not a direct call.
	past := base.Add(2 * time.Minute)
	prod := event.NewProducer(svc, staticSource{}, nil).WithClock(func() time.Time { return past })

	m, err := prod.RunOnce(ctx)
	if err != nil {
		t.Fatalf("run (sweep): %v", err)
	}
	if m.Expired < 1 {
		t.Fatalf("run at/after expiry must sweep the event, got expired=%d", m.Expired)
	}
	if feed, _ := svc.Today(ctx, account); len(feed) != 0 {
		t.Fatalf("an expired event must leave Today, still shows %d", len(feed))
	}
	if s := eventStateByKey(t, pool, key); s != "expired" {
		t.Fatalf("row must transition to 'expired' (freeing the key), got %q", s)
	}

	// Monotonic + idempotent: a second sweep with nothing newly due is a no-op and
	// the terminal row stays expired.
	m2, err := prod.RunOnce(ctx)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if m2.Expired != 0 {
		t.Fatalf("a repeated sweep with nothing due must expire 0, got %d", m2.Expired)
	}
	if s := eventStateByKey(t, pool, key); s != "expired" {
		t.Fatalf("a repeated sweep must never resurrect: state=%q", s)
	}
}

// TestProducerConditionClearResolvesAndIsIdempotent is the issue #66 condition-clear
// acceptance test on the sourced (competitor-price) leg: a threshold-in-force
// movement that is no longer material clears the open event to 'resolved', and a
// replayed clearance is a monotonic no-op.
func TestProducerConditionClearResolvesAndIsIdempotent(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account, variant, target, _ := seedTarget(t, pool, q)
	svc := event.NewService(pool)

	if _, err := svc.SetThreshold(ctx, event.ThresholdParams{
		Account: account, Category: "*", Type: event.TypeCompetitorPrice, Version: 1,
		MoveBp: money.NewBasisPoints(1000), EffectiveFrom: time.Now().UTC().Add(-time.Hour),
	}); err != nil {
		t.Fatalf("set threshold: %v", err)
	}

	base := time.Now().UTC()
	offer := "rival-clear"
	key := openLifecycleEvent(t, svc, account, variant, target, event.TypeCompetitorPrice, offer, base.Add(24*time.Hour))
	if feed, _ := svc.Today(ctx, account); len(feed) != 1 {
		t.Fatalf("pre-clear Today must show the open event, got %d", len(feed))
	}

	// A now-immaterial movement (50bp against a 1000bp threshold) — the condition
	// cleared. Source emits the dormant transition for the SAME offer identity.
	clearTr := event.Transition{
		Account: account, Category: "*", Type: event.TypeCompetitorPrice,
		CompetitorPrice: &event.CompetitorPriceInput{
			Variant: variant, Target: target, OfferIdentity: offer, Unit: "IRR",
			PrevValue: "1000000", CurrValue: "1005000",
			Exposure: event.UnknownExposure(),
			Evidence: event.Evidence{Quality: event.QualitySupported, Ref: "r"},
			Now:      base, TTL: 24 * time.Hour,
		},
	}
	prod := event.NewProducer(svc, staticSource{transitions: []event.Transition{clearTr}}, nil)

	m, err := prod.RunOnce(ctx)
	if err != nil {
		t.Fatalf("clear pass: %v", err)
	}
	if m.Resolved < 1 {
		t.Fatalf("a cleared condition must resolve the open event, got resolved=%d dormant=%d", m.Resolved, m.Dormant)
	}
	if feed, _ := svc.Today(ctx, account); len(feed) != 0 {
		t.Fatalf("a resolved event must leave Today, still shows %d", len(feed))
	}
	if s := eventStateByKey(t, pool, key); s != "resolved" {
		t.Fatalf("row must be 'resolved', got %q", s)
	}

	// Replay: the same clearance again is a monotonic no-op (nothing open), never a
	// second resolve, never a resurrection.
	m2, err := prod.RunOnce(ctx)
	if err != nil {
		t.Fatalf("replay pass: %v", err)
	}
	if m2.Resolved != 0 {
		t.Fatalf("a replayed clearance must resolve 0, got %d", m2.Resolved)
	}
	if s := eventStateByKey(t, pool, key); s != "resolved" {
		t.Fatalf("a replayed clearance must never resurrect: state=%q", s)
	}
}

// TestProducerResolvedRowAllowsNewOccurrence is the issue #66 dedup-key-release
// acceptance test: once an event is resolved (or expired) the dedup key is free, so
// a genuinely new occurrence opens a NEW row while the terminal row stays terminal.
func TestProducerResolvedRowAllowsNewOccurrence(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account, variant, target, _ := seedTarget(t, pool, q)
	svc := event.NewService(pool)

	if _, err := svc.SetThreshold(ctx, event.ThresholdParams{
		Account: account, Category: "*", Type: event.TypeCompetitorPrice, Version: 1,
		MoveBp: money.NewBasisPoints(1000), EffectiveFrom: time.Now().UTC().Add(-time.Hour),
	}); err != nil {
		t.Fatalf("set threshold: %v", err)
	}

	base := time.Now().UTC()
	offer := "rival-reopen"
	key := openLifecycleEvent(t, svc, account, variant, target, event.TypeCompetitorPrice, offer, base.Add(24*time.Hour))

	origID := eventIDByKeyState(t, pool, key, "open")

	// Clear it (dormant movement) -> resolved.
	clearTr := event.Transition{
		Account: account, Category: "*", Type: event.TypeCompetitorPrice,
		CompetitorPrice: &event.CompetitorPriceInput{
			Variant: variant, Target: target, OfferIdentity: offer, Unit: "IRR",
			PrevValue: "1000000", CurrValue: "1005000",
			Exposure: event.UnknownExposure(),
			Evidence: event.Evidence{Quality: event.QualitySupported, Ref: "r"},
			Now:      base, TTL: 24 * time.Hour,
		},
	}
	if _, err := event.NewProducer(svc, staticSource{transitions: []event.Transition{clearTr}}, nil).RunOnce(ctx); err != nil {
		t.Fatalf("clear pass: %v", err)
	}
	if s := eventStateByKey(t, pool, key); s != "resolved" {
		t.Fatalf("precondition: row must be resolved, got %q", s)
	}

	// A genuinely NEW occurrence: a material 30% movement for the same identity.
	fireTr := event.Transition{
		Account: account, Category: "*", Type: event.TypeCompetitorPrice,
		CompetitorPrice: &event.CompetitorPriceInput{
			Variant: variant, Target: target, OfferIdentity: offer, Unit: "IRR",
			PrevValue: "1000000", CurrValue: "1300000",
			Exposure: event.UnknownExposure(),
			Evidence: event.Evidence{Quality: event.QualitySupported, Ref: "r"},
			Now:      base.Add(time.Hour), TTL: 24 * time.Hour,
		},
	}
	m, err := event.NewProducer(svc, staticSource{transitions: []event.Transition{fireTr}}, nil).RunOnce(ctx)
	if err != nil {
		t.Fatalf("reopen pass: %v", err)
	}
	if m.Produced < 1 {
		t.Fatalf("a freed dedup key must let a new occurrence OPEN a new row, got produced=%d deduped=%d", m.Produced, m.Deduped)
	}

	// Two rows on the key: the terminal (resolved) original + a fresh open row.
	if n := countByKey(t, pool, key); n != 2 {
		t.Fatalf("want exactly 2 rows on the dedup key (1 resolved + 1 new open), got %d", n)
	}
	newID := eventIDByKeyState(t, pool, key, "open")
	if newID == origID {
		t.Fatalf("the new occurrence must be a NEW row, not the resurrected original")
	}
	if feed, _ := svc.Today(ctx, account); len(feed) != 1 {
		t.Fatalf("Today must show exactly the new open event, got %d", len(feed))
	}
}

// TestProducerConditionClearPerDetectorType is the issue #66 per-type acceptance
// test: EACH of the five detector types has a clear-condition fixture that reaches
// 'resolved'. One open event per type, one pass feeding a dormant transition per
// type, all five resolve.
func TestProducerConditionClearPerDetectorType(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account, variant, target, _ := seedTarget(t, pool, q)
	svc := event.NewService(pool)

	base := time.Now().UTC()
	ev := event.Evidence{Quality: event.QualitySupported, Ref: "r"}
	longExpiry := base.Add(24 * time.Hour)

	// Open one event per type (competitor_price carries an offer scope).
	keys := map[event.Type]string{
		event.TypeWinningState:        openLifecycleEvent(t, svc, account, variant, target, event.TypeWinningState, "", longExpiry),
		event.TypeCompetitorPrice:     openLifecycleEvent(t, svc, account, variant, target, event.TypeCompetitorPrice, "offer-x", longExpiry),
		event.TypeSellerCount:         openLifecycleEvent(t, svc, account, variant, target, event.TypeSellerCount, "", longExpiry),
		event.TypeSuppressionBoundary: openLifecycleEvent(t, svc, account, variant, target, event.TypeSuppressionBoundary, "", longExpiry),
		event.TypeContributionFloor:   openLifecycleEvent(t, svc, account, variant, target, event.TypeContributionFloor, "", longExpiry),
	}

	// A DORMANT (condition-cleared) transition for each type: steady/immaterial state
	// so the detector does not fire and the producer resolves the matching open row.
	clears := []event.Transition{
		{Account: account, Category: "*", Type: event.TypeWinningState, WinningState: &event.WinningStateInput{
			Variant: variant, Target: target, WasWinning: false, IsWinning: false,
			Exposure: event.UnknownExposure(), Evidence: ev, Now: base, TTL: 24 * time.Hour,
		}},
		{Account: account, Category: "*", Type: event.TypeCompetitorPrice, CompetitorPrice: &event.CompetitorPriceInput{
			Variant: variant, Target: target, OfferIdentity: "offer-x", Unit: "IRR",
			PrevValue: "1000000", CurrValue: "1000000", // no movement — not material
			Exposure: event.UnknownExposure(), Evidence: ev, Now: base, TTL: 24 * time.Hour,
		}},
		{Account: account, Category: "*", Type: event.TypeSellerCount, SellerCount: &event.SellerCountInput{
			Variant: variant, Target: target, PrevCount: 3, CurrCount: 3, // no delta — not material
			Exposure: event.UnknownExposure(), Evidence: ev, Now: base, TTL: 24 * time.Hour,
		}},
		{Account: account, Category: "*", Type: event.TypeSuppressionBoundary, SuppressionBoundary: &event.SuppressionBoundaryInput{
			Variant: variant, Target: target, WasSuppressed: false, IsSuppressed: false, BoundaryChanged: false,
			Exposure: event.UnknownExposure(), Evidence: ev, Now: base, TTL: 24 * time.Hour,
		}},
		{Account: account, Category: "*", Type: event.TypeContributionFloor, ContributionFloor: &event.ContributionFloorInput{
			Variant: variant, Target: target, Readiness: cost.StateMissing, HasContribution: false,
			Evidence: ev, Now: base, TTL: 24 * time.Hour,
		}},
	}

	m, err := event.NewProducer(svc, staticSource{transitions: clears}, nil).RunOnce(ctx)
	if err != nil {
		t.Fatalf("per-type clear pass: %v", err)
	}
	if m.Resolved != 5 {
		t.Fatalf("every detector type's clear-condition fixture must resolve, got resolved=%d dormant=%d", m.Resolved, m.Dormant)
	}
	for ty, key := range keys {
		if s := eventStateByKey(t, pool, key); s != "resolved" {
			t.Errorf("type %s clear-condition fixture must reach 'resolved', got %q", ty, s)
		}
	}
	if feed, _ := svc.Today(ctx, account); len(feed) != 0 {
		t.Fatalf("all five events resolved must leave Today empty, got %d", len(feed))
	}
}

func eventIDByKeyState(t *testing.T, pool *pgxpool.Pool, key, state string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(context.Background(),
		`SELECT id FROM market_events WHERE dedup_key=$1 AND state=$2 ORDER BY updated_at DESC LIMIT 1`, key, state).Scan(&id); err != nil {
		t.Fatalf("id for %q state %q: %v", key, state, err)
	}
	return id
}
