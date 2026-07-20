package recommendation_test

import (
	"errors"
	"testing"
	"time"

	"github.com/mhosseinab/market-ops/services/core/internal/cost"
	"github.com/mhosseinab/market-ops/services/core/internal/money"
	"github.com/mhosseinab/market-ops/services/core/internal/policy"
	"github.com/mhosseinab/market-ops/services/core/internal/recommendation"
)

// permissivePolicyContext builds a PolicyContext carrying the account's REAL
// configured policy (Hold + MaximizeContribution): the only binding hard
// constraints are the marketplace boundary and the 5% movement cap around the
// current price (1000), the floor is trivially satisfiable, and the contribution
// oracle returns a fixed positive amount. It is the fixture the edited-price
// re-check unit tests target. The re-check runs this AUTHORITATIVE chain (issue
// #134), so — under MaximizeContribution — its authoritative proposal is feasHigh
// (1050 = boundary ∩ movement upper edge). An edited price is admissible ONLY when
// it EQUALS that authoritative proposal; a merely in-window price is not.
func permissivePolicyContext(t *testing.T) recommendation.PolicyContext {
	t.Helper()
	return recommendation.PolicyContext{
		Config: policy.Config{
			Boundary:          policy.Boundary{Known: true, Min: irr(t, 900), Max: irr(t, 1200)},
			ContributionFloor: irr(t, 0),
			MovementCap:       policy.DefaultMovementCap(), // 5%.
			Cooldown:          policy.DefaultCooldown,
			Strategy:          policy.StrategyHold,
			StrategyEnabled:   true,
			Objective:         policy.ObjectiveMaximizeContribution,
		},
		CurrentPrice: irr(t, 1000),
		Contribution: func(price money.Money) (money.Money, error) { return irr(t, 500), nil },
		Readiness:    cost.StateComplete,
	}
}

// TestAdmitEditedPrice_OutsideBoundaryRejected is the never-cut policy-order
// negative (issue #134, §4.6): an edited price outside the feasible window the
// full boundary→floor→movement-cap→cooldown→strategy→objective chain admits is
// NOT admissible, so it can never mint a control-bearing card. 1300 is outside
// both the boundary (max 1200) and the 5% movement window ([950,1050]).
func TestAdmitEditedPriceOutsideBoundaryRejected(t *testing.T) {
	pc := permissivePolicyContext(t)
	_, ok, err := recommendation.AdmitEditedPrice(pc, irr(t, 1300), time.Now())
	if err != nil {
		t.Fatalf("AdmitEditedPrice: %v", err)
	}
	if ok {
		t.Fatal("an edited price outside the boundary must NOT be admissible (policy-order re-check, §4.6)")
	}
}

// TestAdmitEditedPrice_OutsideMovementCapRejected proves the movement-cap hard
// stage rejects an edited price inside the boundary but beyond the 5% cap: 1100
// is inside [900,1200] yet outside [950,1050].
func TestAdmitEditedPriceOutsideMovementCapRejected(t *testing.T) {
	pc := permissivePolicyContext(t)
	_, ok, err := recommendation.AdmitEditedPrice(pc, irr(t, 1100), time.Now())
	if err != nil {
		t.Fatalf("AdmitEditedPrice: %v", err)
	}
	if ok {
		t.Fatal("an edited price beyond the 5% movement cap must NOT be admissible")
	}
}

// TestAdmitEditedPrice_BelowFloorRejected proves the hard contribution floor
// rejects an in-window edited price whose contribution falls below the floor.
func TestAdmitEditedPriceBelowFloorRejected(t *testing.T) {
	pc := permissivePolicyContext(t)
	pc.Config.ContributionFloor = irr(t, 300)
	pc.Contribution = func(price money.Money) (money.Money, error) { return irr(t, 100), nil } // below 300.
	_, ok, err := recommendation.AdmitEditedPrice(pc, irr(t, 1010), time.Now())
	if err != nil {
		t.Fatalf("AdmitEditedPrice: %v", err)
	}
	if ok {
		t.Fatal("an edited price whose contribution is below the hard floor must NOT be admissible")
	}
}

// TestAdmitEditedPrice_ValidPriceAdmitted proves an edited price that EQUALS the
// account's authoritative proposal — here feasHigh (1050) under the fixture's real
// Hold + MaximizeContribution chain, floor-satisfying, readiness Complete —
// re-passes every stage and IS admissible (so the edit may mint a new version).
func TestAdmitEditedPriceValidPriceAdmitted(t *testing.T) {
	pc := permissivePolicyContext(t)
	res, ok, err := recommendation.AdmitEditedPrice(pc, irr(t, 1050), time.Now())
	if err != nil {
		t.Fatalf("AdmitEditedPrice: %v", err)
	}
	if !ok {
		t.Fatalf("an edited price equal to the authoritative proposal must be admissible; result blockers=%v", res.Blockers)
	}
}

