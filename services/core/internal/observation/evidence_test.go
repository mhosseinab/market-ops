package observation_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mhosseinab/market-ops/services/core/internal/money"
	obs "github.com/mhosseinab/market-ops/services/core/internal/observation"
)

// completeCapture is a fully-populated valid capture (OBS-002).
func completeCapture() obs.Capture {
	return obs.Capture{
		TargetID:        uuid.New(),
		Account:         uuid.New(),
		NativeVariantID: 12345,
		Route:           obs.RouteC,
		SourceType:      obs.SourcePublicWebEndpoint,
		ParserVersion:   "p1.0.0",
		EvidenceRef:     "fixture://abc",
		Availability:    obs.InStock,
		Confidence:      obs.ConfPartiallyVerified,
		CapturedAt:      time.Now(),
		Price:           money.NewRawAmount("۱٬۲۰۰٬۰۰۰ ریال", "1200000", "IRR-rial"),
	}
}

// TestValidateRejectsIncompleteEvidence asserts the schema validation REJECTS a
// capture missing any OBS-002 required field (schema validation rejects incomplete
// evidence).
func TestValidateRejectsIncompleteEvidence(t *testing.T) {
	if err := completeCapture().Validate(); err != nil {
		t.Fatalf("complete capture must validate, got %v", err)
	}

	cases := []struct {
		name   string
		mutate func(c *obs.Capture)
	}{
		{"missing targetId", func(c *obs.Capture) { c.TargetID = uuid.Nil }},
		{"missing account", func(c *obs.Capture) { c.Account = uuid.Nil }},
		{"missing nativeVariantId", func(c *obs.Capture) { c.NativeVariantID = 0 }},
		{"missing route", func(c *obs.Capture) { c.Route = "" }},
		{"missing sourceType", func(c *obs.Capture) { c.SourceType = "" }},
		{"missing parserVersion", func(c *obs.Capture) { c.ParserVersion = "" }},
		{"missing evidenceRef", func(c *obs.Capture) { c.EvidenceRef = "" }},
		{"missing availability", func(c *obs.Capture) { c.Availability = "" }},
		{"missing confidence", func(c *obs.Capture) { c.Confidence = "" }},
		{"missing capturedAt", func(c *obs.Capture) { c.CapturedAt = time.Time{} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := completeCapture()
			tc.mutate(&c)
			err := c.Validate()
			if err == nil {
				t.Fatalf("expected rejection for %s", tc.name)
			}
			if !errors.Is(err, obs.ErrIncompleteEvidence) {
				t.Fatalf("expected ErrIncompleteEvidence, got %v", err)
			}
		})
	}
}

// TestHasCurrentPriceValue is the issue #43 guard: a capture carries a usable
// CURRENT price value only when its availability bears a value AND the raw price is
// structurally complete (Text, Value, Unit all present). An empty/absent price on
// an otherwise in-stock capture must NOT count as a current value — it fails closed.
func TestHasCurrentPriceValue(t *testing.T) {
	full := money.NewRawAmount("۱٬۲۰۰٬۰۰۰ ریال", "1200000", "IRR-rial")
	cases := []struct {
		name         string
		availability obs.Availability
		price        money.RawAmount
		want         bool
	}{
		{"in_stock + empty price", obs.InStock, money.RawAmount{}, false},
		{"in_stock + whitespace price", obs.InStock, money.NewRawAmount("  ", "\t", "\n"), false},
		{"in_stock + missing unit", obs.InStock, money.NewRawAmount("1200000", "1200000", ""), false},
		{"disappeared + full price", obs.Disappeared, full, false},
		{"in_stock + full price", obs.InStock, full, true},
		{"out_of_stock + full price", obs.OutOfStock, full, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := completeCapture()
			c.Availability = tc.availability
			c.Price = tc.price
			if got := c.HasCurrentPriceValue(); got != tc.want {
				t.Fatalf("HasCurrentPriceValue() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestDedupKeyProvenance asserts the dedup key is stable for a true replay yet
// distinct across routes (route provenance preserved, OBS-008) and across capture
// instants (genuine new evidence).
func TestDedupKeyProvenance(t *testing.T) {
	c := completeCapture()

	replay := c
	if obs.DedupKey(c) != obs.DedupKey(replay) {
		t.Fatal("dedup key must be stable for an identical replay")
	}

	// Same value, different route → distinct key (both routes retained).
	b := c
	b.Route = obs.RouteB
	if obs.DedupKey(c) == obs.DedupKey(b) {
		t.Fatal("different routes must produce distinct dedup keys (provenance)")
	}

	// Same value/route, different instant → distinct key (new evidence).
	later := c
	later.CapturedAt = c.CapturedAt.Add(time.Minute)
	if obs.DedupKey(c) == obs.DedupKey(later) {
		t.Fatal("different capture instants must produce distinct dedup keys")
	}
}

// TestTierWindows asserts the freshness tiers are data (PRD §10.1).
func TestTierWindows(t *testing.T) {
	cases := []struct {
		tier          obs.Tier
		wantCadence   time.Duration
		wantFreshness time.Duration
	}{
		{obs.TierPriority, 60 * time.Minute, 60 * time.Minute},
		{obs.TierStandard, 6 * time.Hour, 6 * time.Hour},
		{obs.TierBackground, 24 * time.Hour, 24 * time.Hour},
	}
	for _, tc := range cases {
		cad, fresh := obs.TierWindow(tc.tier)
		if cad != tc.wantCadence || fresh != tc.wantFreshness {
			t.Errorf("%s: got (%s,%s) want (%s,%s)", tc.tier, cad, fresh, tc.wantCadence, tc.wantFreshness)
		}
	}
}
