package policy

import (
	"errors"
	"testing"
	"time"

	"pgregory.net/rapid"

	"github.com/mhosseinab/market-ops/services/core/internal/cost"
	"github.com/mhosseinab/market-ops/services/core/internal/money"
)

// Issue #63 (§9.3, PRC-003 / §4.6 quarantine-over-inference): policy configuration
// is a CLOSED, authoritative input. An unknown or empty Strategy/Objective must
// FAIL CLOSED with a typed error — it must NEVER be silently reinterpreted as a
// different commercial strategy (Hold) or objective (TrackStrategy). These are
// negative tests, written first and kept passing on every change.

// allStrategies / allObjectives are the declared closed sets — every one must
// remain accepted.
var allStrategies = []Strategy{StrategyHold, StrategyMatch, StrategyUndercut}
var allObjectives = []Objective{ObjectiveMaximizeContribution, ObjectiveTrackStrategy}

// validEnumParams is a ConfigParams that is valid EXCEPT for the enum fields the
// caller overrides; it isolates the enum-validation behavior under test.
func validEnumParams(t *testing.T) ConfigParams {
	t.Helper()
	return ConfigParams{
		Boundary:          Boundary{Known: true, Min: m0(t, 900), Max: m0(t, 1100)},
		ContributionFloor: m0(t, 0),
		Strategy:          StrategyHold,
		StrategyEnabled:   true,
		Reference:         m0(t, 1000),
		Objective:         ObjectiveTrackStrategy,
	}
}

// TestNewConfig_RejectsUnknownStrategy proves NewConfig fails closed on a strategy
// token outside the closed set.
func TestNewConfig_RejectsUnknownStrategy(t *testing.T) {
	p := validEnumParams(t)
	p.Strategy = Strategy("typo")
	if _, err := NewConfig(p); !errors.Is(err, ErrUnknownStrategy) {
		t.Fatalf("NewConfig(Strategy=typo) err = %v, want ErrUnknownStrategy", err)
	}
}

// TestNewConfig_RejectsEmptyStrategy proves the empty token is rejected too (it is
// not a valid closed-set member and must never default to Hold).
func TestNewConfig_RejectsEmptyStrategy(t *testing.T) {
	p := validEnumParams(t)
	p.Strategy = Strategy("")
	if _, err := NewConfig(p); !errors.Is(err, ErrUnknownStrategy) {
		t.Fatalf("NewConfig(Strategy=\"\") err = %v, want ErrUnknownStrategy", err)
	}
}

// TestNewConfig_RejectsUnknownObjective proves NewConfig fails closed on an
// objective token outside the closed set.
func TestNewConfig_RejectsUnknownObjective(t *testing.T) {
	p := validEnumParams(t)
	p.Objective = Objective("typo")
	if _, err := NewConfig(p); !errors.Is(err, ErrUnknownObjective) {
		t.Fatalf("NewConfig(Objective=typo) err = %v, want ErrUnknownObjective", err)
	}
}

// TestNewConfig_RejectsEmptyObjective proves the empty objective token is rejected
// (never silently defaults to TrackStrategy).
func TestNewConfig_RejectsEmptyObjective(t *testing.T) {
	p := validEnumParams(t)
	p.Objective = Objective("")
	if _, err := NewConfig(p); !errors.Is(err, ErrUnknownObjective) {
		t.Fatalf("NewConfig(Objective=\"\") err = %v, want ErrUnknownObjective", err)
	}
}

// TestNewConfig_AcceptsEveryDeclaredStrategyAndObjective proves the closed sets
// remain fully accepted (no valid value is collateral damage of the guard).
func TestNewConfig_AcceptsEveryDeclaredStrategyAndObjective(t *testing.T) {
	for _, s := range allStrategies {
		for _, o := range allObjectives {
			p := validEnumParams(t)
			p.Strategy = s
			p.Objective = o
			if _, err := NewConfig(p); err != nil {
				t.Fatalf("NewConfig(Strategy=%q, Objective=%q) err = %v, want nil", s, o, err)
			}
		}
	}
}

