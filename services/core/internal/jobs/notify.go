package jobs

import (
	"context"
	"log/slog"

	"github.com/riverqueue/river"
)

// --- Daily email digest job (NOT-001 / §6.8) -----------------------------------

// DigestGenerateArgs schedules the once-per-business-day email digest fan-out. The
// job body iterates accounts and sends each account's digest of the day's batched
// (non-bypass) notifications. Generation is idempotent per business day (unique
// account+business_day), so a re-run of the periodic job never sends a duplicate
// digest. Execution/safety failures bypass this job entirely (delivered immediately).
type DigestGenerateArgs struct{}

// Kind is the stable River job identifier.
func (DigestGenerateArgs) Kind() string { return "notification_digest_generate" }

// DigestWorker runs the injected digest-generation pass.
type DigestWorker struct {
	river.WorkerDefaults[DigestGenerateArgs]
	run    RunOnceFunc
	logger *slog.Logger
}

// NewDigestWorker builds the worker over a digest-send pass.
func NewDigestWorker(run RunOnceFunc, logger *slog.Logger) *DigestWorker {
	return &DigestWorker{run: run, logger: logger}
}

// Work runs one pass; a nil runner is a no-op (fail closed, never a panic). The
// count is the number of digests SENT this pass (0 on an idempotent same-day
// re-run or an empty day) — logged, never silent.
func (w *DigestWorker) Work(ctx context.Context, job *river.Job[DigestGenerateArgs]) error {
	if w.run == nil {
		return nil
	}
	n, err := w.run(ctx)
	if w.logger != nil {
		w.logger.InfoContext(ctx, "daily digest pass", "job_id", job.ID, "sent", n, "error", errText(err))
	}
	return err
}
