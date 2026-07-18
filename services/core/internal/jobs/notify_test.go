package jobs

import (
	"context"
	"errors"
	"testing"

	"github.com/riverqueue/river"
)

// TestDigestWorker_InvokesRunner proves the River worker drives its injected daily
// email-digest pass (the production caller seam for NOT-001).
func TestDigestWorker_InvokesRunner(t *testing.T) {
	called := 0
	w := NewDigestWorker(func(context.Context) (int, error) { called++; return 3, nil }, nil)
	if err := w.Work(context.Background(), &river.Job[DigestGenerateArgs]{}); err != nil {
		t.Fatalf("Work: %v", err)
	}
	if called != 1 {
		t.Fatalf("runner invoked %d times; want 1", called)
	}
}

// TestDigestWorker_NilRunnerFailsClosed proves a nil runner is a no-op (never a panic).
func TestDigestWorker_NilRunnerFailsClosed(t *testing.T) {
	w := NewDigestWorker(nil, nil)
	if err := w.Work(context.Background(), &river.Job[DigestGenerateArgs]{}); err != nil {
		t.Fatalf("nil-runner Work: %v", err)
	}
}

// TestDigestWorker_PropagatesError proves a pass error surfaces so River retries.
func TestDigestWorker_PropagatesError(t *testing.T) {
	want := errors.New("boom")
	w := NewDigestWorker(func(context.Context) (int, error) { return 0, want }, nil)
	if err := w.Work(context.Background(), &river.Job[DigestGenerateArgs]{}); !errors.Is(err, want) {
		t.Fatalf("want propagated error, got %v", err)
	}
}
