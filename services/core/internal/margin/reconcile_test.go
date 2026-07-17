package margin

import "testing"

// TestReconcile_SyntheticSettlements is the Gate 0a reconciliation check: the
// contribution engine must reproduce every synthetic settlement example within
// its declared tolerance. Real settlement examples arrive at S35 (GATED); the
// five synthetic examples here exercise absolute-only, rate rounding away, exact
// half-to-even rounding, Partial-readiness analysis, and a negative contribution.
func TestReconcile_SyntheticSettlements(t *testing.T) {
	examples, err := LoadSettlementExamples()
	if err != nil {
		t.Fatalf("LoadSettlementExamples: %v", err)
	}
	if len(examples) < 5 {
		t.Fatalf("expected at least 5 synthetic settlement examples, got %d", len(examples))
	}
	for _, r := range Reconcile(examples) {
		if r.Err != nil {
			t.Errorf("%s: reconcile error: %v", r.Name, r.Err)
			continue
		}
		if !r.Matched {
			t.Errorf("%s: engine %s vs expected %s (delta %d mantissa)",
				r.Name, r.Got.String(), r.Expected.String(), r.DeltaMantissa)
		}
	}
}
