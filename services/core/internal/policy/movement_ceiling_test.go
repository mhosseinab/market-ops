package policy

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/mhosseinab/market-ops/services/core/internal/money"
)

// Issue #62 (PRC-004): the 500 bp movement ceiling is a never-cut hard maximum
// and must be IMMUTABLE runtime state. No public symbol may raise it, and both
// the NewConfig default path and the direct-Evaluate validate path must compare
// against the fixed 500 literal — never a mutable variable.

// TestMovementCeiling_DefaultIsFiveHundredBp asserts the exported default is a
// fresh value of exactly 500 bp obtained through a function (not an assignable
// package variable).
func TestMovementCeiling_DefaultIsFiveHundredBp(t *testing.T) {
	got := DefaultMovementCap()
	if got.Value() != 500 {
		t.Fatalf("DefaultMovementCap().Value() = %d, want 500", got.Value())
	}
	// A second call returns an independent value: there is no shared mutable
	// guardrail state behind the default.
	if again := DefaultMovementCap(); again.Value() != got.Value() {
		t.Fatalf("DefaultMovementCap() not stable: %d vs %d", again.Value(), got.Value())
	}
}

// TestMovementCeiling_NewConfigRejects501AndAccepts500 pins the NewConfig
// default-path ceiling at exactly 500 bp.
func TestMovementCeiling_NewConfigRejects501AndAccepts500(t *testing.T) {
	at500 := money.NewBasisPoints(500)
	if _, err := NewConfig(ConfigParams{MovementCap: &at500}); err != nil {
		t.Fatalf("NewConfig(500 bp) err = %v, want nil (500 is the ceiling and is accepted)", err)
	}

	at501 := money.NewBasisPoints(501)
	if _, err := NewConfig(ConfigParams{MovementCap: &at501}); !errors.Is(err, ErrMovementCapTooLoose) {
		t.Fatalf("NewConfig(501 bp) err = %v, want ErrMovementCapTooLoose", err)
	}
}

// TestMovementCeiling_EvaluateRejects501AndAccepts500 pins the direct-Evaluate
// defense-in-depth ceiling at exactly 500 bp for a Config built by bypassing
// NewConfig.
func TestMovementCeiling_EvaluateRejects501AndAccepts500(t *testing.T) {
	base := func(capBp int64) EvaluateInput {
		return EvaluateInput{
			Config: Config{
				Boundary:          Boundary{Known: true, Min: m0(t, 900), Max: m0(t, 1100)},
				ContributionFloor: m0(t, 0),
				MovementCap:       money.NewBasisPoints(capBp),
				Cooldown:          DefaultCooldown,
				Strategy:          StrategyHold,
				StrategyEnabled:   true,
				Objective:         ObjectiveTrackStrategy,
			},
			CurrentPrice: m0(t, 1000),
			Contribution: mkContrib(t, m0(t, 800), 0),
			Now:          time.Unix(1_000_000, 0),
		}
	}

	if _, err := Evaluate(base(500)); err != nil {
		t.Fatalf("Evaluate(cap=500 bp) err = %v, want nil (ceiling accepted)", err)
	}
	if _, err := Evaluate(base(501)); !errors.Is(err, ErrMovementCapTooLoose) {
		t.Fatalf("Evaluate(cap=501 bp) err = %v, want ErrMovementCapTooLoose", err)
	}
}

// TestMovementCeiling_ConcurrentNoSharedMutableState exercises NewConfig and
// Evaluate concurrently. Under -race this fails if the ceiling is backed by
// shared mutable guardrail state.
func TestMovementCeiling_ConcurrentNoSharedMutableState(t *testing.T) {
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			// Default path.
			if _, err := NewConfig(ConfigParams{}); err != nil {
				t.Errorf("NewConfig(default) err = %v", err)
			}
			// Stricter value is accepted.
			strict := money.NewBasisPoints(300)
			if _, err := NewConfig(ConfigParams{MovementCap: &strict}); err != nil {
				t.Errorf("NewConfig(300 bp) err = %v", err)
			}
			// Ceiling is never widened: 501 always rejected.
			loose := money.NewBasisPoints(501)
			if _, err := NewConfig(ConfigParams{MovementCap: &loose}); !errors.Is(err, ErrMovementCapTooLoose) {
				t.Errorf("NewConfig(501 bp) err = %v, want ErrMovementCapTooLoose", err)
			}
			// Direct Evaluate path shares the same immutable ceiling.
			in := EvaluateInput{
				Config: Config{
					Boundary:        Boundary{Known: true, Min: m0(t, 900), Max: m0(t, 1100)},
					MovementCap:     money.NewBasisPoints(501),
					Cooldown:        DefaultCooldown,
					Strategy:        StrategyHold,
					StrategyEnabled: true,
					Objective:       ObjectiveTrackStrategy,
				},
				CurrentPrice: m0(t, 1000),
				Contribution: mkContrib(t, m0(t, 800), 0),
				Now:          time.Unix(1_000_000, 0),
			}
			if _, err := Evaluate(in); !errors.Is(err, ErrMovementCapTooLoose) {
				t.Errorf("Evaluate(cap=501 bp) err = %v, want ErrMovementCapTooLoose", err)
			}
		}()
	}
	wg.Wait()
}
