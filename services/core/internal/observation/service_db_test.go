package observation_test

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/money"
	obs "github.com/mhosseinab/market-ops/services/core/internal/observation"
)

// newPool connects to DATABASE_URL (schema applied via `task db:reset`). Skips
// when unset so the suite still runs where no Postgres is provisioned.
func newPool(t *testing.T) (*pgxpool.Pool, *db.Queries) {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set; skipping observation DB test")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("connect pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool, db.New(pool)
}

// seedVariant creates org+account+product+variant with fresh native ids and
// returns the account id, variant id, and native ids.
func seedVariant(t *testing.T, q *db.Queries) (account, variant uuid.UUID, nativeVariant, nativeProduct int64) {
	t.Helper()
	ctx := context.Background()
	org, err := q.CreateOrganization(ctx, "obs-test-"+uuid.NewString())
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	acct, err := q.CreateMarketplaceAccount(ctx, db.CreateMarketplaceAccountParams{
		OrganizationID:  org.ID,
		NativeAccountID: "native-" + uuid.NewString(),
		DisplayName:     "Obs Seller",
	})
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	nativeProduct = int64(uuid.New().ID())
	nativeVariant = int64(uuid.New().ID())
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
	return acct.ID, v.ID, nativeVariant, nativeProduct
}

// insertConfirmedIdentity inserts an active Confirmed mapping directly (S11 owns
// the confirm flow; here we need a confirmed row to make a target).
func insertConfirmedIdentity(t *testing.T, pool *pgxpool.Pool, account, variant uuid.UUID, nv, np int64) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := pool.QueryRow(context.Background(), `
		INSERT INTO market_product_identities
		    (marketplace_account_id, variant_id, native_variant_id, native_product_id, state, active)
		VALUES ($1,$2,$3,$4,'confirmed',true)
		RETURNING id`, account, variant, nv, np).Scan(&id)
	if err != nil {
		t.Fatalf("insert confirmed identity: %v", err)
	}
	return id
}

// clock is a settable time source for deterministic freshness/expiry.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time  { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) set(t time.Time) { c.mu.Lock(); defer c.mu.Unlock(); c.t = t }

func captureFor(target, account uuid.UUID, nv int64, route obs.Route, at time.Time) obs.Capture {
	return obs.Capture{
		TargetID:        target,
		Account:         account,
		NativeVariantID: nv,
		NativeSellerID:  "seller-9",
		Route:           route,
		SourceType:      obs.SourcePublicWebEndpoint,
		ParserVersion:   "p1.0.0",
		EvidenceRef:     "fixture://obs",
		Availability:    obs.InStock,
		Confidence:      obs.ConfPartiallyVerified,
		CapturedAt:      at,
		SchemaValid:     true,
		Price:           money.NewRawAmount("1٬200٬000 ریال", "1200000", "IRR-rial"),
	}
}

// TestUnconfirmedIdentityCannotCreateTarget is the OBS-001 structural guard: a
// target for a NON-confirmed identity is rejected by the trigger.
func TestUnconfirmedIdentityCannotCreateTarget(t *testing.T) {
	_, q := newPool(t)
	ctx := context.Background()
	account, variant, nv, np := seedVariant(t, q)

	// A NeedsReview candidate (unconfirmed) — must NOT be able to spawn a target.
	cand, err := q.CreateIdentityCandidate(ctx, db.CreateIdentityCandidateParams{
		MarketplaceAccountID: account,
		VariantID:            variant,
		NativeVariantID:      nv,
		NativeProductID:      np,
	})
	if err != nil {
		t.Fatalf("create candidate: %v", err)
	}
	_, err = q.InsertObservationTarget(ctx, db.InsertObservationTargetParams{
		MarketplaceAccountID:     account,
		IdentityID:               cand.ID,
		VariantID:                variant,
		NativeVariantID:          nv,
		NativeProductID:          np,
		Tier:                     "standard",
		CadenceSeconds:           21600,
		FreshnessDeadlineSeconds: 21600,
	})
	if err == nil {
		t.Fatal("expected the trigger to reject a target for an unconfirmed identity (OBS-001)")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23514" { // check_violation
		t.Fatalf("expected check_violation (23514), got %v", err)
	}
}

