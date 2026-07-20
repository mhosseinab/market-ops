package jobs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

// --- Durable urgent-email dispatch (issue #122, NOT-001 bypass + SRE never-shed) --
//
// Execution/safety failures BYPASS the digest AND must select an IMMEDIATE delivery
// channel. When notify.Store.Deliver commits such a failure it inserts a durable
// notification_urgent_outbox row and enqueues ONE urgent-email job in the SAME
// transaction (see EnqueueUrgentEmailTx). The outbox row is the restart-safe record;
// this job DRIVES the send. It has its OWN retry/backoff + dead-letter, kept fully
// SEPARATE from the daily digest job — an urgent email never batches and is never
// shed. Idempotency is the outbox's permanent (notification_id, channel) uniqueness
// plus its delivery_state guard, so an at-least-once retry never duplicates the
// logical email. This package stays domain-agnostic: the send itself is an injected
// UrgentEmailFunc, so jobs imports no notify package (no import cycle).

// UrgentEmailArgs is the durable urgent-email dispatch intent. It carries only
// JSON-safe business data (plan §4.8): the notification identity (the outbox key),
// the SHARED event id, the closed catalog keys, category/severity, and the named
// slots — never rendered copy (LOC-001). The dispatcher re-renders from the closed
// catalog in the account's locale at send time.
type UrgentEmailArgs struct {
	NotificationID uuid.UUID         `json:"notification_id"`
	Account        uuid.UUID         `json:"account"`
	EventID        uuid.UUID         `json:"event_id"`
	Channel        string            `json:"channel"`
	Category       string            `json:"category"`
	Severity       string            `json:"severity"`
	TitleKey       string            `json:"title_key"`
	BodyKey        string            `json:"body_key"`
	Params         map[string]string `json:"params,omitempty"`
}

// Kind is River's stable job identifier; never change once shipped (it would orphan
// in-flight durable urgent-email intents).
func (UrgentEmailArgs) Kind() string { return "notification_urgent_email" }

// UrgentEmailFunc drives the durable, idempotent send of one urgent-email intent
// (notify.UrgentDispatcher.Dispatch). lastAttempt is true on the final River attempt
// so the runner can record the OBSERVABLE dead-letter terminal state (metric +
// structured log + durable outbox row) instead of merely returning a retryable error
// — a permanent failure is never silently dropped and never marks the email
// delivered. It is injected (like the other domain runners) so jobs depends on no
// notify package. A non-nil error makes River retry with backoff (restart-safe).
type UrgentEmailFunc func(ctx context.Context, args UrgentEmailArgs, lastAttempt bool) error

// errNoUrgentEmailRunner is returned when no runner is wired. It fails CLOSED (River
// retries) rather than silently completing — a committed urgent-email intent is never
// dropped by a missing consumer. Production wires the runner whenever a mail sender is
// configured; without a sender the intent parks and retries (never lost).
var errNoUrgentEmailRunner = errors.New("jobs: no urgent-email runner wired; refusing to complete intent (fail closed)")

// ErrUrgentDeadLetterUnpersisted marks the recovery obligation left when the FINAL
// attempt's send failed permanently but the pending → dead_letter state write ITSELF
// failed (issue #122 reopen residual). The terminal signals (dead-letter metric +
// observer) MUST NOT fire for a state that was never persisted — that would let
// monitoring report a durable terminal dead letter that does not exist, with no retry
// anchored to repair it. The dispatcher wraps this sentinel instead of returning the
// send cause, so the worker RE-DRIVES the intent (JobSnooze) rather than discarding it
// on the exhausted attempt; a terminal signal fires only after the durable transition
// actually succeeds. Observability truthfulness + never-shed are never-cut (§4.6).
var ErrUrgentDeadLetterUnpersisted = errors.New("jobs: urgent dead-letter state write failed; re-drive to repair (no terminal signal emitted)")

