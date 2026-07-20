package guardrail

import (
	"errors"

	"github.com/mhosseinab/market-ops/services/core/internal/policy"
)

// PRC-004 absolute defaults (§9.3): the LOOSEST values any account may hold. An
// account may only configure STRICTER (a smaller movement cap, a longer
// cooldown). These mirror policy.DefaultMovementCap()/policy.DefaultCooldown so
// the guardrail write gate and the policy engine share ONE source for the
// defaults and can never drift (TestDefaultsMirrorPolicy).
var (
	defaultMovementCapBp   = policy.DefaultMovementCap().Value()
	defaultCooldownSeconds = int64(policy.DefaultCooldown.Seconds())
)

// ErrNotStricter is returned when a guardrail write would LOOSEN the account's
// effective baseline (PRC-004 / §8.3 "guardrails may only be tightened"): a
// larger movement cap, a shorter cooldown, or a lower contribution floor than
// the currently persisted value (or, on the first write, than the PRC-004
// default). It maps to a structured 400 — the write is a legitimate request the
// policy gate declined, and nothing is persisted (fail closed, §4.6).
var ErrNotStricter = errors.New("guardrail: value looser than the current effective baseline is not allowed (stricter-only, PRC-004)")

// validateStricter enforces the stricter-only invariant against the AUTHORITATIVE
// effective baseline. next must be no looser than baseline on every tightenable
// axis, and never looser than the PRC-004 absolute defaults:
//
//   - movement cap:      smaller basis points == stricter
//   - cooldown:          more seconds        == stricter
//   - contribution floor: higher amount      == stricter (bounded only once a
//     baseline exists — there is no universal default floor)
//
// When hasBaseline is false (the first write) only the absolute PRC-004 defaults
// apply and the floor is unbounded. The floor is compared through money methods,
// so a mismatched currency/exponent is a typed rejection, never a silent pass
// (§9.1, never-cut).
func validateStricter(baseline, next Settings, hasBaseline bool) error {
	// Movement cap: the accepted ceiling is the STRICTER of the baseline and the
	// PRC-004 default, so a legacy-loose baseline can only ever be tightened
	// toward (or past) the default, never held loose.
	capLimit := defaultMovementCapBp
	if hasBaseline && baseline.MovementCapBp < capLimit {
		capLimit = baseline.MovementCapBp
	}
	if next.MovementCapBp > capLimit {
		return ErrNotStricter
	}

	// Cooldown: the accepted floor is the STRICTER (longer) of the baseline and
	// the PRC-004 default.
	cooldownFloor := defaultCooldownSeconds
	if hasBaseline && baseline.CooldownSeconds > cooldownFloor {
		cooldownFloor = baseline.CooldownSeconds
	}
	if next.CooldownSeconds < cooldownFloor {
		return ErrNotStricter
	}

	// Contribution floor: only bounded once a baseline exists. A higher floor is
	// stricter (more contribution protected); a lower floor loosens the guardrail
	// and is rejected.
	if hasBaseline {
		cmp, err := next.ContributionFloor.Compare(baseline.ContributionFloor)
		if err != nil {
			return err
		}
		if cmp < 0 {
			return ErrNotStricter
		}
	}
	return nil
}
