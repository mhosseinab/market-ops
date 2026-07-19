package policy

import (
	"errors"
	"testing"
	"time"

	"github.com/mhosseinab/market-ops/services/core/internal/cost"
	"github.com/mhosseinab/market-ops/services/core/internal/money"
)

// m0 builds an IRR/exp-0 money for tests.
func m0(t *testing.T, mantissa int64) money.Money {
	t.Helper()
	m, err := money.New(mantissa, "IRR", 0)
	if err != nil {
		t.Fatalf("money.New(%d): %v", mantissa, err)
	}
	return m
}

// dur parses a duration string without any arithmetic operator (the money guard
// covers this test file too).
func dur(t *testing.T, s string) time.Duration {
	t.Helper()
	d, err := time.ParseDuration(s)
	if err != nil {
		t.Fatalf("ParseDuration(%q): %v", s, err)
	}
	return d
}

// mkContrib returns a monotone-increasing-in-price contribution oracle:
// contribution(price) = price − commission(commBp of price) − cogs, all in Money
// arithmetic. It is monotone for commBp < 10000, matching the real model.
func mkContrib(t *testing.T, cogs money.Money, commBp int64) ContributionFunc {
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

func bpPtr(v int64) *money.BasisPoints {
	bp := money.NewBasisPoints(v)
	return &bp
}

func durPtr(d time.Duration) *time.Duration { return &d }

func TestNewConfig_StricterOnly(t *testing.T) {
	base := ConfigParams{
		Boundary:  Boundary{Known: true, Min: m0(t, 1), Max: m0(t, 10)},
		Strategy:  StrategyHold,
		Objective: ObjectiveTrackStrategy,
	}

	// Default cap/cooldown (nil) are accepted.
	if _, err := NewConfig(base); err != nil {
		t.Fatalf("default config rejected: %v", err)
	}
	// Stricter cap (300 bp < 500) and stricter cooldown (90m > 60m) accepted.
	strict := base
	strict.MovementCap = bpPtr(300)
	strict.Cooldown = durPtr(dur(t, "90m"))
	if _, err := NewConfig(strict); err != nil {
		t.Fatalf("stricter config rejected: %v", err)
	}
	// Looser cap (600 bp > 500) rejected (PRC-004).
	looseCap := base
	looseCap.MovementCap = bpPtr(600)
	if _, err := NewConfig(looseCap); !errors.Is(err, ErrMovementCapTooLoose) {
		t.Fatalf("loose cap err = %v, want ErrMovementCapTooLoose", err)
	}
	// Looser cooldown (30m < 60m) rejected (PRC-004).
	looseCd := base
	looseCd.Cooldown = durPtr(dur(t, "30m"))
	if _, err := NewConfig(looseCd); !errors.Is(err, ErrCooldownTooLoose) {
		t.Fatalf("loose cooldown err = %v, want ErrCooldownTooLoose", err)
	}
	// Negative values rejected.
	negCap := base
	negCap.MovementCap = bpPtr(-1)
	if _, err := NewConfig(negCap); !errors.Is(err, ErrInvalidMovementCap) {
		t.Fatalf("negative cap err = %v, want ErrInvalidMovementCap", err)
	}
}

func happyConfig(t *testing.T, strategy Strategy, obj Objective, ref money.Money, floor money.Money) Config {
	cfg, err := NewConfig(ConfigParams{
		Boundary:          Boundary{Known: true, Min: m0(t, 900), Max: m0(t, 1100)},
		ContributionFloor: floor,
		Strategy:          strategy,
		StrategyEnabled:   true,
		Reference:         ref,
		Objective:         obj,
	})
	if err != nil {
		t.Fatalf("NewConfig: %v", err)
	}
	return cfg
}

func TestEvaluate_HappyProposal(t *testing.T) {
	cfg := happyConfig(t, StrategyHold, ObjectiveTrackStrategy, m0(t, 1000), m0(t, 100))
	res, err := Evaluate(EvaluateInput{
		Config:       cfg,
		CurrentPrice: m0(t, 1000),
		Contribution: mkContrib(t, m0(t, 800), 0), // contribution(1000) = 200
		Now:          time.Unix(1_000_000, 0),
		Readiness:    cost.StateComplete,
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if res.Proposed == nil {
		t.Fatalf("expected a proposal, got blockers %+v", res.Blockers)
	}
	if res.Proposed.Price.Mantissa() != 1000 {
		t.Fatalf("price = %d, want 1000", res.Proposed.Price.Mantissa())
	}
	if res.Proposed.Contribution.Mantissa() != 200 {
		t.Fatalf("contribution = %d, want 200", res.Proposed.Contribution.Mantissa())
	}
	if !res.Approvable() {
		t.Fatal("a clean non-simulation proposal must be approvable")
	}
}

func TestEvaluate_FloorForcesPriceUp_OrderingOverObjective(t *testing.T) {
	// Objective TrackStrategy wants 950; the hard floor (stage 2) forces the
	// price up to the max-contribution feasible price 1050, proving stage 2
	// overrides stage 6.
	cfg := happyConfig(t, StrategyMatch, ObjectiveTrackStrategy, m0(t, 950), m0(t, 80))
	res, err := Evaluate(EvaluateInput{
		Config:       cfg,
		CurrentPrice: m0(t, 1000),
		Contribution: mkContrib(t, m0(t, 900), 0), // contribution(price) = price − 900
		Now:          time.Unix(1_000_000, 0),
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if res.Proposed == nil {
		t.Fatalf("expected a proposal, got blockers %+v", res.Blockers)
	}
	// window is [950,1050]; 950→50 (<floor 80); floor forces up to 1050→150.
	if res.Proposed.Price.Mantissa() != 1050 {
		t.Fatalf("price = %d, want 1050 (floor forced up)", res.Proposed.Price.Mantissa())
	}
	if res.Proposed.Contribution.Mantissa() != 150 {
		t.Fatalf("contribution = %d, want 150", res.Proposed.Contribution.Mantissa())
	}
}

func TestEvaluate_BoundaryUnknownBlocks(t *testing.T) {
	cfg, err := NewConfig(ConfigParams{
		Boundary:        Boundary{Known: false},
		Strategy:        StrategyHold,
		StrategyEnabled: true,
		Objective:       ObjectiveTrackStrategy,
	})
	if err != nil {
		t.Fatalf("NewConfig: %v", err)
	}
	res, err := Evaluate(EvaluateInput{
		Config:       cfg,
		CurrentPrice: m0(t, 1000),
		Contribution: mkContrib(t, m0(t, 800), 0),
		Now:          time.Unix(1_000_000, 0),
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if res.Proposed != nil {
		t.Fatal("unknown boundary must block")
	}
	if len(res.Blockers) != 1 || res.Blockers[0].Code != BlockerBoundaryUnknown {
		t.Fatalf("blockers = %+v, want single boundary_unknown", res.Blockers)
	}
}

func TestEvaluate_MovementCapInfeasibleBlocks(t *testing.T) {
	cfg, err := NewConfig(ConfigParams{
		Boundary:        Boundary{Known: true, Min: m0(t, 1200), Max: m0(t, 1300)},
		MovementCap:     bpPtr(100), // ±1% around 1000 = [990,1010]
		Strategy:        StrategyHold,
		StrategyEnabled: true,
		Objective:       ObjectiveTrackStrategy,
	})
	if err != nil {
		t.Fatalf("NewConfig: %v", err)
	}
	res, err := Evaluate(EvaluateInput{
		Config:       cfg,
		CurrentPrice: m0(t, 1000),
		Contribution: mkContrib(t, m0(t, 100), 0),
		Now:          time.Unix(1_000_000, 0),
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if res.Proposed != nil {
		t.Fatal("empty feasible window must block")
	}
	if res.Blockers[0].Code != BlockerMovementInfeasible {
		t.Fatalf("blockers = %+v, want movement_cap_infeasible", res.Blockers)
	}
}

func TestEvaluate_CooldownChangeBlockedHoldAllowed(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	last := now.Add(dur(t, "-10m")) // 10m ago, inside a 60m cooldown

	// A change (Match 1050) during cooldown is blocked.
	changeCfg := happyConfig(t, StrategyMatch, ObjectiveTrackStrategy, m0(t, 1050), m0(t, 0))
	res, err := Evaluate(EvaluateInput{
		Config:       changeCfg,
		CurrentPrice: m0(t, 1000),
		Contribution: mkContrib(t, m0(t, 100), 0),
		Now:          now,
		LastActionAt: &last,
	})
	if err != nil {
		t.Fatalf("Evaluate change: %v", err)
	}
	if res.Proposed != nil {
		t.Fatal("a change during cooldown must block")
	}
	if res.Blockers[0].Code != BlockerCooldownActive {
		t.Fatalf("blockers = %+v, want cooldown_active", res.Blockers)
	}

	// A hold (no change) during cooldown is allowed.
	holdCfg := happyConfig(t, StrategyHold, ObjectiveTrackStrategy, m0(t, 1000), m0(t, 0))
	res, err = Evaluate(EvaluateInput{
		Config:       holdCfg,
		CurrentPrice: m0(t, 1000),
		Contribution: mkContrib(t, m0(t, 100), 0),
		Now:          now,
		LastActionAt: &last,
	})
	if err != nil {
		t.Fatalf("Evaluate hold: %v", err)
	}
	if res.Proposed == nil {
		t.Fatalf("a hold during cooldown must be allowed, got %+v", res.Blockers)
	}
	if res.Proposed.Price.Mantissa() != 1000 {
		t.Fatalf("hold price = %d, want 1000", res.Proposed.Price.Mantissa())
	}
}

func TestEvaluate_BlockersReturnedInPolicyOrder(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	last := now.Add(dur(t, "-10m"))
	// Match 1050 is a change; cogs 2000 makes contribution negative at every
	// feasible price ⇒ hard-floor (crosses-zero) blocker AND cooldown blocker.
	cfg := happyConfig(t, StrategyMatch, ObjectiveTrackStrategy, m0(t, 1050), m0(t, 0))
	res, err := Evaluate(EvaluateInput{
		Config:       cfg,
		CurrentPrice: m0(t, 1000),
		Contribution: mkContrib(t, m0(t, 2000), 0),
		Now:          now,
		LastActionAt: &last,
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if res.Proposed != nil {
		t.Fatal("expected blockers, got a proposal")
	}
	if len(res.Blockers) != 2 {
		t.Fatalf("want 2 blockers, got %+v", res.Blockers)
	}
	// Policy order: hard floor (stage 2) before cooldown (stage 4).
	if res.Blockers[0].Stage != StageHardFloor || res.Blockers[1].Stage != StageCooldown {
		t.Fatalf("blockers not in policy order: %+v", res.Blockers)
	}
	if res.Blockers[0].Code != BlockerCrossesZero {
		t.Fatalf("first blocker = %v, want contribution_crosses_zero", res.Blockers[0].Code)
	}
}

func TestSimulate_NeverApprovable(t *testing.T) {
	cfg := happyConfig(t, StrategyHold, ObjectiveTrackStrategy, m0(t, 1000), m0(t, 100))
	res, err := Simulate(EvaluateInput{
		Config:       cfg,
		CurrentPrice: m0(t, 1000),
		Contribution: mkContrib(t, m0(t, 800), 0),
		Now:          time.Unix(1_000_000, 0),
	})
	if err != nil {
		t.Fatalf("Simulate: %v", err)
	}
	if !res.Simulation {
		t.Fatal("Simulate must mark the result as a simulation")
	}
	if res.Proposed == nil {
		t.Fatal("simulation should still compute a what-if proposal")
	}
	if res.Approvable() {
		t.Fatal("a simulation must NEVER be approvable (free-text/simulation containment)")
	}
}

func TestEvaluate_LooseConfigCannotPropose(t *testing.T) {
	// A Config built by bypassing NewConfig with a loose cap must be rejected by
	// Evaluate itself (defense in depth for the never-cut invariant).
	cfg := Config{
		Boundary:        Boundary{Known: true, Min: m0(t, 900), Max: m0(t, 1100)},
		MovementCap:     money.NewBasisPoints(9000), // grossly loose
		Cooldown:        DefaultCooldown,
		Strategy:        StrategyHold,
		StrategyEnabled: true,
		Objective:       ObjectiveTrackStrategy,
	}
	_, err := Evaluate(EvaluateInput{
		Config:       cfg,
		CurrentPrice: m0(t, 1000),
		Contribution: mkContrib(t, m0(t, 800), 0),
		Now:          time.Unix(1_000_000, 0),
	})
	if !errors.Is(err, ErrMovementCapTooLoose) {
		t.Fatalf("Evaluate err = %v, want ErrMovementCapTooLoose", err)
	}
}
