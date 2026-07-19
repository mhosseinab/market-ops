package jobs

import (
	"context"
	"log/slog"

	"github.com/riverqueue/river"
)

// --- Recommendation production job (PRC-001) -------------------------------------

// RecommendationProduceArgs schedules a recommendation production pass: the runtime
// producer consumes eligible open|updated market events, resolves their authoritative
// margin/policy/evidence inputs, assembles the PRC-001 recommendation, persists its
// version, and mints the Draft approval card when approvable. Its body is idempotent
// per (event, evidence version), so the periodic re-run never double-produces.
type RecommendationProduceArgs struct{}

// Kind is the stable River job identifier.
func (RecommendationProduceArgs) Kind() string { return "recommendation_produce" }

// RecommendationProduceWorker runs the injected production pass. Like the other domain
// workers, the runner is a RunOnceFunc so this package depends on no domain package
// (no import cycle).
type RecommendationProduceWorker struct {
	river.WorkerDefaults[RecommendationProduceArgs]
	run    RunOnceFunc
	logger *slog.Logger
}

// NewRecommendationProduceWorker builds the worker over a production pass.
func NewRecommendationProduceWorker(run RunOnceFunc, logger *slog.Logger) *RecommendationProduceWorker {
	return &RecommendationProduceWorker{run: run, logger: logger}
}

// Work runs one pass; a nil runner is a no-op (fail closed, never a panic — no producer
// wired means no recommendations, not a crash). The count is the number of
// recommendations produced/persisted this pass; an error is returned so River retries
// (idempotent, so a retry cannot double-produce).
func (w *RecommendationProduceWorker) Work(ctx context.Context, job *river.Job[RecommendationProduceArgs]) error {
	if w.run == nil {
		return nil
	}
	n, err := w.run(ctx)
	if w.logger != nil {
		w.logger.InfoContext(ctx, "recommendation production pass", "job_id", job.ID, "persisted", n, "error", errText(err))
	}
	return err
}
