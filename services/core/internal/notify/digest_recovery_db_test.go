package notify_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mhosseinab/market-ops/services/core/internal/notify"
)

// Issue #124 / PD-4 item 1 — NEGATIVE FIRST: ZERO RESEND.
//
// The owned recovery pass rediscovers every NONTERMINAL (account, business_day)
// delivery row and re-enqueues it, WITHOUT consulting River attempt metadata (which
// shares the failing PostgreSQL boundary). That recovery must be idempotent: an
// account/day that already reached the terminal `delivered` state must NEVER be
// re-delivered, no matter how many recovery passes run. Idempotency gates every retry
// (§4.6 never-cut) — a recovery that resends is a bug, not a repair.

// TestRecovery_DeliveredAccountDayIsNeverResent proves the never-cut idempotency
// boundary: after one successful delivery, an unbounded number of recovery passes
// (rediscover + re-enqueue + re-drive) sends EXACTLY ONE email for that account/day.
func TestRecovery_DeliveredAccountDayIsNeverResent(t *testing.T) {
	ctx := context.Background()
	pool, q := newPool(t)
	account := seedAccount(t, q)

	// The pass finalizes the most recently CLOSED UTC day.
	at := time.Date(2026, 3, 12, 9, 0, 0, 0, time.UTC)
	day := time.Date(2026, 3, 11, 0, 0, 0, 0, time.UTC)
	insertNotifAt(t, pool, account, uuid.New(), "recov-"+uuid.NewString(), "v1", day.Add(4*time.Hour))

	mailer := &captureMailer{}
	svc := digestFor(pool, mailer, at)

	// The fan-out records the durable work row; delivery is a separate, per-account job.
	if err := svc.EnsureDelivery(ctx, account, day, false); err != nil {
		t.Fatalf("ensure delivery row: %v", err)
	}

	// First drive: the account/day delivers exactly once.
	outcome, err := svc.DeliverAccountDay(ctx, account, day, false)
	if err != nil {
		t.Fatalf("first delivery: %v", err)
	}
	if outcome != notify.OutcomeDelivered {
		t.Fatalf("first delivery outcome = %q, want %q", outcome, notify.OutcomeDelivered)
	}
	if got := len(mailer.messages()); got != 1 {
		t.Fatalf("first delivery sent %d messages, want 1", got)
	}

	// The row is now TERMINAL (delivered) and therefore invisible to recovery.
	rows, err := svc.NonterminalDeliveries(ctx, at.Add(time.Hour))
	if err != nil {
		t.Fatalf("rediscover nonterminal: %v", err)
	}
	for _, r := range rows {
		if r.Account == account && r.BusinessDay.Equal(day) {
			t.Fatalf("delivered account/day %s/%s is still discoverable as nonterminal (state %q) — recovery would resend",
				account, day.Format(time.DateOnly), r.State)
		}
	}

	// Even if a recovery pass DID re-drive it (a duplicate job, a concurrent pass, a
	// re-enqueue racing the terminal write), the drive itself is a no-op: ZERO resend.
	for i := range 5 {
		out, err := svc.DeliverAccountDay(ctx, account, day, false)
		if err != nil {
			t.Fatalf("recovery re-drive %d: %v", i, err)
		}
		if out != notify.OutcomeNoop {
			t.Fatalf("recovery re-drive %d outcome = %q, want %q (a terminal row is never re-driven)", i, out, notify.OutcomeNoop)
		}
	}
	if got := len(mailer.messages()); got != 1 {
		t.Fatalf("after 5 recovery re-drives the account/day sent %d messages, want exactly 1 (zero resend)", got)
	}
}
