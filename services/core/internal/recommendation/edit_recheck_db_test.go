package recommendation_test

import (
	"context"
	"testing"
	"time"

	"github.com/mhosseinab/market-ops/services/core/internal/cost"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/money"
	"github.com/mhosseinab/market-ops/services/core/internal/policy"
	"github.com/mhosseinab/market-ops/services/core/internal/recommendation"
)

// admitAllRechecker is the test EditPriceRechecker: it derives a self-consistent
// PolicyContext from the recommendation's OWN persisted, versioned inputs
// (CST-002) — its boundary (AllowedRange), current price, Complete readiness — with
// a trivially-satisfiable floor and a fixed positive contribution oracle. It admits
// exactly the prices the real six-stage chain would: those inside boundary ∩ the 5%
// movement window around the current price. It is NOT permissive about the never-cut
// hard stages — it reuses policy.Evaluate through recommendation.AdmitEditedPrice.
type admitAllRechecker struct{}

func (admitAllRechecker) PolicyContextFor(_ context.Context, rec db.Recommendation) (recommendation.PolicyContext, error) {
	current, err := money.New(rec.CurrentPriceMantissa, rec.CurrentPriceCurrency, int8(rec.CurrentPriceExponent))
	if err != nil {
		return recommendation.PolicyContext{}, err
	}
	boundary := policy.Boundary{Known: false}
	if rec.AllowedRangeAvailable {
		min, err := money.New(rec.AllowedRangeMinMantissa.Int64, rec.AllowedRangeCurrency, int8(rec.AllowedRangeExponent))
		if err != nil {
			return recommendation.PolicyContext{}, err
		}
		max, err := money.New(rec.AllowedRangeMaxMantissa.Int64, rec.AllowedRangeCurrency, int8(rec.AllowedRangeExponent))
		if err != nil {
			return recommendation.PolicyContext{}, err
		}
		boundary = policy.Boundary{Known: true, Min: min, Max: max}
	}
	floor, err := money.New(0, current.Currency(), current.Exponent())
	if err != nil {
		return recommendation.PolicyContext{}, err
	}
	return recommendation.PolicyContext{
		Config: policy.Config{
			Boundary:          boundary,
			ContributionFloor: floor,
			MovementCap:       policy.DefaultMovementCap(),
			Cooldown:          policy.DefaultCooldown,
			Strategy:          policy.StrategyHold,
			StrategyEnabled:   true,
			Objective:         policy.ObjectiveMaximizeContribution,
		},
		CurrentPrice: current,
		Contribution: func(money.Money) (money.Money, error) {
			return money.New(500, current.Currency(), current.Exponent())
		},
		Readiness: cost.StateComplete,
	}, nil
}

// TestEditPrice_OutsideBoundaryNeverMintsControlBearingVersion is the issue #134
// end-to-end negative (§4.6 never-cut policy order): an edited price outside the
// boundary is REJECTED by the re-run policy chain, mints NO new card version and NO
// new parameter version, so the current lineage version stays the original Draft
// and can never reach AwaitingConfirmation. Deferred to CI (needs a database).
func TestEditPriceOutsideBoundaryNeverMintsControlBearingVersion(t *testing.T) {
	pool, q := newPool(t)
	account, variant := seedVariant(t, q)
	svc := recommendation.NewService(pool).SetEditPriceRechecker(admitAllRechecker{})
	original := persistApprovableCard(t, svc, account, variant)

	// 1300 is outside the seeded boundary [900,1200] and the 5% movement window.
	outside := mustMoney(t, 1300, "IRR", 0)
	if _, err := svc.EditPrice(context.Background(), original.ID, outside, time.Now().UTC()); err != recommendation.ErrEditedPriceRejected {
		t.Fatalf("EditPrice(out-of-boundary) err = %v, want ErrEditedPriceRejected", err)
	}

	// No new version was minted: the current version is still the original.
	rows, err := svc.ListActions(context.Background(), account, "", 0)
	if err != nil {
		t.Fatalf("ListActions: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != original.ID {
		t.Fatalf("current version changed after a rejected edit; rows=%d want the original card %s", len(rows), original.ID)
	}
	if rows[0].ParameterVersion != original.ParameterVersion {
		t.Fatalf("parameter version advanced on a rejected edit: got %d want %d", rows[0].ParameterVersion, original.ParameterVersion)
	}
}

// TestEditPrice_FailsClosedWithoutRechecker proves the dark P0 posture: with no
// rechecker wired, EditPrice fails closed — no edited price mints a control-bearing
// version. Deferred to CI (needs a database).
func TestEditPriceFailsClosedWithoutRechecker(t *testing.T) {
	pool, q := newPool(t)
	account, variant := seedVariant(t, q)
	svc := recommendation.NewService(pool) // no rechecker wired (dark).
	original := persistApprovableCard(t, svc, account, variant)

	if _, err := svc.EditPrice(context.Background(), original.ID, mustMoney(t, 1010, "IRR", 0), time.Now().UTC()); err != recommendation.ErrEditedPriceRejected {
		t.Fatalf("EditPrice(no rechecker) err = %v, want ErrEditedPriceRejected (fail closed)", err)
	}
}
