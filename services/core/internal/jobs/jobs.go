// Package jobs is the platform job pipeline: a thin, domain-agnostic wrapper
// over River (PRD §19.3 "Jobs: River, transactionally enqueued from Go").
//
// Two invariants this package exists to hold:
//
//   - Enqueue is transactional. Callers insert jobs with EnqueueTx inside the
//     same pgx transaction that writes their business rows, so a job never
//     becomes visible for a change that rolled back, and a committed change
//     never loses its follow-up job. Non-transactional Insert is intentionally
//     not surfaced here.
//   - River's own schema is applied through Migrate, mirroring `river
//     migrate-up` in task db:reset, so tests and the binary share one path.
//
// Business logic inside a given job belongs to the domain that owns it; this
// package owns the queue, its registration, migration, and observability seam.
package jobs

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
	"github.com/riverqueue/river/rivertype"
)

// Client is the platform River client bound to the pgx transaction type. It is
// the single handle callers use to enqueue work and that main() starts/stops.
type Client = river.Client[pgx.Tx]

// NewWorkers builds the worker registry for the core binary. Every worker the
// platform runs is registered here; domain steps add their workers alongside
// the heartbeat as they land.
func NewWorkers(logger *slog.Logger) (*river.Workers, error) {
	workers := river.NewWorkers()
	if err := river.AddWorkerSafely(workers, &HeartbeatWorker{logger: logger}); err != nil {
		return nil, fmt.Errorf("jobs: register heartbeat worker: %w", err)
	}
	return workers, nil
}

// NewClient constructs the River client over a pgx pool with the default queue
// enabled. A nil workers registry yields an insert-only client (no queues), for
// callers that enqueue but do not process.
func NewClient(pool *pgxpool.Pool, workers *river.Workers, logger *slog.Logger) (*Client, error) {
	cfg := &river.Config{Logger: logger}
	if workers != nil {
		cfg.Workers = workers
		cfg.Queues = map[string]river.QueueConfig{
			river.QueueDefault: {MaxWorkers: 5},
		}
	}
	client, err := river.NewClient(riverpgxv5.New(pool), cfg)
	if err != nil {
		return nil, fmt.Errorf("jobs: new river client: %w", err)
	}
	return client, nil
}

// EnqueueTx inserts a job inside the caller's transaction. The job is only
// dequeued once tx commits; a rollback discards it atomically with the rest of
// the transaction's writes.
func EnqueueTx(ctx context.Context, client *Client, tx pgx.Tx, args river.JobArgs) (*rivertype.JobInsertResult, error) {
	res, err := client.InsertTx(ctx, tx, args, nil)
	if err != nil {
		return nil, fmt.Errorf("jobs: transactional enqueue %q: %w", args.Kind(), err)
	}
	return res, nil
}

// Migrate applies River's migration set to the target database. It is
// idempotent and mirrors `river migrate-up` run by task db:reset, so tests can
// self-provision the river_* tables without depending on task ordering.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	migrator, err := rivermigrate.New(riverpgxv5.New(pool), nil)
	if err != nil {
		return fmt.Errorf("jobs: new river migrator: %w", err)
	}
	if _, err := migrator.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		return fmt.Errorf("jobs: apply river migrations: %w", err)
	}
	return nil
}
