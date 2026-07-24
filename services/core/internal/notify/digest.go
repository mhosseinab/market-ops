package notify

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/jobs"
)

// Message is one rendered email the digest sends. Body is plain text rendered
// entirely from catalog keys (LOC-002) — it holds no key literals, only resolved
// copy in the target locale plus technical identifiers (event ids, URLs).
type Message struct {
	To      string
	Subject string
	Body    string
}

// Mailer sends a rendered digest email. The SMTP implementation targets mailpit in
// dev; tests inject a capturing fake.
//
// Implementations should report failures as *SendError so the delivery state machine
// can tell a DEFINITIVE non-acceptance (safe to retry) from an AMBIGUOUS one (the relay
// may already hold the message, so retrying could duplicate it). Any other error is
// treated conservatively as definitive-but-transient.
type Mailer interface {
	Send(ctx context.Context, msg Message) error
}

// Target is the per-account digest destination: the recipient, the locale the
// email renders in (DATA — the pack is selected by this string, never a branch),
// and the deep-link to the account's daily briefing (§6.8 — the email LINKS to the
// briefing, it does not regenerate it).
type Target struct {
	Email       string
	Locale      string
	BriefingURL string
}

// TargetResolver yields the digest Target for an account. Injecting it keeps the
// digest service free of any user/email schema coupling and lets tests supply a
// deterministic target.
type TargetResolver interface {
	Resolve(ctx context.Context, account uuid.UUID) (Target, error)
}

// ErrUnsendableTarget is returned when an account has no deliverable digest target
// (no recipient or an unsupported locale). Fail closed — never send to nobody, and
// never silently fall back to another locale.
var ErrUnsendableTarget = errors.New("notify: unsendable digest target")

// SentObserver is notified after a digest is successfully sent for one account. It
// is the seam the §18 analytics pipe hooks into (the digest emits a briefing-family
// event + a §17.3 briefing cost on the same pipe) WITHOUT coupling this package to
// analytics. A nil observer is a no-op; an observer error is logged by the caller,
// never fatal (analytics is advisory, off the delivery-correctness path).
type SentObserver func(ctx context.Context, account uuid.UUID, itemCount int)

// IsolatedObserver is notified when the digest ISOLATES one persisted row that
// violates the closed message schema (a legacy/invalid row): the row is skipped
// (never sent, never mutated — append-only preserved) so it cannot poison the
// whole account's digest pass, and the skip is OBSERVABLE (never a silent drop).
// This is the typed seam a test asserts on; the service also emits a metric and a
// warn log for every isolation. reason is the bounded ValidationReason.
type IsolatedObserver func(ctx context.Context, account, notificationID uuid.UUID, titleKey, bodyKey string, reason ValidationReason)

// AccountFailedObserver is notified when the digest ISOLATES one account whose attempt
// failed (issue #124): the failure is CONTAINED to that account — every OTHER account
// still delivers on its own job and its own retry budget, so one tenant's failure can
// neither leak into nor abort another's digest — and it is OBSERVABLE (this typed
// observer + a metric + a structured log), never silently swallowed. The error always
// wraps a bounded *DigestFailure naming the account, the pinned business day, the
// outcome, and a closed-set reason. A nil observer is a no-op.
type AccountFailedObserver func(ctx context.Context, account uuid.UUID, err error)

// AccountAttemptObserver is notified after every per-account digest ATTEMPT completes
// (issue #124), carrying the pinned business day, the terminal-or-transient outcome, a
// BOUNDED machine reason, and the attempt's own lag. Lag is measured from the moment
// the account's business day became finalizable (day + 24h) to THIS attempt's
// completion, so a delivery two days late reports roughly 48 hours rather than a
// figure recomputed from the current midnight. It is captured per attempt, so a healthy
// account never inherits a poison account's delay. A nil observer is a no-op.
type AccountAttemptObserver func(ctx context.Context, account uuid.UUID, day time.Time, outcome, reason string, lag time.Duration)

// DigestAccountEnqueuer enqueues one durable per-(account, business_day) digest job
// inside the caller's transaction — the transactional-enqueue seam the fan-out uses so
// the durable delivery row and its driving job commit atomically. Injected so the
// digest service depends on no concrete River client.
type DigestAccountEnqueuer interface {
	EnqueueDigestAccountTx(ctx context.Context, tx pgx.Tx, account uuid.UUID, day time.Time) error
}

// sendingStaleAfter bounds how long a row may sit in the ambiguous `sending` state
// before the owned recovery pass rediscovers it. It is comfortably longer than any
// per-account attempt deadline, so recovery can never race a live in-flight send.
const sendingStaleAfter = 15 * time.Minute

// durableWriteTimeout bounds each DETACHED durable delivery-state write. It is short —
// a single guarded UPDATE on an indexed key — so a dead database cannot hold a worker
// slot past the attempt that spawned it. It is owned by the jobs package because the
// graceful-stop budget (jobs.StopGrace) is derived from it: the process must outlast the
// detached write a drained attempt still owes.
const durableWriteTimeout = jobs.DurableStateWriteTimeout

// durableCtx derives the context every durable delivery transition runs on.
//
// It is DETACHED from the attempt's context (context.WithoutCancel) and separately
// bounded. River wraps a job's Work context with the worker's Timeout, so when that
// deadline fires mid-send the mailer reports a DEFINITIVE non-acceptance and the
// follow-up state write would immediately fail on the already-dead context: the row
// stranded in `sending`, the next drive wrote it off as terminally unconfirmed, and the
// worker snoozed without consuming an attempt — a silent, permanent non-delivery on the
// FIRST relay hang. Recording the outcome is the last thing an attempt owes, so it must
// outlive the attempt's cancellation. River applies the same pattern to its own
// completer (client.go: c.completer.Start(context.WithoutCancel(ctx))).
func durableCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), durableWriteTimeout)
}

