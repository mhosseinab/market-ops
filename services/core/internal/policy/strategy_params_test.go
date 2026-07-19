package policy

import (
	"errors"
	"testing"
	"time"

	"github.com/mhosseinab/market-ops/services/core/internal/cost"
	"github.com/mhosseinab/market-ops/services/core/internal/money"
)

// Issue #64 (§9.3, §4.6 money correctness / quarantine-over-inference): strategy
// inputs are AUTHORITATIVE policy configuration. Match and Undercut require a
// valid, same-unit reference price; Undercut requires a bounded non-negative
// depth in basis points (fixed-point, no float on the money path). Invalid
// commercial configuration must FAIL CLOSED with a typed error BEFORE evaluation —
// a missing reference must never reach Money arithmetic as a zero value, and a
// negative/oversized undercut must never reverse or corrupt the named strategy.
// These are negative tests, written first and kept passing on every change.

// refMismatch builds a well-formed reference in a DIFFERENT money unit (USD) than
// the IRR policy unit produced by m0, to exercise the unit-mismatch guard.
func refMismatch(t *testing.T, mantissa int64) money.Money {
	t.Helper()
	m, err := money.New(mantissa, "USD", 0)
	if err != nil {
		t.Fatalf("money.New: %v", err)
	}
	return m
}

// strategyParams returns a ConfigParams valid on every hard stage and enum, whose
// strategy/reference/undercut fields the caller overrides to isolate the guard.
func strategyParams(t *testing.T) ConfigParams {
	t.Helper()
	return ConfigParams{
		Boundary:          Boundary{Known: true, Min: m0(t, 900), Max: m0(t, 1100)},
		ContributionFloor: m0(t, 0), // IRR, exponent 0 — the policy money unit
		Strategy:          StrategyMatch,
		StrategyEnabled:   true,
		Reference:         m0(t, 1000),
		UndercutBp:        money.NewBasisPoints(0),
		Objective:         ObjectiveTrackStrategy,
	}
}

// --- NewConfig: reference requirement (Match / Undercut) ---------------------

func TestNewConfig_MatchRejectsMissingReference(t *testing.T) {
	p := strategyParams(t)
	p.Strategy = StrategyMatch
	p.Reference = money.Money{} // zero-value: absent reference
	if _, err := NewConfig(p); !errors.Is(err, ErrMissingReference) {
		t.Fatalf("NewConfig(match, no reference) err = %v, want ErrMissingReference", err)
	}
}

func TestNewConfig_UndercutRejectsMissingReference(t *testing.T) {
	p := strategyParams(t)
	p.Strategy = StrategyUndercut
	p.Reference = money.Money{}
	p.UndercutBp = money.NewBasisPoints(500)
	if _, err := NewConfig(p); !errors.Is(err, ErrMissingReference) {
		t.Fatalf("NewConfig(undercut, no reference) err = %v, want ErrMissingReference", err)
	}
}

func TestNewConfig_MatchRejectsZeroValuedReference(t *testing.T) {
	p := strategyParams(t)
	p.Strategy = StrategyMatch
	p.Reference = m0(t, 0) // valid IRR unit but zero price
	if _, err := NewConfig(p); !errors.Is(err, ErrMissingReference) {
		t.Fatalf("NewConfig(match, zero reference) err = %v, want ErrMissingReference", err)
	}
}

func TestNewConfig_MatchRejectsUnitMismatchedReference(t *testing.T) {
	p := strategyParams(t)
	p.Strategy = StrategyMatch
	p.Reference = refMismatch(t, 1000) // USD reference against an IRR policy unit
	if _, err := NewConfig(p); !errors.Is(err, ErrReferenceUnitMismatch) {
		t.Fatalf("NewConfig(match, USD ref vs IRR unit) err = %v, want ErrReferenceUnitMismatch", err)
	}
}

func TestNewConfig_UndercutRejectsUnitMismatchedReference(t *testing.T) {
	p := strategyParams(t)
	p.Strategy = StrategyUndercut
	p.Reference = refMismatch(t, 1000)
	p.UndercutBp = money.NewBasisPoints(500)
	if _, err := NewConfig(p); !errors.Is(err, ErrReferenceUnitMismatch) {
		t.Fatalf("NewConfig(undercut, USD ref vs IRR unit) err = %v, want ErrReferenceUnitMismatch", err)
	}
}

// --- NewConfig: undercut depth bound ----------------------------------------

func TestNewConfig_UndercutRejectsNegativeDepth(t *testing.T) {
	p := strategyParams(t)
	p.Strategy = StrategyUndercut
	p.UndercutBp = money.NewBasisPoints(-1) // negative cut would RAISE the desired price
	if _, err := NewConfig(p); !errors.Is(err, ErrUndercutOutOfRange) {
		t.Fatalf("NewConfig(undercut, -1 bp) err = %v, want ErrUndercutOutOfRange", err)
	}
}

func TestNewConfig_UndercutRejectsAboveMaxDepth(t *testing.T) {
	p := strategyParams(t)
	p.Strategy = StrategyUndercut
	p.UndercutBp = money.NewBasisPoints(10001) // > 100% would drive the price below zero
	if _, err := NewConfig(p); !errors.Is(err, ErrUndercutOutOfRange) {
		t.Fatalf("NewConfig(undercut, 10001 bp) err = %v, want ErrUndercutOutOfRange", err)
	}
}

