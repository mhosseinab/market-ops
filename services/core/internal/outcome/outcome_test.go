package outcome

import (
	"testing"
	"time"
)

// TestEvaluate_Rule covers the §15.3 result table, including the fail-closed
// NotMeasurable path and breach-beats-improvement precedence.
func TestEvaluate_Rule(t *testing.T) {
	cases := []struct {
		name string
		in   Inputs
		want Result
	}{
		{"no evidence -> not measurable", Inputs{EvidenceComplete: false, ObjectiveImproved: true}, NotMeasurable},
		{"attribution blocked -> inconclusive", Inputs{EvidenceComplete: true, AttributionBlocked: true, ObjectiveImproved: true}, Inconclusive},
		{"floor breach -> negative even if improved", Inputs{EvidenceComplete: true, FloorBreached: true, ObjectiveImproved: true}, Negative},
		{"contribution bound breach -> negative", Inputs{EvidenceComplete: true, ContributionBreachedBound: true}, Negative},
		{"within materiality -> neutral", Inputs{EvidenceComplete: true, WithinMateriality: true, ObjectiveImproved: true}, Neutral},
		{"worsened -> negative", Inputs{EvidenceComplete: true, ObjectiveWorsened: true}, Negative},
		{"improved cleanly -> positive", Inputs{EvidenceComplete: true, ObjectiveImproved: true}, Positive},
		{"no direction -> neutral", Inputs{EvidenceComplete: true}, Neutral},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got, _ := Evaluate(tc.in); got != tc.want {
				t.Fatalf("result = %q; want %q", got, tc.want)
			}
		})
	}
}

// TestEvaluate_Confidence covers the §15.3 confidence grading by concurrent count.
func TestEvaluate_Confidence(t *testing.T) {
	cases := map[int]Confidence{0: High, 1: Medium, 2: Low, 5: Low}
	for n, want := range cases {
		if _, got := Evaluate(Inputs{EvidenceComplete: true, ConcurrentMaterialChanges: n}); got != want {
			t.Fatalf("confidence(%d) = %q; want %q", n, got, want)
		}
	}
}

// TestWindow_SevenDays proves OUT-001: the window is exactly seven days and only
// closes at/after ClosesAt.
func TestWindow_SevenDays(t *testing.T) {
	opened := time.Now()
	w := Open(opened)
	if w.ClosesAt.Sub(opened) != 7*24*time.Hour {
		t.Fatalf("window span = %v; want 168h", w.ClosesAt.Sub(opened))
	}
	if w.Closed(opened.Add(6 * 24 * time.Hour)) {
		t.Fatalf("window closed early")
	}
	if !w.Closed(opened.Add(7 * 24 * time.Hour)) {
		t.Fatalf("window did not close at seven days")
	}
}
