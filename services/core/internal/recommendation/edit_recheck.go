package recommendation

import (
	"context"
	"errors"
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

// AdmitEditedPrice re-runs the canonical six-stage policy chain (policy.Evaluate)
// against edited and reports whether edited is admissible: every hard stage
// (boundary → floor → movement cap → cooldown) passes, the strategy is enabled,
// the verified readiness is Complete, and the chain's accepted proposal is
// EXACTLY the edited price. It REUSES the single canonical ordering in
// internal/policy — it never re-implements a stage (DRY; the order is a never-cut
// invariant, §4.6). Any hard-stage blocker, a clamp/move away from the edited
// price, a non-Complete readiness, or a cross-unit edit yields a non-admitting
// (or errored) result — fail closed.
func AdmitEditedPrice(pc PolicyContext, edited money.Money, now time.Time) (policy.Result, bool, error) {
	res, err := evaluateEditedPrice(pc, edited, now)
	if err != nil {
		return policy.Result{}, false, classifyEditedValueRejection(err)
	}
	return res, admitsEditedPrice(res, edited), nil
}

// classifyEditedValueRejection folds the policy sentinels that can ONLY arise
// from evaluating the EDITED VALUE itself — a zero/absent edited price
// (policy.ErrMissingReference) or a cross-unit/cross-currency edited price
// (policy.ErrReferenceUnitMismatch) — into the single declined-edit class
// (ErrEditedPriceRejected). evaluateEditedPrice pins Strategy=Match and
// Reference=edited, so the six-stage re-check's validateReference only ever
// inspects the EDITED value; both sentinels are therefore ALWAYS a DECLINED edit
// (PRC-002, §8.4, issue #306), never a server fault. This gives the transport a
// SINGLE decision class to map to a 409, so it never keys on a shared policy
// sentinel that could equally denote a genuine fault.
//
// Any OTHER error — including a stored-config resolution/integrity fault that
// surfaces a raw policy sentinel from OUTSIDE this edited-value evaluation — is
// returned UNCHANGED, so it still fails as a 500. A genuine server fault is never
// masked as a declined edit (issue #306 related safety advisory).
func classifyEditedValueRejection(err error) error {
	if errors.Is(err, policy.ErrMissingReference) || errors.Is(err, policy.ErrReferenceUnitMismatch) {
		return ErrEditedPriceRejected
	}
	return err
}

// evaluateEditedPrice runs policy.Evaluate with the account's resolved context but
// with the subordinate strategy/objective (stages 5–6) set to TARGET the edited
// price, so the returned Result reflects whether the EDITED value itself survives
// the hard stages. The account's real StrategyEnabled (stage 5 gate) is preserved;
// only the desired-price selection is redirected to the edited price. Stage 6
// (objective) never emits a blocker in P0, so overriding it suppresses nothing.
func evaluateEditedPrice(pc PolicyContext, edited money.Money, now time.Time) (policy.Result, error) {
	cfg := pc.Config
	cfg.Strategy = policy.StrategyMatch
	cfg.Reference = edited
	cfg.Objective = policy.ObjectiveTrackStrategy
	return policy.Evaluate(policy.EvaluateInput{
		Config:       cfg,
		CurrentPrice: pc.CurrentPrice,
		Contribution: pc.Contribution,
		Now:          now,
		LastActionAt: pc.LastActionAt,
		Readiness:    pc.Readiness,
	})
}

// admitsEditedPrice reports whether a re-check Result admits exactly the edited
// price: the result must be Approvable (no blocker, not a simulation, readiness
// Complete, a proposal present — policy.Result.Approvable) AND its accepted price
// must equal the edited price. A clamp to a window edge (proposal != edited) is a
// non-admit. Comparison is Money-only (mismatched unit is a non-admit, never a
// panic).
func admitsEditedPrice(res policy.Result, edited money.Money) bool {
	if !res.Approvable() || res.Proposed == nil {
		return false
	}
	cmp, err := res.Proposed.Price.Compare(edited)
	return err == nil && cmp == 0
}
