// Package policy implements the fixed six-stage pricing policy order (PRD §9.3,
// PRC-003/004):
//
//  1. marketplace price boundary
//  2. hard contribution floor
//  3. maximum price movement (cap)
//  4. cooldown
//  5. selected pricing strategy
//  6. objective optimization
//
// The order is a never-cut invariant (§4.6): a later stage can NEVER override an
// earlier HARD constraint (boundary, floor, cap, cooldown), and no action may
// cross zero contribution. Those two properties are proven by rapid property
// tests (policy_prop_test.go), not merely reviewed. The default movement cap is
// 5% (500 bp) and the default cooldown is 60 minutes; a P0 account may configure
// STRICTER values only — a looser cap or shorter cooldown is rejected (PRC-004).
//
// Money invariant (§9.1): every value here is a money.Money or a fixed-point
// money.BasisPoints; there is NO float and NO raw integer operator in this
// package (the semgrep/forbidigo money guard covers internal/policy). All price
// math routes through Money methods. This engine — with internal/margin — is the
// only authoritative source of a pricing decision; the model plane may never
// compute one (§12.3). Blockers carry human-readable messages but NO authority:
// free text never approves (§8, never-cut). A simulation NEVER carries an
// approval control (see Simulate).
package policy

import (
	"time"

	"github.com/mhosseinab/market-ops/services/core/internal/money"
)

// movementCapMaxBp is the HARD maximum price movement: 5% == 500 basis points
// (§9.3, PRC-004). It is an immutable, unexported constant — the never-cut
// ceiling is compile-time data, never mutable runtime state, so no caller can
// widen the default or the accepted maximum. validateCap compares against this
// literal directly, and DefaultMovementCap constructs a fresh value from it.
const movementCapMaxBp int64 = 500

// DefaultMovementCap returns the default maximum price movement as a FRESH
// money.BasisPoints value: 5% == 500 basis points (§9.3). An account may
// configure a SMALLER cap (stricter) only. It is a function returning a new
// value — not an assignable package variable — so the hard ceiling can never be
// mutated or widened through it (PRC-004).
func DefaultMovementCap() money.BasisPoints {
	return money.NewBasisPoints(movementCapMaxBp)
}

// DefaultCooldown is the default minimum interval between price actions: 60
// minutes (§9.3). An account may configure a LONGER cooldown (stricter) only.
// time.Hour is exactly 60 minutes (no arithmetic operator on a money path).
const DefaultCooldown = time.Hour

// Config errors (PRC-004). A looser-than-default cap or cooldown is rejected;
// nonsensical values are rejected too. These are typed so the transport can map
// them to a precise 4xx rather than free text.
var (
	// ErrMovementCapTooLoose — configured cap exceeds the 5% default (looser).
	ErrMovementCapTooLoose = policyError("policy: movement cap looser than the 5% default is not allowed (PRC-004)")
	// ErrCooldownTooLoose — configured cooldown is shorter than the 60m default.
	ErrCooldownTooLoose = policyError("policy: cooldown shorter than the 60m default is not allowed (PRC-004)")
	// ErrInvalidMovementCap — negative cap.
	ErrInvalidMovementCap = policyError("policy: movement cap must be non-negative")
	// ErrInvalidCooldown — negative cooldown.
	ErrInvalidCooldown = policyError("policy: cooldown must be non-negative")
	// ErrUnknownStrategy — Strategy is empty or outside the closed set (§4.6
	// quarantine-over-inference): an unknown token fails closed and is NEVER
	// reinterpreted as StrategyHold.
	ErrUnknownStrategy = policyError("policy: unknown pricing strategy (closed set: hold, match, undercut)")
	// ErrUnknownObjective — Objective is empty or outside the closed set: an
	// unknown token fails closed and is NEVER reinterpreted as ObjectiveTrackStrategy.
	ErrUnknownObjective = policyError("policy: unknown optimization objective (closed set: maximize_contribution, track_strategy)")
	// ErrMissingReference — Match/Undercut require a non-zero reference price; it is
	// absent (zero-value Money) or zero-priced (issue #64, §4.6). A missing reference
	// is NEVER coerced to a zero-value Money that reaches evaluation.
	ErrMissingReference = policyError("policy: match/undercut strategy requires a non-zero reference price")
	// ErrReferenceUnitMismatch — the reference currency/exponent differs from the
	// policy money unit (the contribution floor). Cross-unit money never reaches
	// Money arithmetic; it fails closed here with a typed error (issue #64, §4.6).
	ErrReferenceUnitMismatch = policyError("policy: reference price unit must match the contribution floor unit")
	// ErrUndercutOutOfRange — undercut depth is negative or above the maximum
	// (0..10000 bp, i.e. 0%..100%). A negative depth would RAISE the desired price
	// (reversing Undercut); above 100% would drive it below zero. Enforced as
	// fixed-point basis points — no float on the money path (issue #64, §4.6).
	ErrUndercutOutOfRange = policyError("policy: undercut basis points out of range [0, 10000]")
)