// TestSyncTargetsOnlyFromConfirmed asserts SyncTargetsFromConfirmed creates a
// target for a Confirmed identity and nothing for an unconfirmed one (OBS-001).
func TestSyncTargetsOnlyFromConfirmed(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account, variant, nv, np := seedVariant(t, q)
	insertConfirmedIdentity(t, pool, account, variant, nv, np)

	svc := obs.NewService(pool)
	created, err := svc.SyncTargetsFromConfirmed(ctx, account)
	if err != nil {
		t.Fatalf("sync targets: %v", err)
	}
	if len(created) != 1 {
		t.Fatalf("want 1 target created, got %d", len(created))
	}
	// Idempotent: a re-run creates nothing.
	again, err := svc.SyncTargetsFromConfirmed(ctx, account)
	if err != nil {
		t.Fatalf("sync targets (2): %v", err)
	}
	if len(again) != 0 {
		t.Fatalf("want 0 targets on re-run, got %d", len(again))
	}
}

// TestReplayDedupRetainsProvenance is the OBS-008 proof: a replayed capture
// creates no duplicate current offer and retains route provenance; a DIFFERENT
// route corroborating the same value is retained (and promotes to Verified).
func TestReplayDedupRetainsProvenance(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account, variant, nv, np := seedVariant(t, q)
	insertConfirmedIdentity(t, pool, account, variant, nv, np)

	clk := &clock{t: time.Now().UTC()}
	svc := obs.NewService(pool).WithClock(clk.now)
	created, err := svc.SyncTargetsFromConfirmed(ctx, account)
	if err != nil || len(created) != 1 {
		t.Fatalf("sync targets: %v (n=%d)", err, len(created))
	}
	target := created[0].ID

	capC := captureFor(target, account, nv, obs.RouteC, clk.now())

	first, err := svc.Ingest(ctx, capC)
	if err != nil {
		t.Fatalf("ingest first: %v", err)
	}
	if first.Deduped {
		t.Fatal("first capture must not dedup")
	}
	// First-ever sighting: no recent history, no corroboration → Unverified (a
	// single capture never self-promotes to Supported, §10.3).
	if first.Quality != obs.Unverified {
		t.Fatalf("first-ever Route C sighting must be Unverified, got %s", first.Quality)
	}

	// Exact replay — same instant, same value, same route.
	replay, err := svc.Ingest(ctx, capC)
	if err != nil {
		t.Fatalf("ingest replay: %v", err)
	}
	if !replay.Deduped {
		t.Fatal("replayed capture must be deduplicated (OBS-008)")
	}

	// No duplicate current offer, and exactly ONE observation row so far.
	offers, err := svc.ListObservedOffers(ctx, account)
	if err != nil {
		t.Fatalf("list offers: %v", err)
	}
	if len(offers) != 1 {
		t.Fatalf("replay must not create a duplicate current offer, got %d", len(offers))
	}
	rows, err := svc.ListObservations(ctx, target, 100)
	if err != nil {
		t.Fatalf("list observations: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("replay must not append duplicate evidence, got %d rows", len(rows))
	}
	if !containsRoute(offers[0].Routes, "route_c") {
		t.Fatalf("provenance must retain route_c, got %s", offers[0].Routes)
	}

	// A DIFFERENT route (B) corroborating the SAME value: retained provenance +
	// promotion to Verified (§10.1 second qualifying path).
	capB := captureFor(target, account, nv, obs.RouteB, clk.now().Add(time.Second))
	corr, err := svc.Ingest(ctx, capB)
	if err != nil {
		t.Fatalf("ingest corroboration: %v", err)
	}
	if corr.Deduped {
		t.Fatal("a different route is not a replay")
	}
	if corr.Quality != obs.Verified {
		t.Fatalf("cross-route corroboration must be Verified, got %s", corr.Quality)
	}
	offers, _ = svc.ListObservedOffers(ctx, account)
	if !containsRoute(offers[0].Routes, "route_c") || !containsRoute(offers[0].Routes, "route_b") {
		t.Fatalf("provenance must retain BOTH routes, got %s", offers[0].Routes)
	}
}

// TestExpirySweepNeverSatisfiesCurrentGate is the OBS-004 negative test: after the
// freshness deadline passes, the sweep marks the offer Stale and a Stale value can
// NEVER satisfy a current-data gate.
func TestExpirySweepNeverSatisfiesCurrentGate(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account, variant, nv, np := seedVariant(t, q)
	insertConfirmedIdentity(t, pool, account, variant, nv, np)

	clk := &clock{t: time.Now().UTC()}
	svc := obs.NewService(pool).WithClock(clk.now)
	created, _ := svc.SyncTargetsFromConfirmed(ctx, account)
	target := created[0].ID

	res, err := svc.Ingest(ctx, captureFor(target, account, nv, obs.RouteC, clk.now()))
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if !obs.Quality(res.Offer.Quality).SatisfiesCurrentDataGate() {
		t.Fatal("a fresh value should satisfy the current-data gate before expiry")
	}

	// Advance past the standard freshness window (6h) and sweep.
	clk.set(clk.now().Add(7 * time.Hour))
	n, err := svc.SweepExpired(ctx, account)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 offer swept to Stale, got %d", n)
	}

	offers, _ := svc.ListObservedOffers(ctx, account)
	if got := obs.Quality(offers[0].Quality); got != obs.Stale {
		t.Fatalf("expired offer must be Stale, got %s", got)
	}
	// OBS-004: the expired value can never satisfy a current-data gate.
	if obs.Quality(offers[0].Quality).SatisfiesCurrentDataGate() {
		t.Fatal("an expired (Stale) value must NEVER satisfy a current-data gate (OBS-004)")
	}
	if obs.ConsequenceOf(obs.Stale).Display != obs.DisplayAgeOnly {
		t.Fatal("a Stale value must render age-only")
	}
}

