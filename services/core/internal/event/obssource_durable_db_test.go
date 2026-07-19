package event_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/event"
	"github.com/mhosseinab/market-ops/services/core/internal/money"
)

// appendObsSellerOffer writes one append-only in-stock observation with an EXPLICIT
// native_seller_id that may DIFFER from the offer_identity, so a test can seed two
// sellers sharing one offer identity (the cross-seller collision case, issue #212).
func appendObsSellerOffer(t *testing.T, q *db.Queries, account, target uuid.UUID, nv int64, seller, offer, rawValue string, at time.Time) {
	t.Helper()
	_, err := q.InsertObservation(context.Background(), db.InsertObservationParams{
		CapturedAt:           at,
		TargetID:             target,
		MarketplaceAccountID: account,
		NativeVariantID:      nv,
		NativeSellerID:       seller,
		OfferIdentity:        offer,
		Route:                "route_c",
		ParserVersion:        "p1",
		SourceType:           "public-web-endpoint",
		EvidenceRef:          "fixture://evt-durable",
		PriceRawText:         rawValue + " IRR",
		PriceRawValue:        rawValue,
		PriceRawUnit:         "IRR",
		AvailabilityStatus:   "in_stock",
		Quality:              "supported",
		FreshnessDeadline:    at.Add(6 * time.Hour),
		DedupKey:             seller + ":" + offer + ":" + rawValue + ":" + at.Format(time.RFC3339Nano),
		SchemaValid:          true,
		IdentityValid:        true,
		Confidence:           "partially_verified",
		ParsingWarnings:      []byte("[]"),
	})
	if err != nil {
		t.Fatalf("insert observation: %v", err)
	}
}

func competitorThreshold(t *testing.T, svc *event.Service, account uuid.UUID, moveBp int64) {
	t.Helper()
	if _, err := svc.SetThreshold(context.Background(), event.ThresholdParams{
		Account: account, Category: "*", Type: event.TypeCompetitorPrice, Version: 1,
		MoveBp: money.NewBasisPoints(moveBp), EffectiveFrom: time.Now().UTC().Add(-time.Hour),
	}); err != nil {
		t.Fatalf("set threshold: %v", err)
	}
}

// failFirstMaterialRecorder wraps the real event.Recorder and returns ONE transient
// error from RecordFor for the consumed candidate whose newest observation value is
// failValue, then delegates every subsequent call to the wrapped recorder. It models
// a transient DB fault (deadlock/timeout/conn reset) on a SINGLE material transition
// in a burst, so a test can prove the producer does not advance a stream past an
// errored predecessor (issue #212).
type failFirstMaterialRecorder struct {
	event.Recorder
	failValue string
	tripped   bool
}

func (r *failFirstMaterialRecorder) RecordFor(ctx context.Context, account uuid.UUID, c event.Candidate) (event.RecordResult, error) {
	if !r.tripped && c.Consumption != nil && c.Consumption.CurrValue == r.failValue {
		r.tripped = true
		return event.RecordResult{}, errors.New("simulated transient fault: deadlock")
	}
	return r.Recorder.RecordFor(ctx, account, c)
}