// barrierWriteCtx bounds the ONE durable write that runs while the relay is waiting: the
// ambiguity marker at the post-DATA boundary.
//
// Every other durable transition happens after the exchange is over, so spending the
// full durableWriteTimeout costs nothing. This one is different — the SMTP socket
// deadline is already armed at the ATTEMPT's deadline, so a slow database here consumes
// the relay's remaining budget and can turn a healthy send into a boundary timeout. It
// therefore takes the SMALLER of durableWriteTimeout and half the attempt's remaining
// budget, leaving the other half for the terminator and the verdict. A write that cannot
// finish in that budget fails the barrier, which fails CLOSED (the send is abandoned
// before the window opens and stays definitively retryable) rather than entering a
// window the record could not describe.
func barrierWriteCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	budget := durableWriteTimeout
	if dl, ok := ctx.Deadline(); ok {
		if half := time.Until(dl) / 2; half < budget {
			budget = half
		}
	}
	return context.WithTimeout(context.WithoutCancel(ctx), budget)
}

// Durable-transition wrappers. Every one runs its guarded UPDATE on a DETACHED, bounded
// context (durableCtx) and reports whether the write actually LANDED, so a guard miss is
// never mistaken for a persisted state and a cancelled attempt still records its verdict.
func (s *DigestService) markDelivered(ctx context.Context, a uuid.UUID, d time.Time) (bool, error) {
	wctx, cancel := durableCtx(ctx)
	defer cancel()
	return s.deliveries.MarkDelivered(wctx, a, d, s.now())
}

func (s *DigestService) markSkipped(ctx context.Context, a uuid.UUID, d time.Time, r DigestReason) (bool, error) {
	wctx, cancel := durableCtx(ctx)
	defer cancel()
	return s.deliveries.MarkSkipped(wctx, a, d, r, s.now())
}

func (s *DigestService) markDeadLetter(ctx context.Context, a uuid.UUID, d time.Time, r DigestReason, code int32) (bool, error) {
	wctx, cancel := durableCtx(ctx)
	defer cancel()
	return s.deliveries.MarkDeadLetter(wctx, a, d, r, code, s.now())
}

func (s *DigestService) markUnconfirmed(ctx context.Context, a uuid.UUID, d time.Time, r DigestReason, code int32) (bool, error) {
	wctx, cancel := durableCtx(ctx)
	defer cancel()
	return s.deliveries.MarkUnconfirmed(wctx, a, d, r, code, s.now())
}

func (s *DigestService) releaseToPending(ctx context.Context, a uuid.UUID, d time.Time, r DigestReason, code int32) error {
	wctx, cancel := durableCtx(ctx)
	defer cancel()
	return s.deliveries.ReleaseToPending(wctx, a, d, r, code, s.now())
}

func (s *DigestService) bumpAttempt(ctx context.Context, a uuid.UUID, d time.Time, r DigestReason, code int32) error {
	wctx, cancel := durableCtx(ctx)
	defer cancel()
	return s.deliveries.BumpAttempt(wctx, a, d, r, code, s.now())
}

// recoveryBatchLimit bounds one recovery pass. The nonterminal backlog is re-driven
// oldest-day-first in bounded batches, so a large backlog applies backpressure instead
// of flooding the queue in a single tick (§17 bounded reads).
const recoveryBatchLimit = 500

// DigestService composes and sends the once-per-business-day email digest.
//
// Delivery is DURABLE and PER-(account, business_day). The fan-out no longer sends
// inline: it ensures one durable delivery row per account for the finalized day,
// enqueues a per-account job for it transactionally, and separately REDISCOVERS every
// nonterminal row of any historical day and re-enqueues that too. One tenant's
// unsendable recipient, unsupported locale, render error, or SMTP failure is therefore
// confined to that tenant's own row, its own job, and its own retry budget — it can
// neither abort nor delay an independent account's delivery, and it cannot be lost when
// the day advances, because the business day is pinned on the row and on the job args.
//
// Idempotency is the (account, business_day) uniqueness plus the guarded state
// transitions: a retry, a duplicate fan-out, and a recovery re-enqueue all converge on
// one logical digest, so no path can resend one that already reached a terminal state.
type DigestService struct {
	pool       *pgxpool.Pool
	mailer     Mailer
	resolver   TargetResolver
	deliveries DigestDeliveryStore
	enqueuer   DigestAccountEnqueuer
	observer   SentObserver
	isolated   IsolatedObserver
	acctFail   AccountFailedObserver
	attempt    AccountAttemptObserver
	logger     *slog.Logger
	now        func() time.Time
}

// NewDigestService builds the digest service over the pool, a mailer, and a target
// resolver. The durable delivery-state store defaults to the pgx-backed
// implementation over the same pool.
func NewDigestService(pool *pgxpool.Pool, mailer Mailer, resolver TargetResolver) *DigestService {
	return &DigestService{
		pool:       pool,
		mailer:     mailer,
		resolver:   resolver,
		deliveries: NewDBDigestDeliveryStore(pool),
		now:        func() time.Time { return time.Now().UTC() },
	}
}

// WithDeliveryStore overrides the durable delivery-state store. Production uses the
// default pgx store; tests inject a fault-injecting wrapper to exercise the correlated
// failure in which the terminal projection write fails alongside River's own.
func (s *DigestService) WithDeliveryStore(store DigestDeliveryStore) *DigestService {
	s.deliveries = store
	return s
}

// SetAccountEnqueuer wires the transactional per-account job enqueuer once the River
// client exists. Without one the fan-out still records durable work rows (nothing is
// lost) but drives nothing — fail closed, never a silent inline send.
func (s *DigestService) SetAccountEnqueuer(e DigestAccountEnqueuer) { s.enqueuer = e }

// WithAttemptObserver attaches the per-attempt observer (issue #124): it fires for
// every per-account attempt with the pinned day, outcome, bounded reason, and lag.
func (s *DigestService) WithAttemptObserver(o AccountAttemptObserver) *DigestService {
	s.attempt = o
	return s
}

