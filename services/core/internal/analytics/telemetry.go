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

// envelopeAttrs is the shared dimension set an analytics signal rolls up by. It
// carries locale/region/currency-contract as DATA labels (never a branch) so a
// dashboard can slice by them; no locale COPY is ever emitted here.
func envelopeAttrs(env Envelope) []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.String("organization_id", env.Organization.String()),
		attribute.String("marketplace_account_id", env.Account.String()),
		attribute.String("locale", env.Locale),
		attribute.String("region", env.Region),
		attribute.String("currency_contract_version", env.CurrencyContractVersion),
		attribute.String("source_surface", env.SourceSurface),
	}
}

// event increments the per-family events counter with the envelope dimensions.
func (t *telemetry) event(ctx context.Context, env Envelope, family Family, name string) {
	attrs := append(envelopeAttrs(env),
		attribute.String("family", string(family)),
		attribute.String("name", name),
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