// TestDurableConsumerBurstPartialFailureDefersTail is the issue #212 event-dedup
// never-cut regression (§4.6): when a stream drains TWO material transitions in one
// pass (o1→o2, o2→o3) and the FIRST one's record errors transiently, the producer
// must NOT let the second transition advance the durable cursor past the errored
// predecessor. Otherwise the next pass reads strictly after o3 and the first material
// movement (o1→o2) is permanently dropped — no market event, no Today item, and a
// River retry cannot recover it because the cursor already moved. The pass must defer
// the whole tail IN CAPTURED ORDER so the errored transition is re-derived next pass,
// and emit the distinct held-back signal (stream_blocked) so telemetry can tell a
// deliberate defer from an ordinary transient error.
func TestDurableConsumerBurstPartialFailureDefersTail(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account, variant, target, nv := seedTarget(t, pool, q)
	svc := event.NewService(pool)
	competitorThreshold(t, svc, account, 1000) // 10% — both steps below are material

	base := time.Now().UTC().Add(-30 * time.Minute)
	// A burst of three observations for ONE stream: o1→o2 (+30%) and o2→o3 (+~31%),
	// both material, drained contiguously in a single pass.
	appendObservation(t, q, account, target, nv, "seller-1", "1000000", base)                    // o1
	appendObservation(t, q, account, target, nv, "seller-1", "1300000", base.Add(2*time.Minute)) // o2
	appendObservation(t, q, account, target, nv, "seller-1", "1700000", base.Add(4*time.Minute)) // o3

	// Fail the FIRST material transition (o1→o2, whose newest value is o2=1300000) once.
	rec := &failFirstMaterialRecorder{Recorder: svc, failValue: "1300000"}
	prod := event.NewProducer(rec, event.NewObservationSource(pool), nil)

	// Pass 1: T1 (o1→o2) errors → the stream is blocked. T2 (o2→o3) must be DEFERRED,
	// not recorded, so the cursor never advances past the errored predecessor.
	m1, err := prod.RunOnce(ctx)
	if err == nil {
		t.Fatal("the errored first transition must be surfaced for retry, got nil error")
	}
	if m1.Errors < 1 {
		t.Fatalf("the transient record fault must be counted, got errors=%d", m1.Errors)
	}
	// (c) The deliberate defer emits its OWN signal, distinct from the transient error.
	if m1.StreamBlocked < 1 {
		t.Fatalf("the deferred tail must fire the stream_blocked signal, got stream_blocked=%d", m1.StreamBlocked)
	}
	// (a) The durable cursor must NOT have advanced past o1 — in particular it must not
	// be sitting at o3 (which would drop o1→o2 forever). The buggy path advanced it to
	// 1700000 via T2's own transaction.
	var advancedPastPredecessor int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM observation_consumer_cursors
		 WHERE target_id=$1 AND native_seller_id='seller-1'
		   AND last_price_raw_value IN ('1300000','1700000')`, target).Scan(&advancedPastPredecessor); err != nil {
		t.Fatalf("read cursor: %v", err)
	}
	if advancedPastPredecessor != 0 {
		t.Fatalf("the cursor must not advance past the errored predecessor; a later transition leaked it forward")
	}
	// The first material transition must not have produced an event yet (it errored).
	var firstEvent int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM market_events WHERE variant_id=$1 AND evidence_detail->>'curr_value'='1300000'`, variant).Scan(&firstEvent); err != nil {
		t.Fatalf("count first event: %v", err)
	}
	if firstEvent != 0 {
		t.Fatalf("the errored first transition must not have produced an event in pass 1, found %d", firstEvent)
	}

	// Pass 2: the fault clears; the SAME producer re-derives the deferred tail from the
	// unadvanced cursor, oldest-first — o1→o2 (the errored predecessor) FIRST, then
	// o2→o3. Both are competitor moves on the same offer stream, so they collapse onto
	// ONE open event (dedup, EVT-003) that the oldest re-derived transition OPENS.
	m2, err := prod.RunOnce(ctx)
	if err != nil {
		t.Fatalf("pass 2: %v", err)
	}
	// (b) The deferred first transition's market event IS produced: the re-derived
	// oldest transition opens exactly one event this pass — it was never dropped.
	if m2.Produced != 1 {
		t.Fatalf("the re-derived predecessor must open exactly one market event next pass, got produced=%d", m2.Produced)
	}
	// (b, durable proof) The append-only ingestion ledger now carries the o1→o2 claim
	// (its curr observation is the 1300000 row). The buggy path advanced the cursor past
	// o1→o2 and this claim NEVER appears — the movement is silently lost.
	var firstTransitionLedger int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM event_input_transitions eit
		 JOIN observations o ON o.id = eit.curr_observation_id
		 WHERE eit.target_id=$1 AND o.price_raw_value='1300000'`, target).Scan(&firstTransitionLedger); err != nil {
		t.Fatalf("count first-transition ledger claim: %v", err)
	}
	if firstTransitionLedger != 1 {
		t.Fatalf("the errored first transition o1→o2 must be re-derived and ingested exactly once (its ledger claim), got %d", firstTransitionLedger)
	}
	// The whole burst is consumed in captured order with NO gap and NO duplicate: both
	// adjacent material transitions carry exactly one append-only ledger row each.
	var ledger int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM event_input_transitions WHERE target_id=$1`, target).Scan(&ledger); err != nil {
		t.Fatalf("count ledger: %v", err)
	}
	if ledger != 2 {
		t.Fatalf("both burst transitions must be ingested exactly once (2 ledger rows), got %d", ledger)
	}
	// Exactly one open event survives (the two same-stream moves dedup onto it).
	var open int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM market_events WHERE variant_id=$1 AND state IN ('open','updated')`, variant).Scan(&open); err != nil {
		t.Fatalf("count open events: %v", err)
	}
	if open != 1 {
		t.Fatalf("exactly one open event expected after the burst drains, got %d", open)
	}
	// The cursor now reaches the newest observation — the deferred tail advanced only
	// AFTER its predecessor was consumed, in captured order.
	var lastVal string
	if err := pool.QueryRow(ctx,
		`SELECT last_price_raw_value FROM observation_consumer_cursors WHERE target_id=$1 AND native_seller_id='seller-1'`, target).
		Scan(&lastVal); err != nil {
		t.Fatalf("read final cursor: %v", err)
	}
	if lastVal != "1700000" {
		t.Fatalf("after the burst fully drains the cursor must reach the newest observation 1700000, got %s", lastVal)
	}
}

// TestDurableConsumerCrossSellerNeverPaired proves two observations that share one
// offer_identity but belong to DIFFERENT sellers are never paired into a synthetic
// price movement (issue #212). The stream key includes native_seller_id, so S1 and
// S2 are separate streams; a seller change is a NEW stream, not a transition.
func TestDurableConsumerCrossSellerNeverPaired(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account, _, target, nv := seedTarget(t, pool, q)

	base := time.Now().UTC().Add(-30 * time.Minute)
	// Same offer identity, two different sellers, different values, no within-stream
	// movement. The OLD offer-only pairing would emit a synthetic 1000000→2000000
	// movement no single seller made.
	appendObsSellerOffer(t, q, account, target, nv, "seller-1", "OFFER-X", "1000000", base)
	appendObsSellerOffer(t, q, account, target, nv, "seller-2", "OFFER-X", "2000000", base.Add(5*time.Minute))

	all, err := event.NewObservationSource(pool).Transitions(ctx)
	if err != nil {
		t.Fatalf("transitions: %v", err)
	}
	got := transitionsForTarget(all, target)
	if len(got) != 0 {
		t.Fatalf("two sellers sharing an offer identity must never be paired; got %d: %+v", len(got), got)
	}
}

// TestDurableConsumerSellerReassignmentStartsNewStream proves a seller change on the
// same offer identity does not create a transition, while each seller's OWN
// within-stream movement is still evaluated.
func TestDurableConsumerSellerReassignmentStartsNewStream(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account, _, target, nv := seedTarget(t, pool, q)

	base := time.Now().UTC().Add(-40 * time.Minute)
	// seller-1 makes a real movement 1000000 -> 1500000.
	appendObsSellerOffer(t, q, account, target, nv, "seller-1", "OFFER-Y", "1000000", base)
	appendObsSellerOffer(t, q, account, target, nv, "seller-1", "OFFER-Y", "1500000", base.Add(2*time.Minute))
	// Then the offer is taken over by seller-2 at a different value.
	appendObsSellerOffer(t, q, account, target, nv, "seller-2", "OFFER-Y", "3000000", base.Add(5*time.Minute))

	all, err := event.NewObservationSource(pool).Transitions(ctx)
	if err != nil {
		t.Fatalf("transitions: %v", err)
	}
	got := transitionsForTarget(all, target)
	if len(got) != 1 {
		t.Fatalf("want exactly seller-1's own movement (1), never a cross-seller pair; got %d: %+v", len(got), got)
	}
	cp := got[0].CompetitorPrice
	if cp.PrevValue != "1000000" || cp.CurrValue != "1500000" {
		t.Fatalf("the only transition must be seller-1's own 1000000->1500000, got %s->%s", cp.PrevValue, cp.CurrValue)
	}
}

// TestDurableConsumerBurstEvaluatesIntermediate proves a material intermediate
// movement is NOT lost when a burst A→B→C is appended before a single pass
// (issue #212). A→B exceeds the threshold; B→C falls below it. A→B must produce an
// event and the trailing immaterial B→C must not resolve it away.
func TestDurableConsumerBurstEvaluatesIntermediate(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account, variant, target, nv := seedTarget(t, pool, q)
	svc := event.NewService(pool)
	competitorThreshold(t, svc, account, 1000) // 10%

	base := time.Now().UTC().Add(-30 * time.Minute)
	// A→B = +30% (material). B→C = +~0.8% (below threshold, immaterial).
	appendObservation(t, q, account, target, nv, "seller-1", "1000000", base)
	appendObservation(t, q, account, target, nv, "seller-1", "1300000", base.Add(2*time.Minute))
	appendObservation(t, q, account, target, nv, "seller-1", "1310000", base.Add(4*time.Minute))

	m, err := event.NewProducer(svc, event.NewObservationSource(pool), nil).RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if m.Produced < 1 {
		t.Fatalf("the material intermediate A→B must be evaluated and produced, got produced=%d scanned=%d dormant=%d",
			m.Produced, m.Scanned, m.Dormant)
	}
	// The event must remain OPEN in Today — the immaterial B→C must not resolve it.
	feed, err := svc.Today(ctx, account)
	if err != nil {
		t.Fatalf("today: %v", err)
	}
	if len(feed) != 1 {
		t.Fatalf("the material burst event must stay open in Today, got %d items", len(feed))
	}
	var open int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM market_events WHERE variant_id=$1 AND state IN ('open','updated')`, variant).Scan(&open); err != nil {
		t.Fatalf("count open: %v", err)
	}
	if open != 1 {
		t.Fatalf("exactly one open event expected after the burst, got %d", open)
	}
}

