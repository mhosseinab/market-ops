package margin

import (
	"errors"
	"time"

	"github.com/mhosseinab/market-ops/services/core/internal/cost"
	"github.com/mhosseinab/market-ops/services/core/internal/money"
)

// Engine errors. Every failure is typed so the transport and the recommendation
// assembler (S17) can turn it into a structured blocker rather than free text.
var (
	// ErrMissingRequiredComponent — a hard-required component (COGS or
	// commission, §9.2) was not supplied. No contribution number exists without
	// it; the SKU is not action-eligible (CST-003 Missing).
	ErrMissingRequiredComponent = errors.New("margin: required cost component missing")
	// ErrDuplicateComponent — the same component appears twice in one input. A
	// contribution is computed over exactly one in-force version per component
	// (CST-002); a duplicate is a caller bug, not a silent last-wins.
	ErrDuplicateComponent = errors.New("margin: duplicate cost component in input")
	// ErrInvalidComponent — an unknown cost component token was supplied.
	ErrInvalidComponent = errors.New("margin: unknown cost component")
	// ErrRateBaseRequired — a rate-kind component was supplied with no rate base
	// to apply the percentage to.
	ErrRateBaseRequired = errors.New("margin: rate component requires a rate base")
	// ErrNegativeDeduction — an absolute deduction was supplied with a negative
	// amount. Every §9.2 deduction subtracts from net proceeds, so a negative
	// value would ADD to contribution and could manufacture an apparently
	// executable margin (issue #60). Deductions are non-negative; zero is valid.
	// The engine fails closed rather than coerce/abs the value.
	ErrNegativeDeduction = errors.New("margin: deduction amount must be non-negative")
	// ErrRateOutOfRange — a rate-kind deduction was supplied with a basis-point
	// rate outside the accepted domain [MinRateBasisPoints, MaxRateBasisPoints]
	// (issue #60). A deduction rate cannot be negative and cannot exceed 100% of
	// its rate base; PRD §9.2 defines no tighter per-component cap, so the
	// defensible universal bound is [0, 10000] bp inclusive.
	ErrRateOutOfRange = errors.New("margin: rate basis points out of range [0,10000]")
)

// Accepted rate domain for a KindRate deduction, in basis points (issue #60).
// The bounds are inclusive: 0% and 100% are both valid boundaries. MaxRate is
// pinned to money.BasisPointScale (10000 bp = 100%) so there is one source of
// truth for "100%". PRD §9.2 specifies no tighter per-component cap.
const (
	MinRateBasisPoints int64 = 0
	MaxRateBasisPoints int64 = money.BasisPointScale
)

// Kind distinguishes an absolute money deduction from a fixed-point rate applied
// to the rate base. Commission and promotion are commonly a percentage of price;
// COGS and packaging are absolute amounts. Both routes stay entirely in money
// arithmetic (no float, §9.1).
type Kind int

const (
	// KindAbsolute — the component is an exact money.Money amount.
	KindAbsolute Kind = iota
	// KindRate — the component is a basis-point rate applied to the input's
	// RateBase (e.g. commission = 12% of price), rounded by ContributionRounder.
	KindRate
)

// ComponentInput is one deduction of the §9.2 model with its cost-profile
// provenance. Exactly one of Amount (KindAbsolute) or Rate (KindRate) is
// meaningful. Version is the cost-profile component version (CST-002) that
// produced this value, carried through so a historical contribution reproduces
// the exact inputs that generated it.
type ComponentInput struct {
	Component cost.Component
	Kind      Kind
	Amount    money.Money       // meaningful when Kind == KindAbsolute
	Rate      money.BasisPoints // meaningful when Kind == KindRate
	Version   int64             // cost-profile version id (0 ⇒ unversioned/synthetic)
}

// ContributionInput is the pure input to the contribution engine. It carries no
// DB and no clock: the caller (cost service / recommendation assembler) resolves
// the in-force cost-profile versions and hands over money values, so the model
// is deterministic and reproducible from its recorded inputs alone (CST-002).
type ContributionInput struct {
	// NetProceeds is the net seller proceeds (top line of §9.2). It is an
	// authoritative money.Money — never a float — in the account's currency.
	NetProceeds money.Money
	// RateBase is the base a KindRate component's percentage applies to (usually
	// the sale/list price). It must share NetProceeds' currency and exponent.
	RateBase money.Money
	// Components are the deductions in §9.2 order. Applicable/optional components
	// that do not apply are simply absent; readiness (below) decides whether that
	// absence blocks execution.
	Components []ComponentInput
	// Readiness is the derived four-state margin readiness (CST-003) for this SKU
	// at the evaluation instant. Only Complete makes the result executable; the
	// engine still computes the analysis number for Partial.
	Readiness cost.State
	// AsOf is the instant the contribution is computed for (audit/provenance,
	// CST-002). Zero is allowed for a synthetic/simulation input.
	AsOf time.Time
}

// Deduction is one resolved subtraction in the contribution breakdown, retained
// so the number is fully explainable (PRC-001 inputs) and reproducible.
type Deduction struct {
	Component cost.Component
	Amount    money.Money
	Kind      Kind
	Version   int64
}