// policyError is a tiny sentinel error type (avoids importing errors for New).
type policyError string

func (e policyError) Error() string { return string(e) }

// Strategy is the selected pricing strategy (stage 5). The set is minimal and
// closed for P0; each strategy proposes a desired price that the objective and
// the hard stages then constrain.
type Strategy string

const (
	// StrategyHold keeps the current price (no movement desired).
	StrategyHold Strategy = "hold"
	// StrategyMatch targets the reference price exactly.
	StrategyMatch Strategy = "match"
	// StrategyUndercut targets the reference price reduced by UndercutBp.
	StrategyUndercut Strategy = "undercut"
)

// Objective is the optimization objective (stage 6): how to pick within the
// feasible window the hard stages permit.
type Objective string

const (
	// ObjectiveMaximizeContribution prefers the highest feasible price (highest
	// contribution under a monotone-in-price contribution model).
	ObjectiveMaximizeContribution Objective = "maximize_contribution"
	// ObjectiveTrackStrategy prefers the strategy's desired price, clamped into
	// the feasible window.
	ObjectiveTrackStrategy Objective = "track_strategy"
)

// Boundary is the marketplace price boundary (stage 1, §9.2 "required for
// executable action"). Known=false is an UNKNOWN boundary and blocks (§16
// "unknown commission or boundary → block executable recommendation").
type Boundary struct {
	Known bool
	Min   money.Money
	Max   money.Money
}

// Config is the resolved, validated policy configuration for one account/SKU.
// Build it with NewConfig so the stricter-only rule (PRC-004) is enforced; a
// Config obtained any other way is re-validated by Evaluate before use.
type Config struct {
	Boundary          Boundary
	ContributionFloor money.Money
	MovementCap       money.BasisPoints
	Cooldown          time.Duration
	Strategy          Strategy
	StrategyEnabled   bool
	Reference         money.Money       // reference price for match/undercut
	UndercutBp        money.BasisPoints // undercut depth for StrategyUndercut
	Objective         Objective
}

// ConfigParams is the input to NewConfig. MovementCap and Cooldown are pointers:
// nil means "use the default"; a non-nil value must be STRICTER than the default
// (a smaller cap, a longer cooldown) or NewConfig rejects it (PRC-004).
type ConfigParams struct {
	Boundary          Boundary
	ContributionFloor money.Money
	MovementCap       *money.BasisPoints
	Cooldown          *time.Duration
	Strategy          Strategy
	StrategyEnabled   bool
	Reference         money.Money
	UndercutBp        money.BasisPoints
	Objective         Objective
}

// NewConfig applies the §9.3 defaults and enforces the stricter-only rule
// (PRC-004): a movement cap looser than 5% or a cooldown shorter than 60 minutes
// is rejected. Nil cap/cooldown take the default. This is the ONLY sanctioned way
// to build a Config for evaluation.
func NewConfig(p ConfigParams) (Config, error) {
	movementCap := DefaultMovementCap()
	if p.MovementCap != nil {
		movementCap = *p.MovementCap
	}
	if err := validateCap(movementCap); err != nil {
		return Config{}, err
	}

	cooldown := DefaultCooldown
	if p.Cooldown != nil {
		cooldown = *p.Cooldown
	}
	if err := validateCooldown(cooldown); err != nil {
		return Config{}, err
	}

	if err := validateStrategy(p.Strategy); err != nil {
		return Config{}, err
	}
	if err := validateObjective(p.Objective); err != nil {
		return Config{}, err
	}
	if err := validateStrategyParams(p.Strategy, p.Reference, p.UndercutBp, p.ContributionFloor); err != nil {
		return Config{}, err
	}

	return Config{
		Boundary:          p.Boundary,
		ContributionFloor: p.ContributionFloor,
		MovementCap:       movementCap,
		Cooldown:          cooldown,
		Strategy:          p.Strategy,
		StrategyEnabled:   p.StrategyEnabled,
		Reference:         p.Reference,
		UndercutBp:        p.UndercutBp,
		Objective:         p.Objective,
	}, nil
}

