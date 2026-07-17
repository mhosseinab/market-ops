package policy

import (
	"testing"
	"time"

	"pgregory.net/rapid"

	"github.com/mhosseinab/market-ops/services/core/internal/money"
)

// PRD §9.3 / PRC-003 require the policy-ordering and zero-floor guarantees to be
// proven by property test, not example. These run under `-rapid.checks=10000`
// (see the S16 Verify) so the invariants hold over ≥10k generated cases with 0
// counterexamples. The contribution oracle is an arbitrary MONOTONE-in-price
// model (the real model's shape), so the proof does not depend on any single
// cost profile.

var propDurations = []string{"60m", "90m", "2h", "24h"}
var propLastOffsets = []string{"-5m", "-30m", "-90m", "-3h"}

// propNow is a fixed evaluation instant; last-action times are derived from it.
var propNow = time.Unix(1_700_000_000, 0)

// mkPropContrib builds a monotone-increasing contribution oracle in pure Money
// arithmetic: contribution(price) = price − (commBp of price) − cogs.
func mkPropContrib(cogs money.Money, commBp int64) ContributionFunc {
	return func(price money.Money) (money.Money, error) {
		comm, err := price.ApplyRate(money.NewBasisPoints(commBp), money.RoundDown)
		if err != nil {
			return money.Money{}, err
		}
		afterComm, err := price.Sub(comm)
		if err != nil {
			return money.Money{}, err
		}
		return afterComm.Sub(cogs)
	}
}

// genInput draws an arbitrary valid EvaluateInput: a valid (stricter-only) config,
// a positive current price, a monotone contribution oracle, and an optional prior
// action within/outside cooldown.
func genInput(t *rapid.T) EvaluateInput {
	newIRR := func(mant int64) money.Money {
		m, err := money.New(mant, "IRR", 0)
		if err != nil {
			t.Fatalf("money.New: %v", err)
		}
		return m
	}

	minVal := rapid.Int64Range(1, 2_000_000).Draw(t, "boundaryMin")
	maxVal := rapid.Int64Range(minVal, 3_000_000).Draw(t, "boundaryMax")
	current := rapid.Int64Range(1, 3_000_000).Draw(t, "current")
	capBp := rapid.Int64Range(0, 500).Draw(t, "capBp")
	floor := rapid.Int64Range(-200_000, 800_000).Draw(t, "floor")
	reference := rapid.Int64Range(1, 3_000_000).Draw(t, "reference")
	undercut := rapid.Int64Range(0, 500).Draw(t, "undercutBp")
	cooldownStr := rapid.SampledFrom(propDurations).Draw(t, "cooldown")
	cooldown, err := time.ParseDuration(cooldownStr)
	if err != nil {
		t.Fatalf("ParseDuration: %v", err)
	}
	strategy := rapid.SampledFrom([]Strategy{StrategyHold, StrategyMatch, StrategyUndercut}).Draw(t, "strategy")
	objective := rapid.SampledFrom([]Objective{ObjectiveMaximizeContribution, ObjectiveTrackStrategy}).Draw(t, "objective")

	movementCap := money.NewBasisPoints(capBp)
	cfg, err := NewConfig(ConfigParams{
		Boundary:          Boundary{Known: true, Min: newIRR(minVal), Max: newIRR(maxVal)},
		ContributionFloor: newIRR(floor),
		MovementCap:       &movementCap,
		Cooldown:          &cooldown,
		Strategy:          strategy,
		StrategyEnabled:   true,
		Reference:         newIRR(reference),
		UndercutBp:        money.NewBasisPoints(undercut),
		Objective:         objective,
	})
	if err != nil {
		t.Fatalf("NewConfig with valid params rejected: %v", err)
	}

	cogs := rapid.Int64Range(0, 3_000_000).Draw(t, "cogs")
	commBp := rapid.Int64Range(0, 9000).Draw(t, "commBp")

	in := EvaluateInput{
		Config:       cfg,
		CurrentPrice: newIRR(current),
		Contribution: mkPropContrib(newIRR(cogs), commBp),
		Now:          propNow,
	}
	if rapid.Bool().Draw(t, "hasPriorAction") {
		off, err := time.ParseDuration(rapid.SampledFrom(propLastOffsets).Draw(t, "lastOffset"))
		if err != nil {
			t.Fatalf("ParseDuration offset: %v", err)
		}
		last := propNow.Add(off)
		in.LastActionAt = &last
	}
	return in
}

