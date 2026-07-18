package execution

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/mhosseinab/market-ops/services/core/internal/db"
)

// TestTelemetry_EmitsGateBlockAndDedupCounters proves the execution-path
// observability seam actually EMITS the never-cut boundary metrics (CLAUDE.md:
// "observability field emission" is mandatory-TDD; test and prod share the schema).
// It installs a manual-reader MeterProvider, drives the telemetry helper, and reads
// the counters back by name.
func TestTelemetry_EmitsGateBlockAndDedupCounters(t *testing.T) {
	reader := metric.NewManualReader()
	provider := metric.NewMeterProvider(metric.WithReader(reader))
	prev := otel.GetMeterProvider()
	otel.SetMeterProvider(provider)
	t.Cleanup(func() { otel.SetMeterProvider(prev) })

	tel := newTelemetry(nil)
	ctx := context.Background()
	card := db.ApprovalCard{ActionID: [16]byte{1}, ParameterVersion: 1, ContextVersion: 1}

	tel.gateBlocked(ctx, card, GateCosts, ModeWrite)
	tel.dedupHit(ctx, "action:x:pv:1")
	tel.recommendOnlyFallback(ctx, card)
	tel.wroteExternal(ctx, card, StatePendingReconciliation)

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
		"execution.gate_blocks", "execution.dedup_hits", "execution.recommend_only",
		"execution.pending_reconciliation", "execution.write_attempts",
	} {
		if got[name] < 1 {
			t.Fatalf("counter %q not emitted (got %d); observability seam incomplete", name, got[name])
		}
	}
}