// TestDisappearanceClosesWithEndTimeNeverZero is the §16 proof: a disappeared
// offer is closed with an end time and the last raw price is retained — never a
// zero price.
func TestDisappearanceClosesWithEndTimeNeverZero(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account, variant, nv, np := seedVariant(t, q)
	insertConfirmedIdentity(t, pool, account, variant, nv, np)

	clk := &clock{t: time.Now().UTC()}
	svc := obs.NewService(pool).WithClock(clk.now)
	created, _ := svc.SyncTargetsFromConfirmed(ctx, account)
	target := created[0].ID

	if _, err := svc.Ingest(ctx, captureFor(target, account, nv, obs.RouteC, clk.now())); err != nil {
		t.Fatalf("ingest live: %v", err)
	}

	// The offer disappears (§16). The capture carries no price token — absence, not
	// a zero — and must never be turned into a zero price.
	gone := captureFor(target, account, nv, obs.RouteC, clk.now().Add(time.Minute))
	gone.Availability = obs.Disappeared
	gone.Price = money.RawAmount{}
	res, err := svc.Ingest(ctx, gone)
	if err != nil {
		t.Fatalf("ingest disappearance: %v", err)
	}
	if res.Quality != obs.Unavailable {
		t.Fatalf("disappeared offer must be Unavailable, got %s", res.Quality)
	}
	if !res.Offer.EndedAt.Valid {
		t.Fatal("disappeared offer must be closed with an end time (§16)")
	}
	if res.Offer.PriceRawValue == "0" || res.Offer.PriceRawText == "0" {
		t.Fatal("disappearance must never convert the price to zero (§16)")
	}
	if res.Offer.PriceRawValue != "1200000" {
		t.Fatalf("last raw price must be retained, got %q", res.Offer.PriceRawValue)
	}
}

