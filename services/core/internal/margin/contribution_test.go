package margin

import (
	"errors"
	"testing"

	"github.com/mhosseinab/market-ops/services/core/internal/cost"
	"github.com/mhosseinab/market-ops/services/core/internal/money"
)

// irr builds an IRR/exp-0 money for tests. It fails the test on an invalid
// currency (never expected here).
func irr(t *testing.T, mantissa int64) money.Money {
	t.Helper()
	m, err := money.New(mantissa, "IRR", 0)
	if err != nil {
		t.Fatalf("money.New(%d): %v", mantissa, err)
	}
	return m
}

func abs(c cost.Component, m money.Money) ComponentInput {
	return ComponentInput{Component: c, Kind: KindAbsolute, Amount: m, Version: 1}
}

func rate(c cost.Component, bp int64) ComponentInput {
	return ComponentInput{Component: c, Kind: KindRate, Rate: money.NewBasisPoints(bp), Version: 1}
}

func TestContribution_AbsoluteAndRate(t *testing.T) {
	var eng Engine
	got, err := eng.Contribution(ContributionInput{
		NetProceeds: irr(t, 1_000_000),
		RateBase:    irr(t, 1_000_000),
		Readiness:   cost.StateComplete,
		Components: []ComponentInput{
			abs(cost.ComponentCOGS, irr(t, 600_000)),
			rate(cost.ComponentCommission, 1200), // 12% of 1,000,000 = 120,000
			abs(cost.ComponentFulfillment, irr(t, 30_000)),
			abs(cost.ComponentShipping, irr(t, 20_000)),
		},
	})
	if err != nil {
		t.Fatalf("Contribution: %v", err)
	}
	// 1,000,000 − 600,000 − 120,000 − 30,000 − 20,000 = 230,000.
	if got.Amount.Mantissa() != 230_000 {
		t.Fatalf("contribution = %d, want 230000", got.Amount.Mantissa())
	}
	if got.RoundingRule != ContributionRoundingRule {
		t.Fatalf("rounding rule = %q, want %q", got.RoundingRule, ContributionRoundingRule)
	}
	if !got.Executable() {
		t.Fatal("Complete readiness must be executable")
	}
	pos, err := got.IsPositive()
	if err != nil {
		t.Fatalf("IsPositive: %v", err)
	}
	if !pos {
		t.Fatal("contribution should be positive")
	}
}

func TestContribution_MissingRequiredBlocks(t *testing.T) {
	var eng Engine
	// COGS present, commission absent ⇒ hard-required missing.
	_, err := eng.Contribution(ContributionInput{
		NetProceeds: irr(t, 100),
		RateBase:    irr(t, 100),
		Components:  []ComponentInput{abs(cost.ComponentCOGS, irr(t, 10))},
	})
	if !errors.Is(err, ErrMissingRequiredComponent) {
		t.Fatalf("err = %v, want ErrMissingRequiredComponent", err)
	}
}

func TestContribution_DuplicateComponentRejected(t *testing.T) {
	var eng Engine
	_, err := eng.Contribution(ContributionInput{
		NetProceeds: irr(t, 100),
		RateBase:    irr(t, 100),
		Components: []ComponentInput{
			abs(cost.ComponentCOGS, irr(t, 10)),
			abs(cost.ComponentCOGS, irr(t, 20)),
			abs(cost.ComponentCommission, irr(t, 5)),
		},
	})
	if !errors.Is(err, ErrDuplicateComponent) {
		t.Fatalf("err = %v, want ErrDuplicateComponent", err)
	}
}

func TestContribution_CurrencyMismatchRejected(t *testing.T) {
	var eng Engine
	usd, err := money.New(10, "USD", 0)
	if err != nil {
		t.Fatalf("money.New USD: %v", err)
	}
	_, err = eng.Contribution(ContributionInput{
		NetProceeds: irr(t, 100),
		RateBase:    irr(t, 100),
		Components: []ComponentInput{
			{Component: cost.ComponentCOGS, Kind: KindAbsolute, Amount: usd},
			abs(cost.ComponentCommission, irr(t, 5)),
		},
	})
	if !errors.Is(err, money.ErrCurrencyMismatch) {
		t.Fatalf("err = %v, want money.ErrCurrencyMismatch", err)
	}
}

func TestContribution_PartialNotExecutable(t *testing.T) {
	var eng Engine
	got, err := eng.Contribution(ContributionInput{
		NetProceeds: irr(t, 500_000),
		RateBase:    irr(t, 500_000),
		Readiness:   cost.StatePartial,
		Components: []ComponentInput{
			abs(cost.ComponentCOGS, irr(t, 300_000)),
			abs(cost.ComponentCommission, irr(t, 50_000)),
		},
	})
	if err != nil {
		t.Fatalf("Contribution: %v", err)
	}
	if got.Executable() {
		t.Fatal("Partial readiness must NOT be executable")
	}
	if got.Amount.Mantissa() != 150_000 {
		t.Fatalf("analysis contribution = %d, want 150000", got.Amount.Mantissa())
	}
}

// zeroValueContribution reports whether c is the engine's zero-value result,
// i.e. no real Contribution was returned (the Money carries no currency).
func zeroValueContribution(c Contribution) bool { return c.Amount.Currency() == "" }

