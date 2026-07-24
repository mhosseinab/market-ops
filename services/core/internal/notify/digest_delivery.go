package notify

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mhosseinab/market-ops/services/core/internal/db"
)

// Durable per-(account, business_day) digest delivery state (issue #124).
//
// The daily digest is a MULTI-TENANT FAN-OUT, and a fan-out must isolate tenant
// failures: one account's missing recipient, unsupported locale, render error, or SMTP
// problem may retry or quarantine, but it must never block an independent account's
// scheduled delivery. A purely in-process loop cannot provide that, because it has no
// durable per-account work record: a failure has nowhere to persist its own retry
// state, and a failure delayed past UTC midnight loses the business day it was meant to
// finalize.
//
// notification_digest_deliveries is that record. It is a DELIVERY-STATE PROJECTION —
// the only mutable surface the digest owns — sitting beside the APPEND-ONLY
// notification_digests header and notification_digest_items membership, exactly as
// notification_urgent_outbox does for the urgent channel. Its (account, business_day)
// uniqueness is the stable idempotency key that makes every retry, duplicate fan-out,
// and recovery re-enqueue converge on one logical digest.

// Delivery-state values (mirrors the migration CHECK). Terminal states are delivered,
// skipped, unconfirmed, and dead_letter; pending and sending are the nonterminal set
// the owned recovery pass rediscovers.
const (
	// DigestStatePending — discovered, not yet attempted, or an attempt failed BEFORE
	// the send was initiated. Definitively not delivered, so retrying is safe.
	DigestStatePending = "pending"
	// DigestStateSending — the send was INITIATED and its outcome is not yet known.
	// Committed before the SMTP conversation starts, so a crash mid-send leaves this
	// ambiguous marker rather than a resend hazard.
	DigestStateSending = "sending"
	// DigestStateDelivered — the relay accepted the message AND this write landed.
	DigestStateDelivered = "delivered"
	// DigestStateSkipped — the day had nothing sendable. Terminal and observed.
	DigestStateSkipped = "skipped"
	// DigestStateUnconfirmed — TERMINAL AMBIGUOUS: the send was initiated but
	// acceptance could never be established. It does NOT claim delivery and it is never
	// resent (zero resend outranks a speculative repair — idempotency is never-cut).
	DigestStateUnconfirmed = "unconfirmed"
	// DigestStateDeadLetter — TERMINAL PERMANENT FAILURE, definitively not delivered.
	DigestStateDeadLetter = "dead_letter"
)

// DigestReason is a BOUNDED machine token recorded on the delivery projection and on
// structured logs. Like SendReason it is a closed vocabulary of LTR technical
// identifiers — never rendered copy, never marketplace or relay free text, never a
// recipient address (free-text containment + PII, §4.6 / LOC-001).
type DigestReason string

// The closed set of digest-delivery reasons. Send failures reuse the mailer's
// SendReason tokens (see DigestReasonFromSend), which are bounded by the same rule.
const (
	// DigestReasonEmptyDay — the closed day held no digest-eligible notification.
	DigestReasonEmptyDay DigestReason = "empty_day"
	// DigestReasonAllItemsIsolated — every eligible row violated the closed message
	// schema and was isolated, so nothing was sendable.
	DigestReasonAllItemsIsolated DigestReason = "all_items_isolated"
	// DigestReasonUnsendableTarget — no recipient, or an unsupported locale.
	DigestReasonUnsendableTarget DigestReason = DigestReason(reasonUnsendableTarget)
	// DigestReasonResolveError — the target could not be resolved.
	DigestReasonResolveError DigestReason = DigestReason(reasonResolveError)
	// DigestReasonRenderError — the message could not be rendered from the catalog.
	DigestReasonRenderError DigestReason = DigestReason(reasonRenderError)
	// DigestReasonQueryError — the day's eligible notifications could not be read.
	DigestReasonQueryError DigestReason = "query_error"
	// DigestReasonClaimError — the header/membership claim transaction failed.
	DigestReasonClaimError DigestReason = "claim_error"
	// DigestReasonSendError — a send failure with no bounded mailer classification
	// (an injected or third-party Mailer that does not return a *SendError).
	DigestReasonSendError DigestReason = DigestReason(reasonSendError)
	// DigestReasonAttemptsExhausted — the final attempt still failed; terminal.
	DigestReasonAttemptsExhausted DigestReason = "attempts_exhausted"
	// DigestReasonSendOutcomeUnknown — acceptance could never be established; the row
	// is finalized as unconfirmed and never resent.
	DigestReasonSendOutcomeUnknown DigestReason = "send_outcome_unknown"
	// DigestReasonCanceled — the attempt's context was canceled or its deadline
	// elapsed before the work completed.
	DigestReasonCanceled DigestReason = "canceled"
	// DigestReasonSendNotInitiated — a previous attempt was abandoned while holding the
	// 'sending' claim but BEFORE the exchange entered its ambiguous post-DATA window.
	// Nothing could have been accepted, so the claim is released and retried rather than
	// written off as unconfirmed.
	DigestReasonSendNotInitiated DigestReason = "send_not_initiated"
)