// TestDurableConsumerDrainsBeyondPageBounded proves that more observations than one
// page are drained across bounded pages with NO gap or duplicate (issue #212). With
// a page of 2 and five adjacent material movements, repeated passes ingest every
// input transition exactly once (five append-only ledger rows) and the cursor
// reaches the newest observation.
func TestDurableConsumerDrainsBeyondPageBounded(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account, _, target, nv := seedTarget(t, pool, q)
	svc := event.NewService(pool)
	competitorThreshold(t, svc, account, 100) // 1% — every step below is material

	base := time.Now().UTC().Add(-time.Hour)
	values := []string{"1000000", "2000000", "3000000", "4000000", "5000000", "6000000"}
	for i, v := range values {
		appendObservation(t, q, account, target, nv, "seller-1", v, base.Add(time.Duration(i)*time.Minute))
	}
	// Five adjacent distinct-value transitions across a page of two: needs several
	// bounded passes to drain fully.
	src := event.NewObservationSource(pool).WithPageLimit(2)
	prod := event.NewProducer(svc, src, nil)
	for i := 0; i < 10; i++ {
		if _, err := prod.RunOnce(ctx); err != nil {
			t.Fatalf("pass %d: %v", i, err)
		}
	}

	var ledger int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM event_input_transitions WHERE target_id=$1`, target).Scan(&ledger); err != nil {
		t.Fatalf("count ledger: %v", err)
	}
	if ledger != len(values)-1 {
		t.Fatalf("every adjacent transition must be ingested exactly once (no gap/duplicate); want %d ledger rows, got %d",
			len(values)-1, ledger)
	}
	var lastVal string
	if err := pool.QueryRow(ctx,
		`SELECT last_price_raw_value FROM observation_consumer_cursors WHERE target_id=$1 AND native_seller_id='seller-1'`, target).
		Scan(&lastVal); err != nil {
		t.Fatalf("read cursor: %v", err)
	}
	if lastVal != values[len(values)-1] {
		t.Fatalf("cursor must reach the newest observation %s, got %s", values[len(values)-1], lastVal)
	}
	// An extra pass with nothing new drains nothing and adds no ledger row.
	before := ledger
	if _, err := prod.RunOnce(ctx); err != nil {
		t.Fatalf("idempotent pass: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM event_input_transitions WHERE target_id=$1`, target).Scan(&ledger); err != nil {
		t.Fatalf("recount ledger: %v", err)
	}
	if ledger != before {
		t.Fatalf("a caught-up pass must ingest nothing; ledger grew %d -> %d", before, ledger)
	}
}

