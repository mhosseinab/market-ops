package notify

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mhosseinab/market-ops/services/core/internal/jobs"
)

// Issue #122 negative-first unit tests for the durable urgent-email dispatcher. They
// are DB-free: the outbox store, mailer, and target resolver are fakes, so the
// fail-closed / idempotent / never-shed / dead-letter DECISIONS are proven without a
// database (the transactional enqueue + the guarded state transitions are exercised by
// the DB integration tests, deferred to CI). NOT-001 + idempotency + load-shedding are
// never-cut invariants — these negatives are kept passing on every change.

// --- fakes -------------------------------------------------------------------------

type fakeUrgentOutbox struct {
	rec        UrgentOutboxRecord
	found      bool
	getErr     error
	delivered  int
	deadLetter int
	bumped     int
	lastReason string
	// deadLetterFailFirst makes the first N MarkDeadLetter calls fail (the state write
	// itself failing on the final attempt), so the recovery obligation is exercised.
	deadLetterFailFirst int
}

func (f *fakeUrgentOutbox) Get(_ context.Context, _ uuid.UUID, _ string) (UrgentOutboxRecord, bool, error) {
	if f.getErr != nil {
		return UrgentOutboxRecord{}, false, f.getErr
	}
	return f.rec, f.found, nil
}

func (f *fakeUrgentOutbox) MarkDelivered(_ context.Context, _ uuid.UUID, _ string, _ time.Time) error {
	f.delivered++
	f.rec.State = urgentStateDelivered
	return nil
}

func (f *fakeUrgentOutbox) MarkDeadLetter(_ context.Context, _ uuid.UUID, _, reason string, _ time.Time) error {
	f.deadLetter++
	if f.deadLetterFailFirst > 0 {
		f.deadLetterFailFirst--
		// The state write failed: the row stays PENDING (no durable transition).
		return errors.New("outbox: dead-letter state write failed")
	}
	f.lastReason = reason
	f.rec.State = urgentStateDeadLetter
	return nil
}

func (f *fakeUrgentOutbox) BumpAttempt(_ context.Context, _ uuid.UUID, _, reason string, _ time.Time) error {
	f.bumped++
	f.lastReason = reason
	return nil
}

type fakeMailer struct {
	sent []Message
	err  error
}

func (m *fakeMailer) Send(_ context.Context, msg Message) error {
	if m.err != nil {
		return m.err
	}
	m.sent = append(m.sent, msg)
	return nil
}

type fakeResolver struct {
	target Target
	err    error
}

func (r fakeResolver) Resolve(_ context.Context, _ uuid.UUID) (Target, error) {
	return r.target, r.err
}

func urgentArgs() jobs.UrgentEmailArgs {
	return jobs.UrgentEmailArgs{
		NotificationID: uuid.New(),
		Account:        uuid.New(),
		EventID:        uuid.New(),
		Channel:        ChannelEmail,
		Category:       string(CategoryExecutionFailure),
		Severity:       "critical",
		TitleKey:       KeyItemExecutionFail,
		BodyKey:        KeyItemExecutionFail,
		Params:         map[string]string{"action": "act-1"},
	}
}

func pendingOutbox(a jobs.UrgentEmailArgs) *fakeUrgentOutbox {
	return &fakeUrgentOutbox{
		found: true,
		rec:   UrgentOutboxRecord{NotificationID: a.NotificationID, Account: a.Account, Channel: ChannelEmail, State: urgentStatePending},
	}
}

func newDispatcher(outbox UrgentOutboxStore, mailer Mailer) *UrgentDispatcher {
	return NewUrgentDispatcher(outbox, mailer, fakeResolver{target: Target{Email: "owner@example.com", Locale: "fa-IR"}}).
		WithClock(func() time.Time { return time.Unix(0, 0).UTC() })
}

// --- classification: market events never take the urgent path -----------------------

