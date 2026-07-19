package routec_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mhosseinab/market-ops/services/core/internal/observation"
	"github.com/mhosseinab/market-ops/services/core/internal/routec"
)

func golden(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read golden %s: %v", name, err)
	}
	return b
}

// TestParseMarketableGolden asserts the parser extracts every seller offer with
// its RAW price preserved as evidence (money quarantine) — never converted and
// never promoted to Money.
func TestParseMarketableGolden(t *testing.T) {
	parsed, err := routec.ParseProductDetail(golden(t, "product_marketable.json"))
	if err != nil {
		t.Fatalf("parse marketable: %v", err)
	}
	if parsed.ProductID != 900100 {
		t.Fatalf("product id: got %d want 900100", parsed.ProductID)
	}
	if parsed.Unavailable {
		t.Fatal("marketable product marked unavailable")
	}
	if len(parsed.Offers) != 2 {
		t.Fatalf("offers: got %d want 2", len(parsed.Offers))
	}
	o := parsed.Offers[0]
	if o.NativeVariantID != 555001 {
		t.Fatalf("variant id: got %d want 555001", o.NativeVariantID)
	}
	if o.SellerCode != "H4SHM" {
		t.Fatalf("seller code: got %q want H4SHM", o.SellerCode)
	}
	// Money quarantine: value is the verbatim integer token; unit is the raw
	// source-unit label, NOT an ISO currency and NOT converted to Toman.
	if o.Price.Value != "450000000" {
		t.Fatalf("price value: got %q want 450000000 (verbatim, no conversion)", o.Price.Value)
	}
	if o.Price.Unit != "IRR-rial" {
		t.Fatalf("price unit: got %q want IRR-rial (raw source unit)", o.Price.Unit)
	}
	if o.ListPrice.Value != "500000000" {
		t.Fatalf("list price value: got %q want 500000000", o.ListPrice.Value)
	}
	if o.Availability != observation.InStock {
		t.Fatalf("availability: got %q want in_stock", o.Availability)
	}
	if o.Stock == nil || *o.Stock != 12 {
		t.Fatalf("stock: got %v want 12", o.Stock)
	}
	if res := routec.Canary(parsed); !res.Passed {
		t.Fatalf("canary should pass on golden: %v", res.Reasons)
	}
}

// TestParseUnavailableGolden asserts an empty-variants product is a VALID
// unavailable state with no invented price, and passes the canary.
func TestParseUnavailableGolden(t *testing.T) {
	parsed, err := routec.ParseProductDetail(golden(t, "product_unavailable.json"))
	if err != nil {
		t.Fatalf("parse unavailable: %v", err)
	}
	if !parsed.Unavailable {
		t.Fatal("empty-variants product not marked unavailable")
	}
	if len(parsed.Offers) != 0 {
		t.Fatalf("unavailable product must invent no offers, got %d", len(parsed.Offers))
	}
	if res := routec.Canary(parsed); !res.Passed {
		t.Fatalf("canary must pass for a valid unavailable product: %v", res.Reasons)
	}
}

// TestParseDriftMarketableNoPrice asserts a marketable variant with no
// selling_price is drift — never coerced to a zero price.
func TestParseDriftMarketableNoPrice(t *testing.T) {
	_, err := routec.ParseProductDetail(golden(t, "drift_marketable_no_price.json"))
	if err == nil {
		t.Fatal("expected drift error for marketable variant without price")
	}
}

// TestParseDriftMissingProduct asserts a payload missing data.product is drift.
func TestParseDriftMissingProduct(t *testing.T) {
	_, err := routec.ParseProductDetail(golden(t, "drift_missing_product.json"))
	if err == nil {
		t.Fatal("expected drift error for payload missing data.product")
	}
}

// TestUnknownAvailabilityFailsClosed is the issue #46 regression guard: a
// variant whose source status is missing (absent field), blank/whitespace-only,
// or a novel unrecognized token MUST fail closed to observation.TempUnavail
// (unavailable/unverified) and MUST NEVER be promoted to a purchasable state
// (InStock/Limited). Only an allow-listed positive token may assert stock
// (CLAUDE.md §4.6 quarantine-over-inference; docs/11 "do not infer from price").
func TestUnknownAvailabilityFailsClosed(t *testing.T) {
	cases := []struct {
		name    string
		fixture string
	}{
		{"missing/absent status field", "availability_missing.json"},
		{"blank/whitespace-only status", "availability_blank.json"},
		{"novel/unmapped status token", "availability_novel.json"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			parsed, err := routec.ParseProductDetail(golden(t, tc.fixture))
			if err != nil {
				// Fail-closed to an unavailable OUTCOME is mandatory; a novel
				// token must not become a hard parse-abort error (§10.4).
				t.Fatalf("unknown availability must produce an outcome, not an error: %v", err)
			}
			if len(parsed.Offers) != 1 {
				t.Fatalf("offers: got %d want 1", len(parsed.Offers))
			}
			got := parsed.Offers[0].Availability
			if got == observation.InStock || got == observation.Limited {
				t.Fatalf("unknown availability leaked a purchasable state %q (manufactured stock claim)", got)
			}
			if got != observation.TempUnavail {
				t.Fatalf("availability: got %q want %q (fail closed)", got, observation.TempUnavail)
			}
		})
	}
}

// TestCanaryDistributionZeroPriced asserts the value/unit distribution check
// flags an all-zero-priced marketable payload as drift.
func TestCanaryDistributionZeroPriced(t *testing.T) {
	body := []byte(`{"status":200,"data":{"product":{"id":1,"status":"marketable","variants":[
		{"id":10,"status":"marketable","seller":{"id":1,"code":"X"},"price":{"selling_price":0,"rrp_price":0}}]}}}`)
	parsed, err := routec.ParseProductDetail(body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if res := routec.Canary(parsed); res.Passed {
		t.Fatal("canary must fail when every valued offer is zero-priced")
	}
}
