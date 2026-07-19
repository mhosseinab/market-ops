package analytics

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// collectMetrics installs a fresh ManualReader-backed meter provider BEFORE the
// telemetry constructor reads otel.Meter(...), runs emit, then returns the
// collected datapoints per counter name. Test fixtures and prod telemetry share
// the same field-name schema (CLAUDE.md observability).
func collectMetrics(t *testing.T, emit func(em *Emitter)) map[string][]metricdata.DataPoint[int64] {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	prev := otel.GetMeterProvider()
	otel.SetMeterProvider(provider)
	t.Cleanup(func() { otel.SetMeterProvider(prev) })

	em := NewEmitter(nil) // counter-only emitter; newTelemetry reads the meter now.
	emit(em)

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

// attrKeySet returns the attribute keys→values on a datapoint.
func attrKeySet(dp metricdata.DataPoint[int64]) map[string]string {
	keys := map[string]string{}
	for _, kv := range dp.Attributes.ToSlice() {
		keys[string(kv.Key)] = kv.Value.AsString()
	}
	return keys
}

// TestMetricLabels_NoTenantOrUnboundedKeys is the money-shot regression guard
// (issue #151, never-cut observability): a tenant UUID or unbounded version value
// must NEVER appear as a metric label KEY or VALUE. Emitting a fully-populated
// envelope yields datapoints whose key set is EXACTLY the bounded allowlist.
func TestMetricLabels_NoTenantOrUnboundedKeys(t *testing.T) {
	env := fullEnvelope()
	uuidStrings := []string{env.Organization.String(), env.Account.String(), env.Entity.String()}

	got := collectMetrics(t, func(em *Emitter) {
		if err := em.Emit(context.Background(), Event{Envelope: env, Family: FamilyBriefing, Name: "daily_digest_sent"}); err != nil {
			t.Fatalf("emit: %v", err)
		}
		if err := em.RecordCost(context.Background(), env, CostBriefing, 42); err != nil {
			t.Fatalf("record cost: %v", err)
		}
	})

	forbiddenKeys := []string{"organization_id", "marketplace_account_id", "entity", "entity_id", "currency_contract_version"}

	assertBounded := func(counter string, allowedKeys map[string]struct{}) {
		dps := got[counter]
		if len(dps) == 0 {
			t.Fatalf("counter %q emitted no datapoints", counter)
		}
		for _, dp := range dps {
			keys := attrKeySet(dp)
			// KEY allowlist: exactly the declared bounded set, nothing else.
			for k := range keys {
				if _, ok := allowedKeys[k]; !ok {
					t.Fatalf("counter %q emitted unexpected label key %q (not in bounded allowlist)", counter, k)
				}
			}
			for _, k := range forbiddenKeys {
				if _, present := keys[k]; present {
					t.Fatalf("counter %q emitted forbidden tenant/unbounded label key %q", counter, k)
				}
			}
			// VALUE guard: no label value equals any envelope UUID or the version.
			for k, v := range keys {
				for _, u := range uuidStrings {
					if v == u {
						t.Fatalf("counter %q label %q leaked tenant UUID value %q", counter, k, v)
					}
				}
				if v == env.CurrencyContractVersion {
					t.Fatalf("counter %q label %q leaked unbounded version value %q", counter, k, v)
				}
			}
		}
	}

	assertBounded("analytics.events", map[string]struct{}{
		"family": {}, "name": {}, "locale": {}, "region": {}, "source_surface": {},
	})
	assertBounded("analytics.cost_minor_units", map[string]struct{}{
		"cost_kind": {}, "locale": {}, "region": {}, "source_surface": {},
	})
}

// TestMetricLabels_CardinalityIndependentOfTenant proves emitting the SAME signal
// for N distinct org/account pairs yields exactly ONE datapoint per counter — the
// series count does not grow with tenant count (issue #151 acceptance test 2).
func TestMetricLabels_CardinalityIndependentOfTenant(t *testing.T) {
	got := collectMetrics(t, func(em *Emitter) {
		for i := 0; i < 25; i++ {
			env := fullEnvelope() // fresh org/account/entity UUIDs each iteration
			if err := em.Emit(context.Background(), Event{Envelope: env, Family: FamilyBriefing, Name: "daily_digest_sent"}); err != nil {
				t.Fatalf("emit %d: %v", i, err)
			}
			if err := em.RecordCost(context.Background(), env, CostBriefing, 1); err != nil {
				t.Fatalf("cost %d: %v", i, err)
			}
		}
	})

	for _, counter := range []string{"analytics.events", "analytics.cost_minor_units"} {
		if n := len(got[counter]); n != 1 {
			t.Fatalf("counter %q produced %d series for 25 tenants, want 1 (cardinality must not grow with tenants)", counter, n)
		}
	}
}

// TestMetricLabels_FreeTextBucketedToSentinel proves the cardinality budget holds
// against free text: an unrecognized name/locale/region/source_surface does NOT
// mint a new series — it buckets to the sentinel (issue #151 acceptance test 2).
func TestMetricLabels_FreeTextBucketedToSentinel(t *testing.T) {
	env := fullEnvelope()
	env.Locale = "zz-" + uuid.NewString()
	env.Region = "ZZ-" + uuid.NewString()
	env.SourceSurface = "surface-" + uuid.NewString()

	got := collectMetrics(t, func(em *Emitter) {
		if err := em.Emit(context.Background(), Event{Envelope: env, Family: FamilyBriefing, Name: "free-text-" + uuid.NewString()}); err != nil {
			t.Fatalf("emit: %v", err)
		}
		if err := em.RecordCost(context.Background(), env, CostBriefing, 1); err != nil {
			t.Fatalf("cost: %v", err)
		}
	})

	for _, dp := range got["analytics.events"] {
		keys := attrKeySet(dp)
		for _, k := range []string{"name", "locale", "region", "source_surface"} {
			if keys[k] != labelSentinel {
				t.Fatalf("events label %q = %q, want sentinel %q for free-text input", k, keys[k], labelSentinel)
			}
		}
	}
	for _, dp := range got["analytics.cost_minor_units"] {
		keys := attrKeySet(dp)
		for _, k := range []string{"locale", "region", "source_surface"} {
			if keys[k] != labelSentinel {
				t.Fatalf("cost label %q = %q, want sentinel %q for free-text input", k, keys[k], labelSentinel)
			}
		}
	}
}

// TestMetricLabels_BoundedSetPresentAndCorrect is the POSITIVE path: a recognized
// envelope carries the exact bounded label set with the right closed values, and
// family/cost_kind carry their declared enum values.
func TestMetricLabels_BoundedSetPresentAndCorrect(t *testing.T) {
	env := Envelope{
		Organization:            uuid.New(),
		Account:                 uuid.New(),
		Entity:                  uuid.New(),
		Locale:                  "fa-IR",
		Region:                  "IR",
		CurrencyContractVersion: "v1",
		SourceSurface:           "email_digest",
		Timestamp:               time.Now().UTC(),
	}

	got := collectMetrics(t, func(em *Emitter) {
		if err := em.Emit(context.Background(), Event{Envelope: env, Family: FamilyBriefing, Name: "daily_digest_sent"}); err != nil {
			t.Fatalf("emit: %v", err)
		}
		if err := em.RecordCost(context.Background(), env, CostBriefing, 7); err != nil {
			t.Fatalf("cost: %v", err)
		}
	})

	eventDPs := got["analytics.events"]
	if len(eventDPs) != 1 {
		t.Fatalf("events: got %d datapoints, want 1", len(eventDPs))
	}
	ek := attrKeySet(eventDPs[0])
	wantEvents := map[string]string{
		"family": "briefing", "name": "daily_digest_sent",
		"locale": "fa-IR", "region": "IR", "source_surface": "email_digest",
	}
	for k, v := range wantEvents {
		if ek[k] != v {
			t.Fatalf("events label %q = %q, want %q", k, ek[k], v)
		}
	}
	if len(ek) != len(wantEvents) {
		t.Fatalf("events key set = %v, want exactly %v", ek, wantEvents)
	}

	costDPs := got["analytics.cost_minor_units"]
	if len(costDPs) != 1 {
		t.Fatalf("cost: got %d datapoints, want 1", len(costDPs))
	}
	ck := attrKeySet(costDPs[0])
	wantCost := map[string]string{
		"cost_kind": "briefing",
		"locale":    "fa-IR", "region": "IR", "source_surface": "email_digest",
	}
	for k, v := range wantCost {
		if ck[k] != v {
			t.Fatalf("cost label %q = %q, want %q", k, ck[k], v)
		}
	}
	if len(ck) != len(wantCost) {
		t.Fatalf("cost key set = %v, want exactly %v", ck, wantCost)
	}
}
