package observation

import (
	"testing"
	"time"

	"github.com/mhosseinab/market-ops/services/core/internal/db"
)

// TestConflictEvidenceFromFailsClosedBelowTwoRoutes is the issue #94 fail-closed
// proof at the READ-MODEL derivation: inspecting a cross-route disagreement needs at
// least TWO in-window routes. With zero or one route the comparison evidence is
// missing/incomplete, so the evidence is reported UNAVAILABLE (Available=false, no
// routes) — the transport surfaces the EXPLICIT error state and never fabricates a
// complete panel or infers the missing route. This is written before the happy path.
func TestConflictEvidenceFromFailsClosedBelowTwoRoutes(t *testing.T) {
	for _, n := range []int{0, 1} {
		ev := conflictEvidenceFrom(rowsN(n))
		if ev.Available {
			t.Fatalf("%d in-window route(s): Available must be false (fail-closed explicit error)", n)
		}
		if len(ev.Routes) != 0 {
			t.Fatalf("%d in-window route(s): Routes must be empty, got %d", n, len(ev.Routes))
		}
	}
}

// TestConflictEvidenceFromSurfacesRoutesVerbatim is the issue #94 happy path: two or
// more in-window routes are exposed VERBATIM (raw value/unit, availability, times),
// with no recompute, so the operator can inspect the disagreement side-by-side.
func TestConflictEvidenceFromSurfacesRoutesVerbatim(t *testing.T) {
	now := time.Now().UTC()
	rows := []db.ListInWindowRouteValuesRow{
		{Route: "route_c", PriceRawValue: "100", PriceRawUnit: "IRR-rial", AvailabilityStatus: "in_stock", CapturedAt: now, FreshnessDeadline: now.Add(time.Hour)},
		{Route: "route_a", PriceRawValue: "200", PriceRawUnit: "IRR-rial", AvailabilityStatus: "in_stock", CapturedAt: now, FreshnessDeadline: now.Add(time.Hour)},
	}
	ev := conflictEvidenceFrom(rows)
	if !ev.Available {
		t.Fatal("two in-window routes must yield Available=true")
	}
	if len(ev.Routes) != 2 {
		t.Fatalf("want 2 route rows, got %d", len(ev.Routes))
	}
	if ev.Routes[0].Route != "route_c" || ev.Routes[0].Value != "100" || ev.Routes[0].Unit != "IRR-rial" {
		t.Fatalf("route_c row not surfaced verbatim: %+v", ev.Routes[0])
	}
	if ev.Routes[1].Value != "200" {
		t.Fatalf("route_a value not surfaced verbatim: %+v", ev.Routes[1])
	}
}

func rowsN(n int) []db.ListInWindowRouteValuesRow {
	now := time.Now().UTC()
	out := make([]db.ListInWindowRouteValuesRow, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, db.ListInWindowRouteValuesRow{
			Route:              "route_c",
			PriceRawValue:      "100",
			PriceRawUnit:       "IRR-rial",
			AvailabilityStatus: "in_stock",
			CapturedAt:         now,
			FreshnessDeadline:  now.Add(time.Hour),
		})
	}
	return out
}