// validate re-checks the stricter-only invariant. Evaluate calls it so a Config
// built by bypassing NewConfig can never produce a proposal under a loose cap or
// cooldown (defense in depth for a never-cut invariant).
func (c Config) validate() error {
	if err := validateCap(c.MovementCap); err != nil {
		return err
	}
	if err := validateCooldown(c.Cooldown); err != nil {
		return err
	}
	if err := validateStrategy(c.Strategy); err != nil {
		return err
	}
	if err := validateObjective(c.Objective); err != nil {
		return err
	}
	return validateStrategyParams(c.Strategy, c.Reference, c.UndercutBp, c.ContributionFloor)
}

// maxUndercutBp bounds the undercut depth to 0..10000 basis points (0%..100%).
// A negative depth would raise the desired price (reversing Undercut semantics);
// a depth above 100% would drive the desired price below zero. It is a fixed-point
// integer bound — never a float on the money path (§4.6).
const maxUndercutBp int64 = 10000

// validateStrategyParams enforces the strategy-specific parameter contract
// (issue #64, §4.6 money correctness / quarantine-over-inference). Match and
// Undercut require a valid, same-unit reference price; Undercut additionally
// requires a bounded non-negative depth. Hold requires neither (it keeps the
// current price). Invalid commercial configuration fails closed HERE — before
// evaluation — so a malformed reference or depth never reaches Money arithmetic
// or produces a proposal. The policy money unit is the contribution floor.
func validateStrategyParams(s Strategy, reference money.Money, undercutBp money.BasisPoints, unit money.Money) error {
	switch s {
	case StrategyHold:
		return nil
	case StrategyMatch:
		return validateReference(reference, unit)
	case StrategyUndercut:
		if err := validateReference(reference, unit); err != nil {
			return err
		}
		return validateUndercutBp(undercutBp)
	default:
		// Unknown/empty strategy is handled by validateStrategy (fails closed with
		// ErrUnknownStrategy); there are no strategy params to check here.
		return nil
	}
}

// validateReference rejects an absent or zero-priced reference (ErrMissingReference)
// and a reference whose currency/exponent differs from the policy money unit
// (ErrReferenceUnitMismatch), so cross-unit money never reaches arithmetic.
func validateReference(reference, unit money.Money) error {
	// An invalid (uninitialised, empty-currency) reference is "absent" just like a
	// zero-priced one, so both fail closed with ErrMissingReference. IsZero now
	// surfaces ErrInvalidMoney for the invalid case rather than silently reporting
	// it as a legitimate zero (issue #4), which lands on the same rejection.
	isZero, err := reference.IsZero()
	if err != nil || isZero {
		return ErrMissingReference
	}
	if reference.Currency() != unit.Currency() || reference.Exponent() != unit.Exponent() {
		return ErrReferenceUnitMismatch
	}
	return nil
}

// validateUndercutBp bounds the undercut depth to [0, maxUndercutBp].
func validateUndercutBp(bp money.BasisPoints) error {
	if bp.Value() < 0 || bp.Value() > maxUndercutBp {
		return ErrUndercutOutOfRange
	}
	return nil
}

// validateStrategy enforces the closed Strategy set (§4.6 quarantine-over-inference,
// issue #63). Empty or unknown tokens fail closed with ErrUnknownStrategy; they are
// NEVER coerced to a default strategy.
func validateStrategy(s Strategy) error {
	switch s {
	case StrategyHold, StrategyMatch, StrategyUndercut:
		return nil
	default:
		return ErrUnknownStrategy
	}
}

// validateObjective enforces the closed Objective set (§4.6, issue #63). Empty or
// unknown tokens fail closed with ErrUnknownObjective; they are NEVER coerced to a
// default objective.
func validateObjective(o Objective) error {
	switch o {
	case ObjectiveMaximizeContribution, ObjectiveTrackStrategy:
		return nil
	default:
		return ErrUnknownObjective
	}
}

// validateCap enforces 0 ≤ cap ≤ default (stricter-only, PRC-004).
func validateCap(cap money.BasisPoints) error {
	if cap.Value() < 0 {
		return ErrInvalidMovementCap
	}
	if cap.Value() > movementCapMaxBp {
		return ErrMovementCapTooLoose
	}
	return nil
}

// validateCooldown enforces cooldown ≥ default ≥ 0 (stricter-only, PRC-004).
func validateCooldown(d time.Duration) error {
	if d < 0 {
		return ErrInvalidCooldown
	}
	if d < DefaultCooldown {
		return ErrCooldownTooLoose
	}
	return nil
}