// unknownEnumConfig builds a Config DIRECTLY (bypassing NewConfig) that is valid on
// every hard stage but carries the given enum tokens — the exact persisted/
// deserialized-config threat model from issue #63.
func unknownEnumConfig(t *testing.T, s Strategy, o Objective) Config {
	t.Helper()
	return Config{
		Boundary:          Boundary{Known: true, Min: m0(t, 900), Max: m0(t, 1100)},
		ContributionFloor: m0(t, 0),
		MovementCap:       DefaultMovementCap(),
		Cooldown:          DefaultCooldown,
		Strategy:          s,
		StrategyEnabled:   true,
		Reference:         m0(t, 1000),
		Objective:         o,
	}
}

func unknownEnumInput(t *testing.T, s Strategy, o Objective) EvaluateInput {
	t.Helper()
	return EvaluateInput{
		Config:       unknownEnumConfig(t, s, o),
		CurrentPrice: m0(t, 1000),
		Contribution: mkContrib(t, m0(t, 800), 0),
		Now:          time.Unix(1_000_000, 0),
		Readiness:    cost.StateComplete,
	}
}

// TestEvaluate_RejectsUnknownStrategy proves defense in depth: a Config built by
// bypassing NewConfig with an unknown strategy is rejected by Evaluate with a
// typed error and NEVER yields a proposal (no silent Hold substitution).
func TestEvaluate_RejectsUnknownStrategy(t *testing.T) {
	for _, s := range []Strategy{Strategy("typo"), Strategy("")} {
		res, err := Evaluate(unknownEnumInput(t, s, ObjectiveTrackStrategy))
		if !errors.Is(err, ErrUnknownStrategy) {
			t.Fatalf("Evaluate(Strategy=%q) err = %v, want ErrUnknownStrategy", s, err)
		}
		if res.Proposed != nil {
			t.Fatalf("Evaluate(Strategy=%q) produced a proposal %+v; unknown strategy must fail closed", s, res.Proposed)
		}
	}
}

// TestEvaluate_RejectsUnknownObjective proves the same fail-closed behavior for an
// unknown objective (no silent TrackStrategy substitution).
func TestEvaluate_RejectsUnknownObjective(t *testing.T) {
	for _, o := range []Objective{Objective("typo"), Objective("")} {
		res, err := Evaluate(unknownEnumInput(t, StrategyHold, o))
		if !errors.Is(err, ErrUnknownObjective) {
			t.Fatalf("Evaluate(Objective=%q) err = %v, want ErrUnknownObjective", o, err)
		}
		if res.Proposed != nil {
			t.Fatalf("Evaluate(Objective=%q) produced a proposal %+v; unknown objective must fail closed", o, res.Proposed)
		}
	}
}

// TestEvaluate_RejectsUnknownEnumsEvenWhenStrategyDisabled proves the enum guard
// applies REGARDLESS of StrategyEnabled: an EMPTY/unknown Strategy or Objective is
// rejected even when the strategy is disabled (validation precedes the enable gate).
func TestEvaluate_RejectsUnknownEnumsEvenWhenStrategyDisabled(t *testing.T) {
	in := unknownEnumInput(t, Strategy(""), ObjectiveTrackStrategy)
	in.Config.StrategyEnabled = false
	if _, err := Evaluate(in); !errors.Is(err, ErrUnknownStrategy) {
		t.Fatalf("Evaluate(empty strategy, disabled) err = %v, want ErrUnknownStrategy", err)
	}

	in = unknownEnumInput(t, StrategyHold, Objective(""))
	in.Config.StrategyEnabled = false
	if _, err := Evaluate(in); !errors.Is(err, ErrUnknownObjective) {
		t.Fatalf("Evaluate(empty objective, disabled) err = %v, want ErrUnknownObjective", err)
	}
}

