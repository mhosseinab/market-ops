package notify_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mhosseinab/market-ops/services/core/internal/notify"
)

// Issue #124 review cycle 1 — durability of the per-attempt terminal write.
//
// The defect these tests close: every durable delivery transition ran on the ATTEMPT's
// own context. River bounds that context with the worker's Timeout, so when the deadline
// fired mid-send the mailer correctly reported a DEFINITIVE non-acceptance (nothing was
// transmitted) and the follow-up ReleaseToPending immediately failed on the dead context.
// The row was stranded in `sending`, the next drive unconditionally finalized it
// `unconfirmed`, and the worker snoozed rather than consuming an attempt — so the FIRST
// relay hang was a silent, terminal, never-retried non-delivery.
//
// The guarantee now under test: a durable transition is DETACHED from the attempt's
// cancellation and separately bounded, and `unconfirmed` is confined to the genuinely
// ambiguous post-DATA window.

// hangingMailer blocks until the caller's context is done, then reports the DEFINITIVE
// timeout the real SMTP mailer reports when the deadline elapses before the relay ever
// accepted the body (nothing was transmitted). It never contacts a relay.
type hangingMailer struct {
	mu      sync.Mutex
	entered chan struct{}
	once    sync.Once
}

func newHangingMailer() *hangingMailer {
	return &hangingMailer{entered: make(chan struct{})}
}

func (m *hangingMailer) Send(ctx context.Context, _ notify.Message) error {
	m.once.Do(func() { close(m.entered) })
	<-ctx.Done()
	// Ambiguous is deliberately FALSE: this models the pre-DATA hang, which is a
	// definitive non-acceptance — the relay never saw a body terminator.
	return &notify.SendError{Reason: notify.ReasonSMTPTimeout}
}

func (m *hangingMailer) waitEntered(t *testing.T) {
	t.Helper()
	select {
	case <-m.entered:
	case <-time.After(20 * time.Second):
		t.Fatal("timed out waiting for the send to be entered")
	}
}

// TestDeliverAccountDay_AttemptDeadlineDuringSendDoesNotStrandTheClaim is F1's
// reproduction. The attempt's context expires while the relay hangs. The claim MUST be
// released durably — on a context detached from the expired attempt — so the account
// retries on its own budget instead of being stranded in `sending` and silently
// finalized as an unretryable `unconfirmed`.
func TestDeliverAccountDay_AttemptDeadlineDuringSendDoesNotStrandTheClaim(t *testing.T) {
	pool, q := newPool(t)
	account := seedAccount(t, q)

	day := time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC)
	at := day.Add(30 * time.Hour)
	insertNotifAt(t, pool, account, uuid.New(), "dl-"+uuid.NewString(), "v1", day.Add(2*time.Hour))

	svc := digestFor(pool, newHangingMailer(), at)
	if err := svc.EnsureDelivery(context.Background(), account, day, false); err != nil {
		t.Fatalf("ensure: %v", err)
	}

	// The bounded per-attempt work deadline River imposes, compressed for the test.
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	outcome, err := svc.DeliverAccountDay(ctx, account, day, false)
	if err == nil {
		t.Fatal("a deadline-expired attempt returned no error; River would treat it as success")
	}
	if outcome == notify.OutcomeTerminalUnpersisted {
		t.Fatalf("outcome = %q: the durable release ran on the already-cancelled attempt context", outcome)
	}
	if outcome != notify.OutcomeRetryableFailure {
		t.Fatalf("outcome = %q, want %q (a definitive non-acceptance is retryable)", outcome, notify.OutcomeRetryableFailure)
	}
	if state := deliveryState(t, pool, account, day); state != notify.DigestStatePending {
		t.Fatalf("delivery state = %q, want %q — a deadline-timed-out attempt must release its claim durably, never strand it in `sending`",
			state, notify.DigestStatePending)
	}
}

