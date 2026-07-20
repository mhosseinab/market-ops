package notify_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/jobs"
	"github.com/mhosseinab/market-ops/services/core/internal/notify"
)

// Issue #122 DB-backed integration tests for the durable urgent-delivery outbox. They
// are gated on DATABASE_URL (skipped locally; run in CI against postgres:18). They use
// a recording enqueuer for the River-job side (the durable OUTBOX ROW is what proves
// restart-safety; the River enqueue is exercised by the jobs integration path) and run
// the real UrgentDispatcher over the DB-backed outbox store to prove a restart between
// notification commit and send still completes delivery.

// recordingEnqueuer captures the urgent-email intents Store.Deliver would enqueue,
// without needing a live River client. The transactional insert of the outbox row (the
// durable, restart-safe record) still happens inside Store.Deliver's transaction.
type recordingEnqueuer struct{ calls []jobs.UrgentEmailArgs }

func (e *recordingEnqueuer) EnqueueUrgentEmailTx(_ context.Context, _ pgx.Tx, args jobs.UrgentEmailArgs) error {
	e.calls = append(e.calls, args)
	return nil
}

// TestDeliver_ExecutionFailureEnqueuesUrgentOutbox_MarketEventDoesNot is the issue #122
// core routing negative: an execution failure commits a durable urgent outbox row +
// urgent-email intent in the SAME transaction as the notification, while a market event
// takes NEITHER — it stays digest-eligible and never touches the urgent path.
func TestDeliver_ExecutionFailureEnqueuesUrgentOutbox_MarketEventDoesNot(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account := seedAccount(t, q)
	enq := &recordingEnqueuer{}
	store := notify.NewStore(pool)
	store.SetUrgentEmailEnqueuer(enq)

	// Execution failure → urgent path.
	exec := uuid.New()
	execRes, err := store.Deliver(ctx, notify.DeliverParams{
		Account: account, EventID: exec, DedupKey: "exec-" + exec.String(),
		Category: notify.CategoryExecutionFailure, Severity: "critical",
		TitleKey: notify.KeyItemExecutionFail, BodyKey: notify.KeyItemExecutionFail,
		BodyParams: map[string]string{"action": exec.String()},
	})
	if err != nil || !execRes.Delivered {
		t.Fatalf("deliver execution failure: delivered=%v err=%v", execRes.Delivered, err)
	}

	// A durable outbox row exists, pending, in the same account — committed with the
	// notification (restart-safe).
	ob, err := q.GetUrgentOutbox(ctx, db.GetUrgentOutboxParams{
		NotificationID: execRes.Notification.ID, Channel: notify.ChannelEmail,
	})
	if err != nil {
		t.Fatalf("urgent outbox row must exist for an execution failure: %v", err)
	}
	if ob.DeliveryState != "pending" || ob.MarketplaceAccountID != account {
		t.Fatalf("outbox row = state %q account %v, want pending/%v", ob.DeliveryState, ob.MarketplaceAccountID, account)
	}
	if len(enq.calls) != 1 || enq.calls[0].NotificationID != execRes.Notification.ID {
		t.Fatalf("execution failure must enqueue exactly one urgent-email intent, got %d", len(enq.calls))
	}

	// Market event → NO urgent outbox row, NO urgent enqueue.
	mkt := uuid.New()
	mktRes, err := store.Deliver(ctx, notify.DeliverParams{
		Account: account, EventID: mkt, DedupKey: "mkt-" + mkt.String(),
		Category: notify.CategoryMarketEvent, Severity: "info",
		TitleKey: notify.KeyItemMarketEvent, BodyKey: notify.KeyItemMarketEvent,
		BodyParams: map[string]string{"variant": "SKU-1"},
	})
	if err != nil || !mktRes.Delivered {
		t.Fatalf("deliver market event: delivered=%v err=%v", mktRes.Delivered, err)
	}
	_, err = q.GetUrgentOutbox(ctx, db.GetUrgentOutboxParams{
		NotificationID: mktRes.Notification.ID, Channel: notify.ChannelEmail,
	})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("market event must NOT create an urgent outbox row, got err=%v", err)
	}
	if len(enq.calls) != 1 {
		t.Fatalf("market event must not enqueue urgent email; calls=%d want 1", len(enq.calls))
	}
}

