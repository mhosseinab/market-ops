package notify

import (
	"testing"
)

// TestCategoryBypass is the safety-bypass rule as a pure unit (written first): only
// execution and safety failures bypass the batched digest; a market event never
// does. This is the single source the store trusts to set bypass_digest.
func TestCategoryBypass(t *testing.T) {
	if !CategoryExecutionFailure.BypassesDigest() {
		t.Fatal("execution failure must bypass the digest")
	}
	if !CategorySafetyFailure.BypassesDigest() {
		t.Fatal("safety failure must bypass the digest")
	}
	if CategoryMarketEvent.BypassesDigest() {
		t.Fatal("market event must NOT bypass the digest")
	}
}

// TestRender_FailsClosedOnUnknownLocale proves the locale boundary never silently
// falls back: an unknown locale is an error, not English.
func TestRender_FailsClosedOnUnknownLocale(t *testing.T) {
	if _, err := Render("de-DE", KeyDigestFooter, nil); err == nil {
		t.Fatal("Render accepted an unknown locale (must fail closed)")
	}
}

// TestRender_FailsClosedOnUnknownKey proves a missing key is an error, never
// invented copy.
func TestRender_FailsClosedOnUnknownKey(t *testing.T) {
	if _, err := Render("en", "notify.does.not.exist", nil); err == nil {
		t.Fatal("Render accepted an unknown key (must fail closed)")
	}
}

// TestRender_FillsNamedSlots proves named-slot substitution (LOC-002) works and is
// deterministic.
func TestRender_FillsNamedSlots(t *testing.T) {
	got, err := Render("en", KeyItemMarketEvent, map[string]string{"variant": "SKU-1"})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if got != "Market event for “SKU-1”" {
		t.Fatalf("slot fill = %q", got)
	}
	// Every locale pack must resolve the same key (coverage across packs).
	if _, err := Render("fa-IR", KeyItemMarketEvent, map[string]string{"variant": "SKU-1"}); err != nil {
		t.Fatalf("fa-IR render: %v", err)
	}
}
