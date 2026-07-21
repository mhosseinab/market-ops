package recommendation_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mhosseinab/market-ops/services/core/internal/cost"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/money"
	"github.com/mhosseinab/market-ops/services/core/internal/policy"
	"github.com/mhosseinab/market-ops/services/core/internal/recommendation"
)

// authoritativeRechecker is the test EditPriceRechecker: it derives a
// self-consistent PolicyContext from the recommendation's OWN persisted, versioned
// inputs (CST-002) — its boundary (AllowedRange), current price, Complete readiness
// — with a trivially-satisfiable floor and a fixed positive contribution oracle,
// under the account's REAL configured Hold + MaximizeContribution chain. Because
// the re-check evaluates that AUTHORITATIVE chain (issue #134), an edit is admitted
// ONLY when it EQUALS the authoritative proposal — feasHigh (the boundary ∩ 5%
// movement upper edge, 1050 for the seeded current price 1000). It is NOT
// permissive about the never-cut hard stages OR the configured strategy/objective —
// it reuses policy.Evaluate through recommendation.AdmitEditedPrice.
type authoritativeRechecker struct{}

func (authoritativeRechecker) PolicyContextFor(_ context.Context, rec db.Recommendation) (recommendation.PolicyContext, error) {
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
	svc := recommendation.NewService(pool).SetEditPriceRechecker(authoritativeRechecker{})
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

// TestEditPrice_EditedValueRejectionsClassifiedAsDeclinedEdit is the issue #306
// fix-cycle regression through a WIRED rechecker (mirrors the live edit path): a
// cross-currency edited value AND a zero/absent edited value are both rejected on
// the EDITED VALUE's own terms by validateEditedValue — BEFORE the authoritative
// chain runs — so EditPrice folds BOTH into the single declined-edit class
// (ErrEditedPriceRejected), independent of which strategy the account runs (issue
// #134) and never a raw policy sentinel a transport could misreport as a 500. Fail
// closed: NO new card version and NO new parameter version is minted (mintDraftCard
// is never reached), so neither declined edit can ever reach a control-bearing
// state (§4.6). Money never-cut: the cross-unit/zero values reject before policy
// arithmetic. Deferred to CI (needs a database).
func TestEditPriceEditedValueRejectionsClassifiedAsDeclinedEdit(t *testing.T) {
	pool, q := newPool(t)
	account, variant := seedVariant(t, q)
	svc := recommendation.NewService(pool).SetEditPriceRechecker(authoritativeRechecker{})
	original := persistApprovableCard(t, svc, account, variant)

	// authoritativeRechecker derives the policy money unit from the recommendation's
	// own current price (seeded IRR). A USD edited value is therefore cross-unit; a
	// zero IRR edited value is a missing/zero value. Both are edited-VALUE
	// rejections, classified identically into the declined-edit class.
	for _, tc := range []struct {
		name  string
		price money.Money
	}{
		{"cross-currency", mustMoney(t, 1010, "USD", 0)},
		{"zero-value", mustMoney(t, 0, "IRR", 0)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := svc.EditPrice(context.Background(), original.ID, tc.price, time.Now().UTC()); !errors.Is(err, recommendation.ErrEditedPriceRejected) {
				t.Fatalf("EditPrice(%s) err = %v, want ErrEditedPriceRejected", tc.name, err)
			}
		})
	}

	// No new version was minted by EITHER declined edit: the current head is still
	// the original Draft at its original parameter version (fail closed, §4.6).
	rows, err := svc.ListActions(context.Background(), account, "", 0)
	if err != nil {
		t.Fatalf("ListActions: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != original.ID {
		t.Fatalf("current version changed after declined edits; rows=%d want the original card %s", len(rows), original.ID)
	}
	if rows[0].ParameterVersion != original.ParameterVersion {
		t.Fatalf("parameter version advanced on a declined edit: got %d want %d", rows[0].ParameterVersion, original.ParameterVersion)
	}
}

// TestEditPrice_RejectsPriceAdmissibleOnlyUnderADifferentStrategy is the issue
// #134 REOPEN regression END TO END through a WIRED rechecker: authoritativeRechecker
// resolves the account's REAL Hold + MaximizeContribution chain, whose authoritative
// proposal is feasHigh (1050). An in-window price (1010) that would pass ONLY under a
// hardcoded Match/TrackStrategy targeting it is REJECTED, mints NO new card version
// and NO new parameter version, and can never reach a control-bearing state (§4.6,
// §9.3). Deferred to CI (needs a database).
func TestEditPriceRejectsPriceAdmissibleOnlyUnderADifferentStrategy(t *testing.T) {
	pool, q := newPool(t)
	account, variant := seedVariant(t, q)
	svc := recommendation.NewService(pool).SetEditPriceRechecker(authoritativeRechecker{})
	original := persistApprovableCard(t, svc, account, variant)

	// 1010 is inside boundary [900,1200] ∩ the 5% movement window ([950,1050]) and
	// floor-satisfying, but it is NOT the account's authoritative Hold/MaximizeContribution
	// proposal (feasHigh, 1050) — so the never-cut re-check declines it.
	nonAuthoritative := mustMoney(t, 1010, "IRR", 0)
	if _, err := svc.EditPrice(context.Background(), original.ID, nonAuthoritative, time.Now().UTC()); err != recommendation.ErrEditedPriceRejected {
		t.Fatalf("EditPrice(non-authoritative strategy price) err = %v, want ErrEditedPriceRejected", err)
	}

	// Fail closed: the current head is still the original Draft at its original
	// parameter version — no control-bearing version was minted.
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