// TestProp_UnknownEnumsRejected is the property proof for issue #63: over a mix of
// valid and invalid enum tokens, an invalid Strategy/Objective is ALWAYS rejected
// by both NewConfig and direct Evaluate with the matching typed error and never a
// proposal; every valid pair is accepted.
func TestProp_UnknownEnumsRejected(t *testing.T) {
	strategyTokens := []Strategy{
		StrategyHold, StrategyMatch, StrategyUndercut,
		Strategy(""), Strategy("hold "), Strategy("Hold"), Strategy("typo"), Strategy("track_strategy"),
	}
	objectiveTokens := []Objective{
		ObjectiveMaximizeContribution, ObjectiveTrackStrategy,
		Objective(""), Objective("track"), Objective("TrackStrategy"), Objective("typo"), Objective("hold"),
	}
	isValidStrategy := func(s Strategy) bool {
		return s == StrategyHold || s == StrategyMatch || s == StrategyUndercut
	}
	isValidObjective := func(o Objective) bool {
		return o == ObjectiveMaximizeContribution || o == ObjectiveTrackStrategy
	}

	rapid.Check(t, func(t *rapid.T) {
		s := rapid.SampledFrom(strategyTokens).Draw(t, "strategy")
		o := rapid.SampledFrom(objectiveTokens).Draw(t, "objective")

		newIRR := func(mant int64) money.Money {
			m, err := money.New(mant, "IRR", 0)
			if err != nil {
				t.Fatalf("money.New: %v", err)
			}
			return m
		}

		_, ncErr := NewConfig(ConfigParams{
			Boundary:          Boundary{Known: true, Min: newIRR(900), Max: newIRR(1100)},
			ContributionFloor: newIRR(0),
			Strategy:          s,
			StrategyEnabled:   true,
			Reference:         newIRR(1000),
			Objective:         o,
		})

		// Build a Config directly to exercise the Evaluate defense-in-depth path.
		cfg := Config{
			Boundary:          Boundary{Known: true, Min: newIRR(900), Max: newIRR(1100)},
			ContributionFloor: newIRR(0),
			MovementCap:       DefaultMovementCap(),
			Cooldown:          DefaultCooldown,
			Strategy:          s,
			StrategyEnabled:   true,
			Reference:         newIRR(1000),
			Objective:         o,
		}
		evRes, evErr := Evaluate(EvaluateInput{
			Config:       cfg,
			CurrentPrice: newIRR(1000),
			Contribution: mkPropContrib(newIRR(800), 0),
			Now:          propNow,
			Readiness:    cost.StateComplete,
		})

		switch {
		case !isValidStrategy(s):
			if !errors.Is(ncErr, ErrUnknownStrategy) {
				t.Fatalf("NewConfig(strategy=%q) err = %v, want ErrUnknownStrategy", s, ncErr)
			}
			if !errors.Is(evErr, ErrUnknownStrategy) {
				t.Fatalf("Evaluate(strategy=%q) err = %v, want ErrUnknownStrategy", s, evErr)
			}
			if evRes.Proposed != nil {
				t.Fatalf("Evaluate(strategy=%q) produced a proposal; must fail closed", s)
			}
		case !isValidObjective(o):
			if !errors.Is(ncErr, ErrUnknownObjective) {
				t.Fatalf("NewConfig(objective=%q) err = %v, want ErrUnknownObjective", o, ncErr)
			}
			if !errors.Is(evErr, ErrUnknownObjective) {
				t.Fatalf("Evaluate(objective=%q) err = %v, want ErrUnknownObjective", o, evErr)
			}
			if evRes.Proposed != nil {
				t.Fatalf("Evaluate(objective=%q) produced a proposal; must fail closed", o)
			}
		default:
			// Both valid: accepted by NewConfig; Evaluate returns no enum error.
			if ncErr != nil {
				t.Fatalf("NewConfig(strategy=%q, objective=%q) err = %v, want nil", s, o, ncErr)
			}
			if errors.Is(evErr, ErrUnknownStrategy) || errors.Is(evErr, ErrUnknownObjective) {
				t.Fatalf("Evaluate(valid enums) rejected as unknown: %v", evErr)
			}
		}
	})
}
