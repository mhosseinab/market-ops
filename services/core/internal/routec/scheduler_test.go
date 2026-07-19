package routec_test

import (
	"math/rand"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mhosseinab/market-ops/services/core/internal/observation"
	"github.com/mhosseinab/market-ops/services/core/internal/routec"
)

func makeIDs(n int) []uuid.UUID {
	ids := make([]uuid.UUID, n)
	for i := range ids {
		ids[i] = uuid.New()
	}
	return ids
}

// TestPlanSweepReducesCountBeforeWideningFreshness is the §10.2/§17.3 acceptance:
// under budget pressure the target COUNT drops while the tier's cadence and
// freshness windows are UNCHANGED.
func TestPlanSweepReducesCountBeforeWideningFreshness(t *testing.T) {
	tier := observation.TierPriority
	cadence, freshness := routec.TierCadence(tier)
	ids := makeIDs(40)

	// Healthy budget: full count, windows unchanged.
	full := routec.PlanSweep(tier, ids, 50, routec.State{RequestsRemaining: 1000, BytesRemaining: 1 << 30}, 0)
	if len(full.TargetIDs) != 40 {
		t.Fatalf("healthy sweep should schedule all 40, got %d", len(full.TargetIDs))
	}
	if full.Trimmed != 0 {
		t.Fatalf("healthy sweep trimmed %d (want 0)", full.Trimmed)
	}
	if full.Cadence != cadence || full.Freshness != freshness {
		t.Fatal("healthy sweep must not alter tier windows")
	}

	// Budget pressure: only 10 requests of headroom left.
	pressured := routec.PlanSweep(tier, ids, 50, routec.State{RequestsRemaining: 10, BytesRemaining: 1 << 30}, 0)
	if len(pressured.TargetIDs) != 10 {
		t.Fatalf("under pressure should reduce to 10 targets, got %d", len(pressured.TargetIDs))
	}
	if pressured.Trimmed != 30 {
		t.Fatalf("under pressure should trim 30 targets, got %d", pressured.Trimmed)
	}
	// THE invariant: windows never widen under pressure.
	if pressured.Cadence != cadence {
		t.Fatalf("cadence widened under pressure: got %s want %s", pressured.Cadence, cadence)
	}
	if pressured.Freshness != freshness {
		t.Fatalf("freshness widened under pressure: got %s want %s", pressured.Freshness, freshness)
	}
}

// TestPlanSweepPriorityCap asserts the priority count cap is applied (default 50
// until S35 measures a higher cap), never above the 200 ceiling.
func TestPlanSweepPriorityCap(t *testing.T) {
	if got := routec.EffectivePriorityCap(0); got != routec.DefaultPriorityCap {
		t.Fatalf("unmeasured cap: got %d want %d", got, routec.DefaultPriorityCap)
	}
	if got := routec.EffectivePriorityCap(120); got != 120 {
		t.Fatalf("measured cap 120: got %d", got)
	}
	if got := routec.EffectivePriorityCap(5000); got != routec.MaxPriorityCap {
		t.Fatalf("cap must not exceed %d, got %d", routec.MaxPriorityCap, got)
	}

	ids := makeIDs(80)
	plan := routec.PlanSweep(observation.TierPriority, ids, routec.EffectivePriorityCap(0), routec.State{RequestsRemaining: 1000, BytesRemaining: 1 << 30}, 0)
	if len(plan.TargetIDs) != routec.DefaultPriorityCap {
		t.Fatalf("priority sweep should cap at %d, got %d", routec.DefaultPriorityCap, len(plan.TargetIDs))
	}
}

// TestPlanSweepRotatesUnderBudgetPressureNoPrefixStarvation is the issue #50
// regression: under a RECURRING tight budget, selection must NOT keep taking the
// same deterministic prefix of the ordered target list. Threading the returned
// NextCursor back in, every eligible target is selected within a BOUNDED number
// of sweeps (ceil(eligible/perSweep)) and no fixed prefix monopolizes the budget.
func TestPlanSweepRotatesUnderBudgetPressureNoPrefixStarvation(t *testing.T) {
	tier := observation.TierStandard
	ids := makeIDs(5)
	budget := routec.State{RequestsRemaining: 2, BytesRemaining: 1 << 30} // 2 of 5 per sweep

	seen := map[uuid.UUID]int{}
	cursor := 0
	// ceil(5/2) = 3 sweeps must cover ALL five targets exactly once.
	for sweep := 0; sweep < 3; sweep++ {
		plan := routec.PlanSweep(tier, ids, -1, budget, cursor)
		if len(plan.TargetIDs) == 0 {
			t.Fatalf("sweep %d selected nothing under a positive budget", sweep)
		}
		if plan.Trimmed != len(ids)-len(plan.TargetIDs) {
			t.Fatalf("sweep %d trimmed accounting wrong: trimmed=%d selected=%d eligible=%d", sweep, plan.Trimmed, len(plan.TargetIDs), len(ids))
		}
		for _, id := range plan.TargetIDs {
			seen[id]++
		}
		cursor = plan.NextCursor
	}
	if len(seen) != len(ids) {
		t.Fatalf("prefix starvation: only %d of %d targets ever selected across 3 constrained sweeps (want all)", len(seen), len(ids))
	}
	// 3 sweeps of 2 = 6 slots over 5 targets, so exactly one target legitimately
	// wraps into a second selection; fairness means selections differ by at most
	// one (no target starves while another is served repeatedly).
	for id, n := range seen {
		if n < 1 || n > 2 {
			t.Fatalf("fairness broken: target %s selected %d times in one rotation (want 1..2, balanced)", id, n)
		}
	}

	// Concretely: the first sweep from cursor 0 must NOT be the only prefix ever
	// used. Sweep 1 (cursor 0) picks ids[0..1]; a second sweep must advance past it.
	first := routec.PlanSweep(tier, ids, -1, budget, 0)
	second := routec.PlanSweep(tier, ids, -1, budget, first.NextCursor)
	if first.TargetIDs[0] == second.TargetIDs[0] {
		t.Fatalf("second constrained sweep re-selected the same head %s — prefix monopolizes the budget", first.TargetIDs[0])
	}
}

