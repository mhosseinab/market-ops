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

// --- Durable identity-reopen dispatch (issue #49, S14 CAT-002 / §16) ------------
//
// When a Confirmed Market Product Identity is reopened, a durable mapping_reopened
// intent is enqueued in the SAME transaction as the guarded state transition, the
// append-only audit row, and the append-only recommendation_invalidation_events row
// (see identity.Service.Reopen + identity.JobReopenDispatcher). The River job row IS
// the durable intent/outbox record: it commits atomically with the reopen, survives a
// client/process crash between commit and any in-process callback, and is claimed
// idempotently by a worker that drives ExpireDependentForVariant exactly-once-
// effectively. Delivery therefore no longer depends on a fire-and-forget post-commit
// sink — the defect issue #49 closes (a committed reopen could be permanently lost).

// MappingReopenedArgs is the durable reopen-invalidation intent. It carries ONLY the
// JSON-safe business data from identity.MappingReopenedEvent (plan §4.8) so the worker
// and telemetry can reconstruct the event from the job alone. DedupKey is the
// uniqueness key (river:"unique"): at most ONE live intent exists per reopen
// (identity, reason, version), so a duplicate/retried enqueue collapses to one row
// (event-dedup, §4.6) — mirroring the append-only event row's own dedup key.
type MappingReopenedArgs struct {
	EventID    uuid.UUID `json:"event_id"`
	AccountID  uuid.UUID `json:"account_id"`
	VariantID  uuid.UUID `json:"variant_id"`
	IdentityID uuid.UUID `json:"identity_id"`
	Reason     string    `json:"reason"`
	DedupKey   string    `json:"dedup_key" river:"unique"`
}

// Kind is River's stable job identifier; never change once shipped.
func (MappingReopenedArgs) Kind() string { return "mapping_reopened" }

// InsertOpts enforces uniqueness by the river:"unique" DedupKey, so a duplicate
// enqueue for the same reopen is deduplicated to one durable intent (idempotency,
// §4.6). This is defence-in-depth behind the append-only event row's own dedup key.
func (MappingReopenedArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{UniqueOpts: river.UniqueOpts{ByArgs: true}}
}

// MappingReopenedFunc drives the durable reopen-invalidation processing for one
// reopened mapping, exactly-once-effectively. It is injected (like the other runners)
// so the jobs package depends on no domain package (no import cycle); main wires it to
// the idempotent consumer (recommendation.ExpireDependentForVariant via a
// ReopenExpirer-style adapter, plus the routec target retirer). Returning a non-nil
// error makes River retry with backoff (restart-safe).
type MappingReopenedFunc func(ctx context.Context, args MappingReopenedArgs) error

// errNoReopenRunner is returned when no reopen consumer is wired. It fails CLOSED
// (River retries) rather than silently completing — a durable reopen intent is never
// lost to a missing consumer. Production always wires the runner (cmd/core/main.go).
var errNoReopenRunner = errors.New("jobs: no mapping-reopened runner wired; refusing to complete intent (fail closed)")

// MappingReopenedWorker claims a durable reopen intent and runs the injected consumer.
// River guarantees at-least-once delivery and durable retry across restarts; the
// injected consumer (ExpireDependentForVariant) is idempotent, so the effect is
// exactly-once (a dependent card is invalidated at most once; a redelivery is a no-op).
type MappingReopenedWorker struct {
	river.WorkerDefaults[MappingReopenedArgs]
	run    MappingReopenedFunc
	logger *slog.Logger
	tel    *reopenDispatchTelemetry
}

// NewMappingReopenedWorker builds the worker over the injected reopen consumer.
func NewMappingReopenedWorker(run MappingReopenedFunc, logger *slog.Logger) *MappingReopenedWorker {
	return &MappingReopenedWorker{run: run, logger: logger, tel: newReopenDispatchTelemetry(logger)}
}

// Work claims and processes one durable reopen intent. It observes the claim, runs the
// injected consumer, and observes the result: success (completed), snooze (park), or
// failure (retryable, terminal on the final attempt). A nil runner fails closed
// (retry) so the durable intent is never silently dropped.
func (w *MappingReopenedWorker) Work(ctx context.Context, job *river.Job[MappingReopenedArgs]) error {
	ctx, span := w.tel.claimed(ctx, job.Args, job.ID, job.Attempt)
	defer span.End()

	if w.run == nil {
		w.tel.failed(ctx, job.Args, job.ID, job.Attempt, job.MaxAttempts, errNoReopenRunner)
		return errNoReopenRunner
	}

	err := w.run(ctx, job.Args)
	if err == nil {
		w.tel.completed(ctx, job.Args, job.ID)
		return nil
	}
	var snooze *rivertype.JobSnoozeError
	if errors.As(err, &snooze) {
		w.tel.snoozed(ctx, job.Args, job.ID)
		return err
	}
	w.tel.failed(ctx, job.Args, job.ID, job.Attempt, job.MaxAttempts, err)
	return err
}

// EnqueueMappingReopenedTx enqueues a durable reopen intent inside the caller's
// transaction (transactional enqueue, jobs pkg invariant): the intent becomes visible
// only if the reopen commit lands, and a rollback discards it atomically. It emits the
// intent-dispatched observability signal. A unique-dedup skip is reported, never an
// error.
func EnqueueMappingReopenedTx(ctx context.Context, client *Client, tx pgx.Tx, args MappingReopenedArgs) (*rivertype.JobInsertResult, error) {
	res, err := client.InsertTx(ctx, tx, args, nil)
	if err != nil {
		return nil, fmt.Errorf("jobs: enqueue mapping_reopened intent: %w", err)
	}
	reopenDispatchTel.dispatched(ctx, args, res.Job.ID, res.UniqueSkippedAsDuplicate)
	return res, nil
}
