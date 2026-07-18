package jobs

import (
	"context"
	"log/slog"

	"github.com/riverqueue/river"
)

// --- Daily briefing job (CHAT-010 / §6.8) ---------------------------------------

// BriefingGenerateArgs schedules the once-per-business-day briefing fan-out. The
// job body iterates accounts and generates each briefing FROM the Today ranking;
// generation is idempotent per business day, so re-running the periodic job never
// produces a duplicate briefing.
type BriefingGenerateArgs struct{}

// Kind is the stable River job identifier.
func (BriefingGenerateArgs) Kind() string { return "briefing_generate" }

// BriefingWorker runs the injected briefing-generation pass.
type BriefingWorker struct {
	river.WorkerDefaults[BriefingGenerateArgs]
	run    RunOnceFunc
	logger *slog.Logger
}

// NewBriefingWorker builds the worker over a generation pass.
func NewBriefingWorker(run RunOnceFunc, logger *slog.Logger) *BriefingWorker {
	return &BriefingWorker{run: run, logger: logger}
}

// Work runs one pass; a nil runner is a no-op (fail closed, never a panic). The
// count is the number of briefings NEWLY created this pass (0 on an idempotent
// same-day re-run) — logged, never silent.
func (w *BriefingWorker) Work(ctx context.Context, job *river.Job[BriefingGenerateArgs]) error {
	if w.run == nil {
		return nil
	}
	n, err := w.run(ctx)
	if w.logger != nil {
		w.logger.InfoContext(ctx, "daily briefing pass", "job_id", job.ID, "created", n, "error", errText(err))
	}
	return err
}
