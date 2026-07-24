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

// --- Daily email digest job (NOT-001 / §6.8) -----------------------------------

// DigestGenerateArgs schedules the once-per-business-day email digest FAN-OUT.
//
// The job body SENDS NOTHING (issue #124). It records one durable (account,
// business_day) delivery row per account and enqueues a per-account job for it
// transactionally, and it re-enqueues every nonterminal row of any historical day
// (owned recovery). Sending happens in DigestAccountArgs jobs, each with its own retry
// budget on its own bounded queue — so one tenant's failure can never block another's
// scheduled delivery. Every step is idempotent per (account, business_day), so a re-run
// of the periodic job never produces a duplicate digest. Execution/safety failures
// bypass this job entirely (delivered immediately through the urgent outbox).
type DigestGenerateArgs struct{}

// Kind is the stable River job identifier.
func (DigestGenerateArgs) Kind() string { return "notification_digest_generate" }

// DigestWorker runs the injected digest fan-out/recovery pass.
type DigestWorker struct {
	river.WorkerDefaults[DigestGenerateArgs]
	run    RunOnceFunc
	logger *slog.Logger
}

// NewDigestWorker builds the worker over a digest fan-out pass.
func NewDigestWorker(run RunOnceFunc, logger *slog.Logger) *DigestWorker {
	return &DigestWorker{run: run, logger: logger}
}

// Work runs one pass; a nil runner is a no-op (fail closed, never a panic). The count
// is the number of per-account jobs ENQUEUED this pass (discovery plus recovery; 0 when
// every account/day already has one) — logged, never silent.
func (w *DigestWorker) Work(ctx context.Context, job *river.Job[DigestGenerateArgs]) error {
	if w.run == nil {
		return nil
	}
	n, err := w.run(ctx)
	if w.logger != nil {
		w.logger.InfoContext(ctx, "daily digest fan-out pass", "job_id", job.ID, "enqueued", n, "error", errText(err))
	}
	return err
}

// --- Durable per-(account, business_day) digest delivery job (issue #124) ----------
//
// The fan-out above no longer sends anything: it records one durable delivery row per
// account for the finalized business day and enqueues ONE of these jobs to drive it.
// That is what makes multi-tenant fan-out isolate tenant failures — each account gets
// its own job, its own retry budget, its own dead-letter, and a queue whose capacity is
// bounded separately from every other kind of work.

// QueueDigestAccount is the SEPARATELY BOUNDED queue per-account digest jobs run on.
//
// The daily digest is advisory UI in the load-shedding order (approval path > audit
// append > reconciliation > observations > advisory UI). Running it on the shared
// default queue meant a handful of accounts with hanging relays could occupy every
// worker and stall urgent-email, execution, and audit-adjacent work. A dedicated queue
// caps the blast radius: digest work can only ever consume its own slots.
const QueueDigestAccount = "digest_account"

// DigestAccountMaxAttempts bounds a single account's retry budget. River's default of
// 25 would let one permanently broken tenant retry for days; a bounded budget reaches
// the OBSERVABLE dead-letter terminal state instead. It is per account — a failing
// tenant no longer shares a budget with healthy ones.
const DigestAccountMaxAttempts = 5

// digest account work-deadline bounds. A per-account attempt must always release its
// worker slot, and the ceiling is what makes "bounded fan-out" true: without a maximum,
// any positive configured timeout could retain a slot for an arbitrarily long period.
const (
	DigestAccountDefaultTimeout = 60 * time.Second
	DigestAccountMinTimeout     = 5 * time.Second
	DigestAccountMaxTimeout     = 5 * time.Minute
)

// ClampDigestAccountTimeout enforces the work-deadline bounds. A non-positive value
// takes the default; anything outside [min, max] is clamped rather than honoured, so a
// misconfiguration can neither disable the deadline nor park a worker slot indefinitely.
func ClampDigestAccountTimeout(d time.Duration) time.Duration {
	switch {
	case d <= 0:
		return DigestAccountDefaultTimeout
	case d < DigestAccountMinTimeout:
		return DigestAccountMinTimeout
	case d > DigestAccountMaxTimeout:
		return DigestAccountMaxTimeout
	default:
		return d
	}
}

