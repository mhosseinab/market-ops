package jobs

import (
	"context"
	"log/slog"
	"time"

	"github.com/riverqueue/river"
)

// HeartbeatArgs is a no-op liveness job. It proves the enqueue → dequeue → work
// seam end to end and gives the pipeline a trivially-schedulable unit before any
// domain job exists. Fields are JSON-safe business data only (plan §4.8).
type HeartbeatArgs struct {
	// EnqueuedAt is when the job was created, carried through so a worker can
	// log observed queue latency.
	EnqueuedAt time.Time `json:"enqueued_at"`
}

// Kind is River's stable job identifier; it is part of the persisted row and
// must never change once shipped.
func (HeartbeatArgs) Kind() string { return "heartbeat" }

// HeartbeatWorker executes HeartbeatArgs. It does no work beyond emitting an
// observability record — its purpose is to confirm the pipeline is alive.
type HeartbeatWorker struct {
	river.WorkerDefaults[HeartbeatArgs]
	logger *slog.Logger
}

// Work satisfies river.Worker. It always succeeds; the heartbeat has no failure
// mode of its own.
func (w *HeartbeatWorker) Work(ctx context.Context, job *river.Job[HeartbeatArgs]) error {
	if w.logger != nil {
		w.logger.InfoContext(ctx, "heartbeat job worked",
			"job_id", job.ID,
			"enqueued_at", job.Args.EnqueuedAt,
			"latency", time.Since(job.Args.EnqueuedAt),
		)
	}
	return nil
}