// TestMismatchedNativeVariantRejected is the identity-quarantine proof (BLOCKING
// 1a): a capture whose native variant id does not match the target's confirmed
// identity is rejected outright — no observation, no observed offer, no
// identity-valid stamp.
func TestMismatchedNativeVariantRejected(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account, variant, nv, np := seedVariant(t, q)
	insertConfirmedIdentity(t, pool, account, variant, nv, np)

	svc := obs.NewService(pool)
	created, _ := svc.SyncTargetsFromConfirmed(ctx, account)
	target := created[0].ID

	// A valid target, but the capture carries a DIFFERENT variant's native id.
	bad := captureFor(target, account, nv+1, obs.RouteB, time.Now().UTC())
	_, err := svc.Ingest(ctx, bad)
	if err == nil {
		t.Fatal("mismatched nativeVariantId must be rejected (identity quarantine)")
	}
	if !errors.Is(err, obs.ErrIdentityMismatch) {
		t.Fatalf("expected ErrIdentityMismatch, got %v", err)
	}
	offers, _ := svc.ListObservedOffers(ctx, account)
	if len(offers) != 0 {
		t.Fatalf("a rejected mismatch must not create an observed offer, got %d", len(offers))
	}
	rows, _ := svc.ListObservations(ctx, target, 100)
	if len(rows) != 0 {
		t.Fatalf("a rejected mismatch must not append evidence, got %d", len(rows))
	}
}

// TestClientConfidenceCannotSelfPromote is the BLOCKING 1b proof: a single Route B
// upload asserting confidence=verified reaches at most Unverified — the client's
// word alone can never reach Supported or Verified.
func TestClientConfidenceCannotSelfPromote(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account, variant, nv, np := seedVariant(t, q)
	insertConfirmedIdentity(t, pool, account, variant, nv, np)

	svc := obs.NewService(pool)
	created, _ := svc.SyncTargetsFromConfirmed(ctx, account)
	target := created[0].ID

	c := captureFor(target, account, nv, obs.RouteB, time.Now().UTC())
	c.Confidence = obs.ConfVerified // the client claims the highest confidence
	res, err := svc.Ingest(ctx, c)
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if res.Quality == obs.Verified || res.Quality == obs.Supported {
		t.Fatalf("a single client capture must not self-promote, got %s", res.Quality)
	}
	if res.Quality != obs.Unverified {
		t.Fatalf("first-ever single client capture must be Unverified, got %s", res.Quality)
	}
}

// TestStalePriorRouteDoesNotVerify is the BLOCKING 2 proof: a corroborating route
// whose OWN evidence has aged out of window must NOT promote a later single fresh
// route to Verified.
func TestStalePriorRouteDoesNotVerify(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account, variant, nv, np := seedVariant(t, q)
	insertConfirmedIdentity(t, pool, account, variant, nv, np)

	clk := &clock{t: time.Now().UTC()}
	svc := obs.NewService(pool).WithClock(clk.now)
	created, _ := svc.SyncTargetsFromConfirmed(ctx, account)
	target := created[0].ID

	// Route C observes the value at T0.
	if _, err := svc.Ingest(ctx, captureFor(target, account, nv, obs.RouteC, clk.now())); err != nil {
		t.Fatalf("ingest C: %v", err)
	}

	// >6h later Route C's evidence is out of window. A single fresh Route B upload of
	// the same value must NOT be Verified: Route B is the only in-window path.
	clk.set(clk.now().Add(7 * time.Hour))
	res, err := svc.Ingest(ctx, captureFor(target, account, nv, obs.RouteB, clk.now()))
	if err != nil {
		t.Fatalf("ingest B: %v", err)
	}
	if res.Quality == obs.Verified {
		t.Fatalf("a stale out-of-window prior route must NOT yield Verified, got %s", res.Quality)
	}
	// Provenance must not retain the aged-out Route C.
	offers, _ := svc.ListObservedOffers(ctx, account)
	if containsRoute(offers[0].Routes, "route_c") {
		t.Fatalf("aged-out route must drop from provenance, got %s", offers[0].Routes)
	}
}

