package jobs

import (
	"context"
	"log/slog"

	"github.com/riverqueue/river"
)

// --- Market-event production job (EVT-001..005) ---------------------------------

// MarketEventProduceArgs schedules a market-event production pass: the runtime
// producer consumes observation/catalog/margin transitions, resolves the versioned
// threshold, runs the correct detector, and records candidates idempotently. Its
// body is idempotent (EVT-003 dedup), so the periodic re-run never double-produces.
type MarketEventProduceArgs struct{}

// Kind is the stable River job identifier.
func (MarketEventProduceArgs) Kind() string { return "market_event_produce" }

// MarketEventProduceWorker runs the injected production pass. Like the other
// domain workers, the runner is a RunOnceFunc so this package depends on no domain
// package (no import cycle).
type MarketEventProduceWorker struct {
	river.WorkerDefaults[MarketEventProduceArgs]
	run    RunOnceFunc
	logger *slog.Logger
}

// NewMarketEventProduceWorker builds the worker over a production pass.
func NewMarketEventProduceWorker(run RunOnceFunc, logger *slog.Logger) *MarketEventProduceWorker {
	return &MarketEventProduceWorker{run: run, logger: logger}
}

// Work runs one pass; a nil runner is a no-op (fail closed, never a panic — no
// producer wired means no events, not a crash). The count is the number of events
// produced/updated this pass; an error is returned so River retries (idempotent).
func (w *MarketEventProduceWorker) Work(ctx context.Context, job *river.Job[MarketEventProduceArgs]) error {
	if w.run == nil {
		return nil
	}
	n, err := w.run(ctx)
	if w.logger != nil {
		w.logger.InfoContext(ctx, "market event production pass", "job_id", job.ID, "recorded", n, "error", errText(err))
	}
	return err
}
