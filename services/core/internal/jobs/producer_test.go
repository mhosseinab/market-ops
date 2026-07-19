package jobs

import (
	"context"
	"errors"
	"testing"

	"github.com/riverqueue/river"
)

// TestMarketEventProduceWorker_InvokesRunner proves the River worker drives its
// injected market-event production pass (the runtime producer seam for EVT-001..005).
func TestMarketEventProduceWorker_InvokesRunner(t *testing.T) {
	called := 0
	w := NewMarketEventProduceWorker(func(context.Context) (int, error) { called++; return 2, nil }, nil)
	if err := w.Work(context.Background(), &river.Job[MarketEventProduceArgs]{}); err != nil {
		t.Fatalf("Work: %v", err)
	}
	if called != 1 {
		t.Fatalf("runner invoked %d times; want 1", called)
	}
}

// TestMarketEventProduceWorker_NilRunnerFailsClosed proves a nil runner is a no-op
// (fail closed, never a panic) — no producer configured means no events, not a crash.
func TestMarketEventProduceWorker_NilRunnerFailsClosed(t *testing.T) {
	w := NewMarketEventProduceWorker(nil, nil)
	if err := w.Work(context.Background(), &river.Job[MarketEventProduceArgs]{}); err != nil {
		t.Fatalf("nil-runner Work: %v", err)
	}
}

// TestMarketEventProduceWorker_PropagatesError proves a pass error surfaces so
// River retries (idempotency gates the retry).
func TestMarketEventProduceWorker_PropagatesError(t *testing.T) {
	want := errors.New("boom")
	w := NewMarketEventProduceWorker(func(context.Context) (int, error) { return 0, want }, nil)
	if err := w.Work(context.Background(), &river.Job[MarketEventProduceArgs]{}); !errors.Is(err, want) {
		t.Fatalf("want propagated error, got %v", err)
	}
}

// TestWorkersRegistersMarketEventProducer proves the worker registry includes the
// producer worker so a running core actually schedules event production.
func TestWorkersRegistersMarketEventProducer(t *testing.T) {
	workers, err := NewWorkers(nil, ExecutionRunners{})
	if err != nil {
		t.Fatalf("NewWorkers: %v", err)
	}
	// AddWorkerSafely would have failed above on a duplicate; a second explicit add
	// of the same kind must now conflict, proving the producer worker is registered.
	err = river.AddWorkerSafely(workers, NewMarketEventProduceWorker(nil, nil))
	if err == nil {
		t.Fatal("market-event producer worker was not registered by NewWorkers")
	}
}