// TestRouteDisagreementConflicted is the BLOCKING 3 / §16 proof: a different
// in-window route disagreeing on value produces Conflicted (which blocks recommend
// and execute) and does NOT let the newer value silently overwrite the offer.
func TestRouteDisagreementConflicted(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account, variant, nv, np := seedVariant(t, q)
	insertConfirmedIdentity(t, pool, account, variant, nv, np)

	clk := &clock{t: time.Now().UTC()}
	svc := obs.NewService(pool).WithClock(clk.now)
	created, _ := svc.SyncTargetsFromConfirmed(ctx, account)
	target := created[0].ID

	// Route C observes 100.
	cCap := captureFor(target, account, nv, obs.RouteC, clk.now())
	cCap.Price = money.NewRawAmount("100", "100", "IRR-rial")
	if _, err := svc.Ingest(ctx, cCap); err != nil {
		t.Fatalf("ingest C: %v", err)
	}

	// Route B (in window) disagrees at 200.
	bCap := captureFor(target, account, nv, obs.RouteB, clk.now().Add(time.Second))
	bCap.Price = money.NewRawAmount("200", "200", "IRR-rial")
	res, err := svc.Ingest(ctx, bCap)
	if err != nil {
		t.Fatalf("ingest B: %v", err)
	}
	if res.Quality != obs.Conflicted {
		t.Fatalf("route disagreement must be Conflicted (§16), got %s", res.Quality)
	}
	// The gate must block, and the disagreeing newer value must NOT overwrite 100.
	if obs.Conflicted.SatisfiesCurrentDataGate() {
		t.Fatal("a Conflicted value must never satisfy a current-data gate (§16 block)")
	}
	if obs.ConsequenceOf(obs.Conflicted).CanExecute || obs.ConsequenceOf(obs.Conflicted).Recommend {
		t.Fatal("Conflicted must block recommend and execute")
	}
	if res.Offer.PriceRawValue != "100" {
		t.Fatalf("a disagreeing newer value must not overwrite the offer, got %q", res.Offer.PriceRawValue)
	}
}

