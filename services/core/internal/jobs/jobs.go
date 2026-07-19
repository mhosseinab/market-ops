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
	"time"

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

// ExecutionRunners bundles the periodic execution-plane passes the platform
// schedules (EXE-005 recommend-only matching and OUT-001 outcome close). Both are
// injected as RunOnceFuncs so the jobs package depends on no domain package. A nil
// runner registers a no-op worker (fail closed).
type ExecutionRunners struct {
	RecommendOnlyMatch RunOnceFunc
	OutcomeClose       RunOnceFunc
	// BriefingGenerate is the CHAT-010 daily-briefing fan-out (once per business
	// day per account, generated from the Today ranking). A nil runner registers a
	// no-op worker (fail closed).
	BriefingGenerate RunOnceFunc
	// DigestGenerate is the NOT-001 daily email-digest fan-out (once per business
	// day per account, batching the day's non-bypass notifications). A nil runner
	// registers a no-op worker (fail closed).
	DigestGenerate RunOnceFunc
	// MarketEventProduce is the EVT-001..005 runtime producer pass: it turns
	// committed observation/catalog/margin transitions into detector inputs and
	// records their candidates idempotently, so a running core actually produces
	// market events (and the Today feed is non-empty in real operation). A nil
	// runner registers a no-op worker (fail closed — no producer, no events).
	MarketEventProduce RunOnceFunc
	// RecommendationProduce is the PRC-001 runtime producer pass: it consumes eligible
	// open|updated market events, resolves their authoritative inputs, assembles the
	// recommendation, persists its version, and mints the Draft approval card when
	// approvable — so a running core reaches the S17 approval journey without direct
	// database/test seeding. A nil runner registers a no-op worker (fail closed — no
	// producer, no recommendations). Every pass is idempotent per (event, evidence
	// version), so the periodic re-run never double-produces.
	RecommendationProduce RunOnceFunc
	// ExecuteApproved is the durable execution-intent consumer (issue #92, S18):
	// it drives execution/recommend-only processing for a confirmed card that was
	// enqueued transactionally at the Approved commit. It is event-driven (one job
	// per confirmation), NOT periodic. A nil runner fails CLOSED (the worker retries
	// rather than silently dropping the durable intent); production always wires it.
	ExecuteApproved ExecuteApprovedFunc
	// MappingReopened is the durable identity-reopen consumer (issue #49, S14): it
	// drives ExpireDependentForVariant (+ observation-target retirement) for a
	// mapping reopened transactionally with its append-only event row. It is
	// event-driven (one job per reopen), NOT periodic. A nil runner fails CLOSED (the
	// worker retries rather than silently dropping the durable intent); production
	// always wires it, so a committed reopen is never permanently lost.
	MappingReopened MappingReopenedFunc
}

// NewWorkers builds the worker registry for the core binary. Every worker the
// platform runs is registered here: the heartbeat plus the periodic execution-plane
// passes (recommend-only matcher, outcome closer).
func NewWorkers(logger *slog.Logger, runners ExecutionRunners) (*river.Workers, error) {
	workers := river.NewWorkers()
	if err := river.AddWorkerSafely(workers, &HeartbeatWorker{logger: logger}); err != nil {
		return nil, fmt.Errorf("jobs: register heartbeat worker: %w", err)
	}
	if err := river.AddWorkerSafely(workers, NewRecommendOnlyMatchWorker(runners.RecommendOnlyMatch, logger)); err != nil {
		return nil, fmt.Errorf("jobs: register recommend-only worker: %w", err)
	}
	if err := river.AddWorkerSafely(workers, NewOutcomeCloseWorker(runners.OutcomeClose, logger)); err != nil {
		return nil, fmt.Errorf("jobs: register outcome-close worker: %w", err)
	}
	if err := river.AddWorkerSafely(workers, NewBriefingWorker(runners.BriefingGenerate, logger)); err != nil {
		return nil, fmt.Errorf("jobs: register briefing worker: %w", err)
	}
	if err := river.AddWorkerSafely(workers, NewDigestWorker(runners.DigestGenerate, logger)); err != nil {
		return nil, fmt.Errorf("jobs: register digest worker: %w", err)
	}
	if err := river.AddWorkerSafely(workers, NewMarketEventProduceWorker(runners.MarketEventProduce, logger)); err != nil {
		return nil, fmt.Errorf("jobs: register market-event producer worker: %w", err)
	}
	if err := river.AddWorkerSafely(workers, NewRecommendationProduceWorker(runners.RecommendationProduce, logger)); err != nil {
		return nil, fmt.Errorf("jobs: register recommendation producer worker: %w", err)
	}
	if err := river.AddWorkerSafely(workers, NewExecuteApprovedWorker(runners.ExecuteApproved, logger)); err != nil {
		return nil, fmt.Errorf("jobs: register execute-approved worker: %w", err)
	}
	if err := river.AddWorkerSafely(workers, NewMappingReopenedWorker(runners.MappingReopened, logger)); err != nil {
		return nil, fmt.Errorf("jobs: register mapping-reopened worker: %w", err)
	}
	return workers, nil
}

