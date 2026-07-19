package jobs

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func reopenJobFor(args MappingReopenedArgs, attempt, maxAttempts int) *river.Job[MappingReopenedArgs] {
	return &river.Job[MappingReopenedArgs]{
		JobRow: &rivertype.JobRow{ID: 1, Attempt: attempt, MaxAttempts: maxAttempts},
		Args:   args,
	}
}

func sampleReopenArgs() MappingReopenedArgs {
	id := uuid.New()
	return MappingReopenedArgs{
		EventID:    uuid.New(),
		AccountID:  uuid.New(),
		VariantID:  uuid.New(),
		IdentityID: id,
		Reason:     "merge",
		DedupKey:   id.String() + ":merge:1",
	}
}

// TestMappingReopenedArgs_Kind pins the stable River kind; it must never change once
// shipped (durable intents already in the queue are keyed by it).
func TestMappingReopenedArgs_Kind(t *testing.T) {
	if got := (MappingReopenedArgs{}).Kind(); got != "mapping_reopened" {
		t.Fatalf("Kind() = %q; want mapping_reopened", got)
	}
}

// TestMappingReopenedArgs_UniqueByDedupKey proves the intent is unique by dedup_key,
// so a duplicate/retried enqueue for the same (identity, reason, version) collapses to
// ONE durable record (event-dedup, §4.6).
func TestMappingReopenedArgs_UniqueByDedupKey(t *testing.T) {
	opts := (MappingReopenedArgs{}).InsertOpts()
	if !opts.UniqueOpts.ByArgs {
		t.Fatalf("InsertOpts must be unique ByArgs (dedup by dedup_key); got %+v", opts.UniqueOpts)
	}
}

// TestMappingReopenedWorker_NilRunnerFailsClosed proves the durable reopen intent is
// never silently completed when no consumer is wired: the worker returns an error so
// River retries (the intent is preserved), rather than acking and dropping it — the
// defect this fix closes (a committed reopen never loses its delivery).
func TestMappingReopenedWorker_NilRunnerFailsClosed(t *testing.T) {
	w := NewMappingReopenedWorker(nil, nil)
	err := w.Work(context.Background(), reopenJobFor(sampleReopenArgs(), 1, 25))
	if !errors.Is(err, errNoReopenRunner) {
		t.Fatalf("nil runner: err = %v; want errNoReopenRunner (fail closed)", err)
	}
}

// TestMappingReopenedWorker_RunnerErrorRetries proves a genuine consumer error is
// returned so River retries with backoff (restart-safe), not swallowed.
func TestMappingReopenedWorker_RunnerErrorRetries(t *testing.T) {
	boom := errors.New("expire boom")
	w := NewMappingReopenedWorker(func(context.Context, MappingReopenedArgs) error { return boom }, nil)
	err := w.Work(context.Background(), reopenJobFor(sampleReopenArgs(), 1, 25))
	if !errors.Is(err, boom) {
		t.Fatalf("runner error: err = %v; want the consumer error surfaced for retry", err)
	}
}

// TestMappingReopenedWorker_SnoozePassThrough proves a JobSnooze from the consumer is
// surfaced verbatim so River parks the intent without burning a retry attempt.
func TestMappingReopenedWorker_SnoozePassThrough(t *testing.T) {
	w := NewMappingReopenedWorker(func(context.Context, MappingReopenedArgs) error {
		return river.JobSnooze(time.Second)
	}, nil)
	err := w.Work(context.Background(), reopenJobFor(sampleReopenArgs(), 1, 25))
	var snooze *rivertype.JobSnoozeError
	if !errors.As(err, &snooze) {
		t.Fatalf("snooze: err = %v; want a JobSnoozeError passed through", err)
	}
}

// TestMappingReopenedWorker_SuccessCompletes proves a successful consumer run acks the
// intent (nil error → River completes it exactly-once-effectively).
func TestMappingReopenedWorker_SuccessCompletes(t *testing.T) {
	w := NewMappingReopenedWorker(func(context.Context, MappingReopenedArgs) error { return nil }, nil)
	if err := w.Work(context.Background(), reopenJobFor(sampleReopenArgs(), 1, 25)); err != nil {
		t.Fatalf("success: err = %v; want nil (intent completed)", err)
	}
}

// TestReopenDispatchTelemetry_EmitsLifecycleCounters proves the durable reopen-intent
// observability seam EMITS the never-cut boundary metrics (CLAUDE.md: a delivery path
// that engages without an emitted, traced event is a §4.6 bug). Test and prod share
// the field names.
func TestReopenDispatchTelemetry_EmitsLifecycleCounters(t *testing.T) {
	reader := metric.NewManualReader()
	provider := metric.NewMeterProvider(metric.WithReader(reader))
	prev := otel.GetMeterProvider()
	otel.SetMeterProvider(provider)
	t.Cleanup(func() { otel.SetMeterProvider(prev) })

	tel := newReopenDispatchTelemetry(nil)
	ctx := context.Background()
	args := sampleReopenArgs()

	tel.dispatched(ctx, args, 1, false)
	_, span := tel.claimed(ctx, args, 1, 1)
	span.End()
	tel.completed(ctx, args, 1)
	tel.snoozed(ctx, args, 1)
	tel.failed(ctx, args, 1, 25, 25, errors.New("boom"))

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(ctx, &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}
	got := map[string]int64{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if sum, ok := m.Data.(metricdata.Sum[int64]); ok {
				var total int64
				for _, dp := range sum.DataPoints {
					total += dp.Value
				}
				got[m.Name] = total
			}
		}
	}
	for _, name := range []string{
		"reopen_dispatch.intents_dispatched",
		"reopen_dispatch.intents_claimed",
		"reopen_dispatch.intents_completed",
		"reopen_dispatch.intents_snoozed",
		"reopen_dispatch.intents_failed",
	} {
		if got[name] < 1 {
			t.Fatalf("counter %q not emitted (got %d); observability seam incomplete", name, got[name])
		}
	}
}
