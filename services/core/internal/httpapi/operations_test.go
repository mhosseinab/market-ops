package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"

	gateway "github.com/mhosseinab/market-ops/gen/go"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/observation"
)

// TestListMarketConflictsSurfacesPerRouteEvidence is the issue #94 transport proof:
// GET /market/conflicts surfaces each conflicted offer's per-route disagreeing
// evidence on the wire. An offer with two in-window routes reports state `available`
// with both routes' raw values; an offer whose comparison evidence is no longer
// inspectable reports the EXPLICIT fail-closed `unavailable` state with no routes —
// never a fabricated complete panel and never client-side inference.
func TestListMarketConflictsSurfacesPerRouteEvidence(t *testing.T) {
	acct := uuid.New()
	fo := &fakeObservation{conflicts: []observation.ConflictView{
		{
			Offer: db.ObservedOffer{ID: uuid.New(), TargetID: uuid.New(), MarketplaceAccountID: acct, OfferIdentity: "nv-1:seller-9", Quality: "conflicted", AvailabilityStatus: "in_stock"},
			Evidence: observation.ConflictEvidence{Available: true, Routes: []observation.ConflictRouteEvidence{
				{Route: "route_c", Value: "100", Unit: "IRR-rial", AvailabilityStatus: "in_stock"},
				{Route: "route_a", Value: "200", Unit: "IRR-rial", AvailabilityStatus: "in_stock"},
			}},
		},
		{
			Offer:    db.ObservedOffer{ID: uuid.New(), TargetID: uuid.New(), MarketplaceAccountID: acct, OfferIdentity: "nv-2:seller-9", Quality: "conflicted", AvailabilityStatus: "in_stock"},
			Evidence: observation.ConflictEvidence{Available: false},
		},
	}}
	srv, tok := systemOwnerServerForOrg(t, uuid.New(), WithObservation(fo))
	rec := getJSON(t, srv, tok, "/market/conflicts?marketplaceAccountId="+acct.String(), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /market/conflicts = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var list gateway.ObservedOfferList
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(list.Items) != 2 {
		t.Fatalf("want 2 conflicted offers, got %d", len(list.Items))
	}
	avail := list.Items[0]
	if avail.ConflictEvidence == nil || avail.ConflictEvidence.State != gateway.ConflictEvidenceStateAvailable {
		t.Fatalf("first offer must carry available conflictEvidence, got %+v", avail.ConflictEvidence)
	}
	if len(avail.ConflictEvidence.Routes) != 2 {
		t.Fatalf("available evidence must list 2 routes, got %d", len(avail.ConflictEvidence.Routes))
	}
	if avail.ConflictEvidence.Routes[0].Value != "100" || avail.ConflictEvidence.Routes[1].Value != "200" {
		t.Fatalf("per-route values not surfaced verbatim: %+v", avail.ConflictEvidence.Routes)
	}
	unavail := list.Items[1]
	if unavail.ConflictEvidence == nil || unavail.ConflictEvidence.State != gateway.ConflictEvidenceStateUnavailable {
		t.Fatalf("second offer must carry the explicit unavailable state, got %+v", unavail.ConflictEvidence)
	}
	if len(unavail.ConflictEvidence.Routes) != 0 {
		t.Fatalf("unavailable evidence must list no routes, got %d", len(unavail.ConflictEvidence.Routes))
	}
}
