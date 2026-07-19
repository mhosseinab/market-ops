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

// TestEvidenceHashDistinguishesMaterialFields is the issue #44 guard for the
// canonical evidence hash: an identical FULL envelope yields an equal, order-stable
// hash (a true replay), while a change to ANY material field OUTSIDE the dedup-key
// subset yields a DIFFERENT hash. This is what lets the service tell a genuine
// replay from a materially different capture that shares the dedup key.
func TestEvidenceHashDistinguishesMaterialFields(t *testing.T) {
	base := completeCapture()
	base.ListPrice = money.NewRawAmount("1٬500٬000 ریال", "1500000", "IRR-rial")
	base.NativeSellerID = "seller-9"
	base.SubRoute = "on-demand"
	base.SourceURL = "https://example.test/p/1"
	base.EvidenceRef = "fixture://abc"
	base.RawFixtureRef = "raw://abc"
	base.ConnectorVersion = "c1.0.0"
	base.ParsingWarnings = []string{"w1"}
	base.StockSignal = int64Ptr(3)

	h1, h2 := obs.EvidenceHash(base), obs.EvidenceHash(base)
	if h1 != h2 {
		t.Fatal("evidence hash must be deterministic for an identical envelope")
	}
	// Order-stable across an independent identical copy built field-by-field is
	// covered by determinism; here assert a struct copy is equal.
	replica := base
	if obs.EvidenceHash(base) != obs.EvidenceHash(replica) {
		t.Fatal("evidence hash must be equal for a byte-equivalent replay")
	}

	// Each of these material fields is OUTSIDE the dedup-key subset; changing any one
	// must move the hash (so it can never be discarded as a replay).
	cases := []struct {
		name   string
		mutate func(c *obs.Capture)
	}{
		{"list price unit", func(c *obs.Capture) { c.ListPrice.Unit = "IRR-toman" }},
		{"list price text", func(c *obs.Capture) { c.ListPrice.Text = "changed" }},
		{"price text", func(c *obs.Capture) { c.Price.Text = "changed" }},
		{"stock signal", func(c *obs.Capture) { c.StockSignal = int64Ptr(9) }},
		{"confidence", func(c *obs.Capture) { c.Confidence = obs.ConfVerified }},
		{"evidence ref", func(c *obs.Capture) { c.EvidenceRef = "fixture://other" }},
		{"raw fixture ref", func(c *obs.Capture) { c.RawFixtureRef = "raw://other" }},
		{"source url", func(c *obs.Capture) { c.SourceURL = "https://example.test/p/2" }},
		{"parser version", func(c *obs.Capture) { c.ParserVersion = "p2.0.0" }},
		{"connector version", func(c *obs.Capture) { c.ConnectorVersion = "c2.0.0" }},
		{"source type", func(c *obs.Capture) { c.SourceType = obs.SourceDOM }},
		{"native seller id", func(c *obs.Capture) { c.NativeSellerID = "seller-x" }},
		{"schema valid", func(c *obs.Capture) { c.SchemaValid = !c.SchemaValid }},
		{"parsing warnings", func(c *obs.Capture) { c.ParsingWarnings = []string{"w1", "w2"} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mutated := base
			// Deep-copy the warnings slice so mutation does not alias base.
			mutated.ParsingWarnings = append([]string(nil), base.ParsingWarnings...)
			tc.mutate(&mutated)
			if obs.EvidenceHash(mutated) == obs.EvidenceHash(base) {
				t.Fatalf("changing %s must change the evidence hash (issue #44)", tc.name)
			}
		})
	}
}

func int64Ptr(v int64) *int64 { return &v }

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
