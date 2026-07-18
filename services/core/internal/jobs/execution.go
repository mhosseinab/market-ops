package jobs

import (
	"context"
	"log/slog"

	"github.com/riverqueue/river"
)

// RunOnceFunc is a single reconciliation pass returning a count of items advanced.
// Both the recommend-only matcher (EXE-005) and the outcome closer (OUT-001) are
// adapted onto it, so the jobs package depends on no domain package (no import
// cycle) while still driving their real production logic.
type RunOnceFunc func(ctx context.Context) (int, error)

// --- Recommend-only matcher job (EXE-005) ---------------------------------------

// RecommendOnlyMatchArgs schedules a recommend-only matching pass.
type RecommendOnlyMatchArgs struct{}

// Kind is the stable River job identifier.
func (RecommendOnlyMatchArgs) Kind() string { return "recommend_only_match" }

// RecommendOnlyMatchWorker runs the injected matcher pass.
type RecommendOnlyMatchWorker struct {
	river.WorkerDefaults[RecommendOnlyMatchArgs]
	run    RunOnceFunc
	logger *slog.Logger
}

// NewRecommendOnlyMatchWorker builds the worker over a reconciliation pass.
func NewRecommendOnlyMatchWorker(run RunOnceFunc, logger *slog.Logger) *RecommendOnlyMatchWorker {
	return &RecommendOnlyMatchWorker{run: run, logger: logger}
}

// Work runs one pass; a nil runner is a no-op (fail closed, never a panic).
func (w *RecommendOnlyMatchWorker) Work(ctx context.Context, job *river.Job[RecommendOnlyMatchArgs]) error {
	if w.run == nil {
		return nil
	}
	n, err := w.run(ctx)
	if w.logger != nil {
		w.logger.InfoContext(ctx, "recommend-only match pass", "job_id", job.ID, "advanced", n, "error", errText(err))
	}
	return err
}

// --- Outcome close job (OUT-001 / §15.3) ----------------------------------------

// OutcomeCloseArgs schedules an outcome-window close pass.
type OutcomeCloseArgs struct{}

// Kind is the stable River job identifier.
func (OutcomeCloseArgs) Kind() string { return "outcome_close" }

// OutcomeCloseWorker runs the injected close pass.
type OutcomeCloseWorker struct {
	river.WorkerDefaults[OutcomeCloseArgs]
	run    RunOnceFunc
	logger *slog.Logger
}

// NewOutcomeCloseWorker builds the worker over a close pass.
func NewOutcomeCloseWorker(run RunOnceFunc, logger *slog.Logger) *OutcomeCloseWorker {
	return &OutcomeCloseWorker{run: run, logger: logger}
}

// Work runs one pass; a nil runner is a no-op (fail closed).
func (w *OutcomeCloseWorker) Work(ctx context.Context, job *river.Job[OutcomeCloseArgs]) error {
	if w.run == nil {
		return nil
	}
	n, err := w.run(ctx)
	if w.logger != nil {
		w.logger.InfoContext(ctx, "outcome close pass", "job_id", job.ID, "closed", n, "error", errText(err))
	}
	return err
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