// DigestAccountArgs is the durable per-account digest intent. BusinessDay is PINNED as
// a YYYY-MM-DD UTC date (JSON-safe business data, plan §4.8): the job finalizes the day
// it was created for, so a delivery delayed across UTC midnight still covers its
// original window instead of silently abandoning it. Both fields are tagged `river:
// "unique"` so River's uniqueness is per (account, day).
type DigestAccountArgs struct {
	Account     uuid.UUID `json:"account"      river:"unique"`
	BusinessDay string    `json:"business_day" river:"unique"`
}

// Kind is River's stable job identifier; never change once shipped (it would orphan
// in-flight durable digest intents).
func (DigestAccountArgs) Kind() string { return "notification_digest_account" }

// InsertOpts pins the queue, the bounded retry budget, and per-(account, day)
// uniqueness.
//
// The unique states deliberately EXCLUDE completed and discarded (River's default set
// includes completed). Uniqueness here exists only to stop duplicate IN-FLIGHT work; it
// must never block the owned recovery pass from re-enqueueing an account/day whose
// previous job was completed or discarded while its durable row stayed nonterminal —
// which is precisely the correlated-failure case this job exists to survive.
func (DigestAccountArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{
		Queue:       QueueDigestAccount,
		MaxAttempts: DigestAccountMaxAttempts,
		UniqueOpts: river.UniqueOpts{
			ByArgs: true,
			ByState: []rivertype.JobState{
				rivertype.JobStateAvailable,
				rivertype.JobStatePending,
				rivertype.JobStateRunning,
				rivertype.JobStateScheduled,
				rivertype.JobStateRetryable,
			},
		},
	}
}

// BusinessDayFormat is the pinned-day wire format on DigestAccountArgs.
const BusinessDayFormat = "2006-01-02"

// Day parses the pinned business day. A malformed value yields the zero time, which the
// runner treats as unworkable (fail closed) rather than silently substituting "today"
// and finalizing the wrong window.
func (a DigestAccountArgs) Day() (time.Time, error) {
	return time.Parse(BusinessDayFormat, a.BusinessDay)
}

// DigestAccountFunc drives one account's digest for one PINNED business day. It returns
// a bounded outcome token plus an error. lastAttempt is true on the final River attempt
// so the runner can record the OBSERVABLE dead-letter terminal state instead of
// retrying forever. Injected so jobs depends on no notify package.
type DigestAccountFunc func(ctx context.Context, account uuid.UUID, day time.Time, lastAttempt bool) (string, error)

// errNoDigestAccountRunner is returned when no runner is wired. It fails CLOSED (River
// retries) rather than silently completing — a committed delivery row is never
// abandoned by a missing consumer.
var errNoDigestAccountRunner = errors.New("jobs: no digest-account runner wired; refusing to complete intent (fail closed)")

// ErrDigestTerminalUnpersisted marks the recovery obligation left when a per-account
// digest attempt reached a TERMINAL decision but the durable state write ITSELF failed
// — typically because PostgreSQL is unavailable, which is exactly when River's own
// attempt-completion write fails too.
//
// It must not be returned verbatim on an exhausted attempt: River would DISCARD the job
// while the delivery row is still nonterminal, and no terminal signal was ever truthfully
// emitted. The worker snoozes instead (a park that does not consume an attempt), and the
// owned recovery pass rediscovers the row from the delivery table regardless — so
// repair never depends on River surviving the same outage.
var ErrDigestTerminalUnpersisted = errors.New("jobs: digest terminal state write failed; re-drive to repair (no terminal signal emitted)")

// digestTerminalRedriveBackoff bounds how long a digest terminal re-drive parks before
// re-attempting the durable transition. Bounded backpressure: long enough to ride out a
// transient database blip, short enough that the observable terminal state is not
// materially delayed.
const digestTerminalRedriveBackoff = 30 * time.Second

// DigestAccountWorker claims one durable per-account digest intent and runs the
// injected delivery. River guarantees at-least-once delivery + durable retry; the
// injected runner is idempotent on the (account, business_day) delivery row, so the
// effect is exactly-once-effectively — one logical digest per account per day.
type DigestAccountWorker struct {
	river.WorkerDefaults[DigestAccountArgs]
	run     DigestAccountFunc
	timeout time.Duration
	logger  *slog.Logger
}