// TestDeliver_UrgentOutboxIsIdempotent proves re-delivering the SAME execution failure
// (a replay) commits exactly ONE outbox row and enqueues exactly ONE urgent-email
// intent — a retry never duplicates the logical email.
func TestDeliver_UrgentOutboxIsIdempotent(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account := seedAccount(t, q)
	enq := &recordingEnqueuer{}
	store := notify.NewStore(pool)
	store.SetUrgentEmailEnqueuer(enq)

	safety := uuid.New()
	p := notify.DeliverParams{
		Account: account, EventID: safety, DedupKey: "safety-" + safety.String(),
		Category: notify.CategorySafetyFailure, Severity: "critical",
		TitleKey: notify.KeyItemSafetyFail, BodyKey: notify.KeyItemSafetyFail,
		BodyParams: map[string]string{"reason": "boundary"},
	}
	first, err := store.Deliver(ctx, p)
	if err != nil || !first.Delivered {
		t.Fatalf("first deliver: delivered=%v err=%v", first.Delivered, err)
	}
	second, err := store.Deliver(ctx, p)
	if err != nil {
		t.Fatalf("replay deliver: %v", err)
	}
	if second.Delivered {
		t.Fatal("replay must be Delivered=false (idempotent)")
	}
	if len(enq.calls) != 1 {
		t.Fatalf("replay must NOT enqueue a second urgent email; calls=%d want 1", len(enq.calls))
	}
}

// TestUrgentDispatch_RestartAfterCommitCompletesDelivery proves the durable-outbox
// restart-safety: after the notification + outbox committed (the enqueue captured), a
// FRESH dispatcher (simulating the worker after a process restart) reads the durable
// row, sends the email, and marks it delivered — the urgent delivery is never lost. A
// second dispatch is an idempotent no-op (no duplicate email).
func TestUrgentDispatch_RestartAfterCommitCompletesDelivery(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account := seedAccount(t, q)
	enq := &recordingEnqueuer{}
	store := notify.NewStore(pool)
	store.SetUrgentEmailEnqueuer(enq)

	exec := uuid.New()
	res, err := store.Deliver(ctx, notify.DeliverParams{
		Account: account, EventID: exec, DedupKey: "exec-" + exec.String(),
		Category: notify.CategoryExecutionFailure, Severity: "critical",
		TitleKey: notify.KeyItemExecutionFail, BodyKey: notify.KeyItemExecutionFail,
		BodyParams: map[string]string{"action": exec.String()},
	})
	if err != nil || len(enq.calls) != 1 {
		t.Fatalf("deliver execution failure: err=%v calls=%d", err, len(enq.calls))
	}

	// Simulate the restarted worker: a brand-new dispatcher over the durable outbox.
	mailer := &captureMailer{}
	dispatcher := notify.NewUrgentDispatcher(
		notify.NewDBUrgentOutboxStore(pool), mailer,
		fixedResolver{notify.Target{Email: "owner@example.com", Locale: "fa-IR"}})

	if err := dispatcher.Dispatch(ctx, enq.calls[0], false); err != nil {
		t.Fatalf("restart dispatch: %v", err)
	}
	if len(mailer.sent) != 1 {
		t.Fatalf("restart must complete delivery (1 email), got %d", len(mailer.sent))
	}
	if !strings.Contains(mailer.sent[0].Body, exec.String()) {
		t.Fatal("urgent email must carry the shared event id")
	}

	ob, err := q.GetUrgentOutbox(ctx, db.GetUrgentOutboxParams{
		NotificationID: res.Notification.ID, Channel: notify.ChannelEmail,
	})
	if err != nil {
		t.Fatalf("get outbox: %v", err)
	}
	if ob.DeliveryState != "delivered" || !ob.DeliveredAt.Valid {
		t.Fatalf("outbox must be delivered with a timestamp, got state %q valid=%v", ob.DeliveryState, ob.DeliveredAt.Valid)
	}

	// A second dispatch (a River retry) is an idempotent no-op — no duplicate email.
	if err := dispatcher.Dispatch(ctx, enq.calls[0], false); err != nil {
		t.Fatalf("second dispatch: %v", err)
	}
	if len(mailer.sent) != 1 {
		t.Fatalf("retry after delivered must not resend; sent=%d want 1", len(mailer.sent))
	}
}