// Contribution is the engine's result: the exact contribution amount, its full
// breakdown, the readiness that governs executability, and the rounding-rule
// version that produced it.
type Contribution struct {
	Amount       money.Money
	NetProceeds  money.Money
	Deductions   []Deduction
	Readiness    cost.State
	RoundingRule string
	AsOf         time.Time
}

// Executable reports whether this contribution may drive an executable
// recommendation: only Complete readiness qualifies (PRD §9.2 / CST-003). Partial
// may be shown as analysis but never exposes an approval control; Stale/Missing
// block outright. This is the readiness gate the recommendation/approval planes
// (S17) rely on.
func (c Contribution) Executable() bool { return c.Readiness == cost.StateComplete }

// IsPositive reports whether the contribution is strictly greater than zero. It
// is the zero-crossing test the policy engine uses ("no action may cross zero
// contribution", §9.3); comparison rejects a currency/exponent mismatch.
func (c Contribution) IsPositive() (bool, error) {
	zero, err := money.Zero(c.Amount.Currency(), c.Amount.Exponent())
	if err != nil {
		return false, err
	}
	cmp, err := c.Amount.Compare(zero)
	if err != nil {
		return false, err
	}
	return cmp > 0, nil
}

// Engine is the stateless contribution calculator. It holds no dependencies so
// the same instance is safe for concurrent use and trivially testable.
type Engine struct{}

// Contribution computes the §9.2 contribution from net proceeds and the supplied
// component deductions, entirely in money/basis-point arithmetic (rule 2, §9.1).
// It requires COGS and commission (the hard-required components); their absence
// is ErrMissingRequiredComponent (a SKU is action-eligible only after a confirmed
// COGS exists). Every subtraction routes through money.Money.Sub, which rejects a
// currency/exponent mismatch, and every rate through ApplyRate with the versioned
// ContributionRounder.
func (Engine) Contribution(in ContributionInput) (Contribution, error) {
	seen := make(map[cost.Component]bool, len(in.Components))
	deductions := make([]Deduction, 0, len(in.Components))

	acc := in.NetProceeds
	for _, comp := range in.Components {
		if !comp.Component.Valid() {
			return Contribution{}, ErrInvalidComponent
		}
		if seen[comp.Component] {
			return Contribution{}, ErrDuplicateComponent
		}
		seen[comp.Component] = true

		// Domain-validate the deduction BEFORE any arithmetic (issue #60): a
		// negative absolute amount or an out-of-range rate would otherwise be
		// subtracted/applied and could manufacture an executable margin. Fail
		// closed with a typed error; never coerce, clamp, or abs.
		if err := validateComponentDomain(comp); err != nil {
			return Contribution{}, err
		}

		amount, err := resolveComponent(comp, in.RateBase)
		if err != nil {
			return Contribution{}, err
		}
		next, err := acc.Sub(amount)
		if err != nil {
			return Contribution{}, err
		}
		acc = next
		deductions = append(deductions, Deduction{
			Component: comp.Component,
			Amount:    amount,
			Kind:      comp.Kind,
			Version:   comp.Version,
		})
	}

	for _, required := range hardRequiredComponents() {
		if !seen[required] {
			return Contribution{}, ErrMissingRequiredComponent
		}
	}

	return Contribution{
		Amount:       acc,
		NetProceeds:  in.NetProceeds,
		Deductions:   deductions,
		Readiness:    in.Readiness,
		RoundingRule: ContributionRoundingRule,
		AsOf:         in.AsOf,
	}, nil
}

// validateComponentDomain rejects a deduction whose sign or rate is outside the
// §9.2 domain (issue #60), so an invalid cost input fails closed instead of
// increasing contribution. It scopes the checks to the deduction components and
// their rates only — NetProceeds and RateBase are top-line values, not
// deductions, and are not subject to the non-negative rule here. It performs no
// money arithmetic: absolute amounts are compared by mantissa and rates by their
// basis-point value.
func validateComponentDomain(comp ComponentInput) error {
	switch comp.Kind {
	case KindAbsolute:
		if comp.Amount.Mantissa() < 0 {
			return ErrNegativeDeduction
		}
	case KindRate:
		v := comp.Rate.Value()
		if v < MinRateBasisPoints || v > MaxRateBasisPoints {
			return ErrRateOutOfRange
		}
	}
	return nil
}

// resolveComponent turns a component input into the exact money amount to
// subtract: an absolute amount as given, or the rate base scaled by the
// basis-point rate through the versioned rounder. No float, no raw operator.
func resolveComponent(comp ComponentInput, rateBase money.Money) (money.Money, error) {
	switch comp.Kind {
	case KindAbsolute:
		return comp.Amount, nil
	case KindRate:
		if rateBase.Currency() == "" {
			return money.Money{}, ErrRateBaseRequired
		}
		return rateBase.ApplyRate(comp.Rate, ContributionRounder())
	default:
		return money.Money{}, ErrInvalidComponent
	}
}

// hardRequiredComponents returns the always-required components (§9.2): COGS and
// commission. It reuses the cost package's classification so there is one source
// of truth for "which components are hard-required".
func hardRequiredComponents() []cost.Component {
	out := make([]cost.Component, 0, 2)
	for _, c := range cost.AllComponents {
		if c.IsHardRequired() {
			out = append(out, c)
		}
	}
	return out
}