// TestAdmitEditedPrice_RejectsPriceAdmissibleOnlyUnderADifferentStrategy is the
// issue #134 REOPEN regression (§4.6 never-cut policy order + §9.3 authoritative
// strategy/objective): the re-check evaluates the account's REAL configured chain,
// NEVER a hardcoded Match/TrackStrategy. permissivePolicyContext is Hold +
// MaximizeContribution, whose authoritative proposal is feasHigh (1050). An
// in-window price (1010) that would pass ONLY if the chain were pinned to
// Match/TrackStrategy targeting it (the old defect) is NOT that authoritative
// proposal, so it is NOT admissible and can never reach review/confirmation.
func TestAdmitEditedPrice_RejectsPriceAdmissibleOnlyUnderADifferentStrategy(t *testing.T) {
	pc := permissivePolicyContext(t) // the account's REAL config: Hold + MaximizeContribution.
	// 1010 is inside boundary ∩ the 5% movement window ([950,1050]) and floor-
	// satisfying, so a chain hardcoded to Match/TrackStrategy targeting 1010 would
	// admit it. The account's real chain proposes feasHigh (1050); 1010 is not it.
	_, ok, err := recommendation.AdmitEditedPrice(pc, irr(t, 1010), time.Now())
	if err != nil {
		t.Fatalf("AdmitEditedPrice: %v", err)
	}
	if ok {
		t.Fatal("a price admissible only under Match/TrackStrategy must NOT be admitted under the account's real Hold/MaximizeContribution chain (issue #134)")
	}
}

// TestAdmitEditedPrice_NonCompleteReadinessRejected proves the CST-003 gate holds
// on the re-check: even a fully feasible edited price is NOT admissible unless the
// verified margin readiness is Complete.
func TestAdmitEditedPriceNonCompleteReadinessRejected(t *testing.T) {
	pc := permissivePolicyContext(t)
	pc.Readiness = cost.StatePartial
	_, ok, err := recommendation.AdmitEditedPrice(pc, irr(t, 1010), time.Now())
	if err != nil {
		t.Fatalf("AdmitEditedPrice: %v", err)
	}
	if ok {
		t.Fatal("a Partial-readiness edited price must NOT be admissible (CST-003)")
	}
}

// TestAdmitEditedPrice_MismatchedUnitRejected proves an edited price whose unit
// differs from the policy money unit fails closed (quarantine-over-inference,
// §9.1) rather than silently admitting. The re-check surfaces the edited-VALUE
// reference sentinel (policy.ErrReferenceUnitMismatch), which AdmitEditedPrice
// folds into the single declined-edit class (recommendation.ErrEditedPriceRejected,
// issue #306) — so a transport can key on ONE decision class and can never
// misreport this DECLINED edit as a 500 server fault.
func TestAdmitEditedPriceMismatchedUnitRejected(t *testing.T) {
	pc := permissivePolicyContext(t)
	usd, err := money.New(1010, "USD", 0)
	if err != nil {
		t.Fatalf("money.New: %v", err)
	}
	_, ok, aerr := recommendation.AdmitEditedPrice(pc, usd, time.Now())
	if ok {
		t.Fatal("a cross-unit edited price must NOT be admissible")
	}
	if !errors.Is(aerr, recommendation.ErrEditedPriceRejected) {
		t.Fatalf("cross-unit edited price err = %v, want the declined-edit class ErrEditedPriceRejected", aerr)
	}
	// The raw policy sentinel must NOT leak past the domain: transport keys on the
	// declined-edit class alone, so a shared sentinel can never drive the mapping.
	if errors.Is(aerr, policy.ErrReferenceUnitMismatch) {
		t.Fatal("the raw policy.ErrReferenceUnitMismatch sentinel must not leak past the domain classification")
	}
}

// TestAdmitEditedPrice_ZeroValueRejected is the issue #306 fix-cycle regression:
// a zero/absent edited value makes the six-stage re-check surface the edited-VALUE
// reference sentinel (policy.ErrMissingReference) from the SAME validateReference
// as the cross-unit case. It is identically a DECLINED EDIT, so AdmitEditedPrice
// folds it into the single declined-edit class (ErrEditedPriceRejected) — never a
// raw policy sentinel a transport could misreport as a 500. Fail closed: not
// admitted. Money never-cut: the zero value is rejected at the policy layer.
func TestAdmitEditedPriceZeroValueRejected(t *testing.T) {
	pc := permissivePolicyContext(t)
	_, ok, err := recommendation.AdmitEditedPrice(pc, irr(t, 0), time.Now())
	if ok {
		t.Fatal("a zero/absent edited price must NOT be admissible")
	}
	if !errors.Is(err, recommendation.ErrEditedPriceRejected) {
		t.Fatalf("zero-value edited price err = %v, want the declined-edit class ErrEditedPriceRejected", err)
	}
	if errors.Is(err, policy.ErrMissingReference) {
		t.Fatal("the raw policy.ErrMissingReference sentinel must not leak past the domain classification")
	}
}
