package httpapi

import (
	"context"
	"errors"
	"fmt"
	"time"

	gateway "github.com/mhosseinab/market-ops/gen/go"
	"github.com/mhosseinab/market-ops/services/core/internal/cost"
	"github.com/mhosseinab/market-ops/services/core/internal/margin"
	"github.com/mhosseinab/market-ops/services/core/internal/money"
	"github.com/mhosseinab/market-ops/services/core/internal/policy"
)

// SimulatePolicy runs the contribution (§9.2) and six-stage policy (§9.3) engines
// over a fully-specified what-if and returns the proposal or the typed blockers
// in policy order. The result is ALWAYS a simulation: non-executable, no approval
// control (§8, §12.3, never-cut). A loose cap/cooldown (PRC-004) or malformed
// money is a structured 400. Authoritative numbers come only from these engines
// (§12.3) — this handler does no arithmetic of its own.
func (s *gatewayServer) SimulatePolicy(
	_ context.Context, req gateway.SimulatePolicyRequestObject,
) (gateway.SimulatePolicyResponseObject, error) {
	if req.Body == nil {
		return gateway.SimulatePolicydefaultJSONResponse{StatusCode: 400, Body: invalidArgErr("request body is required")}, nil
	}
	body := *req.Body

	currentPrice, err := moneyFromGateway(body.CurrentPrice)
	if err != nil {
		return policyBadRequest(err), nil
	}

	components, err := componentsFromGateway(body.Components)
	if err != nil {
		return policyBadRequest(err), nil
	}
	readiness := cost.State(body.Readiness)

	cfg, err := configFromGateway(body.Config)
	if err != nil {
		return policyBadRequest(err), nil
	}

	// The margin engine is the sole authoritative source of a contribution. The
	// contribution oracle evaluates the SKU at a candidate price by taking that
	// price as both the net proceeds and the commission rate base (the P0 owned-
	// offer model), so contribution is a proper function of price.
	var eng margin.Engine
	contributionAt := func(price money.Money) (margin.Contribution, error) {
		return eng.Contribution(margin.ContributionInput{
			NetProceeds: price,
			RateBase:    price,
			Components:  components,
			Readiness:   readiness,
		})
	}

	// Base contribution shown in the result is the as-is contribution at the
	// current price.
	baseContribution, err := contributionAt(currentPrice)
	if err != nil {
		return policyBadRequest(err), nil
	}

	oracle := func(price money.Money) (money.Money, error) {
		c, cerr := contributionAt(price)
		if cerr != nil {
			return money.Money{}, cerr
		}
		return c.Amount, nil
	}

	in := policy.EvaluateInput{
		Config:       cfg,
		CurrentPrice: currentPrice,
		Contribution: oracle,
		Now:          time.Now(),
		// Carry the verified margin readiness through so the approvability gate
		// (CST-003 / PRD §9.2) holds for HTTP callers too. This path is always a
		// simulation (never approvable), but threading readiness keeps the seam
		// honest and fails closed for any non-Complete state.
		Readiness: readiness,
	}
	if body.NowRfc3339 != nil {
		in.Now = *body.NowRfc3339
	}
	if body.LastActionAt != nil {
		t := *body.LastActionAt
		in.LastActionAt = &t
	}

	res, err := policy.Simulate(in)
	if err != nil {
		return policyBadRequest(err), nil
	}

	return gateway.SimulatePolicy200JSONResponse(toGatewaySimulation(res, baseContribution)), nil
}

// componentsFromGateway maps the request components to margin inputs.
func componentsFromGateway(items []gateway.ContributionComponentInput) ([]margin.ComponentInput, error) {
	out := make([]margin.ComponentInput, 0, len(items))
	for _, it := range items {
		c := cost.Component(it.Component)
		if !c.Valid() {
			return nil, errors.New("policy: unknown cost component " + string(it.Component))
		}
		ci := margin.ComponentInput{Component: c}
		if it.Version != nil {
			ci.Version = *it.Version
		}
		switch it.Kind {
		case gateway.Absolute:
			ci.Kind = margin.KindAbsolute
			if it.Amount == nil {
				return nil, errors.New("policy: absolute component " + string(it.Component) + " requires an amount")
			}
			amt, err := moneyFromGateway(*it.Amount)
			if err != nil {
				return nil, err
			}
			ci.Amount = amt
		case gateway.Rate:
			ci.Kind = margin.KindRate
			if it.RateBasisPoints == nil {
				return nil, errors.New("policy: rate component " + string(it.Component) + " requires rateBasisPoints")
			}
			ci.Rate = money.NewBasisPoints(*it.RateBasisPoints)
		default:
			return nil, errors.New("policy: unknown component kind " + string(it.Kind))
		}
		out = append(out, ci)
	}
	return out, nil
}

