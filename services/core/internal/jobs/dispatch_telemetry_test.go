package jobs

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// TestDispatchTelemetry_EmitsLifecycleCounters proves the durable-intent
// observability seam EMITS the never-cut boundary metrics (issue #92 acceptance:
// "intent backlog and terminal failures are observable"; CLAUDE.md: observability
// field emission is mandatory-TDD). Backlog is dispatched minus completed; a
// terminal failure is intents_failed with terminal=true. Test and prod share names.
func TestDispatchTelemetry_EmitsLifecycleCounters(t *testing.T) {
	reader := metric.NewManualReader()
	provider := metric.NewMeterProvider(metric.WithReader(reader))
	prev := otel.GetMeterProvider()
	otel.SetMeterProvider(provider)
	t.Cleanup(func() { otel.SetMeterProvider(prev) })

	tel := newDispatchTelemetry(nil)
	ctx := context.Background()
	args := ExecuteApprovedArgs{CardID: uuid.New(), ActionID: uuid.New(), ParameterVersion: 1, ContextVersion: 1}

	tel.dispatched(ctx, args, 1, false)
	_, span := tel.claimed(ctx, args, 1, 1)
	span.End()
	tel.completed(ctx, args, 1)
	tel.snoozed(ctx, args, 1)
	// A final-attempt failure is the observable TERMINAL-failure signal.
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
		"execution_dispatch.intents_dispatched",
		"execution_dispatch.intents_claimed",
		"execution_dispatch.intents_completed",
		"execution_dispatch.intents_snoozed",
		"execution_dispatch.intents_failed",
	} {
		if got[name] < 1 {
			t.Fatalf("counter %q not emitted (got %d); observability seam incomplete", name, got[name])
		}
	}
}
