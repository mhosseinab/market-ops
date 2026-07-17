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
	full := routec.PlanSweep(tier, ids, 50, routec.State{RequestsRemaining: 1000, BytesRemaining: 1 << 30})
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
	pressured := routec.PlanSweep(tier, ids, 50, routec.State{RequestsRemaining: 10, BytesRemaining: 1 << 30})
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
	plan := routec.PlanSweep(observation.TierPriority, ids, routec.EffectivePriorityCap(0), routec.State{RequestsRemaining: 1000, BytesRemaining: 1 << 30})
	if len(plan.TargetIDs) != routec.DefaultPriorityCap {
		t.Fatalf("priority sweep should cap at %d, got %d", routec.DefaultPriorityCap, len(plan.TargetIDs))
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