// DigestReasons returns the closed digest-reason set (stable order) so a test can pin
// the vocabulary as bounded LTR technical tokens.
func DigestReasons() []DigestReason {
	return []DigestReason{
		DigestReasonEmptyDay, DigestReasonAllItemsIsolated, DigestReasonUnsendableTarget,
		DigestReasonResolveError, DigestReasonRenderError, DigestReasonQueryError,
		DigestReasonClaimError, DigestReasonSendError, DigestReasonAttemptsExhausted,
		DigestReasonSendOutcomeUnknown, DigestReasonCanceled, DigestReasonSendNotInitiated,
	}
}

// DigestDelivery is the notify-domain view of one durable delivery row. It is
// decoupled from db.NotificationDigestDelivery so the worker's idempotency,
// dead-letter, and recovery decisions are testable against a fake with no database.
type DigestDelivery struct {
	Account     uuid.UUID
	BusinessDay time.Time
	State       string
	Attempts    int32
	Reason      string
	StatusCode  int32
	UpdatedAt   time.Time
	// Ambiguous records whether the in-flight (or abandoned) send had entered its
	// genuinely ambiguous post-DATA window. False means the relay provably holds
	// nothing, so the claim is safe to release and retry; true means acceptance cannot
	// be disproven, so the row finalizes 'unconfirmed' and is never resent.
	Ambiguous bool
}

// Terminal reports whether the row has reached a state that must never be re-driven.
// A terminal row is the zero-resend guarantee: recovery skips it and a duplicate drive
// is an idempotent no-op.
func (d DigestDelivery) Terminal() bool {
	switch d.State {
	case DigestStateDelivered, DigestStateSkipped, DigestStateUnconfirmed, DigestStateDeadLetter:
		return true
	default:
		return false
	}
}

// DigestDeliveryStore reads and transitions the durable delivery projection. Every
// transition is GUARDED on its source state in SQL, so a concurrent or duplicate drive
// matches nothing and is an idempotent no-op. Injecting the interface lets a test
// inject a failing terminal write (the correlated-failure case) without a fault-
// injection proxy in front of PostgreSQL.
type DigestDeliveryStore interface {
	// Ensure opens the pending row for (account, day) inside the caller's transaction,
	// so the row and its driving job commit atomically (transactional enqueue). An
	// existing row is left untouched. created reports whether this call inserted it.
	Ensure(ctx context.Context, tx pgx.Tx, account uuid.UUID, day time.Time) (created bool, err error)
	// Get reads the row; found=false with no error when absent.
	Get(ctx context.Context, account uuid.UUID, day time.Time) (rec DigestDelivery, found bool, err error)
	// MarkSending performs the guarded pending → sending claim. claimed=false means
	// another drive already holds the claim (or the row is terminal) — an idempotent
	// no-op, never a duplicate send. ambiguous is the marker the attempt STARTS with:
	// false when the mailer can report its own post-DATA boundary (the marker is then
	// narrowed upward by MarkAmbiguous at the real boundary), true when it cannot and
	// the whole exchange must be treated conservatively as the ambiguous window.
	MarkSending(ctx context.Context, account uuid.UUID, day time.Time, at time.Time, ambiguous bool) (claimed bool, err error)
	// MarkAmbiguous raises the ambiguity marker on a live 'sending' claim at the moment
	// the exchange enters its genuinely ambiguous post-DATA window. applied=false means
	// the claim is no longer live (a guard miss), which the caller must NOT treat as a
	// recorded window.
	MarkAmbiguous(ctx context.Context, account uuid.UUID, day time.Time, at time.Time) (applied bool, err error)
	// MarkDelivered performs the guarded sending → delivered transition. applied=false
	// is a guard MISS: the durable row did not change, so no delivered signal may fire.
	MarkDelivered(ctx context.Context, account uuid.UUID, day time.Time, at time.Time) (applied bool, err error)
	// ReleaseToPending performs the guarded sending → pending transition after a
	// DEFINITIVE non-acceptance, so a retry cannot duplicate an accepted message.
	ReleaseToPending(ctx context.Context, account uuid.UUID, day time.Time, reason DigestReason, code int32, at time.Time) error
	// MarkSkipped performs the guarded pending → skipped transition. applied=false is a
	// guard miss (no terminal signal may fire).
	MarkSkipped(ctx context.Context, account uuid.UUID, day time.Time, reason DigestReason, at time.Time) (applied bool, err error)
	// MarkDeadLetter performs the guarded pending → dead_letter transition. applied=false
	// is a guard miss (no terminal signal may fire).
	MarkDeadLetter(ctx context.Context, account uuid.UUID, day time.Time, reason DigestReason, code int32, at time.Time) (applied bool, err error)
	// MarkUnconfirmed performs the guarded sending → unconfirmed transition. It is
	// additionally guarded on the durable ambiguity marker, so a row that provably never
	// entered the post-DATA window can never be written off as unconfirmed.
	// applied=false is a guard miss (no terminal signal may fire).
	MarkUnconfirmed(ctx context.Context, account uuid.UUID, day time.Time, reason DigestReason, code int32, at time.Time) (applied bool, err error)
	// BumpAttempt records a transient failed attempt while the row stays pending.
	BumpAttempt(ctx context.Context, account uuid.UUID, day time.Time, reason DigestReason, code int32, at time.Time) error
	// ListNonterminal is the OWNED RECOVERY source: every nonterminal row of any
	// historical business day. It consults no River state, so it survives the window in
	// which River's own completion write fails alongside the terminal projection write.
	ListNonterminal(ctx context.Context, staleBefore time.Time, limit int32) ([]DigestDelivery, error)
}