// TestDurableConsumerLifecycleCompletionNoReopen proves that resolving an event
// cannot cause old observations to reopen it — only a newly consumed transition can
// (issue #212). This is the lifecycle-completion replay the durable cursor + ledger
// fix closes.
func TestDurableConsumerLifecycleCompletionNoReopen(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account, variant, target, nv := seedTarget(t, pool, q)
	svc := event.NewService(pool)
	competitorThreshold(t, svc, account, 1000)

	base := time.Now().UTC().Add(-30 * time.Minute)
	appendObservation(t, q, account, target, nv, "seller-1", "1000000", base)
	appendObservation(t, q, account, target, nv, "seller-1", "1300000", base.Add(2*time.Minute))

	prod := event.NewProducer(svc, event.NewObservationSource(pool), nil)
	if _, err := prod.RunOnce(ctx); err != nil {
		t.Fatalf("pass 1: %v", err)
	}
	// Resolve the open event (lifecycle completion frees its dedup key).
	var eventID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM market_events WHERE variant_id=$1 AND state IN ('open','updated')`, variant).Scan(&eventID); err != nil {
		t.Fatalf("find open event: %v", err)
	}
	if _, err := svc.Resolve(ctx, eventID); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	// A restart pass with NO new observations must NOT reopen the resolved event.
	m, err := event.NewProducer(svc, event.NewObservationSource(pool), nil).RunOnce(ctx)
	if err != nil {
		t.Fatalf("pass 2: %v", err)
	}
	if m.Produced != 0 {
		t.Fatalf("a resolved event must not reopen from old observations; got produced=%d", m.Produced)
	}
	var total int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM market_events WHERE variant_id=$1`, variant).Scan(&total); err != nil {
		t.Fatalf("count: %v", err)
	}
	if total != 1 {
		t.Fatalf("no new event row may be created by a replay after resolution; found %d", total)
	}
	// A genuinely NEW movement (a newly consumed transition) DOES open a fresh event.
	appendObservation(t, q, account, target, nv, "seller-1", "1700000", base.Add(10*time.Minute))
	m3, err := event.NewProducer(svc, event.NewObservationSource(pool), nil).RunOnce(ctx)
	if err != nil {
		t.Fatalf("pass 3: %v", err)
	}
	if m3.Produced != 1 {
		t.Fatalf("a newly consumed transition must open a fresh event, got produced=%d", m3.Produced)
	}
}

