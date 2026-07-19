package routec

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/riverqueue/river"

	"github.com/mhosseinab/market-ops/services/core/internal/observation"
)

// TierSweepWorker executes a periodic TierSweepArgs job by running the Observer's
// guarded sweep for that tier. River owns the schedule (jittered per tier); the
// worker owns none of the throttling policy — it delegates to the Observer, which
// applies concurrency, budget, breaker, kill switch, and drift guards.
type TierSweepWorker struct {
	river.WorkerDefaults[TierSweepArgs]
	observer *Observer
	logger   *slog.Logger
}

// NewTierSweepWorker builds the worker over an Observer.
func NewTierSweepWorker(observer *Observer, logger *slog.Logger) *TierSweepWorker {
	return &TierSweepWorker{observer: observer, logger: logger}
}

// Work runs the sweep for the job's tier. An unknown tier string is rejected
// (fail closed) rather than defaulting to a cadence the operator did not intend.
func (w *TierSweepWorker) Work(ctx context.Context, job *river.Job[TierSweepArgs]) error {
	tier := observation.Tier(job.Args.Tier)
	switch tier {
	case observation.TierPriority, observation.TierStandard, observation.TierBackground:
	default:
		return fmt.Errorf("routec: unknown sweep tier %q", job.Args.Tier)
	}
	summary, err := w.observer.RunSweep(ctx, tier)
	if err != nil {
		return err
	}
	if w.logger != nil {
		w.logger.InfoContext(ctx, "routec tier sweep complete",
			"tier", summary.Tier,
			"global_stopped", summary.GlobalStopped,
			"observed", summary.Observed,
			"ingested", summary.Ingested,
			"trimmed", summary.Trimmed,
			"skipped_kill", summary.SkippedKill,
			"skipped_breaker", summary.SkippedBreak,
			"skipped_budget", summary.SkippedBudget,
			"downgraded", summary.Downgraded,
			"persisted_downgrades", summary.PersistedDowngrades,
			"errors", summary.Errors,
		)
	}
	return nil
}
