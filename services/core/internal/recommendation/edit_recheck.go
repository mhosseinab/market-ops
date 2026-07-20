package recommendation

import (
	"context"
	"time"

	"github.com/mhosseinab/market-ops/services/core/internal/cost"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/money"
	"github.com/mhosseinab/market-ops/services/core/internal/policy"
)

// ErrEditedPriceRejected is returned when an operator-edited price does NOT
// re-pass the full six-stage pricing-policy chain against the edited value
// (CHAT-044, issue #134, §4.6 never-cut policy order). It fails CLOSED: the edit
// mints NO new card version and NO new parameter version, so a price the chain
// would block, clamp, or move off — or an edit with no wired re-check context
// (dark P0 posture) — can never reach a control-bearing state. It is a policy
// decision, never free text.
var ErrEditedPriceRejected = policyRejected("recommendation: edited price rejected by the pricing-policy re-check")

type policyRejected string

func (e policyRejected) Error() string { return string(e) }

// PolicyContext is the authoritative six-stage policy evaluation context for a
// recommendation, resolved from committed, versioned data (CST-002) so an edited
// price is re-checked through the SAME engine (policy.Evaluate) that produced the
// original proposal. Money is int64 minor units throughout; rates are fixed-point
// basis points — no float ever touches this path (§9.1).
type PolicyContext struct {
	// Config is the resolved, validated §9.3 policy configuration.
	Config policy.Config
	// CurrentPrice is the in-force price the movement cap is measured against.
	CurrentPrice money.Money
	// Contribution is the margin oracle over the SKU's in-force cost profile.
	Contribution policy.ContributionFunc
	// LastActionAt is the last price-action instant (nil ⇒ cooldown cannot bind).
	LastActionAt *time.Time
	// Readiness is the verified CST-003 margin-readiness state governing the
	// contribution. Only cost.StateComplete admits an executable edit.
	Readiness cost.State
}

// EditPriceRechecker resolves the authoritative PolicyContext for a card's
// recommendation so EditPrice can re-run the full policy chain against an edited
// price. In the dark P0 posture no live resolver is wired (identity/price/cost/
// boundary/permission/policy over committed data lands under the same gated
// enablement as the execution write path), so EditPrice fails closed until one
// is set (see Service.SetEditPriceRechecker).
type EditPriceRechecker interface {
	PolicyContextFor(ctx context.Context, rec db.Recommendation) (PolicyContext, error)
}

// AdmitEditedPrice re-runs the account/SKU's AUTHORITATIVE six-stage policy chain
// (policy.Evaluate) under its REAL configured strategy AND objective (issue #134),
// and reports whether the operator-edited price is admissible: the edited value is
// well-formed and same-unit (else a declined edit), every hard stage
// (boundary → floor → movement cap → cooldown) passes, the strategy is enabled,
// the verified readiness is Complete, and the chain's AUTHORITATIVE accepted
// proposal is EXACTLY the edited price. It REUSES the single canonical ordering in
// internal/policy — it never re-implements a stage NOR pins a subordinate
// selection (DRY; the order and the configured strategy/objective are never-cut,
// §4.6). It fails CLOSED: a zero/absent or cross-unit edited value returns the
// single declined-edit class (ErrEditedPriceRejected); a hard-stage blocker, a
// non-Complete readiness, or a proposal that differs from the edited price
// (including because the account's real objective — e.g. MaximizeContribution —
// would choose a DIFFERENT price) yields a non-admitting result.
func AdmitEditedPrice(pc PolicyContext, edited money.Money, now time.Time) (policy.Result, bool, error) {
	if err := validateEditedValue(edited, pc.CurrentPrice); err != nil {
		return policy.Result{}, false, err
	}
	res, err := evaluateEditedPrice(pc, now)
	if err != nil {
		return policy.Result{}, false, err
	}
	return res, admitsEditedPrice(res, edited), nil
}

// validateEditedValue fails an operator-edited price CLOSED, on its OWN terms and
// BEFORE the authoritative chain runs, into the single declined-edit class
// (ErrEditedPriceRejected, issue #306): a zero/absent value, or a value whose
// currency/exponent differs from the policy money unit (the in-force current
// price), is a DECLINED edit the transport maps to a structured 409 — never a 500
// server fault, and never coerced (quarantine-over-inference, §9.1). Deciding the
// edited VALUE explicitly — rather than by pinning it as the strategy reference —
// keeps this class INDEPENDENT of which strategy the account actually runs (issue
// #134): a raw policy sentinel that later surfaces from the account's OWN stored
// strategy/reference config is a genuine integrity fault, is NOT folded here, and
// still fails as a 500 (via evaluateEditedPrice).
func validateEditedValue(edited, unit money.Money) error {
	if edited.IsZero() {
		return ErrEditedPriceRejected
	}
	if edited.Currency() != unit.Currency() || edited.Exponent() != unit.Exponent() {
		return ErrEditedPriceRejected
	}
	return nil
}

// evaluateEditedPrice runs policy.Evaluate with the account/SKU's AUTHORITATIVE,
// resolved policy configuration UNCHANGED — the real strategy AND the real
// objective (issue #134). It no longer pins Strategy=Match / Objective=TrackStrategy
// / Reference=edited: hardcoding a subordinate stage-5/6 selection re-checked the
// edited price under a DIFFERENT policy than the seller actually runs, which could
// admit an edit the account's real chain (e.g. Hold + MaximizeContribution) would
// never propose. The edited value is compared against this chain's authoritative
// proposal by admitsEditedPrice; it is NEVER injected as the strategy's target.
func evaluateEditedPrice(pc PolicyContext, now time.Time) (policy.Result, error) {
	return policy.Evaluate(policy.EvaluateInput{
		Config:       pc.Config,
		CurrentPrice: pc.CurrentPrice,
		Contribution: pc.Contribution,
		Now:          now,
		LastActionAt: pc.LastActionAt,
		Readiness:    pc.Readiness,
	})
}

// admitsEditedPrice reports whether a re-check Result admits exactly the edited
// price: the result must be Approvable (no blocker, not a simulation, readiness
// Complete, a proposal present — policy.Result.Approvable) AND its authoritative
// accepted price must equal the edited price. A proposal the account's real
// strategy/objective chose DIFFERENTLY (proposal != edited) is a non-admit.
// Comparison is Money-only (mismatched unit is a non-admit, never a panic).
func admitsEditedPrice(res policy.Result, edited money.Money) bool {
	if !res.Approvable() || res.Proposed == nil {
		return false
	}
	cmp, err := res.Proposed.Price.Compare(edited)
	return err == nil && cmp == 0
}
