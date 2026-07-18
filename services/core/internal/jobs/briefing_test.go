package jobs

import (
	"context"
	"errors"
	"testing"

	"github.com/riverqueue/river"
)

// TestBriefingWorker_InvokesRunner proves the River worker drives its injected
// daily-briefing pass (the production caller seam for CHAT-010).
func TestBriefingWorker_InvokesRunner(t *testing.T) {
	called := 0
	w := NewBriefingWorker(func(context.Context) (int, error) { called++; return 2, nil }, nil)
	if err := w.Work(context.Background(), &river.Job[BriefingGenerateArgs]{}); err != nil {
		t.Fatalf("Work: %v", err)
	}
	if called != 1 {
		t.Fatalf("runner invoked %d times; want 1", called)
	}
}

// TestBriefingWorker_NilRunnerFailsClosed proves a nil runner is a no-op (never a panic).
func TestBriefingWorker_NilRunnerFailsClosed(t *testing.T) {
	w := NewBriefingWorker(nil, nil)
	if err := w.Work(context.Background(), &river.Job[BriefingGenerateArgs]{}); err != nil {
		t.Fatalf("nil-runner Work: %v", err)
	}
}

// TestBriefingWorker_PropagatesError proves a pass error surfaces so River retries.
func TestBriefingWorker_PropagatesError(t *testing.T) {
	want := errors.New("boom")
	w := NewBriefingWorker(func(context.Context) (int, error) { return 0, want }, nil)
	if err := w.Work(context.Background(), &river.Job[BriefingGenerateArgs]{}); !errors.Is(err, want) {
		t.Fatalf("want propagated error, got %v", err)
	}
}
