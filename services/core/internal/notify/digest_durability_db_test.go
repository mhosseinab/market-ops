package notify_test

import (
	"context"
	"slices"
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
	if _, err := svc.EnsureDelivery(context.Background(), account, day, false); err != nil {
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
	if _, err := svc.EnsureDelivery(ctx, account, day, false); err != nil {
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
	if _, err := svc.EnsureDelivery(ctx, account, day, false); err != nil {
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
	if _, err := svc.EnsureDelivery(ctx, account, day, false); err != nil {
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

// TestMarkUnconfirmed_GuardRejectsANonAmbiguousSendingRow pins the SQL guard
// INDEPENDENTLY of the service-level branch that normally protects it (issue #124 review
// cycle 2, G2). `unconfirmed` is terminal and never resent, so the durable transition
// itself — not only its caller — must refuse a row that provably never entered the
// post-DATA window. Without the `AND ambiguous` predicate this test writes off a known
// non-delivery as "we may have delivered".
func TestMarkUnconfirmed_GuardRejectsANonAmbiguousSendingRow(t *testing.T) {
	ctx := context.Background()
	pool, q := newPool(t)
	account := seedAccount(t, q)

	day := time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC)
	at := day.Add(30 * time.Hour)
	svc := digestFor(pool, &captureMailer{}, at)
	if _, err := svc.EnsureDelivery(ctx, account, day, false); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	store := notify.NewDBDigestDeliveryStore(pool)

	// A live claim that has NOT entered the ambiguous window.
	claimed, err := store.MarkSending(ctx, account, day, at, false)
	if err != nil || !claimed {
		t.Fatalf("claim: claimed=%v err=%v", claimed, err)
	}
	applied, err := store.MarkUnconfirmed(ctx, account, day, notify.DigestReasonSendOutcomeUnknown, 0, at)
	if err != nil {
		t.Fatalf("mark unconfirmed: %v", err)
	}
	if applied {
		t.Fatal("the guarded transition ACCEPTED a sending row with ambiguous=false; a definitively-untransmitted digest was written off as terminally unconfirmed")
	}
	if state := deliveryState(t, pool, account, day); state != notify.DigestStateSending {
		t.Fatalf("state = %q, want %q (a guard miss must change nothing)", state, notify.DigestStateSending)
	}

	// The SAME transition on a row that DID enter the window is applied — the guard
	// narrows the state, it does not disable it.
	if ok, err := store.MarkAmbiguous(ctx, account, day, at); err != nil || !ok {
		t.Fatalf("mark ambiguous: ok=%v err=%v", ok, err)
	}
	applied, err = store.MarkUnconfirmed(ctx, account, day, notify.DigestReasonSendOutcomeUnknown, 0, at)
	if err != nil {
		t.Fatalf("mark unconfirmed (ambiguous): %v", err)
	}
	if !applied {
		t.Fatal("the guarded transition REJECTED a genuinely ambiguous row")
	}
	if state := deliveryState(t, pool, account, day); state != notify.DigestStateUnconfirmed {
		t.Fatalf("state = %q, want %q", state, notify.DigestStateUnconfirmed)
	}
}

// unboundedReasonMailer is a third-party/injected Mailer that hand-builds a *SendError
// carrying an UNBOUNDED reason. SendReason is an exported plain string type, so nothing
// stops it — the digest must therefore validate at the boundary rather than trust it.
type unboundedReasonMailer struct{}

func (unboundedReasonMailer) Send(context.Context, notify.Message) error {
	return &notify.SendError{Reason: notify.SendReason(
		"relay said: <digest-owner@tenant-example.test> 5.7.1 rejected — unbounded prose")}
}

// TestDeliverAccountDay_UnboundedMailerReasonIsContained is the free-text containment
// negative (§4.6 / LOC-001). The mailer's reason becomes a METRIC LABEL and durable
// `last_reason`, so an unvalidated token is both unbounded label cardinality and a PII
// leak (a 550 echoes the recipient). Anything outside the closed set must degrade to the
// bounded `send_error`, never be persisted or labelled verbatim.
func TestDeliverAccountDay_UnboundedMailerReasonIsContained(t *testing.T) {
	ctx := context.Background()
	pool, q := newPool(t)
	account := seedAccount(t, q)

	day := time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)
	at := day.Add(30 * time.Hour)
	insertNotifAt(t, pool, account, uuid.New(), "unb-"+uuid.NewString(), "v1", day.Add(2*time.Hour))

	var observed []string
	svc := digestFor(pool, unboundedReasonMailer{}, at).
		WithAttemptObserver(func(_ context.Context, _ uuid.UUID, _ time.Time, _, reason string, _ time.Duration) {
			observed = append(observed, reason)
		})
	if _, err := svc.EnsureDelivery(ctx, account, day, false); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if _, err := svc.DeliverAccountDay(ctx, account, day, false); err == nil {
		t.Fatal("a send failure returned no error")
	}

	persisted := deliveryReason(t, pool, account, day)
	if !slices.Contains(notify.DigestReasons(), notify.DigestReason(persisted)) {
		t.Fatalf("durable last_reason = %q is OUTSIDE the closed set; unbounded relay prose reached durable state and the metric label", persisted)
	}
	if persisted != string(notify.DigestReasonSendError) {
		t.Fatalf("durable last_reason = %q, want %q (an unknown token degrades to the bounded fallback)", persisted, notify.DigestReasonSendError)
	}
	for _, r := range observed {
		if !slices.Contains(notify.DigestReasons(), notify.DigestReason(r)) {
			t.Fatalf("attempt observer reported reason %q outside the closed set", r)
		}
	}
}

// deliveryReason reads the durable bounded last_reason for (account, business_day).
func deliveryReason(t *testing.T, pool *pgxpool.Pool, account uuid.UUID, day time.Time) string {
	t.Helper()
	var v *string
	if err := pool.QueryRow(context.Background(),
		`SELECT last_reason FROM notification_digest_deliveries
		 WHERE marketplace_account_id = $1 AND business_day = $2`, account, day.UTC()).Scan(&v); err != nil {
		t.Fatalf("read last_reason: %v", err)
	}
	if v == nil {
		return ""
	}
	return *v
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