// TestDeliverAccountDay_StrandedSendingWithDefinitiveOutcomeIsReleasedNotUnconfirmed is
// F2's reproduction. A row abandoned in `sending` BEFORE the genuinely ambiguous
// post-DATA window was entered is definitively not delivered, so it must be released to
// `pending` and retried — never finalized as the terminal ambiguous state, which would
// claim "we may have delivered" for a known non-delivery.
func TestDeliverAccountDay_StrandedSendingWithDefinitiveOutcomeIsReleasedNotUnconfirmed(t *testing.T) {
	ctx := context.Background()
	pool, q := newPool(t)
	account := seedAccount(t, q)

	day := time.Date(2026, 9, 9, 0, 0, 0, 0, time.UTC)
	at := day.Add(30 * time.Hour)
	insertNotifAt(t, pool, account, uuid.New(), "def-"+uuid.NewString(), "v1", day.Add(2*time.Hour))

	mailer := &captureMailer{}
	svc := digestFor(pool, mailer, at)
	if err := svc.EnsureDelivery(ctx, account, day, false); err != nil {
		t.Fatalf("ensure: %v", err)
	}

	// Abandon the row in `sending` with the ambiguity marker FALSE: the previous attempt
	// died before anything could have been accepted.
	store := notify.NewDBDigestDeliveryStore(pool)
	claimed, err := store.MarkSending(ctx, account, day, at, false)
	if err != nil || !claimed {
		t.Fatalf("seed sending row: claimed=%v err=%v", claimed, err)
	}

	outcome, err := svc.DeliverAccountDay(ctx, account, day, false)
	if outcome == notify.OutcomeUnconfirmed {
		t.Fatal("a definitively-not-transmitted row was finalized UNCONFIRMED; the durable record claims a delivery that provably never happened")
	}
	if err == nil {
		t.Fatal("the released attempt returned no error; River would not retry it")
	}
	if outcome != notify.OutcomeRetryableFailure {
		t.Fatalf("outcome = %q, want %q", outcome, notify.OutcomeRetryableFailure)
	}
	if state := deliveryState(t, pool, account, day); state != notify.DigestStatePending {
		t.Fatalf("delivery state = %q, want %q (released for retry)", state, notify.DigestStatePending)
	}
	if len(mailer.messages()) != 0 {
		t.Fatal("the release path must not send; the retry does")
	}

	// And the released row DOES deliver on its own retry — the non-delivery is repaired,
	// not written off.
	if _, err := svc.DeliverAccountDay(ctx, account, day, false); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if state := deliveryState(t, pool, account, day); state != notify.DigestStateDelivered {
		t.Fatalf("after the retry state = %q, want %q", state, notify.DigestStateDelivered)
	}
}

// TestDeliverAccountDay_StrandedSendingInsideAmbiguousWindowStaysUnconfirmed is the
// NEGATIVE half of F2 and the guard on the reserved product semantics: a row abandoned
// AFTER the body terminator was written is genuinely ambiguous — the relay may already
// hold the message — so it stays terminal and is NEVER resent (at-most-once for the
// ambiguous window; zero resend outranks a speculative repair).
func TestDeliverAccountDay_StrandedSendingInsideAmbiguousWindowStaysUnconfirmed(t *testing.T) {
	ctx := context.Background()
	pool, q := newPool(t)
	account := seedAccount(t, q)

	day := time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)
	at := day.Add(30 * time.Hour)
	insertNotifAt(t, pool, account, uuid.New(), "amb-"+uuid.NewString(), "v1", day.Add(2*time.Hour))

	mailer := &captureMailer{}
	svc := digestFor(pool, mailer, at)
	if err := svc.EnsureDelivery(ctx, account, day, false); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	store := notify.NewDBDigestDeliveryStore(pool)
	claimed, err := store.MarkSending(ctx, account, day, at, true)
	if err != nil || !claimed {
		t.Fatalf("seed sending row: claimed=%v err=%v", claimed, err)
	}

	outcome, err := svc.DeliverAccountDay(ctx, account, day, false)
	if err != nil {
		t.Fatalf("deliver: %v", err)
	}
	if outcome != notify.OutcomeUnconfirmed {
		t.Fatalf("outcome = %q, want %q (the ambiguous window is terminal)", outcome, notify.OutcomeUnconfirmed)
	}
	if state := deliveryState(t, pool, account, day); state != notify.DigestStateUnconfirmed {
		t.Fatalf("state = %q, want %q", state, notify.DigestStateUnconfirmed)
	}
	if len(mailer.messages()) != 0 {
		t.Fatal("an ambiguous row must NEVER be resent")
	}
}