// TestProp_OrderingInvariant proves PRC-003: whenever Evaluate emits a proposal,
// EVERY hard stage (boundary, movement cap, cooldown) is satisfied — no
// subordinate stage (strategy/objective) can override an earlier hard constraint.
// It also asserts blockers come back in policy order.
func TestProp_OrderingInvariant(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		in := genInput(t)
		res, err := Evaluate(in)
		if err != nil {
			t.Fatalf("Evaluate errored on valid input: %v", err)
		}

		if res.Proposed == nil {
			// Blocked: at least one blocker, sorted in policy order.
			if len(res.Blockers) == 0 {
				t.Fatal("no proposal but no blockers")
			}
			first := true
			var prev Stage
			for _, b := range res.Blockers {
				if !first && b.Stage < prev {
					t.Fatalf("blockers not in policy order: %+v", res.Blockers)
				}
				prev = b.Stage
				first = false
			}
			return
		}

		// A proposal must carry no blockers.
		if len(res.Blockers) != 0 {
			t.Fatalf("proposal carried blockers: %+v", res.Blockers)
		}
		price := res.Proposed.Price

		// Hard stage 1 — inside the marketplace boundary.
		assertWithin(t, price, in.Config.Boundary.Min, in.Config.Boundary.Max, "boundary")

		// Hard stage 3 — inside the movement window around the current price.
		delta, err := in.CurrentPrice.ApplyRate(in.Config.MovementCap, money.RoundDown)
		if err != nil {
			t.Fatalf("cap delta: %v", err)
		}
		lo, err := in.CurrentPrice.Sub(delta)
		if err != nil {
			t.Fatalf("moveLow: %v", err)
		}
		hi, err := in.CurrentPrice.Add(delta)
		if err != nil {
			t.Fatalf("moveHigh: %v", err)
		}
		assertWithin(t, price, lo, hi, "movement cap")

		// Hard stage 4 — cooldown: if a change is proposed, cooldown is NOT active.
		if in.LastActionAt != nil {
			deadline := in.LastActionAt.Add(in.Config.Cooldown)
			if in.Now.Before(deadline) {
				changeCmp, err := price.Compare(in.CurrentPrice)
				if err != nil {
					t.Fatalf("change compare: %v", err)
				}
				if changeCmp != 0 {
					t.Fatal("proposal changes price while cooldown is active")
				}
			}
		}
	})
}

// TestProp_ZeroFloorInvariant proves the never-cut zero-cross guarantee and the
// hard floor: any proposed price yields a contribution strictly greater than zero
// AND at least the hard contribution floor. No output crosses zero contribution.
func TestProp_ZeroFloorInvariant(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		in := genInput(t)
		res, err := Evaluate(in)
		if err != nil {
			t.Fatalf("Evaluate errored on valid input: %v", err)
		}
		if res.Proposed == nil {
			return
		}
		c, err := in.Contribution(res.Proposed.Price)
		if err != nil {
			t.Fatalf("contribution oracle: %v", err)
		}
		// The recorded proposal contribution must equal the oracle at that price.
		eq, err := c.Equal(res.Proposed.Contribution)
		if err != nil {
			t.Fatalf("contribution equal: %v", err)
		}
		if !eq {
			t.Fatalf("recorded contribution %s != oracle %s", res.Proposed.Contribution.String(), c.String())
		}
		// Strictly positive (zero-cross guard).
		zero, err := money.Zero("IRR", 0)
		if err != nil {
			t.Fatalf("zero: %v", err)
		}
		zc, err := c.Compare(zero)
		if err != nil {
			t.Fatalf("zero compare: %v", err)
		}
		if zc <= 0 {
			t.Fatalf("output contribution %s crosses zero", c.String())
		}
		// At least the hard floor.
		fc, err := c.Compare(in.Config.ContributionFloor)
		if err != nil {
			t.Fatalf("floor compare: %v", err)
		}
		if fc < 0 {
			t.Fatalf("output contribution %s below hard floor %s", c.String(), in.Config.ContributionFloor.String())
		}
	})
}

// assertWithin fails unless low ≤ v ≤ high.
func assertWithin(t *rapid.T, v, low, high money.Money, label string) {
	lc, err := v.Compare(low)
	if err != nil {
		t.Fatalf("%s low compare: %v", label, err)
	}
	hc, err := v.Compare(high)
	if err != nil {
		t.Fatalf("%s high compare: %v", label, err)
	}
	if lc < 0 || hc > 0 {
		t.Fatalf("%s violated: %s not in [%s, %s]", label, v.String(), low.String(), high.String())
	}
}