// TestBypassesDigest_OnlyFailuresAreUrgent proves the urgent (immediate, never-shed)
// path is selected ONLY for execution/safety failures — a market event batches into
// the sheddable daily digest and NEVER hits the urgent outbox.
func TestBypassesDigest_OnlyFailuresAreUrgent(t *testing.T) {
	if CategoryMarketEvent.BypassesDigest() {
		t.Fatal("market_event must NOT bypass the digest (never on the urgent path)")
	}
	if !CategoryExecutionFailure.BypassesDigest() || !CategorySafetyFailure.BypassesDigest() {
		t.Fatal("execution/safety failures MUST bypass the digest (urgent path)")
	}
}

// --- success: delivered exactly once ------------------------------------------------

func TestUrgentDispatch_SuccessMarksDeliveredOnce(t *testing.T) {
	a := urgentArgs()
	ob := pendingOutbox(a)
	mail := &fakeMailer{}
	if err := newDispatcher(ob, mail).Dispatch(context.Background(), a, false); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if len(mail.sent) != 1 {
		t.Fatalf("sent %d emails, want 1", len(mail.sent))
	}
	if ob.delivered != 1 || ob.deadLetter != 0 {
		t.Fatalf("delivered=%d deadLetter=%d, want 1/0", ob.delivered, ob.deadLetter)
	}
	// The rendered email carries the shared event id (NOT-001) and localized copy, not
	// a catalog key literal.
	body := mail.sent[0].Body
	if !strings.Contains(body, a.EventID.String()) {
		t.Fatalf("email body missing shared event id: %q", body)
	}
	if strings.Contains(body, "notify.item.") || strings.Contains(mail.sent[0].Subject, "notify.urgent.") {
		t.Fatalf("email leaked a catalog key literal: subj=%q body=%q", mail.sent[0].Subject, body)
	}
}

// --- idempotency: an already-terminal row never re-sends ----------------------------

func TestUrgentDispatch_AlreadyDeliveredIsNoOp(t *testing.T) {
	a := urgentArgs()
	ob := pendingOutbox(a)
	ob.rec.State = urgentStateDelivered
	mail := &fakeMailer{}
	if err := newDispatcher(ob, mail).Dispatch(context.Background(), a, true); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if len(mail.sent) != 0 || ob.delivered != 0 || ob.deadLetter != 0 {
		t.Fatalf("already-delivered row must be a no-op: sent=%d delivered=%d dead=%d", len(mail.sent), ob.delivered, ob.deadLetter)
	}
}

// --- transient failure: retryable, no duplicate, NOT delivered ----------------------

// TestUrgentDispatch_TransientFailureRetriesNoDuplicate proves a transient SMTP failure
// on a non-final attempt returns a retryable error, leaves the row PENDING (bumped, not
// delivered, not dead-lettered), and sends no mail — so River retries WITHOUT
// duplicating the logical email or losing the urgent delivery.
func TestUrgentDispatch_TransientFailureRetriesNoDuplicate(t *testing.T) {
	a := urgentArgs()
	ob := pendingOutbox(a)
	mail := &fakeMailer{err: errors.New("smtp temporary failure")}
	err := newDispatcher(ob, mail).Dispatch(context.Background(), a, false)
	if err == nil {
		t.Fatal("transient failure must return a retryable error, got nil")
	}
	if ob.delivered != 0 {
		t.Fatal("transient failure must NOT mark the email delivered")
	}
	if ob.deadLetter != 0 {
		t.Fatal("a non-final attempt must NOT dead-letter")
	}
	if ob.bumped != 1 || ob.lastReason != reasonSendError {
		t.Fatalf("transient failure must bump the attempt with the send reason: bumped=%d reason=%q", ob.bumped, ob.lastReason)
	}
	if ob.rec.State != urgentStatePending {
		t.Fatalf("row must stay pending for retry, got %q", ob.rec.State)
	}
}