// DBDigestDeliveryStore is the pgx-backed DigestDeliveryStore.
type DBDigestDeliveryStore struct{ pool *pgxpool.Pool }

// NewDBDigestDeliveryStore builds the delivery store over the pool.
func NewDBDigestDeliveryStore(pool *pgxpool.Pool) *DBDigestDeliveryStore {
	return &DBDigestDeliveryStore{pool: pool}
}

// digestDay wraps a UTC business day as the pgtype.Date the queries take.
func digestDay(day time.Time) pgtype.Date {
	return pgtype.Date{Time: day.UTC(), Valid: true}
}

// boundedReason renders a bounded token for persistence; an empty reason stores NULL.
func boundedReason(r DigestReason) pgtype.Text {
	return pgtype.Text{String: string(r), Valid: r != ""}
}

func toDelivery(row db.NotificationDigestDelivery) DigestDelivery {
	return DigestDelivery{
		Account:     row.MarketplaceAccountID,
		BusinessDay: row.BusinessDay.Time.UTC(),
		State:       row.DeliveryState,
		Attempts:    row.Attempts,
		Reason:      row.LastReason.String,
		StatusCode:  row.LastStatusCode,
		UpdatedAt:   row.UpdatedAt.UTC(),
		Ambiguous:   row.Ambiguous,
	}
}

