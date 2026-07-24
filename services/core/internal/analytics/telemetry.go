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
	// tenantRejects counts cross-tenant envelope rejections (issue #125). It carries
	// NO labels at all: a tenant-integrity rejection must be observable, but its
	// dimensions (organization/account) are exactly the sensitive identifiers we must
	// not leak or let explode cardinality, so the boundary is a bare, tenant-free
	// counter — a non-zero value is the fail-closed signal, nothing more.
	tenantRejects metric.Int64Counter
	// deduplicated counts events SUPPRESSED by the account-scoped dedup key (issue
	// #111, §4.6 event-deduplication never-cut). It carries the SAME bounded label set
	// as events (family/name + the bounded envelope dimensions) so a dashboard can put
	// written and suppressed side by side per family. The dedup KEY itself is never a
	// label — it is tenant-derived and unbounded, and lives on the persisted row.
	dedups metric.Int64Counter
	// entityRejects counts entity-scope rejections (issue #125 reopen residual): an
	// entity_id not owned by the account or incompatible with the family. Like
	// tenantRejects it is DELIBERATELY label-free — its only natural dimensions
	// (account/entity/family) are sensitive tenant identifiers we must never leak or
	// let explode cardinality — so a non-zero value is the fail-closed signal, nothing
	// more.
	entityRejects metric.Int64Counter
	// emitFailures counts events that could NOT be written because the analytics SINK
	// or its lookup failed — an INFRASTRUCTURE failure, never a validation rejection,
	// a tenant/entity rejection, or a deduplication suppression (each of which has its
	// own signal). It exists because without it an unreachable Postgres is
	// indistinguishable from an IDLE pipe: every other analytics series simply reads
	// zero, which is exactly what "nothing was due" looks like, so no alert can fire
	// (issue #111 acceptance criterion: an unavailable analytics sink is observable).
	// Like tenantRejects/entityRejects it is DELIBERATELY label-free — its natural
	// dimensions are tenant identifiers, which are never metric labels (issue
	// #151/#244) — so a non-zero value is the outage signal, nothing more.
	emitFailures metric.Int64Counter
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
		events:        ctr("analytics.events", "§18 analytics events emitted (by family)"),
		dedups:        ctr("analytics.events_deduplicated", "§18 analytics events suppressed by their account-scoped dedup key (issue #111)"),
		costs:         ctr("analytics.cost_minor_units", "§17.3 variable cost in integer minor units (by kind)"),
		tenantRejects: ctr("analytics.tenant_rejections", "cross-tenant analytics envelope rejections (issue #125; no labels)"),
		entityRejects: ctr("analytics.entity_rejections", "entity-scope analytics envelope rejections (issue #125 reopen; no labels)"),
		emitFailures:  ctr("analytics.emit_failures", "§18 analytics events lost to an analytics SINK/lookup infrastructure failure (issue #111; no labels)"),
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
//   - analytics.events              → {family, name, locale, region, source_surface}
//   - analytics.events_deduplicated → {family, name, locale, region, source_surface}
//   - analytics.cost_minor_units    → {cost_kind, locale, region, source_surface}
//
// The DEDUP KEY (issue #111) is deliberately absent from every label set: it is
// tenant-derived and unbounded (it embeds committed business-row identifiers), so it
// lives ONLY on the persisted analytics_events row, never on a metric.
//
// family and cost_kind are already closed enums (validated before emit). name is a
// CLOSED developer-defined constant — a stable name within its family (analytics.go
// Event.Name), never tenant-derived and never caller free text — so it is bounded by
// construction and emitted VERBATIM: the §18 dashboards slice `analytics_events by
// (name)` per family (recommendation, conversation, and the free-text-containment
// panel that reads name=~".*contain.*|.*guidance.*|.*blocked.*"), so collapsing name
// to a sentinel would silently zero a never-cut observability boundary.
//
// The genuinely open/config/caller-derived dimensions (locale, region,
// source_surface) ARE bounded to a reviewed allowlist below; any value outside it
// buckets to labelSentinel so a free-text or tenant-derived value can NEVER mint a
// new series. Extending a bounded dimension is a deliberate, reviewed edit to these
// sets — an unregistered value stays observable under the sentinel rather than
// growing cardinality. Tenant UUIDs (organization/account/entity) and the unbounded
// currency-contract version are NEVER labels (issue #151) — they live on the
// persisted analytics_events plane.
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
// dimensions plus the closed family and the closed-constant event name. name is a
// developer-defined constant (not caller free text), emitted verbatim so the §18
// dashboards can slice `analytics_events by (name)` per family — see the label
// budget note above.
func (t *telemetry) event(ctx context.Context, env Envelope, family Family, name string) {
	attrs := append(envelopeAttrs(env),
		attribute.String("family", string(family)),
		attribute.String("name", name),
	)
	t.events.Add(ctx, 1, metric.WithAttributes(attrs...))
}

// deduplicated records ONE event suppressed by its account-scoped dedup key (issue
// #111). It shares the events counter's bounded label set so "written" and
// "suppressed" are comparable per family/name; the dedup key and every tenant
// identifier stay OFF the metric (they are on the persisted analytics_events row).
func (t *telemetry) deduplicated(ctx context.Context, env Envelope, family Family, name string) {
	attrs := append(envelopeAttrs(env),
		attribute.String("family", string(family)),
		attribute.String("name", name),
	)
	t.dedups.Add(ctx, 1, metric.WithAttributes(attrs...))
}

// tenantReject records ONE cross-tenant envelope rejection (issue #125). It is
// deliberately label-free: the rejection must be observable and fail-closed, but its
// only natural dimensions are the sensitive tenant identifiers, which are never
// emitted as labels (no leak, no cardinality growth, no existence oracle).
func (t *telemetry) tenantReject(ctx context.Context) {
	t.tenantRejects.Add(ctx, 1)
}

// entityReject records ONE entity-scope envelope rejection (issue #125 reopen
// residual). Like tenantReject it is deliberately label-free: the rejection must be
// observable and fail-closed, but its only natural dimensions are the sensitive
// account/entity/family identifiers, which are never emitted as labels (no leak, no
// cardinality growth, no ownership oracle).
func (t *telemetry) entityReject(ctx context.Context) {
	t.entityRejects.Add(ctx, 1)
}

// emitFailure records ONE event LOST to an analytics sink/lookup infrastructure
// failure (issue #111). It is label-free for the same reason tenantReject is: the
// outage must be observable, but its only natural dimensions are tenant identifiers.
// It is never incremented for a validation rejection, a tenant/entity rejection, or a
// deduplication suppression — those are distinct, correctly-behaving outcomes.
func (t *telemetry) emitFailure(ctx context.Context) {
	t.emitFailures.Add(ctx, 1)
}

// cost adds an integer cost amount to the cost counter tagged by kind. Message
// count and conversation length are anti-metrics (§18) — this pipe never counts
// them; it counts variable COST per §17.3 unit, which is the unit-economics signal.
func (t *telemetry) cost(ctx context.Context, env Envelope, kind CostKind, minorUnits int64) {
	attrs := append(envelopeAttrs(env), attribute.String("cost_kind", string(kind)))
	t.costs.Add(ctx, minorUnits, metric.WithAttributes(attrs...))
}