func TestNewConfig_UndercutAcceptsBoundaryDepths(t *testing.T) {
	for _, bp := range []int64{0, 10000} {
		p := strategyParams(t)
		p.Strategy = StrategyUndercut
		p.UndercutBp = money.NewBasisPoints(bp)
		if _, err := NewConfig(p); err != nil {
			t.Fatalf("NewConfig(undercut, %d bp) err = %v, want nil (documented boundary)", bp, err)
		}
	}
}

// --- NewConfig: Hold requires no reference ----------------------------------

func TestNewConfig_HoldSucceedsWithoutReference(t *testing.T) {
	p := strategyParams(t)
	p.Strategy = StrategyHold
	p.Reference = money.Money{} // Hold keeps the current price — no reference needed
	if _, err := NewConfig(p); err != nil {
		t.Fatalf("NewConfig(hold, no reference) err = %v, want nil", err)
	}
}

func TestNewConfig_MatchAndUndercutAcceptValidReference(t *testing.T) {
	for _, s := range []Strategy{StrategyMatch, StrategyUndercut} {
		p := strategyParams(t)
		p.Strategy = s
		if _, err := NewConfig(p); err != nil {
			t.Fatalf("NewConfig(%q, valid reference) err = %v, want nil", s, err)
		}
	}
}

// --- Direct Evaluate path fails IDENTICALLY to NewConfig --------------------

// invalidStrategyConfig builds a Config DIRECTLY (bypassing NewConfig) — the
// persisted/deserialized-config threat model. Config.validate() runs at the top
// of Evaluate, so the direct path must reject with the same typed error and never
// reach Money arithmetic or emit a proposal.
func invalidStrategyConfig(strategy Strategy, reference money.Money, undercutBp money.BasisPoints, floor money.Money) Config {
	return Config{
		Boundary:          Boundary{Known: true, Min: money.Money{}, Max: money.Money{}},
		ContributionFloor: floor,
		MovementCap:       DefaultMovementCap(),
		Cooldown:          DefaultCooldown,
		Strategy:          strategy,
		StrategyEnabled:   true,
		Reference:         reference,
		UndercutBp:        undercutBp,
		Objective:         ObjectiveTrackStrategy,
	}
}

func TestEvaluate_MatchMissingReferenceRejectedBeforeArithmetic(t *testing.T) {
	// A spy oracle proves invalid config never reaches Money arithmetic: if it is
	// ever called, evaluation progressed past the config gate.
	spy := &countingContrib{inner: mkContrib(t, m0(t, 100), 0)}
	cfg := invalidStrategyConfig(StrategyMatch, money.Money{}, money.NewBasisPoints(0), m0(t, 0))
	_, err := Evaluate(EvaluateInput{
		Config:       cfg,
		CurrentPrice: m0(t, 1000),
		Contribution: spy.fn(),
		Now:          time.Unix(1_000_000, 0),
		Readiness:    cost.StateComplete,
	})
	if !errors.Is(err, ErrMissingReference) {
		t.Fatalf("Evaluate(match, no reference) err = %v, want ErrMissingReference", err)
	}
	if spy.calls != 0 {
		t.Fatalf("contribution oracle called %d times on invalid config; want 0", spy.calls)
	}
}

func TestEvaluate_UndercutNegativeDepthRejectedBeforeArithmetic(t *testing.T) {
	spy := &countingContrib{inner: mkContrib(t, m0(t, 100), 0)}
	cfg := invalidStrategyConfig(StrategyUndercut, m0(t, 1000), money.NewBasisPoints(-1), m0(t, 0))
	_, err := Evaluate(EvaluateInput{
		Config:       cfg,
		CurrentPrice: m0(t, 1000),
		Contribution: spy.fn(),
		Now:          time.Unix(1_000_000, 0),
		Readiness:    cost.StateComplete,
	})
	if !errors.Is(err, ErrUndercutOutOfRange) {
		t.Fatalf("Evaluate(undercut, -1 bp) err = %v, want ErrUndercutOutOfRange", err)
	}
	if spy.calls != 0 {
		t.Fatalf("contribution oracle called %d times on invalid config; want 0", spy.calls)
	}
}

func TestEvaluate_UndercutUnitMismatchRejectedBeforeArithmetic(t *testing.T) {
	cfg := invalidStrategyConfig(StrategyUndercut, refMismatch(t, 1000), money.NewBasisPoints(500), m0(t, 0))
	_, err := Evaluate(EvaluateInput{
		Config:       cfg,
		CurrentPrice: m0(t, 1000),
		Contribution: mkContrib(t, m0(t, 100), 0),
		Now:          time.Unix(1_000_000, 0),
		Readiness:    cost.StateComplete,
	})
	if !errors.Is(err, ErrReferenceUnitMismatch) {
		t.Fatalf("Evaluate(undercut, USD ref) err = %v, want ErrReferenceUnitMismatch", err)
	}
}
