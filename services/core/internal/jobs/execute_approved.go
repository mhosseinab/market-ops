package jobs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

// --- Durable execution-intent dispatch (issue #92, S18 EXE-* / AUD-001) ---------
//
// When an individual confirmation commits a card to Approved, a durable
// execute_approved intent is enqueued in the SAME transaction (see
// recommendation.ConfirmIndividual + recommendation.JobDispatcher). The River job
// row IS the durable intent/outbox record: it commits atomically with the Approved
// state, survives a client/process crash, and is claimed idempotently by a worker
// that drives execution/recommend-only processing exactly-once-effectively. An
// acknowledged confirmation therefore never depends on a second client request.

// ExecuteApprovedArgs is the durable execution intent for a confirmed card. It
// carries the approval-control identity (JSON-safe business data only, plan §4.8)
// so the worker and telemetry can reconstruct the control from the job alone
// (AUD-001). CardID is the uniqueness key (river:"unique"): at most ONE live intent
// exists per confirmed card, so a duplicate/retried enqueue collapses to one row.
type ExecuteApprovedArgs struct {
	CardID           uuid.UUID `json:"card_id" river:"unique"`
	ActionID         uuid.UUID `json:"action_id"`
	ParameterVersion int64     `json:"parameter_version"`
	ContextVersion   int64     `json:"context_version"`
}

// Kind is River's stable job identifier; never change once shipped.
func (ExecuteApprovedArgs) Kind() string { return "execute_approved" }

// InsertOpts enforces uniqueness by encoded args (the river:"unique" CardID key),
// so a duplicate enqueue for the same card is deduplicated to one durable intent
// (idempotency, §4.6). This is defence-in-depth behind the approval state machine
// (a second confirm finds no live control and never re-enqueues).
func (ExecuteApprovedArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{UniqueOpts: river.UniqueOpts{ByArgs: true}}
}

// ExecuteApprovedFunc drives the durable execution/recommend-only processing for a
// confirmed card, exactly-once-effectively. It is injected (like RunOnceFunc) so
// the jobs package depends on no domain package (no import cycle); main wires it to
// execution.Execute. Returning a non-nil error makes River retry with backoff
// (restart-safe); returning river.JobSnooze parks the intent WITHOUT burning an
// attempt — the dark posture (writes OFF) before S35 wires the live resolver.
type ExecuteApprovedFunc func(ctx context.Context, args ExecuteApprovedArgs) error

// errNoExecuteRunner is returned when no execution runner is wired. It fails CLOSED
// (River retries) rather than silently completing — a durable intent is never lost
// to a missing consumer. Production always wires the runner (cmd/core/main.go).
var errNoExecuteRunner = errors.New("jobs: no execute-approved runner wired; refusing to complete intent (fail closed)")

// ExecuteApprovedWorker claims a durable execution intent and runs the injected
// execution processing. River guarantees at-least-once delivery and durable retry
// across restarts; the injected runner (execution.Execute) is idempotent, so the
// effect is exactly-once (one execution record / at most one external write).
type ExecuteApprovedWorker struct {
	river.WorkerDefaults[ExecuteApprovedArgs]
	run    ExecuteApprovedFunc
	logger *slog.Logger
	tel    *dispatchTelemetry
}

// NewExecuteApprovedWorker builds the worker over the injected execution runner.
func NewExecuteApprovedWorker(run ExecuteApprovedFunc, logger *slog.Logger) *ExecuteApprovedWorker {
	return &ExecuteApprovedWorker{run: run, logger: logger, tel: newDispatchTelemetry(logger)}
}

// Work claims and processes one durable execution intent. It observes the claim,
// runs the injected processor, and observes the result: a success (completed), a
// snooze (dark park, not counted as failure), or a failure (retryable, marked
// terminal-failure on the final attempt so an exhausted intent is observable). A
// nil runner fails closed (retry) so the durable intent is never silently dropped.
func (w *ExecuteApprovedWorker) Work(ctx context.Context, job *river.Job[ExecuteApprovedArgs]) error {
	ctx, span := w.tel.claimed(ctx, job.Args, job.ID, job.Attempt)
	defer span.End()

	if w.run == nil {
		w.tel.failed(ctx, job.Args, job.ID, job.Attempt, job.MaxAttempts, errNoExecuteRunner)
		return errNoExecuteRunner
	}

	err := w.run(ctx, job.Args)
	if err == nil {
		w.tel.completed(ctx, job.Args, job.ID)
		return nil
	}
	// A JobSnooze is a durable park (dark, pre-S35), not a failure: surface it
	// verbatim so River reschedules without consuming a retry attempt.
	var snooze *rivertype.JobSnoozeError
	if errors.As(err, &snooze) {
		w.tel.snoozed(ctx, job.Args, job.ID)
		return err
	}
	w.tel.failed(ctx, job.Args, job.ID, job.Attempt, job.MaxAttempts, err)
	return err
}

// EnqueueExecuteApprovedTx enqueues a durable execution intent inside the caller's
// transaction (transactional enqueue, jobs pkg invariant): the intent becomes
// visible only if the Approved commit lands, and a rollback discards it atomically.
// It emits the intent-dispatched observability signal (backlog is dispatched minus
// completed). A unique-dedup skip is reported, never an error.
func EnqueueExecuteApprovedTx(ctx context.Context, client *Client, tx pgx.Tx, args ExecuteApprovedArgs) (*rivertype.JobInsertResult, error) {
	res, err := client.InsertTx(ctx, tx, args, nil)
	if err != nil {
		return nil, fmt.Errorf("jobs: enqueue execute_approved intent: %w", err)
	}
	dispatchTel.dispatched(ctx, args, res.Job.ID, res.UniqueSkippedAsDuplicate)
	return res, nil
}
