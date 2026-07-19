package routec_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/identity"
	"github.com/mhosseinab/market-ops/services/core/internal/money"
	"github.com/mhosseinab/market-ops/services/core/internal/observation"
	"github.com/mhosseinab/market-ops/services/core/internal/routec"
)

func dbPool(t *testing.T) (*pgxpool.Pool, *db.Queries) {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set; skipping routec DB test")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("connect pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool, db.New(pool)
}

// seedConfirmedTarget seeds org/account/product/variant + a Confirmed identity,
// then creates the observation target for it. Returns account, identity, target,
// and native ids.
func seedConfirmedTarget(t *testing.T, pool *pgxpool.Pool, q *db.Queries) (account, identityID, targetID uuid.UUID, nativeVariant, nativeProduct int64) {
	t.Helper()
	ctx := context.Background()
	org, err := q.CreateOrganization(ctx, "routec-test-"+uuid.NewString())
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	acct, err := q.CreateMarketplaceAccount(ctx, db.CreateMarketplaceAccountParams{
		OrganizationID:  org.ID,
		NativeAccountID: "native-" + uuid.NewString(),
		DisplayName:     "RouteC Seller",
	})
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	nativeProduct = int64(uuid.New().ID())
	nativeVariant = int64(uuid.New().ID())
	prod, err := q.UpsertProduct(ctx, db.UpsertProductParams{
		MarketplaceAccountID: acct.ID, NativeProductID: nativeProduct, Title: "Widget",
	})
	if err != nil {
		t.Fatalf("upsert product: %v", err)
	}
	v, err := q.UpsertVariant(ctx, db.UpsertVariantParams{
		MarketplaceAccountID: acct.ID, ProductID: prod.ID,
		NativeVariantID: nativeVariant, NativeProductID: nativeProduct,
		SupplierCode: "SKU-" + uuid.NewString()[:8], Title: "Widget - Red",
	})
	if err != nil {
		t.Fatalf("upsert variant: %v", err)
	}
	var idID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO market_product_identities
		    (marketplace_account_id, variant_id, native_variant_id, native_product_id, state, active, version)
		VALUES ($1,$2,$3,$4,'confirmed',true,1) RETURNING id`,
		acct.ID, v.ID, nativeVariant, nativeProduct).Scan(&idID); err != nil {
		t.Fatalf("insert confirmed identity: %v", err)
	}
	created, err := observation.NewService(pool).SyncTargetsFromConfirmed(ctx, acct.ID)
	if err != nil {
		t.Fatalf("sync targets: %v", err)
	}
	if len(created) != 1 {
		t.Fatalf("expected 1 target created, got %d", len(created))
	}
	return acct.ID, idID, created[0].ID, nativeVariant, nativeProduct
}

// TestReopenRetiresTargets is the S13 carry-forward: reopening a Confirmed
// identity DEACTIVATES its observation target, so a reopened identity stops
// producing executable observations. The wiring is identity.Service ->
// TargetRetirer (EventSink) -> DeactivateObservationTargetsForIdentity.
func TestReopenRetiresTargets(t *testing.T) {
	pool, q := dbPool(t)
	ctx := context.Background()
	account, idID, targetID, _, _ := seedConfirmedTarget(t, pool, q)

	// Target starts active.
	if n, _ := q.CountActiveTargetsForIdentity(ctx, idID); n != 1 {
		t.Fatalf("expected 1 active target before reopen, got %d", n)
	}
	// It appears in the tier enumeration (standard tier).
	src := routec.NewDBTargetSource(pool)
	before, err := src.TargetsByTier(ctx, observation.TierStandard)
	if err != nil {
		t.Fatalf("targets by tier: %v", err)
	}
	if !containsTarget(before, targetID) {
		t.Fatal("active target missing from standard-tier enumeration")
	}

	// Reopen with the retirer wired as the sink.
	retirer := routec.NewTargetRetirer(pool)
	svc := identity.NewService(pool, retirer)
	if _, err := svc.Reopen(ctx, idID, identity.ReasonVariantConflict, identity.Actor(uuid.Nil)); err != nil {
		t.Fatalf("reopen: %v", err)
	}

	// Target is now deactivated: no active target for the identity.
	if n, _ := q.CountActiveTargetsForIdentity(ctx, idID); n != 0 {
		t.Fatalf("expected 0 active targets after reopen, got %d", n)
	}
	// And it is gone from the tier enumeration, so the scheduler never fetches it.
	after, err := src.TargetsByTier(ctx, observation.TierStandard)
	if err != nil {
		t.Fatalf("targets by tier after: %v", err)
	}
	if containsTarget(after, targetID) {
		t.Fatal("retired target still enumerated for fetching (OBS-001 leak)")
	}
	_ = account
}

func containsTarget(refs []routec.TargetRef, id uuid.UUID) bool {
	for _, r := range refs {
		if r.TargetID == id {
			return true
		}
	}
	return false
}

// TestRouteCAloneNeverManufacturesVerified is the S13 carry-forward-2 check:
// Route C has its own SLA/cadence, but repeated Route C captures must NOT
// manufacture false corroboration. Two distinct in-window Route C sightings reach
// Supported (history), never Verified (which requires a DIFFERENT route agreeing
// within window). This proves same-route cadence cannot self-certify Verified.
func TestRouteCAloneNeverManufacturesVerified(t *testing.T) {
	pool, q := dbPool(t)
	ctx := context.Background()
	account, _, targetID, nativeVariant, _ := seedConfirmedTarget(t, pool, q)
	svc := observation.NewService(pool)

	base := time.Now().UTC().Add(-30 * time.Minute)
	cap := func(at time.Time) observation.Capture {
		return observation.Capture{
			TargetID:        targetID,
			Account:         account,
			NativeVariantID: nativeVariant,
			NativeSellerID:  "71001",
			Route:           observation.RouteC,
			SourceType:      observation.SourcePublicWebEndpoint,
			SourceURL:       "https://api.digikala.com/v2/product/1/",
			ParserVersion:   routec.ParserVersion,
			EvidenceRef:     "routec-test",
			Price:           money.NewRawAmount("450000000", "450000000", "IRR-rial"),
			Availability:    observation.InStock,
			CapturedAt:      at,
			Confidence:      observation.ConfPartiallyVerified,
			SchemaValid:     true,
		}
	}

	first, err := svc.Ingest(ctx, cap(base))
	if err != nil {
		t.Fatalf("first ingest: %v", err)
	}
	if first.Quality != observation.Unverified {
		t.Fatalf("first Route C sighting: got %q want unverified (no history yet)", first.Quality)
	}
	// A second, distinct in-window Route C sighting of the same value: history →
	// Supported. NOT Verified: Route C never corroborates itself.
	second, err := svc.Ingest(ctx, cap(base.Add(10*time.Minute)))
	if err != nil {
		t.Fatalf("second ingest: %v", err)
	}
	if second.Quality == observation.Verified {
		t.Fatal("Route C manufactured Verified from a single route (false corroboration)")
	}
	if second.Quality != observation.Supported {
		t.Fatalf("second Route C sighting: got %q want supported", second.Quality)
	}
}

// TestDriftDowngradePersistsAcrossFreshProcess is the issue #47 acceptance: a
// detected drift event must DURABLY downgrade the affected target's current view
// so a FRESH process can no longer read the pre-drift value as current. It seeds a
// Confirmed target, ingests a healthy in-stock capture (whose derived current
// offer satisfies the current-data gate), then drives ObserveTarget down the
// parse-drift path wired to the REAL observation.Service as both ingestor and
// downgrader. Re-reading through a brand-new Service over the same pool must show
// the offer downgraded to stale/unavailable — the current-data gate FALSE.
func TestDriftDowngradePersistsAcrossFreshProcess(t *testing.T) {
	pool, q := dbPool(t)
	ctx := context.Background()
	account, _, targetID, nativeVariant, nativeProduct := seedConfirmedTarget(t, pool, q)

	base := time.Now().UTC()
	svc := observation.NewService(pool).WithClock(func() time.Time { return base })
	capt := observation.Capture{
		TargetID:        targetID,
		Account:         account,
		NativeVariantID: nativeVariant,
		NativeSellerID:  "71001",
		Route:           observation.RouteC,
		SourceType:      observation.SourcePublicWebEndpoint,
		SourceURL:       "https://api.digikala.com/v2/product/1/",
		ParserVersion:   routec.ParserVersion,
		EvidenceRef:     "routec-test",
		Price:           money.NewRawAmount("450000000", "450000000", "IRR-rial"),
		Availability:    observation.InStock,
		CapturedAt:      base,
		Confidence:      observation.ConfPartiallyVerified,
		SchemaValid:     true,
	}
	res, err := svc.Ingest(ctx, capt)
	if err != nil {
		t.Fatalf("seed healthy ingest: %v", err)
	}
	// Pre-drift, the current offer reads as CURRENT (Unverified satisfies the gate).
	if !res.Quality.SatisfiesCurrentDataGate() {
		t.Fatalf("pre-drift offer must read current, got quality %q", res.Quality)
	}

	// Drive a parse-drift observe wired to the real Service (ingestor + downgrader).
	f := &fakeFetcher{result: routec.FetchResult{
		StatusCode: 200,
		Body:       golden(t, "drift_missing_product.json"),
		Signal:     routec.SignalOK,
	}}
	obs := routec.NewObserver(routec.DefaultConfig(), routec.ObserverDeps{
		Fetcher:    f,
		Ingestor:   svc,
		Downgrader: svc,
		Kill:       routec.NewMemKillSwitchStore(),
		Now:        func() time.Time { return base },
	})
	ref := routec.TargetRef{
		Account: account, TargetID: targetID,
		NativeVariantID: nativeVariant, NativeProductID: nativeProduct,
		Tier: observation.TierPriority,
	}
	out, err := obs.ObserveTarget(ctx, routec.Snapshot{}, ref)
	if err != nil {
		t.Fatalf("observe drift: %v", err)
	}
	if out.Skipped != routec.SkipParseDrift {
		t.Fatalf("skip reason: got %q want parse_drift", out.Skipped)
	}
	if out.PersistedDowngrades != 1 {
		t.Fatalf("expected exactly one persisted downgrade, got %d", out.PersistedDowngrades)
	}

	// FRESH process: a brand-new Service struct over the same pool re-reads the view.
	fresh := observation.NewService(pool)
	offers, err := fresh.ListObservedOffers(ctx, account)
	if err != nil {
		t.Fatalf("fresh list observed offers: %v", err)
	}
	var found bool
	for _, o := range offers {
		if o.TargetID != targetID {
			continue
		}
		found = true
		qual := observation.Quality(o.Quality)
		if qual != observation.Stale && qual != observation.Unavailable {
			t.Fatalf("drifted offer quality: got %q want stale/unavailable", qual)
		}
		if qual.SatisfiesCurrentDataGate() {
			t.Fatal("drifted offer must NOT satisfy the current-data gate across a fresh process")
		}
	}
	if !found {
		t.Fatal("expected the target's current offer to still exist (downgraded, not deleted)")
	}

	// Idempotent: a second downgrade does not loosen the already-restricted state.
	n, err := fresh.DowngradeCurrentForDrift(ctx, targetID, "parse_drift")
	if err != nil {
		t.Fatalf("second downgrade: %v", err)
	}
	if n != 0 {
		t.Fatalf("re-running the downgrade must be a no-op, downgraded %d", n)
	}
}

// TestKillSwitchStoreRoundTrip exercises the durable kill-switch store: engage at
// each layer, load a snapshot, evaluate layered blocking, and disengage.
func TestKillSwitchStoreRoundTrip(t *testing.T) {
	pool, q := dbPool(t)
	ctx := context.Background()
	account, _, targetID, _, _ := seedConfirmedTarget(t, pool, q)
	store := routec.NewDBKillSwitchStore(pool)
	other := uuid.New()

	// Target-layer stop.
	if err := store.EngageTarget(ctx, account, targetID, "flaky parser", uuid.Nil); err != nil {
		t.Fatalf("engage target: %v", err)
	}
	snap, err := routec.LoadSnapshot(ctx, store)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if !snap.Blocked(account, targetID) {
		t.Fatal("target should be blocked by target-layer stop")
	}
	if snap.Blocked(account, other) {
		t.Fatal("a different target must not be blocked by another target's stop")
	}

	// Account-layer stop blocks every target in the account.
	if err := store.EngageAccount(ctx, account, "account incident", uuid.Nil); err != nil {
		t.Fatalf("engage account: %v", err)
	}
	snap, _ = routec.LoadSnapshot(ctx, store)
	if !snap.Blocked(account, other) {
		t.Fatal("account stop should block all its targets")
	}

	// Disengage account: target stop still applies.
	if err := store.DisengageAccount(ctx, account); err != nil {
		t.Fatalf("disengage account: %v", err)
	}
	snap, _ = routec.LoadSnapshot(ctx, store)
	if snap.Blocked(account, other) {
		t.Fatal("account stop should be cleared")
	}
	if !snap.Blocked(account, targetID) {
		t.Fatal("target stop should persist after account disengage")
	}
}
