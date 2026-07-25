package conversation

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// Issue #412 — counter half of the tenant-integrity observability seam.
//
// telemetry_db_test.go pins the STRUCTURED LOG (positive and negative) against a
// real database. This file pins the METRIC, which no DB is needed to drive: the
// counter must actually fire, and it must carry ONLY the bounded `seam` label
// documented on telemetry.accountDenied. CLAUDE.md makes TDD mandatory for
// observability field emission, and §4.6 tenant integrity is a never-cut boundary
// — CLAUDE.md requires a counter on every one of those, so an unfired counter or a
// silently widened label set is a release-blocking regression, not a nit.
//
// Shape follows the established repo idiom for this exact concern:
// internal/analytics/telemetry_test.go (the analytics.tenant_rejections /
// ErrCrossTenant seam from #125 that conversation.go cites) and
// internal/catalog/telemetry_label_test.go.

const accountRejectionsCounter = "conversation.account_ownership_rejections"

// collectConversationMetrics installs a fresh ManualReader-backed meter provider
// BEFORE newTelemetry reads otel.Meter(...), drives the denial seam, then returns
// the collected datapoints keyed by metric name.
func collectConversationMetrics(t *testing.T, drive func(tel *telemetry)) map[string][]metricdata.DataPoint[int64] {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	prev := otel.GetMeterProvider()
	otel.SetMeterProvider(provider)
	t.Cleanup(func() { otel.SetMeterProvider(prev) })

	drive(newTelemetry(nil))

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
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

// conversationAttrs returns the attribute key→value pairs on a datapoint.
func conversationAttrs(dp metricdata.DataPoint[int64]) map[string]string {
	attrs := map[string]string{}
	for _, kv := range dp.Attributes.ToSlice() {
		attrs[string(kv.Key)] = kv.Value.AsString()
	}
	return attrs
}

// TestAccountRejectionCounterFires is the positive emission proof: one denial at
// the application seam yields exactly one increment of value 1 carrying
// seam=org_scoped_insert. Without this, the never-cut boundary could stop counting
// and every gate would stay green.
func TestAccountRejectionCounterFires(t *testing.T) {
	org, account := uuid.New(), uuid.New()

	got := collectConversationMetrics(t, func(tel *telemetry) {
		tel.accountDenied(context.Background(), seamOrgScopedInsert, org, account)
	})

	dps := got[accountRejectionsCounter]
	if len(dps) != 1 {
		t.Fatalf("%s datapoints = %d, want exactly 1", accountRejectionsCounter, len(dps))
	}
	if dps[0].Value != 1 {
		t.Fatalf("%s value = %d, want 1", accountRejectionsCounter, dps[0].Value)
	}
	if seam := conversationAttrs(dps[0])["seam"]; seam != string(seamOrgScopedInsert) {
		t.Fatalf("seam = %q, want %q", seam, seamOrgScopedInsert)
	}
}

// TestAccountRejectionCounterLabelsAreBounded enforces the CARDINALITY INVARIANT
// documented on telemetry.accountDenied: the ONLY label key is `seam`, and no
// caller-influenced UUID may appear as a label VALUE. The organization and account
// ids belong in the structured log, never in a metric series — and the OWNING
// organization of the requested account must never be resolved at all (the denial
// is deliberately not an existence oracle, not even in telemetry).
//
// This test must FAIL the instant someone adds organization_id, account_id, or any
// other unbounded attribute to the counter.
func TestAccountRejectionCounterLabelsAreBounded(t *testing.T) {
	org, account := uuid.New(), uuid.New()

	got := collectConversationMetrics(t, func(tel *telemetry) {
		tel.accountDenied(context.Background(), seamOrgScopedInsert, org, account)
		tel.accountDenied(context.Background(), seamCompositeForeignKey, org, account)
	})

	dps := got[accountRejectionsCounter]
	if len(dps) == 0 {
		t.Fatalf("%s emitted no datapoints", accountRejectionsCounter)
	}
	forbiddenValues := []string{org.String(), account.String()}
	for _, dp := range dps {
		attrs := conversationAttrs(dp)
		if len(attrs) != 1 {
			t.Fatalf("%s label set = %v, want exactly {seam}", accountRejectionsCounter, attrs)
		}
		if _, ok := attrs["seam"]; !ok {
			t.Fatalf("%s label set = %v, missing the bounded seam label", accountRejectionsCounter, attrs)
		}
		for k, v := range attrs {
			for _, forbidden := range forbiddenValues {
				if v == forbidden {
					t.Fatalf("%s label %q leaked a tenant UUID value %q", accountRejectionsCounter, k, v)
				}
			}
		}
	}
}

// TestAccountRejectionCounterSeamsAreClosed proves the label domain is the closed
// two-value set the metric documents, and that cardinality does NOT grow with the
// tenant population: 50 denials across 50 distinct org/account pairs collapse to
// exactly one series per seam. A tenant-derived label would produce 50.
func TestAccountRejectionCounterSeamsAreClosed(t *testing.T) {
	const perSeam = 25

	got := collectConversationMetrics(t, func(tel *telemetry) {
		for i := 0; i < perSeam; i++ {
			tel.accountDenied(context.Background(), seamOrgScopedInsert, uuid.New(), uuid.New())
			tel.accountDenied(context.Background(), seamCompositeForeignKey, uuid.New(), uuid.New())
		}
	})

	dps := got[accountRejectionsCounter]
	if len(dps) != 2 {
		t.Fatalf("%s produced %d series for %d tenants, want 2 (one per closed seam)", accountRejectionsCounter, len(dps), perSeam*2)
	}
	bySeam := map[string]int64{}
	for _, dp := range dps {
		bySeam[conversationAttrs(dp)["seam"]] += dp.Value
	}
	for _, seam := range []accountRejectionSeam{seamOrgScopedInsert, seamCompositeForeignKey} {
		if bySeam[string(seam)] != perSeam {
			t.Fatalf("seam %q counted %d, want %d", seam, bySeam[string(seam)], perSeam)
		}
	}
}

// TestFallbackMeterIsGenuinelyNoOp pins the ERROR path of newTelemetry. The
// fallback exists so a metric-wiring hiccup can never break the fail-closed denial
// path it observes — which only holds if the fallback is a REAL no-op meter, not
// another instrument from the SAME global provider that just failed under a scope
// literally named "noop". Two properties must hold:
//
//  1. it records nothing into whatever provider is globally installed, and
//  2. it plants no stray "noop" instrumentation scope in the collected output.
//
// Both fail if the fallback is otel.Meter("noop").
func TestFallbackMeterIsGenuinelyNoOp(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	prev := otel.GetMeterProvider()
	otel.SetMeterProvider(provider)
	t.Cleanup(func() { otel.SetMeterProvider(prev) })

	c, err := noopMeter.Int64Counter(accountRejectionsCounter)
	if err != nil {
		t.Fatalf("fallback counter: %v", err)
	}
	if c == nil {
		t.Fatal("fallback counter is nil; the denial path would panic on Add")
	}
	c.Add(context.Background(), 1)

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}
	for _, sm := range rm.ScopeMetrics {
		if sm.Scope.Name == "noop" {
			t.Fatalf("fallback planted a stray %q instrumentation scope in the global provider", sm.Scope.Name)
		}
		for _, m := range sm.Metrics {
			if m.Name == accountRejectionsCounter {
				t.Fatalf("fallback recorded into the global provider (scope %q); it is not a no-op meter", sm.Scope.Name)
			}
		}
	}
}

// TestNilTelemetryDoesNotPanic pins the fail-open contract on the observability
// seam itself: a zero-value Store built without NewStore must degrade to silence,
// never take down the fail-closed denial path it observes (no panic across a
// runtime boundary).
func TestNilTelemetryDoesNotPanic(t *testing.T) {
	var tel *telemetry
	tel.accountDenied(context.Background(), seamOrgScopedInsert, uuid.New(), uuid.New())
}
