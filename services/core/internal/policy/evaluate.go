package policy

import (
	"sort"
	"time"

	"github.com/mhosseinab/market-ops/services/core/internal/money"
)

// Stage identifies one of the six ordered policy stages (§9.3). The integer
// value IS the precedence order (lower = earlier = higher precedence), so
// blockers sort by Stage to surface in policy order. Stages 1–4 (boundary, hard
// floor, movement cap, cooldown) are HARD constraints a later stage can never
// override; stages 5–6 (strategy, objective) are subordinate selectors.
type Stage int

const (
	StageBoundary Stage = iota
	StageHardFloor
	StageMovementCap
	StageCooldown
	StageStrategy
	StageObjective
)

// String renders the stage name for diagnostics.
func (s Stage) String() string {
	switch s {
	case StageBoundary:
		return "boundary"
	case StageHardFloor:
		return "hard_floor"
	case StageMovementCap:
		return "movement_cap"
	case StageCooldown:
		return "cooldown"
	case StageStrategy:
		return "strategy"
	case StageObjective:
		return "objective"
	default:
		return "unknown"
	}
}

// IsHard reports whether the stage is a hard constraint (boundary, floor, cap,
// cooldown) that no later stage may override.
func (s Stage) IsHard() bool {
	switch s {
	case StageBoundary, StageHardFloor, StageMovementCap, StageCooldown:
		return true
	default:
		return false
	}
}

// BlockerCode is a stable, machine-readable reason a stage blocked. Free text
// lives only in Message and carries no authority (§8 free-text containment).
type BlockerCode string

const (
	BlockerBoundaryUnknown     BlockerCode = "boundary_unknown"
	BlockerBoundaryInvalid     BlockerCode = "boundary_invalid"
	BlockerBelowFloor          BlockerCode = "contribution_below_floor"
	BlockerCrossesZero         BlockerCode = "contribution_crosses_zero"
	BlockerMovementInfeasible  BlockerCode = "movement_cap_infeasible"
	BlockerCooldownActive      BlockerCode = "cooldown_active"
	BlockerStrategyDisabled    BlockerCode = "strategy_disabled"
	BlockerObjectiveInfeasible BlockerCode = "objective_infeasible"
)

// Blocker is one typed reason a policy stage prevented a proposal. Blockers are
// returned in policy order (by Stage) and consumed by the chat/screens surfaces
// (S17+); none of them ever carries an approval control.
type Blocker struct {
	Stage   Stage
	Code    BlockerCode
	Message string
}

// Proposal is an accepted policy result: a proposed price and its contribution.
// It is only ever populated when every hard stage passed and the contribution is
// strictly positive.
type Proposal struct {
	Price        money.Money
	Contribution money.Money
}

// Result is the outcome of an evaluation: at most one Proposal, and the ordered
// blockers that prevented one (empty when a proposal exists). Simulation marks a
// non-executable "what-if" that NEVER carries an approval control.
type Result struct {
	Proposed   *Proposal
	Blockers   []Blocker
	Simulation bool
}

// Approvable reports whether this result is eligible to be bound to an approval
// control downstream (S17). A simulation is NEVER approvable, and a result with
// any blocker or no proposal is not approvable. This method does not itself
// create any control — it only states eligibility; the structured, version-bound
// control is minted outside this package.
func (r Result) Approvable() bool {
	return !r.Simulation && r.Proposed != nil && len(r.Blockers) == 0
}

// ContributionFunc returns the contribution amount at a candidate price. The
// caller wires it from the margin engine over the SKU's in-force cost profile
// (CST-002); policy stays independent of margin so its ordering/zero-floor
// invariants are property-tested against arbitrary monotone contribution models.
type ContributionFunc func(price money.Money) (money.Money, error)

// EvaluateInput is the pure input to Evaluate. It carries the validated Config,
// the current price, a contribution oracle, the evaluation clock, and the last
// action instant (nil ⇒ no prior action, so cooldown cannot bind).
type EvaluateInput struct {
	Config       Config
	CurrentPrice money.Money
	Contribution ContributionFunc
	Now          time.Time
	LastActionAt *time.Time
}