// NewDigestAccountWorker builds the worker over the injected delivery runner. The
// work deadline is clamped into the enforced bounds.
func NewDigestAccountWorker(run DigestAccountFunc, timeout time.Duration, logger *slog.Logger) *DigestAccountWorker {
	return &DigestAccountWorker{run: run, timeout: ClampDigestAccountTimeout(timeout), logger: logger}
}

// Timeout bounds every attempt, so no account can hold a worker slot indefinitely. This
// is what keeps the bounded digest queue genuinely bounded when several tenants point at
// hanging relays at once: each poison attempt releases its slot on a deadline.
func (w *DigestAccountWorker) Timeout(*river.Job[DigestAccountArgs]) time.Duration {
	return ClampDigestAccountTimeout(w.timeout)
}

// Work drives one account/day delivery. A nil runner fails closed (retry) so a durable
// delivery row is never abandoned; an unparseable pinned day fails closed as well
// rather than substituting a different window. The boundary is always logged with
// bounded technical identifiers only (LOC-001) — never relay text or a recipient.
func (w *DigestAccountWorker) Work(ctx context.Context, job *river.Job[DigestAccountArgs]) error {
	if w.run == nil {
		if w.logger != nil {
			w.logger.ErrorContext(ctx, "digest account: no runner wired (fail closed)",
				"job_id", job.ID, "account_id", job.Args.Account, "business_day", job.Args.BusinessDay)
		}
		return errNoDigestAccountRunner
	}
	day, err := job.Args.Day()
	if err != nil {
		if w.logger != nil {
			w.logger.ErrorContext(ctx, "digest account: unparseable pinned business day (fail closed)",
				"job_id", job.ID, "account_id", job.Args.Account, "business_day", job.Args.BusinessDay)
		}
		return fmt.Errorf("jobs: digest account %s has an unparseable business day: %w", job.Args.Account, err)
	}
	// River always populates JobRow in production; guard so a hand-built job in a unit
	// test (no JobRow) never nil-derefs the attempt bookkeeping.
	lastAttempt := job.JobRow != nil && job.Attempt >= job.MaxAttempts
	outcome, err := w.run(ctx, job.Args.Account, day, lastAttempt)
	if w.logger != nil {
		w.logger.InfoContext(ctx, "digest account attempt",
			"job_id", job.ID, "account_id", job.Args.Account, "business_day", job.Args.BusinessDay,
			"outcome", outcome, "attempt", job.Attempt, "max_attempts", job.MaxAttempts,
			"last_attempt", lastAttempt, "error", errText(err))
	}
	// The attempt reached a terminal decision but the durable transition did NOT land.
	// Returning the error verbatim would let River discard the intent on an exhausted
	// attempt, dropping the repair obligation while no terminal signal was ever emitted.
	// Snooze instead — a bounded park that does NOT consume the attempt.
	if errors.Is(err, ErrDigestTerminalUnpersisted) {
		if w.logger != nil {
			w.logger.WarnContext(ctx, "digest account: terminal state unpersisted; snoozing to re-drive (recovery anchored)",
				"job_id", job.ID, "account_id", job.Args.Account, "business_day", job.Args.BusinessDay)
		}
		return river.JobSnooze(digestTerminalRedriveBackoff)
	}
	return err
}

// EnqueueDigestAccountTx enqueues one durable per-account digest intent inside the
// caller's transaction (transactional enqueue, jobs pkg invariant): the intent becomes
// visible only if the owning delivery row commits, and a rollback discards both. The
// idempotency authority is the delivery row's permanent (account, business_day)
// uniqueness — River's unique window only collapses duplicate in-flight work.
func EnqueueDigestAccountTx(ctx context.Context, client *Client, tx pgx.Tx, account uuid.UUID, day time.Time) (*rivertype.JobInsertResult, error) {
	args := DigestAccountArgs{Account: account, BusinessDay: day.UTC().Format(BusinessDayFormat)}
	res, err := client.InsertTx(ctx, tx, args, nil)
	if err != nil {
		return nil, fmt.Errorf("jobs: enqueue notification_digest_account intent: %w", err)
	}
	return res, nil
}
