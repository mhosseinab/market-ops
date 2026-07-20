package notify

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/jobs"
)

// Urgent-email delivery for the NOT-001 bypass categories (issue #122). Execution and
// safety failures bypass the digest AND must reach an IMMEDIATE channel (email), not
// merely drop out of the batched channel. The path is a DURABLE outbox:
// notify.Store.Deliver commits a notification_urgent_outbox row + enqueues one
// urgent-email job in the SAME transaction; this dispatcher DRIVES the send with its
// own retry + dead-letter, fully separate from the daily digest (never batched, never
// shed). Idempotency is the outbox's (notification_id, channel) uniqueness + its
// delivery_state guard, so an at-least-once retry never duplicates the logical email
// and a permanent failure is an OBSERVABLE dead-letter that never marks the email
// delivered (no false "delivered").

// ChannelEmail is the P0 immediate urgent-delivery channel. The per-channel outbox
// key + delivery state generalize to further channels without a schema change.
const ChannelEmail = "email"

// Delivery-state values of the outbox projection (mirrors the migration CHECK).
const (
	urgentStatePending    = "pending"
	urgentStateDelivered  = "delivered"
	urgentStateDeadLetter = "dead_letter"
)

// Bounded technical failure reasons recorded on the outbox + dead-letter telemetry.
// They are LTR technical tokens, never rendered copy or free text (LOC-001).
const (
	reasonUnsendableTarget = "unsendable_target"
	reasonRenderError      = "render_error"
	reasonSendError        = "send_error"
	reasonResolveError     = "resolve_error"
)

// UrgentEmailEnqueuer enqueues a durable urgent-email dispatch job inside the caller's
// transaction — the transactional-enqueue seam Store.Deliver uses so the job commits
// atomically with the notification + its outbox row (restart-safe). Injected so the
// store is unit-testable and depends on no concrete River client.
type UrgentEmailEnqueuer interface {
	EnqueueUrgentEmailTx(ctx context.Context, tx pgx.Tx, args jobs.UrgentEmailArgs) error
}

// UrgentEmailDispatcher is the concrete River-backed enqueuer wired in main once the
// River client exists. It structurally satisfies UrgentEmailEnqueuer.
type UrgentEmailDispatcher struct{ client *jobs.Client }

// NewUrgentEmailDispatcher wires the enqueuer over the platform River client.
func NewUrgentEmailDispatcher(client *jobs.Client) *UrgentEmailDispatcher {
	return &UrgentEmailDispatcher{client: client}
}

// EnqueueUrgentEmailTx enqueues the durable urgent-email intent on tx.
func (d *UrgentEmailDispatcher) EnqueueUrgentEmailTx(ctx context.Context, tx pgx.Tx, args jobs.UrgentEmailArgs) error {
	_, err := jobs.EnqueueUrgentEmailTx(ctx, d.client, tx, args)
	return err
}

// UrgentOutboxRecord is the notify-domain view of one durable outbox row (a
// delivery-state projection). It is decoupled from db.NotificationUrgentOutbox so the
// dispatcher's fail-closed / idempotent / dead-letter decisions are unit-testable
// against a fake, with no database.
type UrgentOutboxRecord struct {
	NotificationID uuid.UUID
	Account        uuid.UUID
	Channel        string
	State          string
	Attempts       int32
}

// UrgentOutboxStore reads one outbox row and transitions its delivery STATE. State is
// the ONLY mutable surface (never the append-only notification/audit); every write
// here is on the outbox projection. Transitions are guarded on delivery_state =
// 'pending', so a re-drive after a terminal state is a no-op (idempotent).
type UrgentOutboxStore interface {
	// Get returns the outbox row for (notificationID, channel); found=false with no
	// error when absent.
	Get(ctx context.Context, notificationID uuid.UUID, channel string) (rec UrgentOutboxRecord, found bool, err error)
	// MarkDelivered performs the pending → delivered transition (the sole success
	// write). A no-op when already non-pending (idempotent).
	MarkDelivered(ctx context.Context, notificationID uuid.UUID, channel string, at time.Time) error
	// MarkDeadLetter performs the pending → dead_letter transition (observable
	// permanent failure; does NOT mark delivered). reason is a bounded technical token.
	MarkDeadLetter(ctx context.Context, notificationID uuid.UUID, channel, reason string, at time.Time) error
	// BumpAttempt records a transient failed attempt while the row stays pending.
	BumpAttempt(ctx context.Context, notificationID uuid.UUID, channel, reason string, at time.Time) error
}

