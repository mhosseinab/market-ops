package money

import "testing"

// TestRawAmountIsStructurallyComplete is the presence-only, negative-first guard
// for issue #43: a raw price is structurally complete ONLY when Text, Value, AND
// Unit are all non-empty after trimming surrounding whitespace. This is presence
// checking, NOT parsing — it never interprets the numeric value or the unit, so it
// stays inside the money quarantine.
func TestRawAmountIsStructurallyComplete(t *testing.T) {
	cases := []struct {
		name             string
		text, value, uni string
		want             bool
	}{
		{"all empty", "", "", "", false},
		{"empty text", "", "1200000", "IRR-rial", false},
		{"empty value", "1٬200٬000 ریال", "", "IRR-rial", false},
		{"empty unit", "1٬200٬000 ریال", "1200000", "", false},
		{"whitespace text", "   ", "1200000", "IRR-rial", false},
		{"whitespace value", "1٬200٬000 ریال", "  \t ", "IRR-rial", false},
		{"whitespace unit", "1٬200٬000 ریال", "1200000", "\n", false},
		{"all whitespace", " ", "\t", "\n", false},
		{"all present", "1٬200٬000 ریال", "1200000", "IRR-rial", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := NewRawAmount(tc.text, tc.value, tc.uni).IsStructurallyComplete()
			if got != tc.want {
				t.Fatalf("IsStructurallyComplete(%q,%q,%q) = %v, want %v",
					tc.text, tc.value, tc.uni, got, tc.want)
			}
		})
	}
}