// WithClock overrides the clock (tests only).
func (s *DigestService) WithClock(now func() time.Time) *DigestService {
	s.now = now
	return s
}

// WithObserver attaches the post-send observer (the §18 analytics hook).
func (s *DigestService) WithObserver(o SentObserver) *DigestService {
	s.observer = o
	return s
}

// WithIsolatedObserver attaches the isolated-row observer (issue #126): it fires
// for every persisted digest row skipped for violating the closed message schema.
func (s *DigestService) WithIsolatedObserver(o IsolatedObserver) *DigestService {
	s.isolated = o
	return s
}

// WithAccountFailedObserver attaches the per-account failure observer (issue #124):
// it fires for every account isolated out of the fan-out because its delivery pass
// failed, while the remaining accounts still deliver.
func (s *DigestService) WithAccountFailedObserver(o AccountFailedObserver) *DigestService {
	s.acctFail = o
	return s
}

// WithLogger attaches a structured logger so a digest-row isolation is logged (in
// addition to the observer and the metric). A nil logger is a no-op.
func (s *DigestService) WithLogger(l *slog.Logger) *DigestService {
	s.logger = l
	return s
}

// FinalizedBusinessDay is the most recently CLOSED UTC business day — the calendar
// day BEFORE the current UTC day (locale-neutral storage; Jalali is a display
// calendar over UTC, LOC-001). The digest finalizes a day only after its window has
// closed (issue #114): finalizing the current, still-OPEN day would strand every
// notification that arrives later the same day (the unique account/business_day
// header turns each later pass into a no-op). Because this day is always in the
// past, its window [day, day+24h) is complete, so the digest covers ALL of the day's
// eligible notifications exactly once.
func (s *DigestService) FinalizedBusinessDay() time.Time {
	n := s.now().UTC()
	today := time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, time.UTC)
	return today.Add(-24 * time.Hour)
}

// GenerateForAccount composes and sends the digest for one account INLINE, for the most
// recently CLOSED business day (issue #114 — never finalize the current, still-open
// day). It gathers that day's NON-bypass notifications over its FULL, now-complete
// window [day, day+24h) (execution/safety failures bypassed the digest and were
// delivered immediately), renders from the account-locale pack, and sends.
//
// It is the DIRECT entry point (used by targeted operations and tests). Scheduled
// delivery goes through the fan-out instead, which drives each account on its own job.
// Both share the same durable (account, business_day) row, so they cannot double-send:
// a day that already reached a terminal state sends nothing and reports sent=false, and
// the covered window is deterministically [business_day, business_day+24h), so a retry
// re-covers the SAME window (no duplicate send, no lost item). An empty day is a no-op.
func (s *DigestService) GenerateForAccount(ctx context.Context, account uuid.UUID) (sent bool, err error) {
	day := s.FinalizedBusinessDay()
	if _, err := s.EnsureDelivery(ctx, account, day, false); err != nil {
		return false, err
	}
	outcome, err := s.DeliverAccountDay(ctx, account, day, false)
	return outcome == OutcomeDelivered, err
}

// Per-attempt outcomes. They are BOUNDED tokens: the metric label, the log field, and
// the typed observer all carry one of these and nothing else.
const (
	// OutcomeDelivered — the relay accepted the digest and the terminal write landed.
	OutcomeDelivered = "delivered"
	// OutcomeNoop — the row was already terminal (or claimed by a concurrent drive), so
	// the attempt did nothing. This is the zero-resend path.
	OutcomeNoop = "noop"
	// OutcomeSkipped — the day had nothing sendable (terminal, not a failure).
	OutcomeSkipped = "skipped"
	// OutcomeRetryableFailure — the attempt failed and the row stays pending.
	OutcomeRetryableFailure = "retryable_failure"
	// OutcomeDeadLetter — TERMINAL permanent failure; definitively NOT delivered.
	OutcomeDeadLetter = "dead_letter"
	// OutcomeUnconfirmed — TERMINAL ambiguous; acceptance could not be established and
	// the digest is never resent.
	OutcomeUnconfirmed = "unconfirmed"
	// OutcomeTerminalUnpersisted — the attempt reached a terminal decision but the
	// durable transition did NOT land, so no terminal signal is emitted and the work is
	// re-driven instead of discarded.
	OutcomeTerminalUnpersisted = "terminal_unpersisted"
)

// DeliverAccountDay drives ONE account's digest for ONE PINNED business day. It is the
// per-account job body and the unit of tenant isolation: everything it can fail on —
// the day's query, the target, rendering, the claim, the relay — is confined to this
// account's own durable row and its own retry budget, so no other tenant is blocked,
// delayed, or aborted by it.
//
// lastAttempt is true on the final River attempt, so a still-failing delivery reaches
// the OBSERVABLE dead-letter terminal state instead of being retried forever or
// silently discarded.
//
// It is idempotent by construction: a row that already reached a terminal state is a
// no-op, so a retry, a duplicate job, and a recovery re-enqueue can never resend a
// digest (idempotency gates every retry, §4.6).
func (s *DigestService) DeliverAccountDay(ctx context.Context, account uuid.UUID, day time.Time, lastAttempt bool) (string, error) {
	day = normalizeBusinessDay(day)
	outcome, reason, err := s.deliverAccountDay(ctx, account, day, lastAttempt)
	// Lag is captured HERE, inside the attempt, against the attempt's own PINNED day —
	// never while draining a batch, and never against the current midnight. A healthy
	// account therefore reports its real lag rather than inheriting a slow peer's.
	lag := s.now().UTC().Sub(day.Add(24 * time.Hour))
	if lag < 0 {
		lag = 0
	}
	recordAccountAttempt(ctx, outcome, reason, lag)
	if s.attempt != nil {
		s.attempt(ctx, account, day, outcome, string(reason), lag)
	}
	return outcome, err
}