// --- permanent failure: dead-letter, observable, NOT delivered ----------------------

// TestUrgentDispatch_PermanentFailureDeadLettersNotDelivered proves the FINAL attempt
// of a still-failing send dead-letters (observable terminal state + observer) and does
// NOT mark the email delivered — no false "delivered", urgent never silently dropped.
func TestUrgentDispatch_PermanentFailureDeadLettersNotDelivered(t *testing.T) {
	a := urgentArgs()
	ob := pendingOutbox(a)
	mail := &fakeMailer{err: errors.New("smtp permanent failure")}
	var obsAccount, obsNotif uuid.UUID
	var obsCategory, obsReason string
	d := newDispatcher(ob, mail).WithDeadLetterObserver(
		func(_ context.Context, account, notificationID uuid.UUID, category, reason string) {
			obsAccount, obsNotif, obsCategory, obsReason = account, notificationID, category, reason
		})
	err := d.Dispatch(context.Background(), a, true)
	if err == nil {
		t.Fatal("permanent failure on the final attempt must return an error (River discards), got nil")
	}
	if ob.delivered != 0 {
		t.Fatal("dead-lettered email must NOT be marked delivered (no false delivered)")
	}
	if ob.deadLetter != 1 || ob.lastReason != reasonSendError {
		t.Fatalf("final attempt must dead-letter with the send reason: dead=%d reason=%q", ob.deadLetter, ob.lastReason)
	}
	if ob.rec.State != urgentStateDeadLetter {
		t.Fatalf("row must be dead_letter, got %q", ob.rec.State)
	}
	if obsAccount != a.Account || obsNotif != a.NotificationID || obsCategory != a.Category || obsReason != reasonSendError {
		t.Fatalf("dead-letter observer got %s/%s/%s/%s", obsAccount, obsNotif, obsCategory, obsReason)
	}
}

// --- reopen residual: dead-letter STATE WRITE fails on the final attempt ------------

// TestUrgentDispatch_DeadLetterPersistFailure_SuppressesTerminalSignalUntilPersisted is
// the issue #122 REOPEN regression. When the FINAL attempt's send fails permanently AND
// the pending → dead_letter state write ITSELF fails, the terminal signals (dead-letter
// metric + observer) MUST NOT fire — monitoring must never report a durable terminal
// dead letter that was never persisted. Instead the dispatcher returns the
// ErrUrgentDeadLetterUnpersisted recovery marker so the worker RE-DRIVES the intent, and
// the terminal observer fires ONLY after a later durable transition actually succeeds.
func TestUrgentDispatch_DeadLetterPersistFailure_SuppressesTerminalSignalUntilPersisted(t *testing.T) {
	a := urgentArgs()
	ob := pendingOutbox(a)
	ob.deadLetterFailFirst = 1 // first dead-letter write fails, second succeeds
	mail := &fakeMailer{err: errors.New("smtp permanent failure")}

	observed := 0
	d := newDispatcher(ob, mail).WithDeadLetterObserver(
		func(context.Context, uuid.UUID, uuid.UUID, string, string) { observed++ })

	// Pass 1: final attempt, send fails, dead-letter STATE WRITE fails.
	err := d.Dispatch(context.Background(), a, true)
	if err == nil {
		t.Fatal("unpersisted dead-letter must return an error so the worker re-drives, got nil")
	}
	if !errors.Is(err, jobs.ErrUrgentDeadLetterUnpersisted) {
		t.Fatalf("error must carry the unpersisted recovery marker (worker snoozes, not discards), got %v", err)
	}
	if observed != 0 {
		t.Fatalf("terminal observer fired for a state that was NEVER persisted (observed=%d)", observed)
	}
	if ob.delivered != 0 {
		t.Fatal("a failed send must NEVER be marked delivered")
	}
	if ob.rec.State != urgentStatePending {
		t.Fatalf("row must stay pending when the dead-letter write failed, got %q", ob.rec.State)
	}
	if ob.deadLetter != 1 {
		t.Fatalf("dead-letter write must have been attempted once, got %d", ob.deadLetter)
	}

	// Pass 2: re-drive. Final attempt again, send still fails, dead-letter write NOW
	// succeeds — the durable transition is real, so the terminal signal may fire.
	err = d.Dispatch(context.Background(), a, true)
	if err == nil {
		t.Fatal("permanent failure on the final attempt must still return the send cause, got nil")
	}
	if errors.Is(err, jobs.ErrUrgentDeadLetterUnpersisted) {
		t.Fatalf("a persisted dead-letter must NOT carry the unpersisted marker, got %v", err)
	}
	if observed != 1 {
		t.Fatalf("terminal observer must fire exactly once, after the durable transition, got %d", observed)
	}
	if ob.rec.State != urgentStateDeadLetter {
		t.Fatalf("row must be dead_letter after the successful write, got %q", ob.rec.State)
	}
	if ob.deadLetter != 2 {
		t.Fatalf("dead-letter write must have been re-attempted, got %d", ob.deadLetter)
	}
}

