package analytics

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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
	return collectMetricsWith(t, func() *Emitter { return NewEmitter(nil) }, emit)
}

// collectMetricsWith is collectMetrics for a caller-BUILT emitter (e.g. one over a
// store double). build runs AFTER the ManualReader-backed provider is installed, so
// newTelemetry binds to the test meter — the same ordering requirement collectMetrics
// has. Both share one schema with prod telemetry (CLAUDE.md observability).
func collectMetricsWith(t *testing.T, build func() *Emitter, emit func(em *Emitter)) map[string][]metricdata.DataPoint[int64] {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	prev := otel.GetMeterProvider()
	otel.SetMeterProvider(provider)
	t.Cleanup(func() { otel.SetMeterProvider(prev) })

	em := build() // newTelemetry reads the meter NOW, so build must run here.
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
		if err := em.Emit(context.Background(), Event{
			Envelope: env, Family: FamilyBriefing, Name: "daily_digest_sent",
			DedupKey: DedupKey(FamilyBriefing, "daily_digest_sent", "labels"),
		}); err != nil {
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
			if err := em.Emit(context.Background(), Event{
				Envelope: env, Family: FamilyBriefing, Name: "daily_digest_sent",
				DedupKey: DedupKey(FamilyBriefing, "daily_digest_sent", strconv.Itoa(i)),
			}); err != nil {
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

// TestMetricLabels_OpenDimensionsBucketedToSentinel proves the cardinality budget
// holds for the genuinely OPEN/config/caller-derived dimensions: an unrecognized
// locale/region/source_surface does NOT mint a new series — it buckets to the
// sentinel (issue #151 acceptance test 2). name is DELIBERATELY excluded here: it is
// a closed developer-defined constant and MUST pass through verbatim (see
// TestMetricLabels_ClosedEventNamePassesThroughVerbatim), so the §18 dashboards can
// slice `analytics_events by (name)`. This test must FAIL if someone re-adds name
// bounding, because a bounded name would collapse the closed constant below.
func TestMetricLabels_OpenDimensionsBucketedToSentinel(t *testing.T) {
	env := fullEnvelope()
	env.Locale = "zz-" + uuid.NewString()
	env.Region = "ZZ-" + uuid.NewString()
	env.SourceSurface = "surface-" + uuid.NewString()

	const closedName = "recommendation_ranked" // a real closed constant, not free text

	got := collectMetrics(t, func(em *Emitter) {
		if err := em.Emit(context.Background(), Event{
			Envelope: env, Family: FamilyRecommendation, Name: closedName,
			DedupKey: DedupKey(FamilyRecommendation, closedName, "sentinel"),
		}); err != nil {
			t.Fatalf("emit: %v", err)
		}
		if err := em.RecordCost(context.Background(), env, CostBriefing, 1); err != nil {
			t.Fatalf("cost: %v", err)
		}
	})

	for _, dp := range got["analytics.events"] {
		keys := attrKeySet(dp)
		for _, k := range []string{"locale", "region", "source_surface"} {
			if keys[k] != labelSentinel {
				t.Fatalf("events label %q = %q, want sentinel %q for out-of-allowlist input", k, keys[k], labelSentinel)
			}
		}
		// name is a closed constant — it must NOT be bucketed to the sentinel.
		if keys["name"] != closedName {
			t.Fatalf("events label \"name\" = %q, want closed constant %q emitted verbatim (name must NOT be bounded)", keys["name"], closedName)
		}
	}
	for _, dp := range got["analytics.cost_minor_units"] {
		keys := attrKeySet(dp)
		for _, k := range []string{"locale", "region", "source_surface"} {
			if keys[k] != labelSentinel {
				t.Fatalf("cost label %q = %q, want sentinel %q for out-of-allowlist input", k, keys[k], labelSentinel)
			}
		}
	}
}

// TestMetricLabels_ClosedEventNamePassesThroughVerbatim is the producer/consumer
// contract guard for the §18 dashboards (issue #151 area review): every event name
// is a closed developer-defined constant and MUST reach the metric as its raw value,
// because checked-in Grafana panels slice `analytics_events by (name)` for the
// recommendation and conversation families — including the FREE-TEXT-CONTAINMENT
// panel (dk-chat.json, name=~".*contain.*|.*guidance.*|.*blocked.*"), a never-cut
// observability boundary. If name were ever bounded to an allowlist+sentinel, these
// names would collapse to "other" and the containment metric would silently read
// ZERO — so this test must FAIL the instant name bounding is re-introduced.
func TestMetricLabels_ClosedEventNamePassesThroughVerbatim(t *testing.T) {
	env := fullEnvelope()

	// Representative closed names across the families the dashboards slice by (name),
	// including the containment-panel regex tokens (contain/guidance/blocked).
	names := []string{
		"recommendation_ranked",
		"conversation_started",
		"free_text_contained",
		"chat_guidance_shown",
		"conversation_blocked",
	}

	for _, name := range names {
		name := name
		got := collectMetrics(t, func(em *Emitter) {
			if err := em.Emit(context.Background(), Event{
				Envelope: env, Family: FamilyConversation, Name: name,
				DedupKey: DedupKey(FamilyConversation, name, "verbatim"),
			}); err != nil {
				t.Fatalf("emit %q: %v", name, err)
			}
		})
		dps := got["analytics.events"]
		if len(dps) != 1 {
			t.Fatalf("name %q: got %d datapoints, want 1", name, len(dps))
		}
		if v := attrKeySet(dps[0])["name"]; v != name {
			t.Fatalf("events label \"name\" = %q, want %q emitted verbatim (dashboards slice by (name); name must never bucket to sentinel)", v, name)
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
		if err := em.Emit(context.Background(), Event{
			Envelope: env, Family: FamilyBriefing, Name: "daily_digest_sent",
			DedupKey: DedupKey(FamilyBriefing, "daily_digest_sent", "bounded"),
		}); err != nil {
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

// dedupEmitter builds an emitter over a store double owning account for org. When
// suppressed is true the store returns pgx.ErrNoRows from the insert — exactly what
// `ON CONFLICT ... DO NOTHING RETURNING *` returns when the partial unique index
// rejects a duplicate — so the suppression path is exercised without a database.
func dedupEmitter(org, account uuid.UUID, suppressed bool) *Emitter {
	fs := &fakeStore{owner: map[uuid.UUID]uuid.UUID{account: org}}
	if suppressed {
		fs.insertErr = pgx.ErrNoRows
	}
	return newEmitterWithStore(fs)
}

// TestEmit_SuppressedDuplicateIsObservableAndNotDoubleCounted is the observability
// half of the event-deduplication invariant (§4.6 never-cut). A duplicate suppressed
// by the (marketplace_account_id, dedup_key) partial unique index must:
//   - NOT increment analytics.events — otherwise the §18 dashboards would count an
//     event that has no row behind it, re-introducing the double-count the dedup key
//     exists to prevent;
//   - increment analytics.events_deduplicated instead, so suppression is OBSERVED,
//     never a silent swallow (a silent no-op and a healthy write must be
//     distinguishable in telemetry).
func TestEmit_SuppressedDuplicateIsObservableAndNotDoubleCounted(t *testing.T) {
	org, account := uuid.New(), uuid.New()
	env := tenantEnvelope(org, account)
	env.SourceSurface = "email_digest"

	got := collectMetricsWith(t,
		func() *Emitter { return dedupEmitter(org, account, true) },
		func(em *Emitter) {
			// A suppressed duplicate is an idempotent SUCCESS, not an error.
			if err := em.Emit(context.Background(), Event{
				Envelope: env, Family: FamilyBriefing, Name: "daily_digest_sent",
				DedupKey: DedupKey(FamilyBriefing, "daily_digest_sent", "digest-1"),
			}); err != nil {
				t.Fatalf("suppressed duplicate returned an error: %v", err)
			}
		})

	if dps := got["analytics.events"]; len(dps) != 0 {
		t.Fatalf("suppressed duplicate incremented analytics.events (%d datapoints), want 0 — a deduplicated event must never be counted twice", len(dps))
	}
	dps := got["analytics.events_deduplicated"]
	if len(dps) != 1 {
		t.Fatalf("analytics.events_deduplicated datapoints = %d, want 1 (suppression must be observable, never silent)", len(dps))
	}
	if dps[0].Value != 1 {
		t.Fatalf("analytics.events_deduplicated = %d, want 1", dps[0].Value)
	}
	keys := attrKeySet(dps[0])
	allowed := map[string]struct{}{
		"family": {}, "name": {}, "locale": {}, "region": {}, "source_surface": {},
	}
	for k, v := range keys {
		if _, ok := allowed[k]; !ok {
			t.Fatalf("analytics.events_deduplicated emitted unexpected label key %q (bounded allowlist only)", k)
		}
		// The dedup key itself is unbounded and tenant-derived: it is PERSISTED on
		// analytics_events, never a metric label (issue #151/#244).
		if v == DedupKey(FamilyBriefing, "daily_digest_sent", "digest-1") {
			t.Fatalf("analytics.events_deduplicated label %q leaked the dedup key value", k)
		}
		for _, u := range []string{org.String(), account.String()} {
			if v == u {
				t.Fatalf("analytics.events_deduplicated label %q leaked tenant UUID %q", k, v)
			}
		}
	}
}

// TestEmit_FirstWriteCountsOnceAndIsNotDeduplicated is the positive counterpart: a
// genuinely NEW event increments analytics.events exactly once and does NOT touch
// the deduplication counter, so the two signals can never be conflated.
func TestEmit_FirstWriteCountsOnceAndIsNotDeduplicated(t *testing.T) {
	org, account := uuid.New(), uuid.New()
	env := tenantEnvelope(org, account)

	got := collectMetricsWith(t,
		func() *Emitter { return dedupEmitter(org, account, false) },
		func(em *Emitter) {
			if err := em.Emit(context.Background(), Event{
				Envelope: env, Family: FamilyBriefing, Name: "daily_digest_sent",
				DedupKey: DedupKey(FamilyBriefing, "daily_digest_sent", "digest-1"),
			}); err != nil {
				t.Fatalf("emit: %v", err)
			}
		})

	if dps := got["analytics.events"]; len(dps) != 1 || dps[0].Value != 1 {
		t.Fatalf("analytics.events = %+v, want exactly one datapoint of value 1", dps)
	}
	if dps := got["analytics.events_deduplicated"]; len(dps) != 0 {
		t.Fatalf("a first write incremented analytics.events_deduplicated (%d datapoints), want 0", len(dps))
	}
}

// TestEmit_SinkFailureIsObservable is the ANALYTICS-SINK-OUTAGE observability guard
// (issue #111 acceptance criterion "an unavailable analytics sink is observable";
// review finding F2). The failure scenario it closes: Postgres is unreachable during
// the nightly digest fan-out. Digests still send (correct — analytics is advisory and
// never rolls back delivery), every Emit errors, and with no failure counter the four
// analytics series all read FLAT ZERO — byte-identical to "no digests were due
// today". No alert can fire on that, and per CLAUDE.md a seam whose telemetry cannot
// distinguish failure from correct behavior is incomplete.
//
// So a failed emit must increment analytics.emit_failures and must increment NEITHER
// analytics.events (nothing was written) NOR analytics.events_deduplicated (nothing
// was suppressed) — an infrastructure outage must never be readable as deduplication.
// The counter is deliberately LABEL-FREE, mirroring tenantRejects/entityRejects: its
// only natural dimensions are tenant identifiers, which are never metric labels
// (issue #151/#244).
func TestEmit_SinkFailureIsObservable(t *testing.T) {
	org, account := uuid.New(), uuid.New()
	env := tenantEnvelope(org, account)
	env.SourceSurface = "email_digest"

	sinkDown := errors.New("dial tcp: connect: connection refused")

	cases := []struct {
		name  string
		build func() *Emitter
	}{
		{
			// The INSERT itself fails (sink unreachable mid-write).
			name: "insert_failure",
			build: func() *Emitter {
				return newEmitterWithStore(&fakeStore{
					owner:     map[uuid.UUID]uuid.UUID{account: org},
					insertErr: sinkDown,
				})
			},
		},
		{
			// The authoritative account lookup fails — an INFRASTRUCTURE error, not a
			// tenant conflict, so it must NOT read as a tenant rejection either.
			name: "account_resolution_failure",
			build: func() *Emitter {
				return newEmitterWithStore(&fakeStore{
					owner:  map[uuid.UUID]uuid.UUID{account: org},
					getErr: sinkDown,
				})
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			var emitErr error
			got := collectMetricsWith(t, tc.build, func(em *Emitter) {
				emitErr = em.Emit(context.Background(), Event{
					Envelope: env, Family: FamilyBriefing, Name: "daily_digest_sent",
					DedupKey: DedupKey(FamilyBriefing, "daily_digest_sent", "digest-1"),
				})
			})

			if emitErr == nil {
				t.Fatal("a failed sink write returned nil; an outage must never read as success")
			}
			dps := got["analytics.emit_failures"]
			if len(dps) != 1 {
				t.Fatalf("analytics.emit_failures datapoints = %d, want 1 — an unavailable analytics sink must be OBSERVABLE, not indistinguishable from an idle pipe", len(dps))
			}
			if dps[0].Value != 1 {
				t.Fatalf("analytics.emit_failures = %d, want 1", dps[0].Value)
			}
			if n := dps[0].Attributes.Len(); n != 0 {
				t.Fatalf("analytics.emit_failures carried %d labels, want 0 (label-free: its only natural dimensions are tenant identifiers)", n)
			}
			if n := len(got["analytics.events"]); n != 0 {
				t.Fatalf("a failed emit incremented analytics.events (%d datapoints), want 0 — nothing was written", n)
			}
			if n := len(got["analytics.events_deduplicated"]); n != 0 {
				t.Fatalf("a failed emit incremented analytics.events_deduplicated (%d datapoints), want 0 — an outage is not a suppressed duplicate", n)
			}
		})
	}
}

// TestEmit_SuccessAndTenantRejectionLeaveEmitFailuresUntouched is the counterpart to
// TestEmit_SinkFailureIsObservable: the failure counter must mean INFRASTRUCTURE
// failure and nothing else. A healthy write and a fail-closed cross-tenant rejection
// (which has its own tenant_rejections signal) must both leave it at zero, or an
// alert on it would fire on correct behavior.
func TestEmit_SuccessAndTenantRejectionLeaveEmitFailuresUntouched(t *testing.T) {
	org, account := uuid.New(), uuid.New()

	t.Run("healthy_write", func(t *testing.T) {
		got := collectMetricsWith(t,
			func() *Emitter { return dedupEmitter(org, account, false) },
			func(em *Emitter) {
				if err := em.Emit(context.Background(), Event{
					Envelope: tenantEnvelope(org, account), Family: FamilyBriefing,
					Name:     "daily_digest_sent",
					DedupKey: DedupKey(FamilyBriefing, "daily_digest_sent", "digest-1"),
				}); err != nil {
					t.Fatalf("emit: %v", err)
				}
			})
		if n := len(got["analytics.emit_failures"]); n != 0 {
			t.Fatalf("a healthy write incremented analytics.emit_failures (%d datapoints), want 0", n)
		}
	})

	t.Run("cross_tenant_rejection", func(t *testing.T) {
		got := collectMetricsWith(t,
			func() *Emitter {
				return newEmitterWithStore(&fakeStore{owner: map[uuid.UUID]uuid.UUID{account: org}})
			},
			func(em *Emitter) {
				// A DIFFERENT organization claiming this account: fail-closed, and a
				// tenant rejection — never an infrastructure failure.
				if err := em.Emit(context.Background(), Event{
					Envelope: tenantEnvelope(uuid.New(), account), Family: FamilyBriefing,
					Name:     "daily_digest_sent",
					DedupKey: DedupKey(FamilyBriefing, "daily_digest_sent", "digest-1"),
				}); !errors.Is(err, ErrCrossTenant) {
					t.Fatalf("got %v, want ErrCrossTenant", err)
				}
			})
		if n := len(got["analytics.emit_failures"]); n != 0 {
			t.Fatalf("a cross-tenant rejection incremented analytics.emit_failures (%d datapoints), want 0 (it has its own tenant_rejections signal)", n)
		}
	})
}

// TestIssue130_DigestSendEmitsNoBriefingCost is the issue #130 regression guard.
// A daily digest LINKS an already-generated briefing (§6.8) — it does NOT generate
// one — so the digest send must emit its briefing-family ANALYTICS EVENT but NO
// §17.3 briefing COST. The old code recorded RecordCost(CostBriefing, itemCount),
// which both mis-scaled a money/minor-unit counter with an item COUNT and risked
// double-counting the real briefing spend owned by the S23 generation path.
//
// The emit block below mirrors the analytics operations the cmd/core digest
// SentObserver performs after #130 (event only, no cost). It asserts the
// cost_minor_units counter is NEVER incremented from the digest path.
func TestIssue130_DigestSendEmitsNoBriefingCost(t *testing.T) {
	env := fullEnvelope()
	env.SourceSurface = "email_digest"
	const itemCount = 12

	got := collectMetrics(t, func(em *Emitter) {
		// Exactly what the digest observer does post-#130: the linked-briefing
		// event with its item_count attribute, and nothing on the cost pipe.
		if err := em.Emit(context.Background(), Event{
			Envelope:   env,
			Family:     FamilyBriefing,
			Name:       "daily_digest_sent",
			DedupKey:   DedupKey(FamilyBriefing, "daily_digest_sent", uuid.NewString()),
			Attributes: map[string]string{"item_count": strconv.Itoa(itemCount)},
		}); err != nil {
			t.Fatalf("emit: %v", err)
		}
	})

	if n := len(got["analytics.events"]); n != 1 {
		t.Fatalf("digest send: events datapoints = %d, want 1", n)
	}
	if dps := got["analytics.cost_minor_units"]; len(dps) != 0 {
		t.Fatalf("digest send emitted %d cost datapoints, want 0 (a link is not a billable briefing generation, issue #130)", len(dps))
	}
}

// TestIssue130_BriefingSpendCountedExactlyOnce proves the money/analytics-correctness
// invariant the #130 fix protects: the authoritative briefing-GENERATION path records
// CostBriefing spend as a true minor-unit amount EXACTLY ONCE. With the digest link no
// longer emitting CostBriefing, a single generation is the only briefing-cost source —
// so the cost counter shows one datapoint whose value is the generation spend, never a
// double count and never an item-count scalar.
func TestIssue130_BriefingSpendCountedExactlyOnce(t *testing.T) {
	env := fullEnvelope()
	const genSpendMinorUnits int64 = 250

	got := collectMetrics(t, func(em *Emitter) {
		// Sole authoritative emitter: the generation path. The digest link (which
		// would previously have added a second, mis-scaled CostBriefing) contributes
		// nothing to the cost pipe after #130, so it is intentionally absent here.
		if err := em.RecordCost(context.Background(), env, CostBriefing, genSpendMinorUnits); err != nil {
			t.Fatalf("generation cost: %v", err)
		}
	})

	dps := got["analytics.cost_minor_units"]
	if len(dps) != 1 {
		t.Fatalf("briefing cost datapoints = %d, want exactly 1 (no double-count)", len(dps))
	}
	if dps[0].Value != genSpendMinorUnits {
		t.Fatalf("briefing cost value = %d, want %d (true minor-unit spend, not an item count)", dps[0].Value, genSpendMinorUnits)
	}
	if attrKeySet(dps[0])["cost_kind"] != "briefing" {
		t.Fatalf("cost_kind = %q, want \"briefing\"", attrKeySet(dps[0])["cost_kind"])
	}
}