func (s *DigestService) deliverAccountDay(ctx context.Context, account uuid.UUID, day time.Time, lastAttempt bool) (string, DigestReason, error) {
	rec, found, err := s.deliveries.Get(ctx, account, day)
	if err != nil {
		return OutcomeRetryableFailure, DigestReasonQueryError, err
	}
	if !found {
		// The row is committed with the driving job, so absence means the account (and
		// its cascaded row) is gone. Terminal no-op — never an infinite retry loop.
		s.logDelivery(ctx, slog.LevelWarn, "digest account/day has no durable delivery row (terminal no-op)",
			account, day, OutcomeNoop, "", 0)
		return OutcomeNoop, "", nil
	}
	if rec.Terminal() {
		// ZERO RESEND. Already delivered, skipped, dead-lettered, or unconfirmed.
		return OutcomeNoop, "", nil
	}

	if rec.State == DigestStateSending {
		// A previous attempt was abandoned while holding the claim. The DURABLE ambiguity
		// marker — not the state alone — decides what that means.
		if !rec.Ambiguous {
			// The attempt never entered the post-DATA window, so the relay provably holds
			// nothing. Writing this off as terminally unconfirmed would claim "we may have
			// delivered" for a KNOWN non-delivery and burn the account's retry budget.
			// Release the claim and retry on this account's own budget.
			//
			// CARRY-FORWARD (issue #124 review): this release is guarded on state + marker
			// only — it carries NO liveness or claim-token check. It is unreachable against
			// a LIVE attempt today because River's ByArgs uniqueness admits one in-flight
			// job per (account, day), recovery only re-drives `sending` rows after
			// sendingStaleAfter (15m) which exceeds the maximum attempt timeout (5m), and
			// GenerateForAccount has no production caller. Adding a production caller that
			// can drive an account/day concurrently with its job makes a CLAIM TOKEN on this
			// transition REQUIRED — without one, a live attempt's claim could be released
			// underneath it and a second send issued.
			if err := s.releaseToPending(ctx, account, day, DigestReasonSendNotInitiated, rec.StatusCode); err != nil {
				return s.terminalUnpersisted(ctx, account, day, DigestReasonSendNotInitiated, err)
			}
			return s.failPending(ctx, account, day, DigestReasonSendNotInitiated, rec.StatusCode, false, lastAttempt,
				fmt.Errorf("notify: digest attempt for %s/%s was abandoned before transmission; claim released for retry",
					account, day.Format(time.DateOnly)))
		}
		// The attempt WAS inside the ambiguous window (a lost relay verdict, or a crash
		// after the body terminator). The relay may already hold the message, so
		// re-sending could duplicate a delivered digest. Finalize as the terminal
		// AMBIGUOUS state: it does not claim delivery, and it is never re-driven.
		ok, err := s.markUnconfirmed(ctx, account, day, DigestReasonSendOutcomeUnknown, rec.StatusCode)
		if err != nil {
			return s.terminalUnpersisted(ctx, account, day, DigestReasonSendOutcomeUnknown, err)
		}
		if !ok {
			// A concurrent drive already finalized it. Idempotent no-op — and NO terminal
			// signal, because this attempt persisted nothing.
			return OutcomeNoop, "", nil
		}
		s.isolateAccount(ctx, account, day, OutcomeUnconfirmed, DigestReasonSendOutcomeUnknown, rec.StatusCode, nil)
		return OutcomeUnconfirmed, DigestReasonSendOutcomeUnknown, nil
	}

	// --- pending: compose this day's digest over its FULL, now-complete window -----
	rows, err := db.New(s.pool).ListPendingDigestNotifications(ctx, db.ListPendingDigestNotificationsParams{
		MarketplaceAccountID: account,
		CreatedAt:            day,
		CreatedAt_2:          day.Add(24 * time.Hour),
	})
	if err != nil {
		return s.failPending(ctx, account, day, DigestReasonQueryError, 0, false, lastAttempt, err)
	}

	// Isolate any legacy/invalid persisted row that violates the closed message schema
	// (issue #126): ONE bad row must never poison the whole account's digest. A
	// violating row is SKIPPED (never sent) but the skip is OBSERVABLE — a typed
	// observer + metric + warn log, never a silent drop — and the row itself is
	// untouched (append-only: no UPDATE/DELETE). The rest of the day still sends.
	items := make([]Notification, 0, len(rows))
	for _, r := range rows {
		n, err := toNotification(r)
		if err != nil {
			return s.failPending(ctx, account, day, DigestReasonQueryError, 0, false, lastAttempt, err)
		}
		if verr := validateShape(n.Category, n.TitleKey, n.BodyKey, n.BodyParams); verr != nil {
			s.isolate(ctx, account, n, verr)
			continue
		}
		items = append(items, n)
	}
	if len(items) == 0 {
		reason := DigestReasonEmptyDay
		if len(rows) > 0 {
			reason = DigestReasonAllItemsIsolated
		}
		ok, err := s.markSkipped(ctx, account, day, reason)
		if err != nil {
			return s.terminalUnpersisted(ctx, account, day, reason, err)
		}
		if !ok {
			return OutcomeNoop, "", nil
		}
		return OutcomeSkipped, reason, nil
	}

	target, err := s.resolver.Resolve(ctx, account)
	if err != nil {
		return s.failPending(ctx, account, day, DigestReasonResolveError, 0, false, lastAttempt, err)
	}
	if target.Email == "" || !SupportedLocale(target.Locale) {
		// Fail closed: never send to nobody, never silently fall back to another locale.
		return s.failPending(ctx, account, day, DigestReasonUnsendableTarget, 0, false, lastAttempt,
			fmt.Errorf("%w: account %s", ErrUnsendableTarget, account))
	}
	msg, err := renderDigest(target, items)
	if err != nil {
		return s.failPending(ctx, account, day, DigestReasonRenderError, 0, false, lastAttempt, err)
	}

	// Claim the append-only header + membership snapshot. On a retry after a definitive
	// non-acceptance the claim already exists and is reused verbatim, so the retry
	// covers the SAME window with the SAME membership (no duplicate rows, no lost item).
	if err := s.claim(ctx, account, day, items); err != nil {
		return s.failPending(ctx, account, day, DigestReasonClaimError, 0, false, lastAttempt, err)
	}

	// Commit the claim BEFORE the SMTP conversation starts, carrying the ambiguity
	// marker this attempt STARTS with. A mailer that reports its own post-DATA boundary
	// starts DEFINITIVE and is narrowed at the real boundary; one that cannot starts
	// AMBIGUOUS, so the whole exchange is treated conservatively and an abandoned send is
	// never resent.
	barrier, reportsBoundary := s.mailer.(BarrierMailer)
	claimed, err := s.deliveries.MarkSending(ctx, account, day, s.now(), !reportsBoundary)
	if err != nil {
		return s.failPending(ctx, account, day, DigestReasonClaimError, 0, false, lastAttempt, err)
	}
	if !claimed {
		// A concurrent drive holds the claim. Idempotent no-op — never a second send.
		return OutcomeNoop, "", nil
	}

	sendErr := s.send(ctx, barrier, account, day, msg)
	if sendErr == nil {
		ok, err := s.markDelivered(ctx, account, day)
		if err != nil {
			// The mail is OUT but the terminal write failed. Emit NO delivered signal
			// (never a signal for a state that was not persisted) and re-drive: the row
			// stays `sending`, which the owned recovery pass rediscovers independently of
			// River, and the re-drive finalizes it without resending.
			return s.terminalUnpersisted(ctx, account, day, DigestReasonSendOutcomeUnknown, err)
		}
		if !ok {
			// The guard MISSED: a concurrent drive already finalized the row, so this
			// attempt persisted nothing. Report the no-op rather than a delivery the
			// durable record does not show, and fire no analytics for it.
			return OutcomeNoop, "", nil
		}
		if s.observer != nil {
			s.observer(ctx, account, len(items))
		}
		return OutcomeDelivered, "", nil
	}

	reason, code, ambiguous, permanent := classifyDigestSendFailure(sendErr)
	if ambiguous {
		// Acceptance is UNKNOWN: the body was transmitted but no verdict arrived. Zero
		// resend outranks a speculative repair, so finalize as terminal AMBIGUOUS.
		ok, err := s.markUnconfirmed(ctx, account, day, reason, code)
		if err != nil {
			return s.terminalUnpersisted(ctx, account, day, reason, err)
		}
		if !ok {
			// The guard missed. Either a concurrent drive already finalized the row, or the
			// mailer reported an ambiguous outcome WITHOUT having invoked the barrier that
			// records the window — a BarrierMailer contract violation. Both leave the row
			// nonterminal and the job "successful", which is indistinguishable from a
			// legitimate no-op unless it is said out loud. Recovery still owns the repair.
			s.logDelivery(ctx, slog.LevelWarn,
				"digest ambiguous outcome did not finalize: the durable window guard missed (concurrent drive, or a mailer reported Ambiguous without entering the barrier)",
				account, day, OutcomeNoop, reason, code)
			return OutcomeNoop, "", nil
		}
		s.isolateAccount(ctx, account, day, OutcomeUnconfirmed, reason, code, sendErr)
		return OutcomeUnconfirmed, reason, nil
	}

	// DEFINITIVE non-acceptance — the relay refused, or nothing was transmitted. The
	// claim is safe to release, because a retry cannot duplicate a message the relay
	// never accepted.
	if err := s.releaseToPending(ctx, account, day, reason, code); err != nil {
		return s.terminalUnpersisted(ctx, account, day, reason, err)
	}
	return s.failPending(ctx, account, day, reason, code, permanent, lastAttempt, sendErr)
}

