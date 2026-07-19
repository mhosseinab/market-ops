package analytics

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// instrumentationName is the stable telemetry scope for the analytics pipe. Test
// fixtures and prod telemetry share these field names (CLAUDE.md observability).
const instrumentationName = "github.com/mhosseinab/market-ops/services/core/internal/analytics"

// telemetry is the analytics observability seam: an events counter (tagged by
// family) and a cost counter (tagged by cost kind). When no OTel provider is
// installed the global meter is a no-op, so this is always safe to call and never
// a hard dependency — a metrics hiccup must never break an emit.
type telemetry struct {
	events metric.Int64Counter
	// costs sums §17.3 variable cost in integer minor units, tagged by cost kind.
	costs metric.Int64Counter
}

// noopMeter backs a counter when the real meter errors, so a counter is never nil.
var noopMeter = otel.Meter("noop")

func newTelemetry() *telemetry {
	m := otel.Meter(instrumentationName)
	ctr := func(name, desc string) metric.Int64Counter {
		c, err := m.Int64Counter(name, metric.WithDescription(desc))
		if err != nil {
			c, _ = noopMeter.Int64Counter(name)
		}
		return c
	}
	return &telemetry{
		events: ctr("analytics.events", "§18 analytics events emitted (by family)"),
		costs:  ctr("analytics.cost_minor_units", "§17.3 variable cost in integer minor units (by kind)"),
	}
}

// REVIEWED LABEL CARDINALITY BUDGET (issue #151, never-cut observability).
//
// Metric labels are a BOUNDED, tenant-free dimension set. The durable attribution
// (organization/account/entity, currency-contract version) is persisted on
// analytics_events (the authorized query plane, analytics.go) — it is NEVER a
// Prometheus label, because a tenant UUID or an unconstrained version value is
// unbounded and tenant-sensitive and would explode series cardinality.
//
// The label KEY allowlist is closed and asserted by telemetry_test.go:
//   - analytics.events           → {family, name, locale, region, source_surface}
//   - analytics.cost_minor_units → {cost_kind, locale, region, source_surface}
//
// family and cost_kind are already closed enums (validated before emit). The
// open-string dimensions (name, locale, region, source_surface) are bounded to a
// reviewed allowlist below; any value outside it buckets to labelSentinel so a
// free-text or tenant-derived value can NEVER mint a new series. Extending a
// bounded dimension is a deliberate, reviewed edit to these sets — an unregistered
// value stays observable under the sentinel rather than growing cardinality.
const labelSentinel = "other"

// allowedLocales is the reviewed locale label domain. Locale is DATA, never a
// branch, and the locale COPY (Persian strings) is never emitted as a label.
var allowedLocales = map[string]struct{}{"fa-IR": {}, "fa": {}, "en-US": {}, "en": {}}

// allowedRegions is the reviewed region label domain (P0 beta serves IR).
var allowedRegions = map[string]struct{}{"IR": {}}

// allowedSourceSurfaces is the reviewed surface label domain: where a signal
// originated. Bounded so an arbitrary surface string cannot widen cardinality.
var allowedSourceSurfaces = map[string]struct{}{
	"screen": {}, "chat": {}, "email_digest": {}, "extension": {}, "system": {},
}

// allowedEventNames is the reviewed event-name label domain. name is the backbone
// of the §18 dashboards (sum by (name)); a new event name must be REGISTERED here
// to carry its own series — otherwise it is observable under labelSentinel. This
// keeps the per-name cardinality bounded by code review, not by caller free text.
var allowedEventNames = map[string]struct{}{
	"daily_digest_sent": {},
}

// boundLabel returns v when it is in the reviewed allowlist, else the sentinel.
func boundLabel(v string, allow map[string]struct{}) string {
	if _, ok := allow[v]; ok {
		return v
	}
	return labelSentinel
}

// envelopeAttrs is the shared BOUNDED dimension set an analytics signal rolls up
// by: locale/region/source_surface as DATA labels (never a branch), each bounded
// to its reviewed allowlist. Tenant identifiers and the currency-contract version
// are deliberately absent — they live on the persisted analytics_events plane.
func envelopeAttrs(env Envelope) []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.String("locale", boundLabel(env.Locale, allowedLocales)),
		attribute.String("region", boundLabel(env.Region, allowedRegions)),
		attribute.String("source_surface", boundLabel(env.SourceSurface, allowedSourceSurfaces)),
	}
}

// event increments the per-family events counter with the bounded envelope
// dimensions plus the closed family and the bounded event name.
func (t *telemetry) event(ctx context.Context, env Envelope, family Family, name string) {
	attrs := append(envelopeAttrs(env),
		attribute.String("family", string(family)),
		attribute.String("name", boundLabel(name, allowedEventNames)),
	)
	t.events.Add(ctx, 1, metric.WithAttributes(attrs...))
}

// cost adds an integer cost amount to the cost counter tagged by kind. Message
// count and conversation length are anti-metrics (§18) — this pipe never counts
// them; it counts variable COST per §17.3 unit, which is the unit-economics signal.
func (t *telemetry) cost(ctx context.Context, env Envelope, kind CostKind, minorUnits int64) {
	attrs := append(envelopeAttrs(env), attribute.String("cost_kind", string(kind)))
	t.costs.Add(ctx, minorUnits, metric.WithAttributes(attrs...))
}
