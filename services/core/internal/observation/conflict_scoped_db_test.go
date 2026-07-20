package observation_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mhosseinab/market-ops/services/core/internal/money"
	obs "github.com/mhosseinab/market-ops/services/core/internal/observation"
)

// TestMarketConflictsForOrgSurfacesPerRouteEvidence is the issue #94 read-seam proof
// at the SERVICE boundary: a cross-route conflicted Observed Offer is returned with
// its per-route DISAGREEING evidence surfaced verbatim from the existing in-window
// query (no recompute) — the operator can inspect WHICH routes disagree and their
// raw values, while the offer stays Conflicted (blocked). Tenant scoping (issue
// #131/#237) is proven too: organization B naming A's account is the uniform
// not-found and never sees A's conflict evidence.
func TestMarketConflictsForOrgSurfacesPerRouteEvidence(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()

	accountA, variantA, nvA, npA := seedVariant(t, q)
	orgA := orgForAccount(t, pool, accountA)
	insertConfirmedIdentity(t, pool, accountA, variantA, nvA, npA)

	accountB, _, _, _ := seedVariant(t, q)
	orgB := orgForAccount(t, pool, accountB)

	clk := &clock{t: time.Now().UTC()}
	svc := obs.NewService(pool).WithClock(clk.now)
	created, err := svc.SyncTargetsFromConfirmed(ctx, accountA)
	if err != nil || len(created) != 1 {
		t.Fatalf("sync A targets: %v (n=%d)", err, len(created))
	}
	target := created[0].ID

	// Route C observes 100; Route A (in window) disagrees at 200 → Conflicted.
	cCap := captureFor(target, accountA, nvA, obs.RouteC, clk.now())
	cCap.Price = money.NewRawAmount("100", "100", "IRR-rial")
	if _, err := svc.Ingest(ctx, cCap); err != nil {
		t.Fatalf("ingest C: %v", err)
	}
	aCap := captureFor(target, accountA, nvA, obs.RouteA, clk.now().Add(time.Second))
	aCap.Price = money.NewRawAmount("200", "200", "IRR-rial")
	res, err := svc.Ingest(ctx, aCap)
	if err != nil {
		t.Fatalf("ingest A: %v", err)
	}
	if res.Quality != obs.Conflicted {
		t.Fatalf("route disagreement must be Conflicted, got %s", res.Quality)
	}

	// --- A reads its OWN conflict with per-route evidence. ---------------------
	views, err := svc.ListMarketConflictsForOrg(ctx, orgA, accountA)
	if err != nil {
		t.Fatalf("A listing its conflicts: %v", err)
	}
	if len(views) != 1 {
		t.Fatalf("want 1 conflicted offer, got %d", len(views))
	}
	ev := views[0].Evidence
	if !ev.Available {
		t.Fatal("two in-window routes disagree — evidence must be Available (not the fail-closed unavailable state)")
	}
	if len(ev.Routes) != 2 {
		t.Fatalf("want per-route evidence for 2 routes, got %d", len(ev.Routes))
	}
	byRoute := map[string]string{}
	for _, r := range ev.Routes {
		byRoute[r.Route] = r.Value
	}
	if byRoute["route_c"] != "100" || byRoute["route_a"] != "200" {
		t.Fatalf("disagreeing values not surfaced verbatim: %+v", byRoute)
	}

	// --- Organization B is denied A's conflict evidence (uniform not-found). ----
	if _, err := svc.ListMarketConflictsForOrg(ctx, orgB, accountA); !errors.Is(err, obs.ErrAccountNotFound) {
		t.Fatalf("B listing A's conflicts: err=%v, want ErrAccountNotFound (no oracle)", err)
	}
	if _, err := svc.ListMarketConflictsForOrg(ctx, orgB, uuid.New()); !errors.Is(err, obs.ErrAccountNotFound) {
		t.Fatalf("B naming an unknown account: err=%v, want ErrAccountNotFound", err)
	}
}
