package observation_test

import (
	"testing"

	obs "github.com/mhosseinab/market-ops/services/core/internal/observation"
)

// TestConsequenceMatrix asserts the §10.3 table for ALL SIX states, including the
// display/recommend/execute consequences, exactly as the PRD wording specifies.
// This is the fixture-driven proof that the closed six-state machine and its
// consequence matrix match the spec (OBS-003).
func TestConsequenceMatrix(t *testing.T) {
	cases := []struct {
		state        obs.Quality
		display      obs.DisplayMode
		canShowValue bool
		recommend    bool
		execute      obs.ExecuteMode
		canExecute   bool
	}{
		// Verified: Display Yes, Recommend Yes, Execute "Yes if all gates pass".
		{obs.Verified, obs.DisplayFull, true, true, obs.ExecuteIfGatesPass, true},
		// Supported: Display Yes, Recommend Yes, Execute "Only after successful JIT refresh".
		{obs.Supported, obs.DisplayFull, true, true, obs.ExecuteAfterJITRefresh, true},
		// Unverified: Display "With warning", Recommend No, Execute No.
		{obs.Unverified, obs.DisplayWithWarning, true, false, obs.ExecuteNever, false},
		// Conflicted: Display "With conflict details", Recommend No, Execute No.
		{obs.Conflicted, obs.DisplayWithConflict, true, false, obs.ExecuteNever, false},
		// Stale: Display "Age only", Recommend No, Execute No.
		{obs.Stale, obs.DisplayAgeOnly, false, false, obs.ExecuteNever, false},
		// Unavailable: Display "State only", Recommend No, Execute No.
		{obs.Unavailable, obs.DisplayStateOnly, false, false, obs.ExecuteNever, false},
	}

	// The matrix must cover exactly the six states — no more, no fewer.
	if len(cases) != len(obs.AllQualities) {
		t.Fatalf("matrix cases %d != six states %d", len(cases), len(obs.AllQualities))
	}

	for _, tc := range cases {
		t.Run(string(tc.state), func(t *testing.T) {
			if !tc.state.Valid() {
				t.Fatalf("%s is not a valid state", tc.state)
			}
			c := obs.ConsequenceOf(tc.state)
			if c.Display != tc.display {
				t.Errorf("display: got %q want %q", c.Display, tc.display)
			}
			if c.CanShowValue != tc.canShowValue {
				t.Errorf("canShowValue: got %v want %v", c.CanShowValue, tc.canShowValue)
			}
			if c.Recommend != tc.recommend {
				t.Errorf("recommend: got %v want %v", c.Recommend, tc.recommend)
			}
			if c.Execute != tc.execute {
				t.Errorf("execute: got %q want %q", c.Execute, tc.execute)
			}
			if c.CanExecute != tc.canExecute {
				t.Errorf("canExecute: got %v want %v", c.CanExecute, tc.canExecute)
			}
		})
	}
}

// TestCurrentDataGate is the OBS-004 gate: only NON-expired states satisfy a
// current-data gate. Stale (expired) and Unavailable never do — no matter their
// age. This is the boolean the "expired value can never satisfy a current-data
// gate" invariant is asserted through.
func TestCurrentDataGate(t *testing.T) {
	want := map[obs.Quality]bool{
		obs.Verified:    true,
		obs.Supported:   true,
		obs.Unverified:  true,
		obs.Conflicted:  false, // routes disagree → block (§16), fails the gate
		obs.Stale:       false,
		obs.Unavailable: false,
	}
	for state, expect := range want {
		if got := state.SatisfiesCurrentDataGate(); got != expect {
			t.Errorf("%s.SatisfiesCurrentDataGate() = %v, want %v", state, got, expect)
		}
	}
}

// TestUnknownStateFailsClosed asserts an unrecognized state degrades to the most
// restrictive consequence — a mislabeled value can never display, recommend, or
// execute.
func TestUnknownStateFailsClosed(t *testing.T) {
	c := obs.ConsequenceOf(obs.Quality("bogus"))
	if c.CanShowValue || c.Recommend || c.CanExecute || c.Execute != obs.ExecuteNever {
		t.Fatalf("unknown state must fail closed, got %+v", c)
	}
}

// TestDeriveQuality drives the six-state derivation precedence (§10.3) from
// capture signals.
func TestDeriveQuality(t *testing.T) {
	base := obs.QualitySignals{
		HasValue: true, Fresh: true, SchemaValid: true, IdentityValid: true,
	}
	cases := []struct {
		name   string
		mutate func(s *obs.QualitySignals)
		want   obs.Quality
	}{
		{"no value → unavailable", func(s *obs.QualitySignals) { s.HasValue = false }, obs.Unavailable},
		{"disappeared → unavailable", func(s *obs.QualitySignals) { s.Disappeared = true }, obs.Unavailable},
		{"not fresh → stale", func(s *obs.QualitySignals) { s.Fresh = false }, obs.Stale},
		{"conflict → conflicted", func(s *obs.QualitySignals) { s.Conflicted = true }, obs.Conflicted},
		{"bad schema → unverified", func(s *obs.QualitySignals) { s.SchemaValid = false }, obs.Unverified},
		{"bad identity → unverified", func(s *obs.QualitySignals) { s.IdentityValid = false }, obs.Unverified},
		{"low confidence → unverified", func(s *obs.QualitySignals) { s.LowConfidence = true }, obs.Unverified},
		// First-ever sighting: no history, no corroboration → Unverified (a single
		// capture can never self-promote to Supported, §10.3).
		{"first sighting → unverified", func(s *obs.QualitySignals) {}, obs.Unverified},
		// One fresh valid path WITH recent history → Supported.
		{"fresh + history → supported", func(s *obs.QualitySignals) { s.HasHistory = true }, obs.Supported},
		// In-window cross-route corroboration → Verified (history implied).
		{"corroborated → verified", func(s *obs.QualitySignals) { s.Corroborated = true; s.HasHistory = true }, obs.Verified},
		// Conflict blocks even with history/corroboration present.
		{"conflict beats corroboration", func(s *obs.QualitySignals) { s.Conflicted = true; s.Corroborated = true; s.HasHistory = true }, obs.Conflicted},
		// Staleness dominates conflict: an expired conflicting value is Stale.
		{"stale beats conflict", func(s *obs.QualitySignals) { s.Fresh = false; s.Conflicted = true }, obs.Stale},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := base
			tc.mutate(&s)
			if got := obs.DeriveQuality(s); got != tc.want {
				t.Errorf("DeriveQuality = %s, want %s", got, tc.want)
			}
		})
	}
}
