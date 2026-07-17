package margin

import (
	_ "embed"
	"encoding/json"
	"fmt"

	"github.com/mhosseinab/market-ops/services/core/internal/cost"
	"github.com/mhosseinab/market-ops/services/core/internal/money"
)

// settlementsJSON is the committed set of synthetic settlement examples used to
// reconcile the contribution engine (Gate 0a, §20.2). Five synthetic examples
// ship now; the real ≥30 representative settlement examples arrive with S35,
// which is GATED (a live/paid measurement step) and never run unattended.
//
//go:embed fixtures/settlements.json
var settlementsJSON []byte

// SettlementComponent is one component of a settlement example as authored in the
// fixture file. Exactly one of Amount (kind "absolute") or RateBp (kind "rate")
// is meaningful.
type SettlementComponent struct {
	Component cost.Component `json:"component"`
	Kind      string         `json:"kind"`
	Amount    money.Money    `json:"amount"`
	RateBp    int64          `json:"rateBp"`
	Version   int64          `json:"version"`
}

// SettlementExample is one reconciliation case: the inputs to the contribution
// engine plus the independently-derived expected contribution and the tolerance
// (in mantissa units of the amount's exponent) the engine must match within.
type SettlementExample struct {
	Name                 string                `json:"name"`
	Description          string                `json:"description"`
	NetProceeds          money.Money           `json:"netProceeds"`
	RateBase             money.Money           `json:"rateBase"`
	Readiness            cost.State            `json:"readiness"`
	Components           []SettlementComponent `json:"components"`
	ExpectedContribution money.Money           `json:"expectedContribution"`
	ToleranceMantissa    int64                 `json:"toleranceMantissa"`
}

// input converts a settlement example into a ContributionInput.
func (e SettlementExample) input() (ContributionInput, error) {
	comps := make([]ComponentInput, 0, len(e.Components))
	for _, c := range e.Components {
		ci := ComponentInput{Component: c.Component, Version: c.Version}
		switch c.Kind {
		case "absolute":
			ci.Kind = KindAbsolute
			ci.Amount = c.Amount
		case "rate":
			ci.Kind = KindRate
			ci.Rate = money.NewBasisPoints(c.RateBp)
		default:
			return ContributionInput{}, fmt.Errorf("margin: settlement %q: unknown component kind %q", e.Name, c.Kind)
		}
		comps = append(comps, ci)
	}
	return ContributionInput{
		NetProceeds: e.NetProceeds,
		RateBase:    e.RateBase,
		Components:  comps,
		Readiness:   e.Readiness,
	}, nil
}

// ReconResult is the outcome of reconciling one settlement example.
type ReconResult struct {
	Name     string
	Expected money.Money
	Got      money.Money
	// DeltaMantissa is got − expected in mantissa units (0 on an exact match).
	DeltaMantissa int64
	Matched       bool
	Err           error
}

// LoadSettlementExamples parses the embedded synthetic settlement fixtures.
func LoadSettlementExamples() ([]SettlementExample, error) {
	var examples []SettlementExample
	if err := json.Unmarshal(settlementsJSON, &examples); err != nil {
		return nil, fmt.Errorf("margin: parse settlement fixtures: %w", err)
	}
	return examples, nil
}

// Reconcile runs the contribution engine over each example and reports, per
// example, whether the engine output matches the expected contribution within
// the example's declared tolerance. The comparison is exact-integer money
// arithmetic (no float): the delta is a money.Sub and the tolerance bound is a
// money.Compare — the same rounding rule (ContributionRounder) governs both the
// engine and the fixtures.
func Reconcile(examples []SettlementExample) []ReconResult {
	var eng Engine
	results := make([]ReconResult, 0, len(examples))
	for _, e := range examples {
		res := ReconResult{Name: e.Name, Expected: e.ExpectedContribution}
		in, err := e.input()
		if err != nil {
			res.Err = err
			results = append(results, res)
			continue
		}
		got, err := eng.Contribution(in)
		if err != nil {
			res.Err = err
			results = append(results, res)
			continue
		}
		res.Got = got.Amount
		matched, delta, err := withinTolerance(got.Amount, e.ExpectedContribution, e.ToleranceMantissa)
		res.Err = err
		res.Matched = matched
		res.DeltaMantissa = delta
		results = append(results, res)
	}
	return results
}

// withinTolerance reports whether got is within ±tolerance (mantissa units) of
// expected, returning the signed delta. All arithmetic is money methods: the
// delta is got−expected, and the ±tolerance bounds are compared with money.Compare.
func withinTolerance(got, expected money.Money, tolerance int64) (bool, int64, error) {
	delta, err := got.Sub(expected)
	if err != nil {
		return false, 0, err
	}
	upper, err := money.New(tolerance, expected.Currency(), expected.Exponent())
	if err != nil {
		return false, 0, err
	}
	lower, err := upper.Neg()
	if err != nil {
		return false, 0, err
	}
	hiCmp, err := delta.Compare(upper)
	if err != nil {
		return false, 0, err
	}
	loCmp, err := delta.Compare(lower)
	if err != nil {
		return false, 0, err
	}
	within := hiCmp <= 0 && loCmp >= 0
	return within, delta.Mantissa(), nil
}
