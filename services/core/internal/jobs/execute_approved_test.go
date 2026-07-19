package jobs

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

func jobFor(args ExecuteApprovedArgs, attempt, maxAttempts int) *river.Job[ExecuteApprovedArgs] {
	return &river.Job[ExecuteApprovedArgs]{
		JobRow: &rivertype.JobRow{ID: 1, Attempt: attempt, MaxAttempts: maxAttempts},
		Args:   args,
	}
}

// TestExecuteApprovedWorker_NilRunnerFailsClosed proves the durable intent is never
// silently completed when no execution runner is wired: the worker returns an error
// so River retries (the intent is preserved), rather than acking and dropping it.
func TestExecuteApprovedWorker_NilRunnerFailsClosed(t *testing.T) {
	w := NewExecuteApprovedWorker(nil, nil)
	err := w.Work(context.Background(), jobFor(ExecuteApprovedArgs{CardID: uuid.New()}, 1, 25))
	if !errors.Is(err, errNoExecuteRunner) {
		t.Fatalf("nil runner: err = %v; want errNoExecuteRunner (fail closed)", err)
	}
}

// TestExecuteApprovedWorker_SnoozePassThrough proves the dark posture: when the
// runner reports the plane is not yet live (JobSnooze), the worker surfaces the
// snooze verbatim so River parks the intent without burning a retry attempt.
func TestExecuteApprovedWorker_SnoozePassThrough(t *testing.T) {
	w := NewExecuteApprovedWorker(func(context.Context, ExecuteApprovedArgs) error {
		return river.JobSnooze(time.Second)
	}, nil)
	err := w.Work(context.Background(), jobFor(ExecuteApprovedArgs{CardID: uuid.New()}, 1, 25))
	var snooze *rivertype.JobSnoozeError
	if !errors.As(err, &snooze) {
		t.Fatalf("snooze: err = %v; want a JobSnoozeError passed through", err)
	}
}

// TestExecuteApprovedWorker_RunnerErrorRetries proves a genuine processing error is
// returned so River retries with backoff (restart-safe), not swallowed.
func TestExecuteApprovedWorker_RunnerErrorRetries(t *testing.T) {
	boom := errors.New("processing boom")
	w := NewExecuteApprovedWorker(func(context.Context, ExecuteApprovedArgs) error { return boom }, nil)
	err := w.Work(context.Background(), jobFor(ExecuteApprovedArgs{CardID: uuid.New()}, 1, 25))
	if !errors.Is(err, boom) {
		t.Fatalf("runner error: err = %v; want the processing error surfaced for retry", err)
	}
}
