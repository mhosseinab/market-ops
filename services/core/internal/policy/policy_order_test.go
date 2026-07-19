package policy

import (
	"testing"
	"time"

	"pgregory.net/rapid"

	"github.com/mhosseinab/market-ops/services/core/internal/cost"
	"github.com/mhosseinab/market-ops/services/core/internal/money"
)

// Issue #61 (PRC-003): the six policy stages must execute in the mandated fixed
// order boundary(1) → hard floor(2) → movement cap(3) → cooldown(4) →
// strategy(5) → objective(6). A later stage can NEVER suppress or override an
// earlier HARD-stage outcome (stages 1–4 are hard; 5–6 are subordinate
// selectors), and blockers must reflect that precedence.
//
// Documented precedence policy (proven below): the hard stages are evaluated and
// accumulated in mandated order; if ANY hard blocker exists, evaluation
// terminates before the subordinate stages 5–6 (their selection/enablement is not
// reported and cannot mask a hard blocker). The subordinate strategy-enablement
// blocker is emitted only when every hard stage passed.

// countingContrib wraps a ContributionFunc and records how many times the oracle
// was invoked, so a test can prove the subordinate/selection callback is NOT
// evaluated once an earlier hard stage terminates (§9.3 acceptance criterion).
type countingContrib struct {
	calls int
	inner ContributionFunc
}

func (c *countingContrib) fn() ContributionFunc {
	return func(p money.Money) (money.Money, error) {
		c.calls++
		return c.inner(p)
	}
}

