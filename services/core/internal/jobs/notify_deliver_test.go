package jobs

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
)

// These are the issue #110 negative-first unit tests for the durable notification-
// delivery intent: a production lifecycle transition enqueues one
// notification_deliver job transactionally, and this worker drives the idempotent
// Store.Deliver exactly-once-effectively. They are DB-free (the River enqueue and
// Store.Deliver are exercised by the DB integration tests, deferred to CI).

// TestNotificationDeliverArgs_Kind pins the stable River job identifier. Changing it
// after ship orphans in-flight durable intents, so it is asserted explicitly.
func TestNotificationDeliverArgs_Kind(t *testing.T) {
	if got := (NotificationDeliverArgs{}).Kind(); got != "notification_deliver" {
		t.Fatalf("Kind() = %q, want notification_deliver", got)
	}
}

// TestNotificationDeliverWorker_NilRunnerFailsClosed proves a missing consumer
// fails CLOSED (returns an error so River retries) rather than silently completing —
// a committed delivery intent is never lost to a missing runner. Production always
// wires the runner (cmd/core/main.go).
func TestNotificationDeliverWorker_NilRunnerFailsClosed(t *testing.T) {
	w := NewNotificationDeliverWorker(nil, nil)
	err := w.Work(context.Background(), &river.Job[NotificationDeliverArgs]{Args: NotificationDeliverArgs{}})
	if err == nil {
		t.Fatal("nil runner must fail closed (return an error), got nil")
	}
}

// TestNotificationDeliverWorker_InvokesRunner proves the worker drives the injected
// delivery runner with the job's args and surfaces its error verbatim (retryable).
func TestNotificationDeliverWorker_InvokesRunner(t *testing.T) {
	want := NotificationDeliverArgs{
		Account:  uuid.New(),
		EventID:  uuid.New(),
		DedupKey: "market_event:" + uuid.NewString(),
		Category: "market_event",
		Severity: "info",
		TitleKey: "notify.item.marketEvent",
		BodyKey:  "notify.item.marketEvent",
		Params:   map[string]string{"variant": "v1"},
	}
	var got NotificationDeliverArgs
	called := 0
	run := func(_ context.Context, a NotificationDeliverArgs) error {
		called++
		got = a
		return nil
	}
	w := NewNotificationDeliverWorker(run, nil)
	if err := w.Work(context.Background(), &river.Job[NotificationDeliverArgs]{Args: want}); err != nil {
		t.Fatalf("Work: %v", err)
	}
	if called != 1 {
		t.Fatalf("runner invoked %d times, want 1", called)
	}
	if got.DedupKey != want.DedupKey || got.EventID != want.EventID || got.Category != want.Category {
		t.Fatalf("runner got %+v, want %+v", got, want)
	}

	sentinel := errors.New("boom")
	w2 := NewNotificationDeliverWorker(func(context.Context, NotificationDeliverArgs) error { return sentinel }, nil)
	if err := w2.Work(context.Background(), &river.Job[NotificationDeliverArgs]{Args: want}); !errors.Is(err, sentinel) {
		t.Fatalf("Work must surface runner error, got %v", err)
	}
}