// Evaluate runs the six stages in order and returns either a Proposal (all hard
// stages passed, contribution strictly positive) or the ordered Blockers that
// prevented one. It re-validates the stricter-only cap/cooldown (PRC-004) so a
// loose Config can never yield a proposal.
//
// The construction guarantees the never-cut ordering invariant: strategy and
// objective (stages 5–6) only influence the DESIRED price; that desire is then
// clamped into the feasible window defined by boundary ∩ movement-cap and gated
// by the hard floor and the zero-cross guard. No stages-5/6 choice can produce an
// output that violates a hard stage — a fact the property tests prove.
func Evaluate(in EvaluateInput) (Result, error) {
	if err := in.Config.validate(); err != nil {
		return Result{}, err
	}
	cfg := in.Config

	// Stage 1 — marketplace price boundary.
	if !cfg.Boundary.Known {
		return blocked(StageBoundary, BlockerBoundaryUnknown,
			"marketplace price boundary is unknown; no executable price exists"), nil
	}
	bcmp, err := cfg.Boundary.Min.Compare(cfg.Boundary.Max)
	if err != nil {
		return Result{}, err
	}
	if bcmp > 0 {
		return blocked(StageBoundary, BlockerBoundaryInvalid,
			"marketplace price boundary is invalid (min exceeds max)"), nil
	}

	// Stage 3 window — maximum price movement around the current price. The cap
	// delta is rounded DOWN (toward zero) so the allowed movement never exceeds
	// the configured cap.
	capDelta, err := in.CurrentPrice.ApplyRate(cfg.MovementCap, money.RoundDown)
	if err != nil {
		return Result{}, err
	}
	moveLow, err := in.CurrentPrice.Sub(capDelta)
	if err != nil {
		return Result{}, err
	}
	moveHigh, err := in.CurrentPrice.Add(capDelta)
	if err != nil {
		return Result{}, err
	}

	// Feasible window = boundary ∩ movement. An empty intersection means the cap
	// cannot reach any boundary-valid price (stage 3 blocks).
	feasLow, err := maxMoney(cfg.Boundary.Min, moveLow)
	if err != nil {
		return Result{}, err
	}
	feasHigh, err := minMoney(cfg.Boundary.Max, moveHigh)
	if err != nil {
		return Result{}, err
	}
	fcmp, err := feasLow.Compare(feasHigh)
	if err != nil {
		return Result{}, err
	}
	if fcmp > 0 {
		return blocked(StageMovementCap, BlockerMovementInfeasible,
			"movement cap admits no price inside the marketplace boundary"), nil
	}

	// Stage 5 — selected strategy proposes a desired price.
	if !cfg.StrategyEnabled {
		return blocked(StageStrategy, BlockerStrategyDisabled,
			"selected pricing strategy is not enabled"), nil
	}
	desired, err := strategyDesired(cfg, in.CurrentPrice)
	if err != nil {
		return Result{}, err
	}

	// Stage 6 — objective picks the preferred feasible price.
	preferred, err := objectivePreferred(cfg, desired, feasLow, feasHigh)
	if err != nil {
		return Result{}, err
	}

	// Stage 2 — hard contribution floor + zero-cross guard, applied to the final.
	// This is where the ordering is enforced: the hard floor can force the price
	// UP off the objective's preference (stage 2 overrides stage 6), and if even
	// the max-contribution feasible price cannot satisfy floor/positivity the
	// result is a hard-floor blocker — never a below-floor or zero-crossing output.
	final, finalContrib, floorBlk, err := resolveFloor(cfg, in.Contribution, preferred, feasHigh)
	if err != nil {
		return Result{}, err
	}

	var blockers []Blocker
	if floorBlk != nil {
		blockers = append(blockers, *floorBlk)
	}

	// Stage 4 — cooldown blocks a price CHANGE while active; a hold is allowed.
	if cd, err := cooldownBlocker(cfg, in, final); err != nil {
		return Result{}, err
	} else if cd != nil {
		blockers = append(blockers, *cd)
	}

	if len(blockers) > 0 {
		return ordered(blockers), nil
	}
	return Result{Proposed: &Proposal{Price: final, Contribution: finalContrib}}, nil
}

// Simulate runs the same engines as Evaluate but labels the result a
// non-executable simulation. A simulation NEVER carries an approval control
// (Approvable always returns false for it) — the never-cut free-text/simulation
// containment invariant (§8, §12.3): a what-if can inform, never authorize.
func Simulate(in EvaluateInput) (Result, error) {
	r, err := Evaluate(in)
	if err != nil {
		return Result{}, err
	}
	r.Simulation = true
	return r, nil
}

// strategyDesired computes stage 5's desired price. All price math is Money
// methods; the undercut depth is a basis-point rate rounded down (never widening
// the discount past the configured depth).
func strategyDesired(cfg Config, current money.Money) (money.Money, error) {
	switch cfg.Strategy {
	case StrategyHold:
		return current, nil
	case StrategyMatch:
		return cfg.Reference, nil
	case StrategyUndercut:
		cut, err := cfg.Reference.ApplyRate(cfg.UndercutBp, money.RoundDown)
		if err != nil {
			return money.Money{}, err
		}
		return cfg.Reference.Sub(cut)
	default:
		// Unknown/empty strategy holds — the safest choice (no movement desired).
		return current, nil
	}
}

// objectivePreferred computes stage 6's preferred price within the feasible
// window. MaximizeContribution prefers the highest feasible price; TrackStrategy
// clamps the strategy's desire into the window.
func objectivePreferred(cfg Config, desired, low, high money.Money) (money.Money, error) {
	switch cfg.Objective {
	case ObjectiveMaximizeContribution:
		return high, nil
	case ObjectiveTrackStrategy:
		return clamp(desired, low, high)
	default:
		return clamp(desired, low, high)
	}
}

