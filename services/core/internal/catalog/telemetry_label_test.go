package catalog

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// This file pins the #244 (PD-4) disposition for the catalog sync-streak telemetry:
// `account_id` on connector.sync_failure_streak / connector.sync_results /
// connector.sync_streak_seed_bound_exhausted is a DELIBERATE, BOUNDED per-account
// dimension — P0 has a small, fixed set of marketplace seller accounts, not an
// unbounded tenant population — required by the §20.1 ConnectorSyncFailureStreak
// alert (`max by (account_id, connector) (connector_sync_failure_streak) >= 3`,
// deploy/prometheus/rules/dk-p0-alerts.yml). It is NOT the issue #151 unbounded-
// cardinality/PII violation (that issue was about the all-tenant `analytics`
// counters); do not remove `account_id` here without a deliberate §20.1 alert
// redesign. These tests pin the label KEY SET only — never the account_id VALUE —
// so a future widening of the label set is a conscious decision, not silent drift.

// collectCatalogTelemetryMetrics installs a fresh ManualReader-backed meter provider
// BEFORE newSyncTelemetry reads otel.Meter(...), drives the sync-streak seam, and
// returns collected datapoints keyed by metric name across both Sum[int64] (counters)
// and Gauge[int64] (the streak gauge) instrument kinds.
func collectCatalogTelemetryMetrics(t *testing.T, drive func(tel *SyncTelemetry)) map[string][]metricdata.DataPoint[int64] {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	prev := otel.GetMeterProvider()
	otel.SetMeterProvider(provider)
	t.Cleanup(func() { otel.SetMeterProvider(prev) })

	tel := newSyncTelemetry(nil)
	drive(tel)

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}
	out := map[string][]metricdata.DataPoint[int64]{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			switch data := m.Data.(type) {
			case metricdata.Sum[int64]:
				out[m.Name] = append(out[m.Name], data.DataPoints...)
			case metricdata.Gauge[int64]:
				out[m.Name] = append(out[m.Name], data.DataPoints...)
			}
		}
	}
	return out
}

// catalogAttrKeySet returns the attribute keys on a datapoint.
func catalogAttrKeySet(dp metricdata.DataPoint[int64]) map[string]struct{} {
	keys := map[string]struct{}{}
	for _, kv := range dp.Attributes.ToSlice() {
		keys[string(kv.Key)] = struct{}{}
	}
	return keys
}

func assertExactKeySet(t *testing.T, counter string, dps []metricdata.DataPoint[int64], want map[string]struct{}) {
	t.Helper()
	if len(dps) == 0 {
		t.Fatalf("counter %q emitted no datapoints; observability seam incomplete", counter)
	}
	for _, dp := range dps {
		got := catalogAttrKeySet(dp)
		if len(got) != len(want) {
			t.Fatalf("counter %q key set = %v, want exactly %v", counter, got, want)
		}
		for k := range want {
			if _, ok := got[k]; !ok {
				t.Fatalf("counter %q missing required label key %q; got %v", counter, k, got)
			}
		}
		for k := range got {
			if _, ok := want[k]; !ok {
				t.Fatalf("counter %q emitted unexpected label key %q (not in bounded allowlist %v)", counter, k, want)
			}
		}
	}
}

// TestSyncTelemetry_StreakAndResultsLabelSet is the #244 (PD-4) pinning regression
// for the recordSyncResult path: connector.sync_failure_streak (gauge) and
// connector.sync_results (counter) each carry EXACTLY their documented bounded label
// key set — account_id + connector for the gauge, account_id + connector +
// disposition for the results counter. account_id is the deliberate bounded
// per-account dimension the §20.1 streak alert depends on (see file doc comment
// above), not an unbounded-cardinality leak.
func TestSyncTelemetry_StreakAndResultsLabelSet(t *testing.T) {
	acct := uuid.New()
	got := collectCatalogTelemetryMetrics(t, func(tel *SyncTelemetry) {
		tel.recordSyncResult(context.Background(), acct, uuid.New(), SyncHTTP5xx)
	})

	assertExactKeySet(t, "connector.sync_failure_streak", got["connector.sync_failure_streak"],
		map[string]struct{}{"account_id": {}, "connector": {}})
	assertExactKeySet(t, "connector.sync_results", got["connector.sync_results"],
		map[string]struct{}{"account_id": {}, "connector": {}, "disposition": {}})
}

// TestSyncTelemetry_SeedBoundExhaustedLabelSet is the #244 (PD-4) pinning regression
// for the restart bound-exhaustion signal (issue #211): connector.sync_streak_seed_
// bound_exhausted carries EXACTLY {account_id, connector} — the same deliberate
// bounded per-account dimension as the streak gauge, nothing wider.
func TestSyncTelemetry_SeedBoundExhaustedLabelSet(t *testing.T) {
	const bound = 2
	acct := uuid.New()
	rows := []SyncRunOutcome{
		{Account: acct, RunID: uuid.New(), Status: "failed", HasError: true},
		{Account: acct, RunID: uuid.New(), Status: "failed", HasError: true},
	}

	got := collectCatalogTelemetryMetrics(t, func(tel *SyncTelemetry) {
		tel.seedFromOutcomes(context.Background(), rows, bound)
	})

	assertExactKeySet(t, "connector.sync_streak_seed_bound_exhausted",
		got["connector.sync_streak_seed_bound_exhausted"],
		map[string]struct{}{"account_id": {}, "connector": {}})
}