// TestDurableConsumerLedgerPreventsDuplicateOnStaleCursor proves the ingestion-
// idempotency ledger is the crash-after-commit backstop (issue #212): even if the
// durable cursor is lost after an event committed (the "crash after event commit but
// before cursor commit" window), re-deriving the same transition produces ZERO
// duplicate events — the append-only ledger claim rejects the re-consumption.
func TestDurableConsumerLedgerPreventsDuplicateOnStaleCursor(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account, variant, target, nv := seedTarget(t, pool, q)
	svc := event.NewService(pool)
	competitorThreshold(t, svc, account, 1000)

	base := time.Now().UTC().Add(-30 * time.Minute)
	appendObservation(t, q, account, target, nv, "seller-1", "1000000", base)
	appendObservation(t, q, account, target, nv, "seller-1", "1300000", base.Add(2*time.Minute))

	prod := event.NewProducer(svc, event.NewObservationSource(pool), nil)
	if _, err := prod.RunOnce(ctx); err != nil {
		t.Fatalf("pass 1: %v", err)
	}
	// Simulate the crash window: the event + ledger row survived, but the cursor
	// advance did NOT (rolled back / lost). Delete the cursor so the source re-derives.
	if _, err := pool.Exec(ctx, `DELETE FROM observation_consumer_cursors WHERE target_id=$1`, target); err != nil {
		t.Fatalf("drop cursor: %v", err)
	}
	m, err := event.NewProducer(svc, event.NewObservationSource(pool), nil).RunOnce(ctx)
	if err != nil {
		t.Fatalf("pass 2: %v", err)
	}
	if m.Produced != 0 {
		t.Fatalf("the ledger must reject the re-consumed transition; got produced=%d (want 0)", m.Produced)
	}
	if m.Skipped < 1 {
		t.Fatalf("a re-consumed transition must be counted as skipped (ingestion dedup), got skipped=%d", m.Skipped)
	}
	var rows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM market_events WHERE variant_id=$1`, variant).Scan(&rows); err != nil {
		t.Fatalf("count: %v", err)
	}
	if rows != 1 {
		t.Fatalf("crash-after-commit replay must not create a second event; found %d", rows)
	}
}

// TestDurableConsumerStableStreamDoesNotStarveSiblings proves the durable cursor
// advances over an immaterial / same-value tail so a stable stream cannot starve a
// sibling stream that sorts after it in the bounded drain page (issue #212 area
// review finding). A stable seller ("seller-1", many same-value observations, more
// than one page) sorts before a live seller ("seller-2") whose material movement
// must still be detected across bounded passes.
func TestDurableConsumerStableStreamDoesNotStarveSiblings(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account, _, target, nv := seedTarget(t, pool, q)
	svc := event.NewService(pool)
	competitorThreshold(t, svc, account, 1000)

	// Keep every observation inside the threshold window (effective_from = now-1h) so
	// the live movement evaluates against an in-force threshold.
	base := time.Now().UTC().Add(-40 * time.Minute)
	// Stable stream (sorts first): six same-value observations — no movement ever.
	for i := 0; i < 6; i++ {
		appendObsSellerOffer(t, q, account, target, nv, "seller-1", "OFFER-A", "1000000", base.Add(time.Duration(i)*time.Minute))
	}
	// Live stream (sorts last): a material movement that MUST be detected.
	appendObsSellerOffer(t, q, account, target, nv, "seller-2", "OFFER-B", "2000000", base.Add(time.Minute))
	appendObsSellerOffer(t, q, account, target, nv, "seller-2", "OFFER-B", "2600000", base.Add(15*time.Minute))

	// Page of two: the six-row stable prefix would monopolise the page for several
	// passes under a materiality-only cursor; the high-water advance must drain it.
	src := event.NewObservationSource(pool).WithPageLimit(2)
	prod := event.NewProducer(svc, src, nil)
	var producedTotal int
	for i := 0; i < 8; i++ {
		m, err := prod.RunOnce(ctx)
		if err != nil {
			t.Fatalf("pass %d: %v", i, err)
		}
		producedTotal += m.Produced
	}
	if producedTotal < 1 {
		t.Fatalf("the live sibling stream's material movement must be detected despite the stable prefix; produced=%d", producedTotal)
	}
	var open int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM market_events WHERE target_id=$1 AND evidence_detail->>'curr_value'='2600000'`, target).Scan(&open); err != nil {
		t.Fatalf("count: %v", err)
	}
	if open != 1 {
		t.Fatalf("seller-2's 2000000->2600000 movement must produce exactly one event, got %d", open)
	}
	// The stable stream's cursor must have advanced to its newest observation (no
	// perpetual re-read of the same-value tail).
	var lastVal string
	var lastCaptured time.Time
	if err := pool.QueryRow(ctx,
		`SELECT last_price_raw_value, last_captured_at FROM observation_consumer_cursors
		 WHERE target_id=$1 AND native_seller_id='seller-1'`, target).Scan(&lastVal, &lastCaptured); err != nil {
		t.Fatalf("read stable cursor: %v", err)
	}
	// The newest seller-1 observation is at base+5min; assert the cursor advanced well
	// past the same-value tail (a materiality-only cursor would be stuck at base+0min).
	// Lower bound at base+4min avoids a false negative from Postgres microsecond
	// truncation vs Go nanosecond precision.
	if lastVal != "1000000" || lastCaptured.Before(base.Add(4*time.Minute)) {
		t.Fatalf("stable stream cursor must advance across its same-value tail (expected ~base+5min), got val=%s at=%s", lastVal, lastCaptured)
	}
}