// resolveFloor enforces the hard floor and the zero-cross guard on the objective
// preference. If the preference satisfies both, it stands. Otherwise the hard
// floor forces the price up to the max-contribution feasible price (feasHigh): if
// THAT satisfies both, it becomes the output (stage 2 overriding stage 6); if not,
// the case is genuinely infeasible and a hard-floor blocker is returned. It never
// returns a below-floor or non-positive contribution as an accepted output.
func resolveFloor(
	cfg Config, contrib ContributionFunc, preferred, feasHigh money.Money,
) (money.Money, money.Money, *Blocker, error) {
	prefC, err := contrib(preferred)
	if err != nil {
		return money.Money{}, money.Money{}, nil, err
	}
	below, zero, err := classifyFloor(cfg, prefC)
	if err != nil {
		return money.Money{}, money.Money{}, nil, err
	}
	if !below && !zero {
		return preferred, prefC, nil, nil
	}

	highC, err := contrib(feasHigh)
	if err != nil {
		return money.Money{}, money.Money{}, nil, err
	}
	belowHigh, zeroHigh, err := classifyFloor(cfg, highC)
	if err != nil {
		return money.Money{}, money.Money{}, nil, err
	}
	if !belowHigh && !zeroHigh {
		return feasHigh, highC, nil, nil
	}

	blk := hardFloorBlocker(belowHigh, zeroHigh)
	return feasHigh, highC, &blk, nil
}

// classifyFloor reports whether a contribution is below the hard floor and/or
// crosses zero (≤ 0). The zero-cross check is INDEPENDENT of the floor: even a
// floor set at or below zero cannot admit a non-positive contribution ("no action
// may cross zero contribution", §9.3, never-cut).
func classifyFloor(cfg Config, c money.Money) (below bool, crossesZero bool, err error) {
	fcmp, err := c.Compare(cfg.ContributionFloor)
	if err != nil {
		return false, false, err
	}
	z, err := money.Zero(c.Currency(), c.Exponent())
	if err != nil {
		return false, false, err
	}
	zcmp, err := c.Compare(z)
	if err != nil {
		return false, false, err
	}
	return fcmp < 0, zcmp <= 0, nil
}

// hardFloorBlocker builds the stage-2 blocker, preferring the zero-cross code
// when the contribution is non-positive (the stronger never-cut violation).
func hardFloorBlocker(below, crossesZero bool) Blocker {
	if crossesZero {
		return Blocker{
			Stage:   StageHardFloor,
			Code:    BlockerCrossesZero,
			Message: "no feasible price keeps contribution above zero",
		}
	}
	return Blocker{
		Stage:   StageHardFloor,
		Code:    BlockerBelowFloor,
		Message: "no feasible price meets the hard contribution floor",
	}
}

// cooldownBlocker returns a cooldown blocker when a prior action is still within
// the cooldown window AND the proposed price differs from the current price (a
// change). A hold during cooldown is permitted.
func cooldownBlocker(cfg Config, in EvaluateInput, final money.Money) (*Blocker, error) {
	if in.LastActionAt == nil {
		return nil, nil
	}
	deadline := in.LastActionAt.Add(cfg.Cooldown)
	if !in.Now.Before(deadline) {
		return nil, nil
	}
	changeCmp, err := final.Compare(in.CurrentPrice)
	if err != nil {
		return nil, err
	}
	if changeCmp == 0 {
		return nil, nil
	}
	return &Blocker{
		Stage:   StageCooldown,
		Code:    BlockerCooldownActive,
		Message: "cooldown is active; a price change is not permitted yet",
	}, nil
}

// clamp returns v constrained to [low, high] using Money comparison.
func clamp(v, low, high money.Money) (money.Money, error) {
	lc, err := v.Compare(low)
	if err != nil {
		return money.Money{}, err
	}
	if lc < 0 {
		return low, nil
	}
	hc, err := v.Compare(high)
	if err != nil {
		return money.Money{}, err
	}
	if hc > 0 {
		return high, nil
	}
	return v, nil
}

// maxMoney returns the larger of a and b.
func maxMoney(a, b money.Money) (money.Money, error) {
	cmp, err := a.Compare(b)
	if err != nil {
		return money.Money{}, err
	}
	if cmp < 0 {
		return b, nil
	}
	return a, nil
}

// minMoney returns the smaller of a and b.
func minMoney(a, b money.Money) (money.Money, error) {
	cmp, err := a.Compare(b)
	if err != nil {
		return money.Money{}, err
	}
	if cmp > 0 {
		return b, nil
	}
	return a, nil
}

// blocked builds a single-blocker Result (no proposal).
func blocked(stage Stage, code BlockerCode, msg string) Result {
	return Result{Blockers: []Blocker{{Stage: stage, Code: code, Message: msg}}}
}

// ordered returns a Result whose blockers are sorted in policy order (by Stage).
func ordered(blockers []Blocker) Result {
	sort.SliceStable(blockers, func(i, j int) bool {
		return blockers[i].Stage < blockers[j].Stage
	})
	return Result{Blockers: blockers}
}