// TestEmptyPriceIngestsUnavailable is the issue #43 SEAM proof: a capture that is
// in_stock but carries an EMPTY price must ingest through the REAL Service.Ingest
// path to Quality == Unavailable — a missing price can never read as usable current
// marketplace evidence. The capture is STILL stored append-only (quarantine over
// reject). Reverting the service.go HasCurrentPriceValue wiring back to
// availability-only would flip this to Unverified/Supported and FAIL this test.
func TestEmptyPriceIngestsUnavailable(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account, variant, nv, np := seedVariant(t, q)
	insertConfirmedIdentity(t, pool, account, variant, nv, np)

	clk := &clock{t: time.Now().UTC()}
	svc := obs.NewService(pool).WithClock(clk.now)
	created, _ := svc.SyncTargetsFromConfirmed(ctx, account)
	target := created[0].ID

	// In-stock, fresh, schema-valid, identity-valid — everything a "current" gate
	// would want — EXCEPT the raw price is entirely absent.
	c := captureFor(target, account, nv, obs.RouteC, clk.now())
	c.Price = money.RawAmount{}
	res, err := svc.Ingest(ctx, c)
	if err != nil {
		t.Fatalf("ingest empty-price: %v", err)
	}
	if res.Quality != obs.Unavailable {
		t.Fatalf("an empty-price in_stock capture must be Unavailable, got %s", res.Quality)
	}
	if obs.Quality(res.Offer.Quality).SatisfiesCurrentDataGate() {
		t.Fatal("an empty-price capture must never satisfy a current-data gate (issue #43)")
	}
	if obs.ConsequenceOf(res.Quality).CanExecute || obs.ConsequenceOf(res.Quality).Recommend {
		t.Fatal("an empty-price capture must block recommend and execute")
	}
	// Quarantine over reject: evidence is STILL stored append-only.
	rows, err := svc.ListObservations(ctx, target, 100)
	if err != nil {
		t.Fatalf("list observations: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("empty-price capture must still be stored append-only, got %d rows", len(rows))
	}

	// Whitespace-only price is likewise absence, not a value.
	ws := captureFor(target, account, nv, obs.RouteB, clk.now().Add(time.Second))
	ws.Price = money.NewRawAmount("   ", "\t", "\n")
	wsRes, err := svc.Ingest(ctx, ws)
	if err != nil {
		t.Fatalf("ingest whitespace-price: %v", err)
	}
	if wsRes.Quality != obs.Unavailable {
		t.Fatalf("a whitespace-only price capture must be Unavailable, got %s", wsRes.Quality)
	}
}

// TestCompletePriceRetainsValueBearingQuality is the issue #43 positive seam case:
// an out_of_stock capture WITH a complete price stays value-bearing (a real state,
// NOT Unavailable). This confirms the gate keys on price PRESENCE, not on in_stock.
func TestCompletePriceRetainsValueBearingQuality(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account, variant, nv, np := seedVariant(t, q)
	insertConfirmedIdentity(t, pool, account, variant, nv, np)

	clk := &clock{t: time.Now().UTC()}
	svc := obs.NewService(pool).WithClock(clk.now)
	created, _ := svc.SyncTargetsFromConfirmed(ctx, account)
	target := created[0].ID

	c := captureFor(target, account, nv, obs.RouteC, clk.now())
	c.Availability = obs.OutOfStock // a value-bearing availability, not disappeared
	res, err := svc.Ingest(ctx, c)
	if err != nil {
		t.Fatalf("ingest complete out_of_stock: %v", err)
	}
	if res.Quality == obs.Unavailable {
		t.Fatalf("an out_of_stock capture with a complete price must stay value-bearing, got %s", res.Quality)
	}
}

// TestDedupMaterialConflictNotDiscarded is the issue #44 negative proof and the
// event-deduplication never-cut guard (§4.6): two captures that share the dedup
// key SUBSET (same target/offer/route/sub_route/price value+unit/list-price
// value/availability/captured_at) but differ in a MATERIAL field OUTSIDE that
// subset (here the ListPrice unit + text) must NOT collapse into a Deduped:true
// success. The second capture is a MATERIAL CONFLICT: it fails closed with an
// explicit conflict outcome, records an append-only conflict row preserving BOTH
// evidence hashes, and never silently overwrites the authoritative current offer.
func TestDedupMaterialConflictNotDiscarded(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account, variant, nv, np := seedVariant(t, q)
	insertConfirmedIdentity(t, pool, account, variant, nv, np)

	clk := &clock{t: time.Now().UTC()}
	svc := obs.NewService(pool).WithClock(clk.now)
	created, err := svc.SyncTargetsFromConfirmed(ctx, account)
	if err != nil || len(created) != 1 {
		t.Fatalf("sync targets: %v (n=%d)", err, len(created))
	}
	target := created[0].ID

	// First capture: in-window, complete, with a list price token.
	first := captureFor(target, account, nv, obs.RouteC, clk.now())
	first.ListPrice = money.NewRawAmount("1٬500٬000 ریال", "1500000", "IRR-rial")
	firstRes, err := svc.Ingest(ctx, first)
	if err != nil {
		t.Fatalf("ingest first: %v", err)
	}
	if firstRes.Deduped {
		t.Fatal("first capture must not dedup")
	}

	// Second capture: IDENTICAL dedup key (same target/offer/route/sub_route/price
	// value+unit/list-price VALUE/availability/captured_at) but a DIFFERENT material
	// field OUTSIDE the key subset — the list-price UNIT and TEXT change. This is a
	// materially different evidence envelope that must not be discarded as a replay.
	conflicting := first
	conflicting.ListPrice = money.NewRawAmount("۱۵۰٬۰۰۰ تومان", "1500000", "IRR-toman")
	if obs.DedupKey(conflicting) != obs.DedupKey(first) {
		t.Fatalf("test setup: the two captures must share a dedup key; got %s vs %s",
			obs.DedupKey(conflicting), obs.DedupKey(first))
	}

	confRes, ingestErr := svc.Ingest(ctx, conflicting)

	// (a) The second ingest must NOT be reported as a Deduped:true success.
	if ingestErr == nil && confRes.Deduped {
		t.Fatal("a material out-of-key evidence change must NOT collapse into a Deduped:true replay (issue #44)")
	}
	// It must produce an EXPLICIT conflict outcome, distinguishable from success.
	if !errors.Is(ingestErr, obs.ErrDedupEvidenceConflict) && !confRes.Conflict {
		t.Fatalf("material dedup conflict must fail closed with an explicit conflict outcome, got err=%v res=%+v", ingestErr, confRes)
	}

	// (b) The conflict is recorded in the APPEND-ONLY conflict table.
	var conflictCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM observation_dedup_conflicts WHERE target_id = $1`, target,
	).Scan(&conflictCount); err != nil {
		t.Fatalf("query conflict table: %v", err)
	}
	if conflictCount != 1 {
		t.Fatalf("expected exactly 1 append-only conflict record, got %d", conflictCount)
	}

	// The conflict row preserves BOTH the stored and the conflicting evidence hash,
	// and they differ (the material change is captured).
	var storedHash, conflictingHash string
	if err := pool.QueryRow(ctx,
		`SELECT stored_evidence_hash, conflicting_evidence_hash
		   FROM observation_dedup_conflicts WHERE target_id = $1`, target,
	).Scan(&storedHash, &conflictingHash); err != nil {
		t.Fatalf("read conflict hashes: %v", err)
	}
	if storedHash == "" || conflictingHash == "" {
		t.Fatalf("conflict row must preserve both evidence hashes, got %q / %q", storedHash, conflictingHash)
	}
	if storedHash == conflictingHash {
		t.Fatal("a material conflict must record DIFFERENT stored vs conflicting evidence hashes")
	}

	// (c) The original evidence remains and the authoritative current offer is not
	// overwritten by the conflicting capture: exactly ONE observed offer, and the
	// original list-price unit is intact on the current offer.
	offers, err := svc.ListObservedOffers(ctx, account)
	if err != nil {
		t.Fatalf("list offers: %v", err)
	}
	if len(offers) != 1 {
		t.Fatalf("a material conflict must not create a duplicate current offer, got %d", len(offers))
	}
	if offers[0].ListPriceRawUnit != "IRR-rial" {
		t.Fatalf("the conflicting capture must not overwrite the authoritative current offer, list_price_raw_unit=%q", offers[0].ListPriceRawUnit)
	}

	// The original evidence must be preserved append-only for review.
	rows, err := svc.ListObservations(ctx, target, 100)
	if err != nil {
		t.Fatalf("list observations: %v", err)
	}
	if len(rows) < 1 {
		t.Fatalf("the original evidence must be preserved, got %d rows", len(rows))
	}
}

func containsRoute(routes []byte, want string) bool {
	// routes is jsonb; a substring check is sufficient for the fixed route tokens.
	return len(routes) > 0 && contains(string(routes), want)
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