// TestContribution_NegativeAbsoluteDeductionRejected (issue #60) — a negative
// absolute deduction cannot manufacture contribution. Every deduction component
// must reject a negative Money amount with ErrNegativeDeduction, before any Sub
// arithmetic, and never return a Contribution.
func TestContribution_NegativeAbsoluteDeductionRejected(t *testing.T) {
	var eng Engine
	// Each case supplies COGS+commission (so the hard-required gate is not the
	// blocker) and makes exactly the target component a negative absolute amount.
	cases := []cost.Component{
		cost.ComponentCOGS,
		cost.ComponentCommission,
		cost.ComponentShipping,
		cost.ComponentPromotion,
		cost.ComponentAds,
		cost.ComponentReturns,
	}
	for _, target := range cases {
		target := target
		t.Run(string(target), func(t *testing.T) {
			comps := []ComponentInput{
				abs(cost.ComponentCOGS, irr(t, 100)),
				abs(cost.ComponentCommission, irr(t, 50)),
			}
			neg := abs(target, irr(t, -1))
			switch target {
			case cost.ComponentCOGS:
				comps[0] = neg
			case cost.ComponentCommission:
				comps[1] = neg
			default:
				comps = append(comps, neg)
			}
			got, err := eng.Contribution(ContributionInput{
				NetProceeds: irr(t, 1000),
				RateBase:    irr(t, 1000),
				Readiness:   cost.StateComplete,
				Components:  comps,
			})
			if !errors.Is(err, ErrNegativeDeduction) {
				t.Fatalf("err = %v, want ErrNegativeDeduction", err)
			}
			if got.Executable() {
				t.Fatal("invalid input must not report executable")
			}
			if !zeroValueContribution(got) {
				t.Fatalf("invalid input must return zero Contribution, got %+v", got)
			}
		})
	}
}

// TestContribution_NegativeRateRejected (issue #60) — a rate below the [0,10000]
// bp domain is rejected with ErrRateOutOfRange before ApplyRate runs.
func TestContribution_NegativeRateRejected(t *testing.T) {
	var eng Engine
	got, err := eng.Contribution(ContributionInput{
		NetProceeds: irr(t, 1000),
		RateBase:    irr(t, 1000),
		Readiness:   cost.StateComplete,
		Components: []ComponentInput{
			abs(cost.ComponentCOGS, irr(t, 100)),
			rate(cost.ComponentCommission, -1),
		},
	})
	if !errors.Is(err, ErrRateOutOfRange) {
		t.Fatalf("err = %v, want ErrRateOutOfRange", err)
	}
	if got.Executable() || !zeroValueContribution(got) {
		t.Fatalf("invalid rate must return no executable, zero Contribution, got %+v", got)
	}
}

// TestContribution_AboveMaxRateRejected (issue #60) — a rate above 10000 bp
// (>100%) is rejected with ErrRateOutOfRange.
func TestContribution_AboveMaxRateRejected(t *testing.T) {
	var eng Engine
	_, err := eng.Contribution(ContributionInput{
		NetProceeds: irr(t, 1000),
		RateBase:    irr(t, 1000),
		Readiness:   cost.StateComplete,
		Components: []ComponentInput{
			abs(cost.ComponentCOGS, irr(t, 100)),
			rate(cost.ComponentCommission, MaxRateBasisPoints+1),
		},
	})
	if !errors.Is(err, ErrRateOutOfRange) {
		t.Fatalf("err = %v, want ErrRateOutOfRange", err)
	}
}

// TestContribution_BoundaryRatesAccepted (issue #60) — 0 bp and 10000 bp are the
// inclusive boundaries of the accepted rate domain and must compute
// deterministically.
func TestContribution_BoundaryRatesAccepted(t *testing.T) {
	var eng Engine
	for _, bp := range []int64{MinRateBasisPoints, MaxRateBasisPoints} {
		bp := bp
		t.Run("bp", func(t *testing.T) {
			_, err := eng.Contribution(ContributionInput{
				NetProceeds: irr(t, 1_000_000),
				RateBase:    irr(t, 1_000_000),
				Readiness:   cost.StateComplete,
				Components: []ComponentInput{
					abs(cost.ComponentCOGS, irr(t, 100)),
					rate(cost.ComponentCommission, bp),
				},
			})
			if err != nil {
				t.Fatalf("boundary rate %d bp rejected: %v", bp, err)
			}
		})
	}
}

// TestContribution_ZeroAbsoluteDeductionAccepted (issue #60) — a zero absolute
// deduction is valid and deterministic (zero is not negative).
func TestContribution_ZeroAbsoluteDeductionAccepted(t *testing.T) {
	var eng Engine
	got, err := eng.Contribution(ContributionInput{
		NetProceeds: irr(t, 1000),
		RateBase:    irr(t, 1000),
		Readiness:   cost.StateComplete,
		Components: []ComponentInput{
			abs(cost.ComponentCOGS, irr(t, 0)),
			abs(cost.ComponentCommission, irr(t, 50)),
		},
	})
	if err != nil {
		t.Fatalf("zero deduction rejected: %v", err)
	}
	if got.Amount.Mantissa() != 950 {
		t.Fatalf("contribution = %d, want 950", got.Amount.Mantissa())
	}
}

func TestContribution_NegativeReportedFaithfully(t *testing.T) {
	var eng Engine
	got, err := eng.Contribution(ContributionInput{
		NetProceeds: irr(t, 100_000),
		RateBase:    irr(t, 100_000),
		Readiness:   cost.StateComplete,
		Components: []ComponentInput{
			abs(cost.ComponentCOGS, irr(t, 90_000)),
			rate(cost.ComponentCommission, 2000), // 20% of 100,000 = 20,000
		},
	})
	if err != nil {
		t.Fatalf("Contribution: %v", err)
	}
	if got.Amount.Mantissa() != -10_000 {
		t.Fatalf("contribution = %d, want -10000", got.Amount.Mantissa())
	}
	pos, err := got.IsPositive()
	if err != nil {
		t.Fatalf("IsPositive: %v", err)
	}
	if pos {
		t.Fatal("negative contribution must not report positive")
	}
}