// DBUrgentOutboxStore is the pgx-backed UrgentOutboxStore.
type DBUrgentOutboxStore struct{ pool *pgxpool.Pool }

// NewDBUrgentOutboxStore builds the outbox store over the pool.
func NewDBUrgentOutboxStore(pool *pgxpool.Pool) *DBUrgentOutboxStore {
	return &DBUrgentOutboxStore{pool: pool}
}

// Get reads the outbox row; a missing row is (found=false, nil error).
func (s *DBUrgentOutboxStore) Get(ctx context.Context, notificationID uuid.UUID, channel string) (UrgentOutboxRecord, bool, error) {
	row, err := db.New(s.pool).GetUrgentOutbox(ctx, db.GetUrgentOutboxParams{
		NotificationID: notificationID,
		Channel:        channel,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return UrgentOutboxRecord{}, false, nil
	}
	if err != nil {
		return UrgentOutboxRecord{}, false, err
	}
	return UrgentOutboxRecord{
		NotificationID: row.NotificationID,
		Account:        row.MarketplaceAccountID,
		Channel:        row.Channel,
		State:          row.DeliveryState,
		Attempts:       row.Attempts,
	}, true, nil
}

// MarkDelivered performs the guarded pending → delivered transition. A pgx.ErrNoRows
// (already transitioned by a concurrent dispatch) is an idempotent no-op, not an error.
func (s *DBUrgentOutboxStore) MarkDelivered(ctx context.Context, notificationID uuid.UUID, channel string, at time.Time) error {
	_, err := db.New(s.pool).MarkUrgentOutboxDelivered(ctx, db.MarkUrgentOutboxDeliveredParams{
		NotificationID: notificationID,
		Channel:        channel,
		DeliveredAt:    pgtype.Timestamptz{Time: at, Valid: true},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	return err
}

// MarkDeadLetter performs the guarded pending → dead_letter transition.
func (s *DBUrgentOutboxStore) MarkDeadLetter(ctx context.Context, notificationID uuid.UUID, channel, reason string, at time.Time) error {
	_, err := db.New(s.pool).MarkUrgentOutboxDeadLetter(ctx, db.MarkUrgentOutboxDeadLetterParams{
		NotificationID: notificationID,
		Channel:        channel,
		UpdatedAt:      at,
		LastError:      pgtype.Text{String: reason, Valid: reason != ""},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	return err
}

// BumpAttempt records a transient failed attempt (row stays pending).
func (s *DBUrgentOutboxStore) BumpAttempt(ctx context.Context, notificationID uuid.UUID, channel, reason string, at time.Time) error {
	_, err := db.New(s.pool).BumpUrgentOutboxAttempt(ctx, db.BumpUrgentOutboxAttemptParams{
		NotificationID: notificationID,
		Channel:        channel,
		UpdatedAt:      at,
		LastError:      pgtype.Text{String: reason, Valid: reason != ""},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	return err
}

// UrgentDeadLetterObserver is notified when an urgent email is dead-lettered (issue
// #122): a permanent send failure that is CONTAINED and OBSERVABLE (metric + warn log
// + durable outbox state + this typed seam), never a silent drop. It carries only safe
// technical identifiers (account, notification id, category, bounded reason) — never
// rendered copy (LOC-001). A nil observer is a no-op.
type UrgentDeadLetterObserver func(ctx context.Context, account, notificationID uuid.UUID, category, reason string)

// UrgentDispatcher drives one durable urgent-email intent to the mail channel. It is
// the injected UrgentEmailFunc runner (via Dispatch): read the outbox row, render from
// the closed catalog in the account locale, send, and record the delivery-state
// transition. It NEVER touches the append-only notification/audit history.
type UrgentDispatcher struct {
	outbox     UrgentOutboxStore
	mailer     Mailer
	resolver   TargetResolver
	logger     *slog.Logger
	deadLetter UrgentDeadLetterObserver
	now        func() time.Time
}

// NewUrgentDispatcher builds the dispatcher over the outbox store, mailer, and target
// resolver (the same resolver the digest uses — recipient + locale as DATA).
func NewUrgentDispatcher(outbox UrgentOutboxStore, mailer Mailer, resolver TargetResolver) *UrgentDispatcher {
	return &UrgentDispatcher{
		outbox:   outbox,
		mailer:   mailer,
		resolver: resolver,
		now:      func() time.Time { return time.Now().UTC() },
	}
}

// WithClock overrides the clock (tests only).
func (d *UrgentDispatcher) WithClock(now func() time.Time) *UrgentDispatcher {
	d.now = now
	return d
}

// WithLogger attaches a structured logger (technical identifiers only, LOC-001).
func (d *UrgentDispatcher) WithLogger(l *slog.Logger) *UrgentDispatcher {
	d.logger = l
	return d
}

// WithDeadLetterObserver attaches the dead-letter observer (issue #122): it fires for
// every urgent email that permanently failed and was dead-lettered.
func (d *UrgentDispatcher) WithDeadLetterObserver(o UrgentDeadLetterObserver) *UrgentDispatcher {
	d.deadLetter = o
	return d
}

// Dispatch delivers one urgent-email intent, idempotently. lastAttempt is true on the
// final River attempt: a still-failing delivery is then dead-lettered (observable,
// NOT delivered) instead of retried forever. It returns a non-nil error whenever the
// send did not succeed, so River retries (transient) or discards (final) — a committed
// urgent intent is never silently completed. A successful or already-terminal row is
// an idempotent no-op (no duplicate email).
func (d *UrgentDispatcher) Dispatch(ctx context.Context, args jobs.UrgentEmailArgs, lastAttempt bool) error {
	channel := args.Channel
	if channel == "" {
		channel = ChannelEmail
	}

	rec, found, err := d.outbox.Get(ctx, args.NotificationID, channel)
	if err != nil {
		return err // transient read failure → River retries
	}
	if !found {
		// The outbox row is committed in the SAME transaction as the notification, so a
		// missing row cannot occur for a live notification. Absence means the
		// notification (and its cascaded outbox) is gone — nothing to deliver. Fail
		// closed to a terminal no-op (never an infinite retry loop), but observe it.
		if d.logger != nil {
			d.logger.WarnContext(ctx, "urgent email: no outbox row (terminal no-op)",
				"notification_id", args.NotificationID, "channel", channel, "category", args.Category)
		}
		return nil
	}
	if rec.State != urgentStatePending {
		// Already delivered or dead-lettered — idempotent no-op (no duplicate email).
		return nil
	}

	target, err := d.resolver.Resolve(ctx, args.Account)
	if err != nil {
		return d.fail(ctx, args, channel, lastAttempt, reasonResolveError,
			fmt.Errorf("notify: urgent resolve target for %s: %w", args.Account, err))
	}
	if target.Email == "" || !SupportedLocale(target.Locale) {
		return d.fail(ctx, args, channel, lastAttempt, reasonUnsendableTarget,
			fmt.Errorf("%w: account %s", ErrUnsendableTarget, args.Account))
	}

	msg, err := renderUrgent(target, args)
	if err != nil {
		return d.fail(ctx, args, channel, lastAttempt, reasonRenderError,
			fmt.Errorf("notify: urgent render for %s: %w", args.NotificationID, err))
	}

	if err := d.mailer.Send(ctx, msg); err != nil {
		return d.fail(ctx, args, channel, lastAttempt, reasonSendError,
			fmt.Errorf("notify: urgent send for %s: %w", args.NotificationID, err))
	}

	if err := d.outbox.MarkDelivered(ctx, args.NotificationID, channel, d.now()); err != nil {
		// The mail is out but the state write failed. Return the error so River retries;
		// the guarded MarkDelivered stays idempotent and the pending→delivered transition
		// completes on the retry (at-least-once, the standard outbox tradeoff).
		return fmt.Errorf("notify: urgent mark delivered for %s: %w", args.NotificationID, err)
	}
	recordUrgentDelivered(ctx, args.Category)
	if d.logger != nil {
		d.logger.InfoContext(ctx, "urgent email delivered",
			"notification_id", args.NotificationID, "channel", channel, "category", args.Category)
	}
	return nil
}

// fail records a failed attempt and returns the cause so River retries (transient) or
// discards (final). On the final attempt it performs the pending → dead_letter
// transition — the OBSERVABLE permanent-failure state (metric + warn log + durable row
// + observer) — and the email is NOT marked delivered (no false "delivered"). An
// urgent category is therefore never silently dropped and never shed.
func (d *UrgentDispatcher) fail(ctx context.Context, args jobs.UrgentEmailArgs, channel string, lastAttempt bool, reason string, cause error) error {
	if lastAttempt {
		if err := d.outbox.MarkDeadLetter(ctx, args.NotificationID, channel, reason, d.now()); err != nil && d.logger != nil {
			d.logger.ErrorContext(ctx, "urgent email: dead-letter state write failed",
				"notification_id", args.NotificationID, "channel", channel, "category", args.Category,
				"reason", reason, "error", err.Error())
		}
		recordUrgentDeadLetter(ctx, args.Category)
		if d.logger != nil {
			d.logger.ErrorContext(ctx, "urgent email dead-lettered (permanent failure; NOT delivered)",
				"notification_id", args.NotificationID, "channel", channel, "category", args.Category,
				"reason", reason, "error", cause.Error())
		}
		if d.deadLetter != nil {
			d.deadLetter(ctx, args.Account, args.NotificationID, args.Category, reason)
		}
		return cause
	}
	if err := d.outbox.BumpAttempt(ctx, args.NotificationID, channel, reason, d.now()); err != nil && d.logger != nil {
		d.logger.WarnContext(ctx, "urgent email: attempt-bump write failed",
			"notification_id", args.NotificationID, "channel", channel, "category", args.Category,
			"reason", reason, "error", err.Error())
	}
	if d.logger != nil {
		d.logger.WarnContext(ctx, "urgent email attempt failed (will retry)",
			"notification_id", args.NotificationID, "channel", channel, "category", args.Category,
			"reason", reason, "error", cause.Error())
	}
	return cause
}

// renderUrgent builds the immediate execution/safety-failure email entirely from the
// closed catalog in the target locale (LOC-002): the urgent frame (subject + footer)
// plus the item line (the SAME item key the in-app notification uses), so the email
// and the in-app item are provably the same event via the SHARED event id (NOT-001).
// No key literal or free text escapes into copy.
func renderUrgent(target Target, args jobs.UrgentEmailArgs) (Message, error) {
	subject, err := Render(target.Locale, KeyUrgentSubject, nil)
	if err != nil {
		return Message{}, err
	}
	line, err := Render(target.Locale, args.TitleKey, args.Params)
	if err != nil {
		return Message{}, err
	}
	footer, err := Render(target.Locale, KeyUrgentFooter, nil)
	if err != nil {
		return Message{}, err
	}
	var b strings.Builder
	b.WriteString(line)
	// The shared event id is a technical identifier (LTR) so the email and the in-app
	// item are provably the SAME event (NOT-001).
	b.WriteString(" [event:")
	b.WriteString(args.EventID.String())
	b.WriteString("]\n\n")
	b.WriteString(footer)
	return Message{To: target.Email, Subject: subject, Body: b.String()}, nil
}