// Ensure inserts the pending row inside the caller's transaction. A conflict means the
// row already exists (created=false) — not an error, and never a second logical digest.
func (s *DBDigestDeliveryStore) Ensure(ctx context.Context, tx pgx.Tx, account uuid.UUID, day time.Time) (bool, error) {
	_, err := db.New(tx).EnsureDigestDelivery(ctx, db.EnsureDigestDeliveryParams{
		MarketplaceAccountID: account,
		BusinessDay:          digestDay(day),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// Get reads the durable row; a missing row is (found=false, nil error).
func (s *DBDigestDeliveryStore) Get(ctx context.Context, account uuid.UUID, day time.Time) (DigestDelivery, bool, error) {
	row, err := db.New(s.pool).GetDigestDelivery(ctx, db.GetDigestDeliveryParams{
		MarketplaceAccountID: account,
		BusinessDay:          digestDay(day),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return DigestDelivery{}, false, nil
	}
	if err != nil {
		return DigestDelivery{}, false, err
	}
	return toDelivery(row), true, nil
}

// applied normalizes a guarded UPDATE result. pgx.ErrNoRows means the guard MATCHED
// NOTHING — the durable row did not change — which every caller must distinguish from a
// landed write, because a signal for a state that was never persisted is exactly the
// failure the terminal-unpersisted path exists to prevent.
func applied(err error) (bool, error) {
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// MarkSending claims the row for a send. A guard miss (pgx.ErrNoRows) means another
// drive holds the claim or the row is terminal: claimed=false, no error, no send.
func (s *DBDigestDeliveryStore) MarkSending(ctx context.Context, account uuid.UUID, day time.Time, at time.Time, ambiguous bool) (bool, error) {
	_, err := db.New(s.pool).MarkDigestDeliverySending(ctx, db.MarkDigestDeliverySendingParams{
		MarketplaceAccountID: account,
		BusinessDay:          digestDay(day),
		UpdatedAt:            at.UTC(),
		Ambiguous:            ambiguous,
	})
	return applied(err)
}

// MarkAmbiguous raises the ambiguity marker on a live claim at the post-DATA boundary.
func (s *DBDigestDeliveryStore) MarkAmbiguous(ctx context.Context, account uuid.UUID, day time.Time, at time.Time) (bool, error) {
	_, err := db.New(s.pool).MarkDigestDeliveryAmbiguous(ctx, db.MarkDigestDeliveryAmbiguousParams{
		MarketplaceAccountID: account,
		BusinessDay:          digestDay(day),
		UpdatedAt:            at.UTC(),
	})
	return applied(err)
}

// MarkDelivered performs the guarded sending → delivered transition. A guard miss
// (applied=false) means the row was already finalized by a concurrent drive — an
// idempotent no-op that must NOT be reported as a delivery this attempt landed.
func (s *DBDigestDeliveryStore) MarkDelivered(ctx context.Context, account uuid.UUID, day time.Time, at time.Time) (bool, error) {
	_, err := db.New(s.pool).MarkDigestDeliveryDelivered(ctx, db.MarkDigestDeliveryDeliveredParams{
		MarketplaceAccountID: account,
		BusinessDay:          digestDay(day),
		UpdatedAt:            at.UTC(),
	})
	return applied(err)
}

// ReleaseToPending performs the guarded sending → pending transition.
func (s *DBDigestDeliveryStore) ReleaseToPending(ctx context.Context, account uuid.UUID, day time.Time, reason DigestReason, code int32, at time.Time) error {
	_, err := db.New(s.pool).ReleaseDigestDeliveryToPending(ctx, db.ReleaseDigestDeliveryToPendingParams{
		MarketplaceAccountID: account,
		BusinessDay:          digestDay(day),
		UpdatedAt:            at.UTC(),
		LastReason:           boundedReason(reason),
		LastStatusCode:       code,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	return err
}

// MarkSkipped performs the guarded pending → skipped transition.
func (s *DBDigestDeliveryStore) MarkSkipped(ctx context.Context, account uuid.UUID, day time.Time, reason DigestReason, at time.Time) (bool, error) {
	_, err := db.New(s.pool).MarkDigestDeliverySkipped(ctx, db.MarkDigestDeliverySkippedParams{
		MarketplaceAccountID: account,
		BusinessDay:          digestDay(day),
		UpdatedAt:            at.UTC(),
		LastReason:           boundedReason(reason),
	})
	return applied(err)
}

// MarkDeadLetter performs the guarded pending → dead_letter transition.
func (s *DBDigestDeliveryStore) MarkDeadLetter(ctx context.Context, account uuid.UUID, day time.Time, reason DigestReason, code int32, at time.Time) (bool, error) {
	_, err := db.New(s.pool).MarkDigestDeliveryDeadLetter(ctx, db.MarkDigestDeliveryDeadLetterParams{
		MarketplaceAccountID: account,
		BusinessDay:          digestDay(day),
		UpdatedAt:            at.UTC(),
		LastReason:           boundedReason(reason),
		LastStatusCode:       code,
	})
	return applied(err)
}

// MarkUnconfirmed performs the guarded sending → unconfirmed transition. The query is
// additionally guarded on the ambiguity marker, so a definitively-not-transmitted row
// yields applied=false rather than a false "we may have delivered".
func (s *DBDigestDeliveryStore) MarkUnconfirmed(ctx context.Context, account uuid.UUID, day time.Time, reason DigestReason, code int32, at time.Time) (bool, error) {
	_, err := db.New(s.pool).MarkDigestDeliveryUnconfirmed(ctx, db.MarkDigestDeliveryUnconfirmedParams{
		MarketplaceAccountID: account,
		BusinessDay:          digestDay(day),
		UpdatedAt:            at.UTC(),
		LastReason:           boundedReason(reason),
		LastStatusCode:       code,
	})
	return applied(err)
}

// BumpAttempt records a transient failed attempt while the row stays pending.
func (s *DBDigestDeliveryStore) BumpAttempt(ctx context.Context, account uuid.UUID, day time.Time, reason DigestReason, code int32, at time.Time) error {
	_, err := db.New(s.pool).BumpDigestDeliveryAttempt(ctx, db.BumpDigestDeliveryAttemptParams{
		MarketplaceAccountID: account,
		BusinessDay:          digestDay(day),
		UpdatedAt:            at.UTC(),
		LastReason:           boundedReason(reason),
		LastStatusCode:       code,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	return err
}

// ListNonterminal returns the bounded nonterminal backlog, oldest business day first.
func (s *DBDigestDeliveryStore) ListNonterminal(ctx context.Context, staleBefore time.Time, limit int32) ([]DigestDelivery, error) {
	rows, err := db.New(s.pool).ListNonterminalDigestDeliveries(ctx, db.ListNonterminalDigestDeliveriesParams{
		StaleBefore: staleBefore.UTC(),
		RowLimit:    limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]DigestDelivery, 0, len(rows))
	for _, r := range rows {
		out = append(out, toDelivery(r))
	}
	return out, nil
}