// TestEvaluate_StrategyDisabledDoesNotSuppressHardBlockers is the exact
// reproduction from issue #61: a disabled strategy (stage 5) combined with an
// infeasible hard floor (stage 2) and an active cooldown with a desired change
// (stage 4). The stage-5 strategy_disabled blocker must NOT preempt or suppress
// the earlier hard-floor and cooldown detection; the returned blockers must be
// the earlier HARD blockers, in mandated precedence.
func TestEvaluate_StrategyDisabledDoesNotSuppressHardBlockers(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	last := now.Add(dur(t, "-10m")) // inside a 60m cooldown

	// Disabled strategy; Match 1050 is a desired CHANGE from current 1000; cogs
	// 5000 makes contribution negative at every feasible price (floor infeasible).
	cfg := Config{
		Boundary:          Boundary{Known: true, Min: m0(t, 900), Max: m0(t, 1100)},
		ContributionFloor: m0(t, 0),
		MovementCap:       DefaultMovementCap(),
		Cooldown:          DefaultCooldown,
		Strategy:          StrategyMatch,
		StrategyEnabled:   false, // stage 5 disabled
		Reference:         m0(t, 1050),
		Objective:         ObjectiveTrackStrategy,
	}
	res, err := Evaluate(EvaluateInput{
		Config:       cfg,
		CurrentPrice: m0(t, 1000),
		Contribution: mkContrib(t, m0(t, 5000), 0), // contribution(price) = price − 5000 < 0
		Now:          now,
		LastActionAt: &last,
		Readiness:    cost.StateComplete,
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if res.Proposed != nil {
		t.Fatalf("expected blockers, got proposal %+v", res.Proposed)
	}

	// The earlier HARD blockers must be present, in mandated order.
	if len(res.Blockers) != 2 {
		t.Fatalf("want 2 hard blockers (floor, cooldown), got %+v", res.Blockers)
	}
	if res.Blockers[0].Stage != StageHardFloor {
		t.Fatalf("first blocker stage = %v, want hard_floor (stage 2)", res.Blockers[0].Stage)
	}
	if res.Blockers[1].Stage != StageCooldown {
		t.Fatalf("second blocker stage = %v, want cooldown (stage 4)", res.Blockers[1].Stage)
	}
	// The stage-5 strategy_disabled blocker must NOT be present: a subordinate
	// stage can never suppress OR accompany-past-precedence an earlier hard block.
	for _, b := range res.Blockers {
		if b.Code == BlockerStrategyDisabled {
			t.Fatalf("stage-5 strategy_disabled must not appear when hard stages block: %+v", res.Blockers)
		}
	}
}

// TestEvaluate_StrategyDisabledBlocksOnlyWhenHardStagesPass proves the flip side:
// when every hard stage passes, a disabled strategy IS the authoritative blocker.
func TestEvaluate_StrategyDisabledBlocksOnlyWhenHardStagesPass(t *testing.T) {
	cfg := Config{
		Boundary:          Boundary{Known: true, Min: m0(t, 900), Max: m0(t, 1100)},
		ContributionFloor: m0(t, 0),
		MovementCap:       DefaultMovementCap(),
		Cooldown:          DefaultCooldown,
		Strategy:          StrategyHold,
		StrategyEnabled:   false,
		Reference:         m0(t, 1000),
		Objective:         ObjectiveTrackStrategy,
	}
	res, err := Evaluate(EvaluateInput{
		Config:       cfg,
		CurrentPrice: m0(t, 1000),
		Contribution: mkContrib(t, m0(t, 100), 0), // contribution(1000)=900, floor ok
		Now:          time.Unix(1_000_000, 0),
		Readiness:    cost.StateComplete,
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if res.Proposed != nil {
		t.Fatalf("disabled strategy must not propose, got %+v", res.Proposed)
	}
	if len(res.Blockers) != 1 || res.Blockers[0].Code != BlockerStrategyDisabled {
		t.Fatalf("want single strategy_disabled blocker, got %+v", res.Blockers)
	}
}

// TestEvaluate_OracleNotCalledWhenBoundaryTerminates proves the subordinate
// contribution/selection callback is NOT evaluated once the stage-1 boundary
// hard stage terminates evaluation.
func TestEvaluate_OracleNotCalledWhenBoundaryTerminates(t *testing.T) {
	spy := &countingContrib{inner: mkContrib(t, m0(t, 100), 0)}
	cfg := Config{
		Boundary:          Boundary{Known: false}, // stage 1 terminates here
		ContributionFloor: m0(t, 0),               // policy money unit (issue #64): Match needs a coherent unit
		MovementCap:       DefaultMovementCap(),
		Cooldown:          DefaultCooldown,
		Strategy:          StrategyMatch,
		StrategyEnabled:   true,
		Reference:         m0(t, 1050),
		Objective:         ObjectiveTrackStrategy,
	}
	res, err := Evaluate(EvaluateInput{
		Config:       cfg,
		CurrentPrice: m0(t, 1000),
		Contribution: spy.fn(),
		Now:          time.Unix(1_000_000, 0),
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if res.Blockers[0].Code != BlockerBoundaryUnknown {
		t.Fatalf("want boundary_unknown, got %+v", res.Blockers)
	}
	if spy.calls != 0 {
		t.Fatalf("contribution oracle called %d times when boundary terminated; want 0", spy.calls)
	}
}

// TestEvaluate_OracleNotCalledWhenWindowEmpty proves the subordinate contribution
// callback is NOT evaluated once the movement-cap hard stage terminates on an
// empty feasible window.
func TestEvaluate_OracleNotCalledWhenWindowEmpty(t *testing.T) {
	spy := &countingContrib{inner: mkContrib(t, m0(t, 100), 0)}
	cfg := Config{
		Boundary:          Boundary{Known: true, Min: m0(t, 1200), Max: m0(t, 1300)},
		ContributionFloor: m0(t, 0),                  // policy money unit (issue #64): Match needs a coherent unit
		MovementCap:       money.NewBasisPoints(100), // ±1% around 1000 = [990,1010]; ∩ boundary = ∅
		Cooldown:          DefaultCooldown,
		Strategy:          StrategyMatch,
		StrategyEnabled:   true,
		Reference:         m0(t, 1250),
		Objective:         ObjectiveTrackStrategy,
	}
	res, err := Evaluate(EvaluateInput{
		Config:       cfg,
		CurrentPrice: m0(t, 1000),
		Contribution: spy.fn(),
		Now:          time.Unix(1_000_000, 0),
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if res.Blockers[0].Code != BlockerMovementInfeasible {
		t.Fatalf("want movement_cap_infeasible, got %+v", res.Blockers)
	}
	if spy.calls != 0 {
		t.Fatalf("contribution oracle called %d times when window empty; want 0", spy.calls)
	}
}

// TestProp_EarlierHardStageDominance generates combinations of failures across
// the six stages and proves earlier-stage dominance (PRC-003): the returned
// blockers are always sorted by mandated stage, a subordinate (stage 5–6) blocker
// never coexists with a hard blocker, and a proposal exists iff no blocker does.
func TestProp_EarlierHardStageDominance(t *testing.T) {
	now := time.Unix(1_000_000, 0)

	rapid.Check(t, func(t *rapid.T) {
		boundaryOK := rapid.Bool().Draw(t, "boundaryOK")
		floorOK := rapid.Bool().Draw(t, "floorOK")
		cooldownActiveChange := rapid.Bool().Draw(t, "cooldownActiveChange")
		strategyEnabled := rapid.Bool().Draw(t, "strategyEnabled")

		boundary := Boundary{Known: false}
		if boundaryOK {
			boundary = Boundary{Known: true, Min: mm(t, 900), Max: mm(t, 1100)}
		}

		// Strategy Match 1050 desires a CHANGE from current 1000. The feasible
		// window is [950,1050] (default 5% cap ∩ boundary), so the cap stage always
		// passes when the boundary is known.
		cfg := Config{
			Boundary:          boundary,
			ContributionFloor: mm(t, 0),
			MovementCap:       DefaultMovementCap(),
			Cooldown:          DefaultCooldown,
			Strategy:          StrategyMatch,
			StrategyEnabled:   strategyEnabled,
			Reference:         mm(t, 1050),
			Objective:         ObjectiveTrackStrategy,
		}

		// floorOK toggles whether any feasible price meets the hard floor.
		cogs := int64(100) // contribution(1050)=950 ≥ floor 0 ⇒ feasible
		if !floorOK {
			cogs = 5000 // contribution < 0 everywhere ⇒ infeasible
		}
		in := EvaluateInput{
			Config:       cfg,
			CurrentPrice: mm(t, 1000),
			Contribution: mkPropContrib(mm(t, cogs), 0),
			Now:          now,
			Readiness:    cost.StateComplete,
		}
		if cooldownActiveChange {
			last := now.Add(propTenMin) // active window, and Match 1050 is a change
			in.LastActionAt = &last
		}

		res, err := Evaluate(in)
		if err != nil {
			t.Fatalf("Evaluate: %v", err)
		}

		// Proposal exists iff no blocker exists.
		if (res.Proposed == nil) == (len(res.Blockers) == 0) {
			t.Fatalf("proposal/blocker exclusivity broken: proposed=%v blockers=%+v", res.Proposed, res.Blockers)
		}

		// Blockers are sorted by mandated stage.
		var prev Stage
		for i, b := range res.Blockers {
			if i > 0 && b.Stage < prev {
				t.Fatalf("blockers not in mandated order: %+v", res.Blockers)
			}
			prev = b.Stage
		}

		hasHard, hasSubordinate := false, false
		for _, b := range res.Blockers {
			if b.Stage.IsHard() {
				hasHard = true
			} else {
				hasSubordinate = true
			}
		}
		// Dominance: a subordinate (stage 5–6) blocker can never coexist with a
		// hard blocker.
		if hasHard && hasSubordinate {
			t.Fatalf("subordinate blocker coexisted with a hard blocker: %+v", res.Blockers)
		}

		// Expected blocker set by construction.
		if !boundaryOK {
			// Stage-1 boundary terminates: exactly one boundary blocker.
			if len(res.Blockers) != 1 || res.Blockers[0].Stage != StageBoundary {
				t.Fatalf("unknown boundary must yield a single stage-1 blocker: %+v", res.Blockers)
			}
			return
		}

		wantFloor := !floorOK
		wantCooldown := cooldownActiveChange
		wantStrategy := !wantFloor && !wantCooldown && !strategyEnabled

		gotFloor, gotCooldown, gotStrategy := false, false, false
		for _, b := range res.Blockers {
			switch b.Stage {
			case StageHardFloor:
				gotFloor = true
			case StageCooldown:
				gotCooldown = true
			case StageStrategy:
				gotStrategy = true
			}
		}
		if gotFloor != wantFloor {
			t.Fatalf("floor blocker=%v want %v (blockers %+v)", gotFloor, wantFloor, res.Blockers)
		}
		if gotCooldown != wantCooldown {
			t.Fatalf("cooldown blocker=%v want %v (blockers %+v)", gotCooldown, wantCooldown, res.Blockers)
		}
		if gotStrategy != wantStrategy {
			t.Fatalf("strategy_disabled blocker=%v want %v (blockers %+v)", gotStrategy, wantStrategy, res.Blockers)
		}
	})
}

// propTenMin is -10 minutes as a Duration built without an arithmetic operator on
// a money path (the money guard covers this file).
var propTenMin = mustParseDur("-10m")

func mustParseDur(s string) time.Duration {
	d, err := time.ParseDuration(s)
	if err != nil {
		panic(err)
	}
	return d
}

// mm builds an IRR/exp-0 money inside a rapid property (fails the rapid.T).
func mm(t *rapid.T, mantissa int64) money.Money {
	m, err := money.New(mantissa, "IRR", 0)
	if err != nil {
		t.Fatalf("money.New(%d): %v", mantissa, err)
	}
	return m
}