// --- unsendable target: fail closed, never send to nobody ---------------------------

func TestUrgentDispatch_UnsendableTargetFailsClosed(t *testing.T) {
	a := urgentArgs()
	ob := pendingOutbox(a)
	mail := &fakeMailer{}
	d := NewUrgentDispatcher(ob, mail, fakeResolver{target: Target{Email: "", Locale: "fa-IR"}}).
		WithClock(func() time.Time { return time.Unix(0, 0).UTC() })
	// Non-final attempt: retryable, pending, no mail.
	if err := d.Dispatch(context.Background(), a, false); err == nil {
		t.Fatal("unsendable target must fail closed (retryable error), got nil")
	}
	if len(mail.sent) != 0 || ob.delivered != 0 {
		t.Fatal("unsendable target must never send and never mark delivered")
	}
	if ob.bumped != 1 || ob.lastReason != reasonUnsendableTarget {
		t.Fatalf("unsendable target must bump with its reason: bumped=%d reason=%q", ob.bumped, ob.lastReason)
	}
}

// --- render: localized, both locales, event id, no key literal ----------------------

func TestRenderUrgent_LocalizedWithSharedEventID(t *testing.T) {
	a := urgentArgs()
	for _, locale := range []string{"fa-IR", "en"} {
		msg, err := renderUrgent(Target{Email: "x@y.z", Locale: locale}, a)
		if err != nil {
			t.Fatalf("renderUrgent(%s): %v", locale, err)
		}
		if msg.To != "x@y.z" {
			t.Fatalf("recipient not set: %q", msg.To)
		}
		if !strings.Contains(msg.Body, a.EventID.String()) {
			t.Fatalf("body missing shared event id (%s): %q", locale, msg.Body)
		}
		if strings.Contains(msg.Body, "{action}") {
			t.Fatalf("body left an unfilled slot (%s): %q", locale, msg.Body)
		}
		if msg.Subject == "" {
			t.Fatalf("empty subject (%s)", locale)
		}
	}
	// An unknown locale fails closed (no silent English fallback).
	if _, err := renderUrgent(Target{Email: "x@y.z", Locale: "de"}, a); err == nil {
		t.Fatal("unknown locale must fail closed")
	}
}

// TestUrgentSubjectFooter_AreFrameKeys proves the urgent frame keys are NOT deliverable
// as a notification title/body key (like the digest frame keys) — they render only
// inside the urgent email, never smuggle free text through the store.
func TestUrgentSubjectFooter_AreFrameKeys(t *testing.T) {
	for _, key := range []string{KeyUrgentSubject, KeyUrgentFooter} {
		if err := validateShape(CategoryExecutionFailure, key, key, nil); err == nil {
			t.Fatalf("frame key %q must not be deliverable as a notification key", key)
		}
	}
}
