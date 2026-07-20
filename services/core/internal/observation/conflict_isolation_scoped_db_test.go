package observation_test

import (
	"context"
	"testing"
	"time"

	"github.com/mhosseinab/market-ops/services/core/internal/money"
	obs "github.com/mhosseinab/market-ops/services/core/internal/observation"
)

// TestUnregisteredParserCannotBlockLegitimateOffer is the #307 proof at the SERVICE
// boundary (follow-up to #154). A legitimate current offer is established from two
// agreeing REGISTERED routes (Verified). Then an UNREGISTERED-parser capture
// (bogus parserVersion → registry miss) arrives with a DISAGREEING value. Before
// the fix its disagreement drove DeriveQuality to Conflicted, and
// MarkObservedOfferConflicted blocked the legitimate offer — a signal-suppression
// vector exploitable by any holder of a valid capture credential. After the fix the
// untrusted capture floors to Unverified BEFORE conflict evaluation, so it never
// forces the offer to Conflicted. The disagreement is still retained as append-only
// evidence.
//
// DB-gated: skips without DATABASE_URL (runs in CI where Postgres is provisioned).
func TestUnregisteredParserCannotBlockLegitimateOffer(t *testing.T) {
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

	// Two agreeing REGISTERED routes in window → a legitimate corroborated offer.
	good := money.NewRawAmount("100", "100", "IRR-rial")
	cCap := captureFor(target, account, nv, obs.RouteC, clk.now())
	cCap.Price = good
	if _, err := svc.Ingest(ctx, cCap); err != nil {
		t.Fatalf("ingest C: %v", err)
	}
	aCap := captureFor(target, account, nv, obs.RouteA, clk.now().Add(time.Second))
	aCap.Price = good
	aRes, err := svc.Ingest(ctx, aCap)
	if err != nil {
		t.Fatalf("ingest A: %v", err)
	}
	if aRes.Quality != obs.Verified {
		t.Fatalf("two agreeing registered routes must corroborate to Verified, got %s", aRes.Quality)
	}

	// UNREGISTERED-parser capture (bogus parserVersion → registry miss → schema
	// withheld) that DISAGREES. It must NOT push the legitimate offer to Conflicted.
	rogue := captureFor(target, account, nv, obs.RouteB, clk.now().Add(2*time.Second))
	rogue.ParserVersion = "rogue-parser/9.9.9" // not in the server-owned registry
	rogue.Price = money.NewRawAmount("999", "999", "IRR-rial")
	res, err := svc.Ingest(ctx, rogue)
	if err != nil {
		t.Fatalf("ingest rogue: %v", err)
	}
	if res.Quality == obs.Conflicted {
		t.Fatalf("unregistered-parser disagreement must NOT force Conflicted (#307)")
	}
	if res.Quality != obs.Unverified {
		t.Fatalf("unregistered-parser capture must floor to Unverified, got %s", res.Quality)
	}

	// The disagreement is retained as append-only evidence (never silently dropped).
	rows, err := svc.ListObservations(ctx, target, 100)
	if err != nil {
		t.Fatalf("list observations: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("want 3 append-only evidence rows (C, A, rogue), got %d", len(rows))
	}
}
