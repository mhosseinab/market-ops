package policy

import (
	"testing"
	"time"

	"pgregory.net/rapid"

	"github.com/mhosseinab/market-ops/services/core/internal/cost"
)

// Issue #59 (CST-003 / PRD §9.2): the policy engine must never declare a result
// approvable unless the verified margin readiness is exactly Complete. A clean
// proposal derived from partial/stale/missing economics can satisfy every price
// stage, but it must NOT be executable — only Complete may drive an approval
// control. These are negative tests: they are written first and kept passing on
// every change.

// readinessGateConfig is the shared clean, all-stages-passing config: it yields a
// proposal (contribution 200 at price 1000) so the ONLY thing that can make the
// result non-approvable is the readiness gate under test.
func readinessGateInput(t *testing.T, readiness cost.State) EvaluateInput {
	t.Helper()
	cfg := happyConfig(t, StrategyHold, ObjectiveTrackStrategy, m0(t, 1000), m0(t, 100))
	return EvaluateInput{
		Config:       cfg,
		CurrentPrice: m0(t, 1000),
		Contribution: mkContrib(t, m0(t, 800), 0), // contribution(1000) = 200
		Now:          time.Unix(1_000_000, 0),
		Readiness:    readiness,
	}
}

// TestEvaluate_NonCompleteReadinessNeverApprovable proves that a clean proposal
// backed by partial, stale, or missing readiness is never approvable, while an
// otherwise-identical Complete result is. The proposal itself may still be
// returned as analysis (Partial may be analyzed) but it is never bindable.
func TestEvaluate_NonCompleteReadinessNeverApprovable(t *testing.T) {
	nonComplete := []cost.State{cost.StatePartial, cost.StateStale, cost.StateMissing}
	for _, st := range nonComplete {
		st := st
		t.Run(string(st), func(t *testing.T) {
			res, err := Evaluate(readinessGateInput(t, st))
			if err != nil {
				t.Fatalf("Evaluate(%s): %v", st, err)
			}
			if res.Approvable() {
				t.Fatalf("readiness %s produced an approvable result; only Complete may (CST-003)", st)
			}
		})
	}

	// The exact same clean proposal with Complete readiness IS approvable.
	res, err := Evaluate(readinessGateInput(t, cost.StateComplete))
	if err != nil {
		t.Fatalf("Evaluate(complete): %v", err)
	}
	if !res.Approvable() {
		t.Fatalf("Complete readiness with all stages passing must be approvable, got blockers %+v", res.Blockers)
	}
}

// TestEvaluate_UnsetReadinessFailsClosed proves the gate fails closed: an unset
// (zero-value / unknown) readiness never yields an approvable result even when
// every price stage passes.
func TestEvaluate_UnsetReadinessFailsClosed(t *testing.T) {
	res, err := Evaluate(readinessGateInput(t, cost.State("")))
	if err != nil {
		t.Fatalf("Evaluate(unset): %v", err)
	}
	if res.Approvable() {
		t.Fatal("unset readiness must fail closed (never approvable)")
	}
}

// TestSimulate_NonCompleteReadinessNeverApprovable confirms a simulation stays
// non-approvable across every readiness state (defense in depth: simulation
// containment AND the readiness gate both hold).
func TestSimulate_NonCompleteReadinessNeverApprovable(t *testing.T) {
	for _, st := range []cost.State{cost.StateComplete, cost.StatePartial, cost.StateStale, cost.StateMissing} {
		st := st
		t.Run(string(st), func(t *testing.T) {
			res, err := Simulate(readinessGateInput(t, st))
			if err != nil {
				t.Fatalf("Simulate(%s): %v", st, err)
			}
			if res.Approvable() {
				t.Fatalf("a simulation must never be approvable (readiness=%s)", st)
			}
		})
	}
}

// TestProp_ReadinessGate is the property proof for issue #59: over arbitrary valid
// policy inputs and all four readiness states, approvability implies Complete
// readiness — non-Complete never yields an approval control.
func TestProp_ReadinessGate(t *testing.T) {
	states := []cost.State{cost.StateComplete, cost.StatePartial, cost.StateStale, cost.StateMissing, cost.State("")}
	rapid.Check(t, func(t *rapid.T) {
		in := genInput(t)
		in.Readiness = rapid.SampledFrom(states).Draw(t, "readiness")

		res, err := Evaluate(in)
		if err != nil {
			t.Fatalf("Evaluate errored on valid input: %v", err)
		}
		if res.Approvable() && res.Readiness != cost.StateComplete {
			t.Fatalf("approvable result with non-Complete readiness %q (CST-003 violated)", res.Readiness)
		}
		if in.Readiness != cost.StateComplete && res.Approvable() {
			t.Fatalf("non-Complete readiness %q yielded an approvable result", in.Readiness)
		}
	})
}