// send performs the relay conversation, recording the genuinely ambiguous post-DATA
// window DURABLY at the moment the mailer reports it is about to be entered.
//
// The durable marker is what confines the terminal `unconfirmed` state to real
// ambiguity: a send abandoned before the barrier provably delivered nothing and is
// retried, while one abandoned after it is never resent. When the marker cannot be
// written the barrier FAILS CLOSED — the mailer abandons the send before the window
// opens, so an unresolvable outcome is never created.
//
// A mailer with no boundary to report already claimed the whole exchange as ambiguous at
// MarkSending, so it sends unchanged.
func (s *DigestService) send(ctx context.Context, barrier BarrierMailer, account uuid.UUID, day time.Time, msg Message) error {
	if barrier == nil {
		return s.mailer.Send(ctx, msg)
	}
	return barrier.SendReportingAmbiguity(ctx, msg, func(c context.Context) error {
		wctx, cancel := barrierWriteCtx(c)
		defer cancel()
		ok, err := s.deliveries.MarkAmbiguous(wctx, account, day, s.now())
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("notify: ambiguous window not recorded for %s/%s (claim no longer live)",
				account, day.Format(time.DateOnly))
		}
		return nil
	})
}

// failPending records a failed attempt on a row that is definitively NOT delivered.
//
// A permanent failure, or the final attempt, performs the pending → dead_letter
// transition: the OBSERVABLE terminal state (durable row + metric + error log + typed
// observer). It does NOT mark the digest delivered — no false "delivered" — and it
// returns nil, because the work is finished (terminally failed) and re-running it could
// only repeat the same permanent failure. Anything else bumps the attempt counter and
// returns the cause so River retries THIS ACCOUNT ALONE with its own backoff.
//
// If the terminal transition itself fails, no terminal signal is emitted and the
// recovery marker is returned instead, so the worker re-drives rather than discarding.
func (s *DigestService) failPending(ctx context.Context, account uuid.UUID, day time.Time, reason DigestReason, code int32, permanent, lastAttempt bool, cause error) (string, DigestReason, error) {
	if permanent || lastAttempt {
		terminal := reason
		if !permanent {
			terminal = DigestReasonAttemptsExhausted
		}
		ok, err := s.markDeadLetter(ctx, account, day, terminal, code)
		if err != nil {
			return s.terminalUnpersisted(ctx, account, day, terminal, err)
		}
		if !ok {
			// The guard missed: a concurrent drive already finalized the row. No terminal
			// signal, because this attempt persisted nothing.
			return OutcomeNoop, "", nil
		}
		s.isolateAccount(ctx, account, day, OutcomeDeadLetter, terminal, code, cause)
		return OutcomeDeadLetter, terminal, nil
	}
	if err := s.bumpAttempt(ctx, account, day, reason, code); err != nil {
		s.logDelivery(ctx, slog.LevelWarn, "digest attempt-bump write failed", account, day, OutcomeRetryableFailure, reason, code)
	}
	s.isolateAccount(ctx, account, day, OutcomeRetryableFailure, reason, code, cause)
	return OutcomeRetryableFailure, reason, cause
}

