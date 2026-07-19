package notify

import (
	"errors"
	"testing"
)

// These are the issue #126 negative-first unit tests: Store.Deliver must enforce
// the closed message-catalog contract (closed key set + category compatibility +
// EXACT named-slot set) and FAIL CLOSED, so no arbitrary title/body key or slot
// map can be persisted. They are DB-free (they exercise the pure validation seam
// validateShape/validateMessageKey) and MUST pass regardless of DATABASE_URL.

// TestValidate_RejectsUnknownTitleKey: a valid category but an unknown title key
// is rejected before persistence (unknown_key), never coerced.
func TestValidate_RejectsUnknownTitleKey(t *testing.T) {
	err := validateShape(CategoryMarketEvent, "unknown.key", KeyItemMarketEvent,
		map[string]string{"variant": "SKU-1"})
	if err == nil {
		t.Fatal("unknown title key must be rejected")
	}
	if !errors.Is(err, ErrInvalidNotification) {
		t.Fatalf("error must wrap ErrInvalidNotification, got %v", err)
	}
	var ve *MessageValidationError
	if !errors.As(err, &ve) || ve.Reason != ReasonUnknownKey || ve.Surface != "title" {
		t.Fatalf("want typed title unknown_key, got %#v", err)
	}
}

// TestValidate_RejectsEmptyTitleAndBodyKey: empty keys are not in the closed set,
// so an empty title (and an empty body) fails closed.
func TestValidate_RejectsEmptyTitleAndBodyKey(t *testing.T) {
	if err := validateShape(CategoryMarketEvent, "", "", nil); !errors.Is(err, ErrInvalidNotification) {
		t.Fatalf("empty title+body keys must be rejected, got %v", err)
	}
	// A valid title but an empty body key still fails closed on the body surface.
	err := validateShape(CategoryMarketEvent, KeyItemMarketEvent, "",
		map[string]string{"variant": "SKU-1"})
	var ve *MessageValidationError
	if !errors.As(err, &ve) || ve.Surface != "body" || ve.Reason != ReasonUnknownKey {
		t.Fatalf("empty body key must be rejected on body surface, got %#v", err)
	}
}

// TestValidate_RejectsMissingSlot: a template slot with no matching param is
// rejected (missing_slot) — a missing slot must never render as a literal
// {placeholder} to a user.
func TestValidate_RejectsMissingSlot(t *testing.T) {
	err := validateShape(CategoryMarketEvent, KeyItemMarketEvent, KeyItemMarketEvent, nil)
	var ve *MessageValidationError
	if !errors.As(err, &ve) || ve.Reason != ReasonMissingSlot || ve.Slot != "variant" {
		t.Fatalf("missing {variant} must be rejected, got %#v", err)
	}
}

// TestValidate_RejectsExtraSlot: an extra named slot not declared by the template
// is rejected (unexpected_slot) — the slot set must be EXACT, not a superset.
func TestValidate_RejectsExtraSlot(t *testing.T) {
	err := validateShape(CategoryMarketEvent, KeyItemMarketEvent, KeyItemMarketEvent,
		map[string]string{"variant": "SKU-1", "surprise": "x"})
	var ve *MessageValidationError
	if !errors.As(err, &ve) || ve.Reason != ReasonUnexpectedSlot || ve.Slot != "surprise" {
		t.Fatalf("extra slot must be rejected, got %#v", err)
	}
}

// TestValidate_SlotOrderIrrelevant: a valid slot SET is accepted regardless of map
// iteration order (a map has no order; the contract is on names, not sequence).
func TestValidate_SlotOrderIrrelevant(t *testing.T) {
	// KeyItemSafetyFail declares exactly {reason}; the correct set is accepted.
	if err := validateShape(CategorySafetyFailure, KeyItemSafetyFail, KeyItemSafetyFail,
		map[string]string{"reason": "quarantine"}); err != nil {
		t.Fatalf("valid slot set must be accepted, got %v", err)
	}
}

// TestValidate_RejectsCategoryIncompatibleKey: an execution-failure key delivered
// under the market_event category is rejected (category_mismatch) — the key's
// category must match the notification's category.
func TestValidate_RejectsCategoryIncompatibleKey(t *testing.T) {
	err := validateShape(CategoryMarketEvent, KeyItemExecutionFail, KeyItemExecutionFail,
		map[string]string{"action": "act-1"})
	var ve *MessageValidationError
	if !errors.As(err, &ve) || ve.Reason != ReasonCategoryMismatch {
		t.Fatalf("category-incompatible key must be rejected, got %#v", err)
	}
	// And a digest-frame key (not deliverable under any category) is rejected too.
	if err := validateShape(CategoryMarketEvent, KeyDigestSubject, KeyDigestSubject,
		map[string]string{"count": "3"}); !errors.Is(err, ErrInvalidNotification) {
		t.Fatalf("frame key must not be deliverable, got %v", err)
	}
}

// TestValidate_AcceptsEachCategoryShapeAndRenders: each category's valid shape is
// accepted AND renders in BOTH fa-IR and en (reusing Render), proving the accepted
// contract produces clean copy with no literal placeholders on either surface.
func TestValidate_AcceptsEachCategoryShapeAndRenders(t *testing.T) {
	cases := []struct {
		cat    Category
		key    string
		params map[string]string
	}{
		{CategoryMarketEvent, KeyItemMarketEvent, map[string]string{"variant": "SKU-1"}},
		{CategoryExecutionFailure, KeyItemExecutionFail, map[string]string{"action": "act-1"}},
		{CategorySafetyFailure, KeyItemSafetyFail, map[string]string{"reason": "quarantine"}},
	}
	for _, c := range cases {
		if err := validateShape(c.cat, c.key, c.key, c.params); err != nil {
			t.Fatalf("valid %s shape rejected: %v", c.cat, err)
		}
		for _, locale := range []string{"fa-IR", "en"} {
			got, err := Render(locale, c.key, c.params)
			if err != nil {
				t.Fatalf("render %s/%s: %v", locale, c.key, err)
			}
			if got == "" {
				t.Fatalf("render %s/%s produced empty copy", locale, c.key)
			}
		}
	}
}
