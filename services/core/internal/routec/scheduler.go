package routec

import (
	"math/rand"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"

	"github.com/mhosseinab/market-ops/services/core/internal/observation"
)

// SweepPlan is the decided work for one tier sweep (PRD §10.2, §17.3). The
// load-bearing invariant it encodes: under budget pressure the target COUNT is
// reduced (Trimmed > 0) while Cadence and Freshness are the tier's fixed windows,
// NEVER widened. A consumer that widened freshness instead of trimming would
// violate §10.2 — this type makes that impossible to express, because the plan
// carries the unchanged windows and a trimmed target list.
type SweepPlan struct {
	Tier      observation.Tier
	Cadence   time.Duration
	Freshness time.Duration
	TargetIDs []uuid.UUID
	// Trimmed is how many eligible targets were dropped this sweep due to cap or
	// budget pressure. They are deferred to a later sweep at the SAME cadence —
	// freshness is never widened to cover them.
	Trimmed int
}

// PlanSweep decides which targets to observe this sweep. It applies the count cap
// first, then reduces further if the account's request budget cannot cover the
// capped count. It returns the tier's UNCHANGED cadence/freshness windows
// alongside the (possibly trimmed) target list.
//
//   - targetIDs: the tier's eligible active targets, in scheduler order.
//   - countCap: the max targets to schedule this sweep (for priority this is the
//     EffectivePriorityCap; for standard/background it is len(targetIDs) or an
//     operator cap).
//   - budget: the account's remaining request headroom.
func PlanSweep(tier observation.Tier, targetIDs []uuid.UUID, countCap int, budget State) SweepPlan {
	cadence, freshness := TierCadence(tier)
	plan := SweepPlan{Tier: tier, Cadence: cadence, Freshness: freshness}

	eligible := len(targetIDs)
	selected := eligible
	if countCap >= 0 && countCap < selected {
		selected = countCap
	}
	// Budget pressure: trim COUNT before anything else. Never touch the windows.
	if budget.RequestsRemaining < selected {
		selected = budget.RequestsRemaining
	}
	if selected < 0 {
		selected = 0
	}
	plan.TargetIDs = append([]uuid.UUID(nil), targetIDs[:selected]...)
	plan.Trimmed = eligible - selected
	return plan
}

// TierSweepArgs is the periodic job for one cadence tier. The worker enumerates
// the tier's active targets, plans the sweep, and observes each admitted target.
// Fields are JSON-safe business data only (plan §4.8).
type TierSweepArgs struct {
	Tier string `json:"tier"`
}

// Kind is River's stable identifier; it must never change once shipped.
func (TierSweepArgs) Kind() string { return "routec_tier_sweep" }

// jitteredSchedule is a river.PeriodicSchedule that runs at a base interval
// spread by ± jitterBP basis points (OBS-006: jitter so Route C never emits a
// fixed, fingerprintable cadence). Each Next stays within
// [interval*(1-jitterBP/10000), interval*(1+jitterBP/10000)].
type jitteredSchedule struct {
	interval time.Duration
	jitterBP int
	rng      *rand.Rand
}

// NewJitteredSchedule builds a jittered periodic schedule for a tier cadence.
// jitterBP is the spread in basis points (e.g. 2000 = ±20%).
func NewJitteredSchedule(interval time.Duration, jitterBP int, rng *rand.Rand) river.PeriodicSchedule {
	if rng == nil {
		rng = rand.New(rand.NewSource(time.Now().UnixNano()))
	}
	return &jitteredSchedule{interval: interval, jitterBP: jitterBP, rng: rng}
}

// Next returns the next run time: now + jittered interval.
func (s *jitteredSchedule) Next(t time.Time) time.Time {
	return t.Add(Jitter(s.interval, s.jitterBP, s.rng))
}

// PeriodicJobs builds one jittered periodic job per cadence tier (PRD §10.1:
// priority 60m, standard 6h, background 24h). RunOnStart is false so a leader
// election does not stampede an immediate full sweep. The returned jobs are
// registered on the River client; the TierSweepWorker executes them.
func PeriodicJobs(cfg Config, rng *rand.Rand) []*river.PeriodicJob {
	tiers := []observation.Tier{observation.TierPriority, observation.TierStandard, observation.TierBackground}
	jobs := make([]*river.PeriodicJob, 0, len(tiers))
	for _, tier := range tiers {
		cadence, _ := TierCadence(tier)
		tierName := string(tier)
		job := river.NewPeriodicJob(
			NewJitteredSchedule(cadence, cfg.JitterBP, rng),
			func() (river.JobArgs, *river.InsertOpts) {
				return TierSweepArgs{Tier: tierName}, nil
			},
			&river.PeriodicJobOpts{ID: "routec_tier_" + tierName, RunOnStart: false},
		)
		jobs = append(jobs, job)
	}
	return jobs
}