// terminalUnpersisted is the correlated-failure path: the attempt reached a terminal
// decision but the durable transition did NOT land — typically because PostgreSQL is
// unavailable, which is exactly when River's own completion/snooze write fails too.
//
// Two things must hold. First, NO terminal signal may fire, because monitoring must
// never report a durable terminal state that was never persisted. Second, the work must
// not be discarded: the returned marker makes the worker re-drive without consuming the
// exhausted attempt. Even if that snooze write also fails and River's rescuer discards
// the job, the row is still nonterminal, so the OWNED recovery pass rediscovers and
// re-enqueues it from the delivery table alone — recovery never depends on River state.
func (s *DigestService) terminalUnpersisted(ctx context.Context, account uuid.UUID, day time.Time, reason DigestReason, cause error) (string, DigestReason, error) {
	s.logDelivery(ctx, slog.LevelError, "digest terminal state write failed; re-driving (no terminal signal emitted)",
		account, day, OutcomeTerminalUnpersisted, reason, 0)
	return OutcomeTerminalUnpersisted, reason, fmt.Errorf(
		"notify: digest terminal state unpersisted for %s/%s: %w (cause: %w)",
		account, day.Format(time.DateOnly), jobs.ErrDigestTerminalUnpersisted, cause)
}

// claim writes the APPEND-ONLY digest header + membership snapshot in one transaction.
// A business-day conflict means an earlier attempt already claimed the day; the
// existing claim is reused verbatim so the retry covers the SAME window with the SAME
// membership (never a duplicate header, never a duplicated item).
func (s *DigestService) claim(ctx context.Context, account uuid.UUID, day time.Time, items []Notification) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	qtx := db.New(tx)

	header, err := qtx.InsertDigest(ctx, db.InsertDigestParams{
		MarketplaceAccountID: account,
		BusinessDay:          pgtype.Date{Time: day, Valid: true},
		GeneratedAt:          s.now().UTC(),
		ItemCount:            int32(len(items)),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // already claimed by an earlier attempt (idempotent)
	}
	if err != nil {
		return err
	}
	// Membership snapshot: each item carries the SHARED event id (NOT-001).
	for _, n := range items {
		if _, err := qtx.InsertDigestItem(ctx, db.InsertDigestItemParams{
			DigestID:       header.ID,
			NotificationID: n.ID,
			EventID:        n.EventID,
		}); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// classifyDigestSendFailure maps a Mailer failure onto the bounded delivery vocabulary.
// A *SendError carries the mailer's own closed classification (never relay text). Any
// other Mailer implementation is treated CONSERVATIVELY as a definitive, transient
// failure: definitive so the claim may be released and retried, transient so a
// third-party error is never mistaken for a permanent quarantine.
func classifyDigestSendFailure(err error) (reason DigestReason, code int32, ambiguous, permanent bool) {
	var se *SendError
	if errors.As(err, &se) {
		return boundedSendReason(se.Reason), int32(se.Code), se.Ambiguous, se.Permanent()
	}
	return DigestReasonSendError, 0, false, false
}

// boundedSendReason projects a mailer reason onto the closed digest vocabulary.
//
// SendReason is an exported plain string type, so an injected or third-party Mailer can
// hand-build a *SendError carrying ANY token — and that token becomes a metric label and
// the durable `last_reason`. Trusting it would reintroduce unbounded label cardinality
// and, because a 550 commonly echoes the recipient address, a PII leak into durable
// state (free-text containment, §4.6 / LOC-001). Anything outside the closed set
// therefore degrades to the bounded fallback rather than being persisted verbatim.
func boundedSendReason(r SendReason) DigestReason {
	if slices.Contains(SendReasons(), r) {
		return DigestReason(r)
	}
	return DigestReasonSendError
}

// normalizeBusinessDay reduces an instant to its UTC calendar day, so a pinned day is
// identical however it was carried (row, job args, or clock).
func normalizeBusinessDay(day time.Time) time.Time {
	u := day.UTC()
	return time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC)
}

// EnsureDelivery opens the durable (account, day) work row, and — when an enqueuer is
// wired, enqueue is requested, and the row is newly created — enqueues its driving job
// in the SAME transaction (transactional enqueue: the row and its job commit or roll
// back together). Opening the row is idempotent, so repeated passes converge on one
// logical digest rather than creating a second unit of work.
//
// enqueued reports whether this call actually inserted a driving job. It is FALSE for
// the common re-run in which the row already existed, so a caller counting work can
// report the number of jobs it really enqueued rather than the number of accounts it
// looked at.
func (s *DigestService) EnsureDelivery(ctx context.Context, account uuid.UUID, day time.Time, enqueue bool) (enqueued bool, err error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	created, err := s.deliveries.Ensure(ctx, tx, account, day)
	if err != nil {
		return false, err
	}
	enqueued = created && enqueue && s.enqueuer != nil
	if enqueued {
		if err := s.enqueuer.EnqueueDigestAccountTx(ctx, tx, account, day); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return enqueued, nil
}

// NonterminalDeliveries is the OWNED RECOVERY source (issue #124 / PD-4): every
// nonterminal (account, business_day) row, of ANY historical day, read directly from
// the durable delivery table.
//
// It deliberately consults NO River state. River's completion/snooze write and the
// terminal projection write share one PostgreSQL dependency, so a correlated outage can
// leave a job `running` on its final attempt — which River's rescuer then DISCARDS —
// while the row is still nonterminal. And because `GenerateAll` only ever finalizes the
// CURRENT closed day, an abandoned older day would never be revisited once the day
// advanced. Rediscovering from the pinned rows here survives both.
//
// A `sending` row is only rediscovered once STALE, so recovery never races a live send.
func (s *DigestService) NonterminalDeliveries(ctx context.Context, now time.Time) ([]DigestDelivery, error) {
	return s.deliveries.ListNonterminal(ctx, now.UTC().Add(-sendingStaleAfter), recoveryBatchLimit)
}

// GenerateAll is the periodic digest FAN-OUT pass. It sends nothing itself. It:
//
//  1. DISCOVERS this business day's work — one durable (account, business_day) row per
//     account, each committed together with its own driving job (transactional
//     enqueue); and
//  2. RECOVERS abandoned work — every nonterminal row of ANY historical day,
//     rediscovered from the delivery table alone and re-enqueued.
//
// Both steps are idempotent, so repeated passes converge rather than duplicate. It
// returns the number of per-account jobs enqueued this pass.
//
// Splitting discovery from delivery is what makes tenant isolation real: a poison
// account can no longer abort the pass, delay its successors, or share a retry budget
// with them, because each account owns a separate durable row, a separate job, and a
// separate retry budget on a separately bounded queue. Ordering is therefore
// irrelevant to delivery completeness. A per-account DISCOVERY failure is still
// ISOLATED (observed, contained, aggregated) so one unenqueueable account cannot stop
// the rest from being enqueued.
func (s *DigestService) GenerateAll(ctx context.Context) (int, error) {
	day := s.FinalizedBusinessDay()
	ids, err := db.New(s.pool).ListMarketplaceAccountIDs(ctx)
	if err != nil {
		return 0, err
	}
	discovered, discoverErr := s.generateEach(ctx, day, ids, func(c context.Context, id uuid.UUID) (bool, error) {
		return s.EnsureDelivery(c, id, day, true)
	})
	recovered, recoverErr := s.RecoverNonterminal(ctx)
	return discovered + recovered, errors.Join(discoverErr, recoverErr)
}

// RecoverNonterminal re-enqueues every nonterminal delivery row. It runs on every
// fan-out pass and is also the operator-facing repair entry point named by
// runbooks/digest-delivery.md. It is the OWNED recovery mechanism required because River's
// own durability shares the PostgreSQL
// boundary that the terminal write depends on: when both fail together the job can be
// discarded while the row stays nonterminal, and once the business day advances the
// normal pass would never look at that day again. Re-enqueueing from the pinned rows
// repairs exactly that window, and it is safe to run every pass because a terminal row
// is invisible here and a re-driven nonterminal row never resends.
func (s *DigestService) RecoverNonterminal(ctx context.Context) (int, error) {
	rows, err := s.NonterminalDeliveries(ctx, s.now())
	if err != nil {
		return 0, err
	}
	enqueued := 0
	var failures []error
	for _, r := range rows {
		if cerr := ctx.Err(); cerr != nil {
			failures = append(failures, cerr)
			break
		}
		if s.enqueuer == nil {
			break // nothing wired to drive the work; the durable rows are not lost
		}
		if err := s.reenqueue(ctx, r.Account, r.BusinessDay); err != nil {
			s.isolateAccount(ctx, r.Account, r.BusinessDay, OutcomeRetryableFailure, DigestReasonClaimError, 0, err)
			failures = append(failures, fmt.Errorf("recover account %s day %s: %w",
				r.Account, r.BusinessDay.Format(time.DateOnly), err))
			continue
		}
		enqueued++
	}
	if enqueued > 0 {
		recordRecoveryReenqueue(ctx, enqueued)
	}
	return enqueued, errors.Join(failures...)
}

// reenqueue re-drives one nonterminal row. The enqueue is transactional for
// consistency with every other job in the platform; River's own per-(account, day)
// uniqueness collapses a re-enqueue that duplicates work already in flight.
func (s *DigestService) reenqueue(ctx context.Context, account uuid.UUID, day time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := s.enqueuer.EnqueueDigestAccountTx(ctx, tx, account, day); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// generateEach runs perAccount for every account id, ISOLATING a per-account failure
// (issue #124, identity/tenant quarantine, §4.6): one account's failure can neither
// abort nor leak into another account's delivery. A failed account is OBSERVED
// (metric + warn log + typed observer) — never silently swallowed — the pass
// continues to the next account, and at the end it returns an AGGREGATE error (nil
// when every account succeeded) so the River job retries the failed account(s). That
// retry is safe: an already-sent account is a same-day idempotent no-op. It returns
// the count of accounts for which perAccount reported it did WORK — for the fan-out,
// the number of driving jobs actually enqueued, which is 0 on a re-run whose rows all
// already exist. perAccount is injected so the
// isolation loop is unit-testable without a database.
func (s *DigestService) generateEach(ctx context.Context, day time.Time, ids []uuid.UUID, perAccount func(context.Context, uuid.UUID) (bool, error)) (int, error) {
	sent := 0
	var failures []error
	for _, id := range ids {
		// Respect cancellation/deadline: stop hammering a dead context, but surface
		// the reason so the pass fails closed (River retries) rather than reporting a
		// clean finish it did not achieve.
		if cerr := ctx.Err(); cerr != nil {
			failures = append(failures, cerr)
			break
		}
		ok, err := perAccount(ctx, id)
		if err != nil {
			s.isolateAccount(ctx, id, day, OutcomeRetryableFailure, DigestReasonClaimError, 0, err)
			failures = append(failures, fmt.Errorf("account %s: %w", id, err))
			continue
		}
		if ok {
			sent++
		}
	}
	return sent, errors.Join(failures...)
}

// isolateAccount records an observable, contained per-account delivery failure
// (issue #124). It emits the metric, a structured log, and the typed observer, and it
// mutates NO other account: the failure is contained, not swallowed.
//
// Everything it records is BOUNDED — the account id and pinned day (technical
// identifiers), the outcome token, the closed-set reason, and a numeric status code.
// The relay's response text is NEVER a field here: a 550 commonly echoes the recipient
// address, and free-text containment plus PII are never-cut (§4.6 / LOC-001).
// cause, when non-nil, is handed to the in-process typed observer so a caller can still
// inspect the original failure; it is deliberately NOT a log field.
func (s *DigestService) isolateAccount(ctx context.Context, account uuid.UUID, day time.Time, outcome string, reason DigestReason, code int32, cause error) {
	recordAccountFailure(ctx, reason)
	// Both TERMINAL non-delivery outcomes signal identically and at the same severity,
	// whichever route reached them: a digest that will never arrive is an ERROR, and it
	// fires the metric, the log, and the typed observer exactly once.
	level := slog.LevelWarn
	msg := "digest account isolated: attempt failed (contained; other accounts unaffected)"
	switch outcome {
	case OutcomeDeadLetter:
		level = slog.LevelError
		msg = "digest DEAD-LETTERED (permanent failure; NOT delivered)"
	case OutcomeUnconfirmed:
		level = slog.LevelError
		msg = "digest finalized UNCONFIRMED: send acceptance could not be established (NOT delivered, never resent)"
	}
	s.logDelivery(ctx, level, msg, account, day, outcome, reason, code)
	if s.acctFail == nil {
		return
	}
	bounded := &DigestFailure{Account: account, BusinessDay: day, Outcome: outcome, Reason: reason, Code: code}
	if cause == nil {
		s.acctFail(ctx, account, bounded)
		return
	}
	s.acctFail(ctx, account, fmt.Errorf("%w: %w", bounded, cause))
}

// DigestFailure is the BOUNDED per-account failure handed to observers and logs. It
// deliberately carries no relay text and no recipient address — only technical
// identifiers, a closed-set reason, and a numeric status code.
type DigestFailure struct {
	Account     uuid.UUID
	BusinessDay time.Time
	Outcome     string
	Reason      DigestReason
	Code        int32
}

// Error renders the bounded failure.
func (e *DigestFailure) Error() string {
	return fmt.Sprintf("notify: digest %s for account %s day %s (reason=%s, code=%d)",
		e.Outcome, e.Account, e.BusinessDay.Format(time.DateOnly), e.Reason, e.Code)
}

// logDelivery emits one structured delivery-boundary log with the stable bounded key
// set. A nil logger is a no-op.
func (s *DigestService) logDelivery(ctx context.Context, level slog.Level, msg string, account uuid.UUID, day time.Time, outcome string, reason DigestReason, code int32) {
	if s.logger == nil {
		return
	}
	s.logger.Log(ctx, level, msg,
		"account_id", account,
		"business_day", day.Format(time.DateOnly),
		"outcome", outcome,
		"reason", string(reason),
		"status_code", code)
}

// isolate records an observable skip of one persisted digest row that violates the
// closed message schema. It emits the metric, the warn log (technical identifiers
// only — never Persian copy), and the typed observer. It performs NO write: the row
// stays in the append-only store untouched (issue #126, never-cut: no silent drop,
// no UPDATE/DELETE).
func (s *DigestService) isolate(ctx context.Context, account uuid.UUID, n Notification, verr *MessageValidationError) {
	recordIsolation(ctx, verr)
	if s.logger != nil {
		s.logger.WarnContext(ctx, "digest row isolated: invalid message shape",
			"account_id", account, "notification_id", n.ID, "category", string(n.Category),
			"surface", verr.Surface, "reason", string(verr.Reason),
			"title_key", n.TitleKey, "body_key", n.BodyKey, "slot", verr.Slot)
	}
	if s.isolated != nil {
		s.isolated(ctx, account, n.ID, n.TitleKey, n.BodyKey, verr.Reason)
	}
}

// renderDigest builds the email entirely from catalog keys in the target locale.
// Each line references its notification's SHARED event id, so the in-app item and
// the digest line are provably the same event (NOT-001). The email LINKS to the
// briefing (§6.8) — it never re-renders it.
func renderDigest(target Target, items []Notification) (Message, error) {
	count := strconv.Itoa(len(items))
	subject, err := Render(target.Locale, KeyDigestSubject, map[string]string{"count": count})
	if err != nil {
		return Message{}, err
	}
	intro, err := Render(target.Locale, KeyDigestIntro, map[string]string{"count": count})
	if err != nil {
		return Message{}, err
	}
	link, err := Render(target.Locale, KeyDigestBriefingLink, map[string]string{"url": target.BriefingURL})
	if err != nil {
		return Message{}, err
	}
	footer, err := Render(target.Locale, KeyDigestFooter, nil)
	if err != nil {
		return Message{}, err
	}

	var b strings.Builder
	b.WriteString(intro)
	b.WriteString("\n\n")
	for _, n := range items {
		line, err := Render(target.Locale, n.TitleKey, n.BodyParams)
		if err != nil {
			return Message{}, err
		}
		// The shared event id is emitted as a technical identifier (LTR) so the
		// in-app item and this digest line are the SAME event (NOT-001).
		b.WriteString("- ")
		b.WriteString(line)
		b.WriteString(" [event:")
		b.WriteString(n.EventID.String())
		b.WriteString("]\n")
	}
	b.WriteString("\n")
	b.WriteString(link)
	b.WriteString("\n\n")
	b.WriteString(footer)

	return Message{To: target.Email, Subject: subject, Body: b.String()}, nil
}