// configFromGateway maps the request config into a validated policy.Config,
// enforcing the stricter-only rule via policy.NewConfig (PRC-004).
func configFromGateway(c gateway.PolicyConfig) (policy.Config, error) {
	floor, err := moneyFromGateway(c.ContributionFloor)
	if err != nil {
		return policy.Config{}, err
	}

	boundary := policy.Boundary{Known: c.Boundary.Known}
	if c.Boundary.Min != nil {
		m, err := moneyFromGateway(*c.Boundary.Min)
		if err != nil {
			return policy.Config{}, err
		}
		boundary.Min = m
	}
	if c.Boundary.Max != nil {
		m, err := moneyFromGateway(*c.Boundary.Max)
		if err != nil {
			return policy.Config{}, err
		}
		boundary.Max = m
	}

	params := policy.ConfigParams{
		Boundary:          boundary,
		ContributionFloor: floor,
		Strategy:          policy.Strategy(c.Strategy),
		StrategyEnabled:   c.StrategyEnabled,
		Objective:         policy.Objective(c.Objective),
	}
	if c.MovementCapBasisPoints != nil {
		bp := money.NewBasisPoints(*c.MovementCapBasisPoints)
		params.MovementCap = &bp
	}
	if c.CooldownSeconds != nil {
		d := time.Duration(*c.CooldownSeconds) * time.Second
		params.Cooldown = &d
	}
	if c.Reference != nil {
		ref, err := moneyFromGateway(*c.Reference)
		if err != nil {
			return policy.Config{}, err
		}
		params.Reference = ref
	}
	if c.UndercutBasisPoints != nil {
		params.UndercutBp = money.NewBasisPoints(*c.UndercutBasisPoints)
	}
	return policy.NewConfig(params)
}

// toGatewaySimulation maps a policy result and the base contribution to the wire
// type. It always sets simulation=true and approvable=false — a simulation never
// carries an approval control.
func toGatewaySimulation(res policy.Result, base margin.Contribution) gateway.PolicySimulationResult {
	out := gateway.PolicySimulationResult{
		Simulation:   true,
		Approvable:   false,
		Contribution: toGatewayContribution(base),
		Blockers:     toGatewayBlockers(res.Blockers),
	}
	if res.Proposed != nil {
		out.Proposal = &gateway.PolicyProposal{
			Price:        moneyToGateway(res.Proposed.Price),
			Contribution: moneyToGateway(res.Proposed.Contribution),
		}
	}
	return out
}

func toGatewayContribution(c margin.Contribution) gateway.Contribution {
	deductions := make([]gateway.ContributionDeduction, 0, len(c.Deductions))
	for _, d := range c.Deductions {
		deductions = append(deductions, gateway.ContributionDeduction{
			Component: gateway.CostComponent(d.Component),
			Amount:    moneyToGateway(d.Amount),
			Kind:      kindToGateway(d.Kind),
			Version:   d.Version,
		})
	}
	return gateway.Contribution{
		Amount:       moneyToGateway(c.Amount),
		NetProceeds:  moneyToGateway(c.NetProceeds),
		Deductions:   deductions,
		Readiness:    gateway.MarginReadinessState(c.Readiness),
		Executable:   c.Executable(),
		RoundingRule: c.RoundingRule,
	}
}

func toGatewayBlockers(blockers []policy.Blocker) []gateway.PolicyBlocker {
	out := make([]gateway.PolicyBlocker, 0, len(blockers))
	for _, b := range blockers {
		out = append(out, gateway.PolicyBlocker{
			Stage:      gateway.PolicyStage(b.Stage.String()),
			StageOrder: int(b.Stage),
			Code:       gateway.PolicyBlockerCode(b.Code),
			Message:    b.Message,
		})
	}
	return out
}

func kindToGateway(k margin.Kind) gateway.ContributionComponentKind {
	if k == margin.KindRate {
		return gateway.Rate
	}
	return gateway.Absolute
}

// moneyFromGateway builds an authoritative money.Money from the wire triple,
// validating the currency and exponent range (§9.1). No float is involved.
func moneyFromGateway(a gateway.MoneyAmount) (money.Money, error) {
	if a.Exponent > 127 || a.Exponent < -128 {
		return money.Money{}, errors.New("policy: money exponent out of int8 range")
	}
	mantissa, err := parseWireMantissa(a.Mantissa)
	if err != nil {
		return money.Money{}, fmt.Errorf("policy: invalid money mantissa %q: %w", a.Mantissa, err)
	}
	return money.New(mantissa, a.Currency, int8(a.Exponent))
}

// moneyToGateway renders a money.Money as the wire triple.
func moneyToGateway(m money.Money) gateway.MoneyAmount {
	return gateway.MoneyAmount{
		Mantissa: wireMantissa(m.Mantissa()),
		Currency: m.Currency(),
		Exponent: int(m.Exponent()),
	}
}

func policyBadRequest(err error) gateway.SimulatePolicydefaultJSONResponse {
	return gateway.SimulatePolicydefaultJSONResponse{StatusCode: 400, Body: policyErr(err)}
}

func policyErr(err error) gateway.ErrorEnvelope {
	code := "POLICY_ERROR"
	switch {
	case errors.Is(err, policy.ErrMovementCapTooLoose), errors.Is(err, policy.ErrCooldownTooLoose),
		errors.Is(err, policy.ErrInvalidMovementCap), errors.Is(err, policy.ErrInvalidCooldown):
		code = "POLICY_CONFIG_INVALID"
	case errors.Is(err, margin.ErrMissingRequiredComponent):
		code = "COST_INCOMPLETE"
	case errors.Is(err, margin.ErrDuplicateComponent), errors.Is(err, margin.ErrInvalidComponent),
		errors.Is(err, margin.ErrRateBaseRequired):
		code = "INVALID_CONTRIBUTION_INPUT"
	case errors.Is(err, money.ErrCurrencyMismatch), errors.Is(err, money.ErrExponentMismatch),
		errors.Is(err, money.ErrInvalidCurrency):
		code = "MONEY_INVALID"
	}
	return gateway.ErrorEnvelope{Code: code, Message: err.Error()}
}