// periodicJobs schedules the execution-plane passes. Recommend-only matching runs
// often (a 24h window, so minute-granularity is ample); outcome close runs hourly
// (seven-day windows). RunOnStart makes them fire once at boot too.
func periodicJobs() []*river.PeriodicJob {
	return []*river.PeriodicJob{
		river.NewPeriodicJob(
			river.PeriodicInterval(5*time.Minute),
			func() (river.JobArgs, *river.InsertOpts) { return RecommendOnlyMatchArgs{}, nil },
			&river.PeriodicJobOpts{RunOnStart: true},
		),
		river.NewPeriodicJob(
			river.PeriodicInterval(time.Hour),
			func() (river.JobArgs, *river.InsertOpts) { return OutcomeCloseArgs{}, nil },
			&river.PeriodicJobOpts{RunOnStart: true},
		),
		// Daily briefing (CHAT-010). Runs hourly with RunOnStart; generation is
		// idempotent per business day (unique account+business_day), so the first
		// run each UTC day creates the briefing and later runs are no-ops — "once
		// per business day per account" without depending on a precise wake time.
		river.NewPeriodicJob(
			river.PeriodicInterval(time.Hour),
			func() (river.JobArgs, *river.InsertOpts) { return BriefingGenerateArgs{}, nil },
			&river.PeriodicJobOpts{RunOnStart: true},
		),
		// Daily email digest (NOT-001). Runs hourly with RunOnStart; sending is
		// idempotent per business day (unique account+business_day), so the first
		// run each UTC day sends the digest and later runs are no-ops — "once per
		// business day per account" without depending on a precise wake time.
		// Execution/safety failures bypass this job (delivered immediately).
		river.NewPeriodicJob(
			river.PeriodicInterval(time.Hour),
			func() (river.JobArgs, *river.InsertOpts) { return DigestGenerateArgs{}, nil },
			&river.PeriodicJobOpts{RunOnStart: true},
		),
		// Market-event production (EVT-001..005). Runs often (a 5-minute cadence,
		// well inside observation freshness windows) with RunOnStart so a booted
		// core immediately turns committed transitions into events. Every pass is
		// idempotent (RecordFor dedup), so frequent re-runs never duplicate a Today
		// item; an errored pass is retried by River.
		river.NewPeriodicJob(
			river.PeriodicInterval(5*time.Minute),
			func() (river.JobArgs, *river.InsertOpts) { return MarketEventProduceArgs{}, nil },
			&river.PeriodicJobOpts{RunOnStart: true},
		),
		// Recommendation production (PRC-001). Runs on the same 5-minute cadence, just
		// after market-event production, with RunOnStart so a booted core turns eligible
		// events into recommendations/cards immediately. Every pass is idempotent per
		// (event, evidence version), so frequent re-runs never duplicate a version; an
		// errored pass is retried by River.
		river.NewPeriodicJob(
			river.PeriodicInterval(5*time.Minute),
			func() (river.JobArgs, *river.InsertOpts) { return RecommendationProduceArgs{}, nil },
			&river.PeriodicJobOpts{RunOnStart: true},
		),
	}
}

// NewClient constructs the River client over a pgx pool with the default queue
// enabled. A nil workers registry yields an insert-only client (no queues), for
// callers that enqueue but do not process. When workers are present the periodic
// execution-plane jobs are scheduled.
func NewClient(pool *pgxpool.Pool, workers *river.Workers, logger *slog.Logger) (*Client, error) {
	cfg := &river.Config{Logger: logger}
	if workers != nil {
		cfg.Workers = workers
		cfg.Queues = map[string]river.QueueConfig{
			river.QueueDefault: {MaxWorkers: 5},
		}
		cfg.PeriodicJobs = periodicJobs()
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
