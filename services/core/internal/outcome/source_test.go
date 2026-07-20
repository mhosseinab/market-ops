package outcome

import "testing"

// TestResolve_EvidenceStates proves the evidence-quality never-cut (§4.6): the
// source distinguishes absent / incomplete / measurable WITHOUT guessing, and a
// genuinely-absent determination is the ONLY path to NotMeasurable.
func TestResolve_EvidenceStates(t *testing.T) {
	complete := &objectiveEvidence{complete: true, objectiveImproved: true}
	absent := &objectiveEvidence{complete: false}

	cases := []struct {
		name        string
		execPending bool
		ev          *objectiveEvidence
		concurrent  int
		wantDisp    Disposition
	}{
		// Unknown write result (EXE-003) must NOT close; it is retried.
		{"pending write -> incomplete", true, complete, 0, DispositionIncomplete},
		// No determination yet must NOT become NotMeasurable — it stays unclosed.
		{"no evidence row -> incomplete", false, nil, 0, DispositionIncomplete},
		// The pipeline LOOKED and required evidence is genuinely absent -> the only
		// legitimate NotMeasurable path.
		{"evidence not complete -> absent", false, absent, 0, DispositionAbsent},
		// Complete evidence classifies.
		{"complete -> measurable", false, complete, 0, DispositionMeasurable},
		// Pending write dominates even when a complete row exists (result unknown).
		{"pending beats complete row", true, complete, 3, DispositionIncomplete},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolve(tc.execPending, tc.ev, tc.concurrent)
			if got.Disposition != tc.wantDisp {
				t.Fatalf("disposition = %d; want %d", got.Disposition, tc.wantDisp)
			}
		})
	}
}

// TestResolve_MeasurableClasses proves complete evidence yields each §15.3 class
// with the expected confidence — the classification the nil-source bug destroyed.
func TestResolve_MeasurableClasses(t *testing.T) {
	cases := []struct {
		name           string
		ev             objectiveEvidence
		concurrent     int
		wantResult     Result
		wantConfidence Confidence
	}{
		{"positive/high", objectiveEvidence{complete: true, objectiveImproved: true}, 0, Positive, High},
		{"negative worsened/medium", objectiveEvidence{complete: true, objectiveWorsened: true}, 1, Negative, Medium},
		{"negative floor breach beats improved", objectiveEvidence{complete: true, objectiveImproved: true, floorBreached: true}, 0, Negative, High},
		{"neutral within materiality/low", objectiveEvidence{complete: true, withinMateriality: true, objectiveImproved: true}, 2, Neutral, Low},
		{"inconclusive attribution blocked", objectiveEvidence{complete: true, attributionBlocked: true, objectiveImproved: true}, 5, Inconclusive, Low},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := resolve(false, &tc.ev, tc.concurrent)
			if res.Disposition != DispositionMeasurable {
				t.Fatalf("disposition = %d; want measurable", res.Disposition)
			}
			gotResult, gotConf := Evaluate(res.Inputs)
			if gotResult != tc.wantResult {
				t.Fatalf("result = %q; want %q", gotResult, tc.wantResult)
			}
			if gotConf != tc.wantConfidence {
				t.Fatalf("confidence = %q; want %q", gotConf, tc.wantConfidence)
			}
			if !res.Inputs.EvidenceComplete {
				t.Fatalf("measurable Inputs must carry EvidenceComplete=true")
			}
		})
	}
}

// TestResolve_AbsentIsNotMeasurable proves the DispositionAbsent path evaluates to
// NotMeasurable (and only that path — TestResolve_EvidenceStates proves incomplete
// and pending never reach it).
func TestResolve_AbsentIsNotMeasurable(t *testing.T) {
	res := resolve(false, &objectiveEvidence{complete: false}, 0)
	if res.Disposition != DispositionAbsent {
		t.Fatalf("disposition = %d; want absent", res.Disposition)
	}
	// The closer feeds Inputs{EvidenceComplete:false} for an absent disposition.
	if got, _ := Evaluate(Inputs{EvidenceComplete: false}); got != NotMeasurable {
		t.Fatalf("absent evidence classifies %q; want not_measurable", got)
	}
}
