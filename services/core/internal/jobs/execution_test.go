package jobs

import (
	"context"
	"errors"
	"testing"

	"github.com/riverqueue/river"
)

// TestRecommendOnlyMatchWorker_InvokesRunner proves the River worker drives its
// injected reconciliation pass (the production caller seam for EXE-005).
func TestRecommendOnlyMatchWorker_InvokesRunner(t *testing.T) {
	called := 0
	w := NewRecommendOnlyMatchWorker(func(context.Context) (int, error) { called++; return 3, nil }, nil)
	if err := w.Work(context.Background(), &river.Job[RecommendOnlyMatchArgs]{}); err != nil {
		t.Fatalf("Work: %v", err)
	}
	if called != 1 {
		t.Fatalf("runner invoked %d times; want 1", called)
	}
}

// TestOutcomeCloseWorker_InvokesRunner proves the River worker drives its injected
// close pass (the production caller seam for OUT-001).
func TestOutcomeCloseWorker_InvokesRunner(t *testing.T) {
	called := 0
	w := NewOutcomeCloseWorker(func(context.Context) (int, error) { called++; return 0, nil }, nil)
	if err := w.Work(context.Background(), &river.Job[OutcomeCloseArgs]{}); err != nil {
		t.Fatalf("Work: %v", err)
	}
	if called != 1 {
		t.Fatalf("runner invoked %d times; want 1", called)
	}
}

// TestWorkers_NilRunnerFailsClosed proves a nil runner is a no-op (never a panic).
func TestWorkers_NilRunnerFailsClosed(t *testing.T) {
	w := NewRecommendOnlyMatchWorker(nil, nil)
	if err := w.Work(context.Background(), &river.Job[RecommendOnlyMatchArgs]{}); err != nil {
		t.Fatalf("nil-runner Work: %v", err)
	}
}

// TestOutcomeCloseWorker_PropagatesError proves a pass error surfaces (River retries).
func TestOutcomeCloseWorker_PropagatesError(t *testing.T) {
	want := errors.New("boom")
	w := NewOutcomeCloseWorker(func(context.Context) (int, error) { return 0, want }, nil)
	if err := w.Work(context.Background(), &river.Job[OutcomeCloseArgs]{}); !errors.Is(err, want) {
		t.Fatalf("want propagated error, got %v", err)
	}
}
