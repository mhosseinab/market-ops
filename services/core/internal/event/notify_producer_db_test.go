package event_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mhosseinab/market-ops/services/core/internal/event"
)

// These are the issue #110 DB integration tests for the market-event notification
// producer: RecordFor enqueues the NOT-001 delivery intent INSIDE the event-write
// transaction (so it commits atomically with a fresh open, and a rollback discards
// both). They run only when DATABASE_URL is set (deferred to CI); the pure catalog
// binding + dedup identity are covered by the DB-free notify/dispatch_test.go.

// recordingNotifier captures each in-tx market-event enqueue. When err is set it
// FAILS the enqueue, which must roll the whole event write back (atomicity).
type recordingNotifier struct {
	calls []struct{ account, eventID, variant uuid.UUID }
	err   error
}

func (n *recordingNotifier) MarketEventTx(_ context.Context, _ pgx.Tx, account, eventID, variant uuid.UUID) error {
	n.calls = append(n.calls, struct{ account, eventID, variant uuid.UUID }{account, eventID, variant})
	return n.err
}

// TestNotifier_FreshOpenEnqueuesOnceDedupDoesNot proves a fresh open enqueues one
// market_event intent carrying the event's shared id, while a dedup replay of the
// same source transition enqueues nothing (idempotent — no duplicate notification).
func TestNotifier_FreshOpenEnqueuesOnceDedupDoesNot(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account, variant := seedVariant(t, q)
	notifier := &recordingNotifier{}
	svc := event.NewService(pool).SetNotifier(notifier)
	now := time.Now().UTC()

	// Non-corroborated quality (self-assertable without a cited observation, issue
	// #70) so a fresh event opens and the #110 enqueue path is exercised without
	// seeding an observation.
	first, _ := event.DetectWinningState(event.WinningStateInput{
		Variant: variant, WasWinning: true, IsWinning: false,
		Exposure: event.UnknownExposure(),
		Evidence: event.Evidence{Quality: event.QualityUnverified, Ref: "r1"},
		Now:      now, TTL: time.Hour,
	})
	r1, err := svc.RecordFor(ctx, account, first)
	if err != nil {
		t.Fatalf("record first: %v", err)
	}
	if len(notifier.calls) != 1 {
		t.Fatalf("fresh open must enqueue exactly one notification, got %d", len(notifier.calls))
	}
	if notifier.calls[0].eventID != r1.Event.ID {
		t.Fatalf("notification must carry the shared event id %v, got %v", r1.Event.ID, notifier.calls[0].eventID)
	}
	if notifier.calls[0].variant != variant {
		t.Fatalf("notification must carry the variant %v, got %v", variant, notifier.calls[0].variant)
	}

	// A dedup replay of the same condition opens no new event → enqueues nothing.
	second, _ := event.DetectWinningState(event.WinningStateInput{
		Variant: variant, WasWinning: true, IsWinning: false,
		Exposure: event.UnknownExposure(),
		Evidence: event.Evidence{Quality: event.QualityUnverified, Ref: "r2"},
		Now:      now.Add(10 * time.Minute), TTL: time.Hour,
	})
	if _, err := svc.RecordFor(ctx, account, second); err != nil {
		t.Fatalf("record dup: %v", err)
	}
	if len(notifier.calls) != 1 {
		t.Fatalf("a dedup replay must enqueue NO additional notification, got %d total", len(notifier.calls))
	}
}

// TestNotifier_EnqueueFailureRollsBackEvent proves atomicity (issue #110 acceptance:
// "Transaction rollback creates no notification"): if the in-tx enqueue fails, the
// event write rolls back — no market_events row is committed, so there is nothing to
// notify about. The intent and the event are all-or-nothing.
func TestNotifier_EnqueueFailureRollsBackEvent(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account, variant := seedVariant(t, q)
	boom := errors.New("enqueue boom")
	notifier := &recordingNotifier{err: boom}
	svc := event.NewService(pool).SetNotifier(notifier)
	now := time.Now().UTC()

	cand, _ := event.DetectWinningState(event.WinningStateInput{
		Variant: variant, WasWinning: true, IsWinning: false,
		Exposure: event.UnknownExposure(),
		Evidence: event.Evidence{Quality: event.QualityUnverified, Ref: "r"},
		Now:      now, TTL: time.Hour,
	})
	// The event write itself succeeds inside the tx (a fresh open), so the ONLY error
	// source is the in-tx enqueue — proving the rollback is caused by the failed
	// notification enqueue, not by evidence derivation.
	if _, err := svc.RecordFor(ctx, account, cand); !errors.Is(err, boom) {
		t.Fatalf("a failed in-tx enqueue must fail the RecordFor with the enqueue error, got %v", err)
	}
	if len(notifier.calls) != 1 {
		t.Fatalf("the enqueue must have been reached exactly once before rollback, got %d calls", len(notifier.calls))
	}

	var rows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM market_events WHERE variant_id=$1`, variant).Scan(&rows); err != nil {
		t.Fatalf("count: %v", err)
	}
	if rows != 0 {
		t.Fatalf("a rolled-back enqueue must leave ZERO event rows; found %d", rows)
	}
}