// UrgentEmailWorker claims a durable urgent-email intent and runs the injected send.
// River guarantees at-least-once delivery + durable retry; the injected runner is
// idempotent on the outbox (notification_id, channel), so the effect is
// exactly-once-effectively (one logical urgent email per failure).
type UrgentEmailWorker struct {
	river.WorkerDefaults[UrgentEmailArgs]
	run    UrgentEmailFunc
	logger *slog.Logger
}

// NewUrgentEmailWorker builds the worker over the injected send runner.
func NewUrgentEmailWorker(run UrgentEmailFunc, logger *slog.Logger) *UrgentEmailWorker {
	return &UrgentEmailWorker{run: run, logger: logger}
}

// Work claims and dispatches one durable urgent-email intent. A nil runner fails
// closed (retry) so the intent is never silently dropped; a send error is surfaced
// for River retry. lastAttempt is derived from River's attempt bookkeeping so the
// runner can dead-letter observably on the final attempt. The boundary is always
// logged (never silent), with technical identifiers only (LOC-001).
func (w *UrgentEmailWorker) Work(ctx context.Context, job *river.Job[UrgentEmailArgs]) error {
	if w.run == nil {
		if w.logger != nil {
			w.logger.ErrorContext(ctx, "urgent email: no runner wired (fail closed)",
				"job_id", job.ID, "notification_id", job.Args.NotificationID, "category", job.Args.Category)
		}
		return errNoUrgentEmailRunner
	}
	// River always populates JobRow in production; guard so a hand-built job in a unit
	// test (no JobRow) never nil-derefs the attempt bookkeeping.
	lastAttempt := job.JobRow != nil && job.Attempt >= job.MaxAttempts
	err := w.run(ctx, job.Args, lastAttempt)
	if w.logger != nil {
		w.logger.InfoContext(ctx, "urgent email dispatch pass",
			"job_id", job.ID, "notification_id", job.Args.NotificationID, "category", job.Args.Category,
			"attempt", job.Attempt, "max_attempts", job.MaxAttempts, "last_attempt", lastAttempt,
			"error", errText(err))
	}
	// The exhausted attempt's send failed permanently BUT the dead-letter state write
	// itself failed (issue #122 reopen residual). Returning the error verbatim would let
	// River DISCARD the intent, dropping the recovery obligation while no terminal signal
	// was ever truthfully emitted. Re-drive via JobSnooze — a bounded park that does NOT
	// consume the exhausted attempt — so the durable pending → dead_letter transition is
	// repaired on a later pass, and only then does the terminal signal fire.
	if errors.Is(err, ErrUrgentDeadLetterUnpersisted) {
		if w.logger != nil {
			w.logger.WarnContext(ctx, "urgent email: dead-letter unpersisted; snoozing to re-drive (recovery anchored)",
				"job_id", job.ID, "notification_id", job.Args.NotificationID, "category", job.Args.Category)
		}
		return river.JobSnooze(urgentDeadLetterRedriveBackoff)
	}
	return err
}

// urgentDeadLetterRedriveBackoff bounds how long a dead-letter re-drive parks before
// re-attempting the durable transition. It is a bounded backpressure signal (CLAUDE.md
// load handling: queues never grow unbounded), long enough to ride out a transient
// outbox/DB blip, short enough that the observable terminal state is not delayed
// materially.
const urgentDeadLetterRedriveBackoff = 30 * time.Second

// EnqueueUrgentEmailTx enqueues a durable urgent-email intent inside the caller's
// transaction (transactional enqueue, jobs pkg invariant): the intent becomes visible
// only if the owning notification commits, and a rollback discards it atomically with
// the notification + its outbox row. Idempotency is the outbox's permanent
// (notification_id, channel) uniqueness, so no River-level unique window is relied
// upon.
func EnqueueUrgentEmailTx(ctx context.Context, client *Client, tx pgx.Tx, args UrgentEmailArgs) (*rivertype.JobInsertResult, error) {
	res, err := client.InsertTx(ctx, tx, args, nil)
	if err != nil {
		return nil, fmt.Errorf("jobs: enqueue notification_urgent_email intent: %w", err)
	}
	return res, nil
}