// TestDeliverAccountDay_AmbiguityMarkerNarrowsToThePostDataWindow proves the marker is
// written by the mailer at the real post-DATA boundary rather than assumed for the whole
// exchange: a mailer that reports its boundary and then fails BEFORE it leaves the row
// definitive, so the account keeps its retry budget.
func TestDeliverAccountDay_AmbiguityMarkerNarrowsToThePostDataWindow(t *testing.T) {
	ctx := context.Background()
	pool, q := newPool(t)
	account := seedAccount(t, q)

	day := time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC)
	at := day.Add(30 * time.Hour)
	insertNotifAt(t, pool, account, uuid.New(), "narrow-"+uuid.NewString(), "v1", day.Add(2*time.Hour))

	// A barrier-reporting mailer that fails BEFORE entering the ambiguous window.
	pre := &barrierMailer{failBefore: true}
	svc := digestFor(pool, pre, at)
	if err := svc.EnsureDelivery(ctx, account, day, false); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if _, err := svc.DeliverAccountDay(ctx, account, day, false); err == nil {
		t.Fatal("a pre-DATA failure must surface as a retryable error")
	}
	if got := ambiguousFlag(t, pool, account, day); got {
		t.Fatal("a pre-DATA failure recorded the row as AMBIGUOUS; it is definitively not delivered")
	}
	if state := deliveryState(t, pool, account, day); state != notify.DigestStatePending {
		t.Fatalf("state = %q, want %q", state, notify.DigestStatePending)
	}

	// The same mailer entering the window and then losing the verdict records AMBIGUOUS.
	post := &barrierMailer{failAfter: true}
	svc2 := digestFor(pool, post, at)
	if _, err := svc2.DeliverAccountDay(ctx, account, day, false); err != nil {
		t.Fatalf("deliver: %v", err)
	}
	if state := deliveryState(t, pool, account, day); state != notify.DigestStateUnconfirmed {
		t.Fatalf("state = %q, want %q (a lost post-DATA verdict is terminal ambiguous)", state, notify.DigestStateUnconfirmed)
	}
}

// barrierMailer reports the post-DATA boundary the way the real SMTP mailer does, so a
// test can place a failure precisely before or after the genuinely ambiguous window.
type barrierMailer struct {
	failBefore bool
	failAfter  bool
}

func (m *barrierMailer) Send(ctx context.Context, msg notify.Message) error {
	return m.SendReportingAmbiguity(ctx, msg, func(context.Context) error { return nil })
}

func (m *barrierMailer) SendReportingAmbiguity(ctx context.Context, _ notify.Message, enter notify.SendBarrier) error {
	if m.failBefore {
		return &notify.SendError{Reason: notify.ReasonSMTPConnectionLost}
	}
	if err := enter(ctx); err != nil {
		return err
	}
	if m.failAfter {
		return &notify.SendError{Reason: notify.ReasonSMTPConnectionLost, Ambiguous: true}
	}
	return nil
}

// ambiguousFlag reads the durable ambiguity marker for (account, business_day).
func ambiguousFlag(t *testing.T, pool *pgxpool.Pool, account uuid.UUID, day time.Time) bool {
	t.Helper()
	var v bool
	if err := pool.QueryRow(context.Background(),
		`SELECT ambiguous FROM notification_digest_deliveries
		 WHERE marketplace_account_id = $1 AND business_day = $2`, account, day.UTC()).Scan(&v); err != nil {
		t.Fatalf("read ambiguity marker: %v", err)
	}
	return v
}
