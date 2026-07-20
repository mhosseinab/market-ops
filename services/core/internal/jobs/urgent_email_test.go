package jobs

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

// These are the issue #122 negative-first unit tests for the durable urgent-email
// intent: an execution/safety failure enqueues one notification_urgent_email job
// transactionally with its notification + outbox row, and this worker drives the
// idempotent send with its own retry + dead-letter. They are DB-free (the River
// enqueue and the outbox transitions are exercised by the DB integration tests,
// deferred to CI).

// TestUrgentEmailArgs_Kind pins the stable River job identifier. Changing it after
// ship orphans in-flight durable urgent-email intents, so it is asserted explicitly.
func TestUrgentEmailArgs_Kind(t *testing.T) {
	if got := (UrgentEmailArgs{}).Kind(); got != "notification_urgent_email" {
		t.Fatalf("Kind() = %q, want notification_urgent_email", got)
	}
}

// TestUrgentEmailWorker_NilRunnerFailsClosed proves a missing consumer fails CLOSED
// (returns an error so River retries) rather than silently completing — a committed
// urgent-email intent for a safety/execution failure is NEVER silently dropped.
func TestUrgentEmailWorker_NilRunnerFailsClosed(t *testing.T) {
	w := NewUrgentEmailWorker(nil, nil)
	err := w.Work(context.Background(), &river.Job[UrgentEmailArgs]{Args: UrgentEmailArgs{}})
	if err == nil {
		t.Fatal("nil runner must fail closed (return an error), got nil")
	}
}

// TestUrgentEmailWorker_InvokesRunnerAndSurfacesError proves the worker drives the
// injected runner with the job's args and surfaces its error verbatim (retryable, so
// a transient send failure retries — the urgent delivery is never lost).
func TestUrgentEmailWorker_InvokesRunnerAndSurfacesError(t *testing.T) {
	want := UrgentEmailArgs{
		NotificationID: uuid.New(),
		Account:        uuid.New(),
		EventID:        uuid.New(),
		Channel:        "email",
		Category:       "execution_failure",
		Severity:       "critical",
		TitleKey:       "notify.item.executionFailure",
		BodyKey:        "notify.item.executionFailure",
		Params:         map[string]string{"action": "a1"},
	}
	var got UrgentEmailArgs
	called := 0
	run := func(_ context.Context, a UrgentEmailArgs, _ bool) error {
		called++
		got = a
		return nil
	}
	w := NewUrgentEmailWorker(run, nil)
	if err := w.Work(context.Background(), &river.Job[UrgentEmailArgs]{Args: want}); err != nil {
		t.Fatalf("Work: %v", err)
	}
	if called != 1 {
		t.Fatalf("runner invoked %d times, want 1", called)
	}
	if got.NotificationID != want.NotificationID || got.Category != want.Category {
		t.Fatalf("runner got %+v, want %+v", got, want)
	}

	sentinel := errors.New("smtp boom")
	w2 := NewUrgentEmailWorker(func(context.Context, UrgentEmailArgs, bool) error { return sentinel }, nil)
	if err := w2.Work(context.Background(), &river.Job[UrgentEmailArgs]{Args: want}); !errors.Is(err, sentinel) {
		t.Fatalf("Work must surface runner error, got %v", err)
	}
}

// TestUrgentEmailWorker_LastAttemptDerivedFromRiver proves the worker tells the runner
// when it is the FINAL attempt, so the runner can record the observable dead-letter
// terminal state (never a silent drop). Attempt < MaxAttempts is not last; Attempt ==
// MaxAttempts is.
func TestUrgentEmailWorker_LastAttemptDerivedFromRiver(t *testing.T) {
	cases := []struct {
		attempt, max int
		wantLast     bool
	}{
		{attempt: 1, max: 5, wantLast: false},
		{attempt: 4, max: 5, wantLast: false},
		{attempt: 5, max: 5, wantLast: true},
		{attempt: 6, max: 5, wantLast: true},
	}
	for _, c := range cases {
		var gotLast bool
		w := NewUrgentEmailWorker(func(_ context.Context, _ UrgentEmailArgs, last bool) error {
			gotLast = last
			return nil
		}, nil)
		job := &river.Job[UrgentEmailArgs]{
			JobRow: &rivertype.JobRow{Attempt: c.attempt, MaxAttempts: c.max},
			Args:   UrgentEmailArgs{},
		}
		if err := w.Work(context.Background(), job); err != nil {
			t.Fatalf("Work: %v", err)
		}
		if gotLast != c.wantLast {
			t.Fatalf("attempt=%d max=%d lastAttempt=%v, want %v", c.attempt, c.max, gotLast, c.wantLast)
		}
	}
}