// TestPlanSweepCursorWrapsAndStaysInRange asserts the rotation cursor is always a
// valid index into the ordered list (deterministic, stable tie-breaking) and
// wraps at the end. A cursor beyond the eligible count is normalized, never a
// panic or an out-of-range slice.
func TestPlanSweepCursorWrapsAndStaysInRange(t *testing.T) {
	tier := observation.TierBackground
	ids := makeIDs(3)
	budget := routec.State{RequestsRemaining: 2, BytesRemaining: 1 << 30}

	// From the last valid cursor (2), a 2-wide window wraps: [2,0], next cursor 1.
	plan := routec.PlanSweep(tier, ids, -1, budget, 2)
	if len(plan.TargetIDs) != 2 {
		t.Fatalf("wrapped sweep should still select 2, got %d", len(plan.TargetIDs))
	}
	if plan.TargetIDs[0] != ids[2] || plan.TargetIDs[1] != ids[0] {
		t.Fatalf("wrap order wrong: got [%s %s] want [%s %s]", plan.TargetIDs[0], plan.TargetIDs[1], ids[2], ids[0])
	}
	if plan.NextCursor < 0 || plan.NextCursor >= len(ids) {
		t.Fatalf("next cursor out of range: %d (eligible %d)", plan.NextCursor, len(ids))
	}
	if plan.NextCursor != 1 {
		t.Fatalf("next cursor: got %d want 1", plan.NextCursor)
	}

	// An oversized/stale cursor is normalized, never a panic or bad index.
	stale := routec.PlanSweep(tier, ids, -1, budget, 999)
	if stale.NextCursor < 0 || stale.NextCursor >= len(ids) {
		t.Fatalf("stale cursor not normalized: next=%d eligible=%d", stale.NextCursor, len(ids))
	}
}

// TestPlanSweepFullSweepIgnoresCursor asserts that when the budget covers every
// eligible target (no trim), all are selected and the cursor is irrelevant to
// fairness — a full sweep observes everyone regardless of the rotation offset.
func TestPlanSweepFullSweepIgnoresCursor(t *testing.T) {
	tier := observation.TierStandard
	ids := makeIDs(4)
	budget := routec.State{RequestsRemaining: 100, BytesRemaining: 1 << 30}

	for _, cursor := range []int{0, 1, 3, 7} {
		plan := routec.PlanSweep(tier, ids, -1, budget, cursor)
		if len(plan.TargetIDs) != len(ids) {
			t.Fatalf("cursor %d: full sweep should select all %d, got %d", cursor, len(ids), len(plan.TargetIDs))
		}
		if plan.Trimmed != 0 {
			t.Fatalf("cursor %d: full sweep must trim nothing, got %d", cursor, plan.Trimmed)
		}
		seen := map[uuid.UUID]bool{}
		for _, id := range plan.TargetIDs {
			seen[id] = true
		}
		if len(seen) != len(ids) {
			t.Fatalf("cursor %d: full sweep must cover every target once, covered %d", cursor, len(seen))
		}
	}
}

// TestJitteredScheduleWithinBounds asserts each tier's periodic schedule fires
// within [cadence*(1-frac), cadence*(1+frac)] — jitter spreads the cadence but
// never widens it into a different tier.
func TestJitteredScheduleWithinBounds(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	jitterBP := 2000 // ±20%
	for _, tier := range []observation.Tier{observation.TierPriority, observation.TierStandard, observation.TierBackground} {
		cadence, _ := routec.TierCadence(tier)
		sched := routec.NewJitteredSchedule(cadence, jitterBP, rng)
		lo := time.Duration(int64(cadence) * (10000 - int64(jitterBP)) / 10000)
		hi := time.Duration(int64(cadence) * (10000 + int64(jitterBP)) / 10000)
		base := time.Unix(0, 0)
		for i := 0; i < 1000; i++ {
			next := sched.Next(base)
			d := next.Sub(base)
			if d < lo || d > hi {
				t.Fatalf("tier %s: interval %s out of jitter bounds [%s,%s]", tier, d, lo, hi)
			}
		}
	}
}
