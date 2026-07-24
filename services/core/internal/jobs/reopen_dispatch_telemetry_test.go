package jobs

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// collectReopenDispatchMetrics installs a fresh ManualReader-backed meter provider
// BEFORE newReopenDispatchTelemetry reads otel.Meter(...), drives the full lifecycle,
// then returns the collected Sum[int64] datapoints per counter name. Test fixtures
// and prod telemetry share the same field-name schema (CLAUDE.md observability).
func collectReopenDispatchMetrics(t *testing.T) map[string][]metricdata.DataPoint[int64] {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	prev := otel.GetMeterProvider()
	otel.SetMeterProvider(provider)
	t.Cleanup(func() { otel.SetMeterProvider(prev) })

	tel := newReopenDispatchTelemetry(nil)
	ctx := context.Background()
	args := MappingReopenedArgs{
		EventID:    uuid.New(),
		AccountID:  uuid.New(),
		VariantID:  uuid.New(),
		IdentityID: uuid.New(),
		Reason:     "manual_reopen",
		DedupKey:   "dedup-key",
	}

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
	out := map[string][]metricdata.DataPoint[int64]{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if sum, ok := m.Data.(metricdata.Sum[int64]); ok {
				out[m.Name] = append(out[m.Name], sum.DataPoints...)
			}
		}
	}
	return out
}

// reopenDispatchAttrKeys returns the attribute keys on a datapoint.
func reopenDispatchAttrKeys(dp metricdata.DataPoint[int64]) map[string]struct{} {
	keys := map[string]struct{}{}
	for _, kv := range dp.Attributes.ToSlice() {
		keys[string(kv.Key)] = struct{}{}
	}
	return keys
}

// TestReopenDispatchTelemetry_NoTenantUUIDMetricLabels is the #244 (PD-4) pinning
// regression: reopenDispatchTelemetry's five lifecycle COUNTERS
// (intents_dispatched/claimed/completed/snoozed/failed) carry no tenant/identity UUID
// as a metric label. Per the accepted #244 escalation, `identity_id`/`account_id` on
// this seam live ONLY in trace attributes (reopenIntentAttrs, consumed by
// tracer.Start) and structured logs, never on a metric counter — every counter here
// emits only the bounded Bool labels `dedup_skipped`/`terminal`, or none at all. This
// test pins that state so a future counter change is a conscious decision (CLAUDE.md:
// "the same field names are emitted by tests and prod telemetry").
func TestReopenDispatchTelemetry_NoTenantUUIDMetricLabels(t *testing.T) {
	got := collectReopenDispatchMetrics(t)

	allowedByCounter := map[string]map[string]struct{}{
		"reopen_dispatch.intents_dispatched": {"dedup_skipped": {}},
		"reopen_dispatch.intents_claimed":    {},
		"reopen_dispatch.intents_completed":  {},
		"reopen_dispatch.intents_snoozed":    {},
		"reopen_dispatch.intents_failed":     {"terminal": {}},
	}
	forbidden := []string{"identity_id", "account_id", "variant_id", "event_id"}

	for name, allowed := range allowedByCounter {
		dps := got[name]
		if len(dps) == 0 {
			t.Fatalf("counter %q emitted no datapoints; observability seam incomplete", name)
		}
		for _, dp := range dps {
			keys := reopenDispatchAttrKeys(dp)
			for k := range keys {
				if _, ok := allowed[k]; !ok {
					t.Fatalf("counter %q emitted unexpected label key %q (not in bounded allowlist %v)", name, k, allowed)
				}
			}
			for _, k := range forbidden {
				if _, present := keys[k]; present {
					t.Fatalf("counter %q emitted forbidden tenant/identity UUID label key %q", name, k)
				}
			}
		}
	}
}
