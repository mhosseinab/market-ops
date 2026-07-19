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

// --- Durable notification-delivery intent (issue #110, NOT-001) -----------------
//
// A production lifecycle transition (a market event opening, an execution failure,
// a safety/gate failure) enqueues ONE notification_deliver intent in the SAME
// transaction that commits the transition + its append-only history/audit rows (see
// notify.JobDispatcher, wired into event.Service and execution.Service). The River
// job row IS the durable intent/outbox record: it commits atomically with the
// owning transition, so a rolled-back transition enqueues nothing and a committed
// one always carries its delivery intent (survives a process crash / restart). The
// worker drives the idempotent notify.Store.Deliver, whose permanent (account,
// dedup_key) uniqueness is the exactly-once-effectively authority — a duplicate or
// retried job creates NO duplicate product event (NOT-001). This package stays
// domain-agnostic: the delivery itself is an injected NotificationDeliverFunc, so
// jobs imports no notify/event/execution package (no import cycle).

// NotificationDeliverArgs is the durable delivery intent. It carries only JSON-safe
// business data (plan §4.8): the SERVER-derived event identity + dedup key, the
// closed catalog keys, the category/severity, and the named slots — never rendered
// copy (LOC-001). The store re-validates the closed message-catalog contract and
// derives bypass_digest from the category, so a smuggled bad shape fails closed at
// delivery.
type NotificationDeliverArgs struct {
	Account  uuid.UUID         `json:"account"`
	EventID  uuid.UUID         `json:"event_id"`
	DedupKey string            `json:"dedup_key"`
	Category string            `json:"category"`
	Severity string            `json:"severity"`
	TitleKey string            `json:"title_key"`
	BodyKey  string            `json:"body_key"`
	Params   map[string]string `json:"params,omitempty"`
}

// Kind is River's stable job identifier; never change once shipped.
func (NotificationDeliverArgs) Kind() string { return "notification_deliver" }

// NotificationDeliverFunc drives the idempotent delivery of one intent
// (notify.Store.Deliver), exactly-once-effectively. It is injected (like
// RunOnceFunc / ExecuteApprovedFunc) so the jobs package depends on no domain
// package; main wires it to the notification store. Returning a non-nil error makes
// River retry with backoff (restart-safe) — a committed intent is never lost.
type NotificationDeliverFunc func(ctx context.Context, args NotificationDeliverArgs) error

// errNoNotifyRunner is returned when no delivery runner is wired. It fails CLOSED
// (River retries) rather than silently completing — a durable delivery intent is
// never dropped by a missing consumer. Production always wires the runner.
var errNoNotifyRunner = errors.New("jobs: no notification-deliver runner wired; refusing to complete intent (fail closed)")

// NotificationDeliverWorker claims a durable delivery intent and runs the injected
// delivery. River guarantees at-least-once delivery + durable retry; the injected
// runner (notify.Store.Deliver) is idempotent on (account, dedup_key), so the effect
// is exactly-once (one product event on both surfaces, NOT-001).
type NotificationDeliverWorker struct {
	river.WorkerDefaults[NotificationDeliverArgs]
	run    NotificationDeliverFunc
	logger *slog.Logger
}

// NewNotificationDeliverWorker builds the worker over the injected delivery runner.
func NewNotificationDeliverWorker(run NotificationDeliverFunc, logger *slog.Logger) *NotificationDeliverWorker {
	return &NotificationDeliverWorker{run: run, logger: logger}
}

// Work claims and delivers one durable notification intent. A nil runner fails
// closed (retry) so the intent is never silently dropped; a delivery error is
// surfaced for River retry. The boundary is always logged (never silent).
func (w *NotificationDeliverWorker) Work(ctx context.Context, job *river.Job[NotificationDeliverArgs]) error {
	if w.run == nil {
		if w.logger != nil {
			w.logger.ErrorContext(ctx, "notification deliver: no runner wired (fail closed)",
				"job_id", job.ID, "dedup_key", job.Args.DedupKey, "category", job.Args.Category)
		}
		return errNoNotifyRunner
	}
	err := w.run(ctx, job.Args)
	if w.logger != nil {
		w.logger.InfoContext(ctx, "notification deliver pass",
			"job_id", job.ID, "dedup_key", job.Args.DedupKey, "category", job.Args.Category,
			"error", errText(err))
	}
	return err
}

// EnqueueNotificationDeliverTx enqueues a durable delivery intent inside the
// caller's transaction (transactional enqueue, jobs pkg invariant): the intent
// becomes visible only if the owning transition commits, and a rollback discards it
// atomically. Idempotency is the store's permanent (account, dedup_key) uniqueness,
// so no River-level unique window is relied upon.
func EnqueueNotificationDeliverTx(ctx context.Context, client *Client, tx pgx.Tx, args NotificationDeliverArgs) (*rivertype.JobInsertResult, error) {
	res, err := client.InsertTx(ctx, tx, args, nil)
	if err != nil {
		return nil, fmt.Errorf("jobs: enqueue notification_deliver intent: %w", err)
	}
	return res, nil
}