// TestDurableConsumerCrossAccountIsolation proves cursor state and event/input
// idempotency are tenant-scoped (issue #212): observations from account A cannot
// advance, suppress, or generate events for account B.
func TestDurableConsumerCrossAccountIsolation(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	accountA, variantA, targetA, nvA := seedTarget(t, pool, q)
	accountB, variantB, targetB, _ := seedTarget(t, pool, q)
	svc := event.NewService(pool)
	competitorThreshold(t, svc, accountA, 1000)
	competitorThreshold(t, svc, accountB, 1000)

	base := time.Now().UTC().Add(-30 * time.Minute)
	// Only account A gets a real movement.
	appendObservation(t, q, accountA, targetA, nvA, "seller-1", "1000000", base)
	appendObservation(t, q, accountA, targetA, nvA, "seller-1", "1300000", base.Add(2*time.Minute))

	if _, err := event.NewProducer(svc, event.NewObservationSource(pool), nil).RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	var aEvents, bEvents int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM market_events WHERE variant_id=$1`, variantA).Scan(&aEvents); err != nil {
		t.Fatalf("count A: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM market_events WHERE variant_id=$1`, variantB).Scan(&bEvents); err != nil {
		t.Fatalf("count B: %v", err)
	}
	if aEvents != 1 {
		t.Fatalf("account A must have its own event, got %d", aEvents)
	}
	if bEvents != 0 {
		t.Fatalf("account A's observations must not generate events for account B, got %d", bEvents)
	}
	// B's cursor + ledger must be untouched.
	var bCursors, bLedger int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM observation_consumer_cursors WHERE target_id=$1`, targetB).Scan(&bCursors); err != nil {
		t.Fatalf("count B cursors: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM event_input_transitions WHERE marketplace_account_id=$1`, accountB).Scan(&bLedger); err != nil {
		t.Fatalf("count B ledger: %v", err)
	}
	if bCursors != 0 || bLedger != 0 {
		t.Fatalf("account B's durable consumer state must be untouched, got cursors=%d ledger=%d", bCursors, bLedger)
	}
}
