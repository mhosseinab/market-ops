package connector

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// connInstrumentationName is the stable telemetry scope for the connector plane.
// The same field names are emitted by tests and prod (CLAUDE.md observability).
const connInstrumentationName = "github.com/mhosseinab/market-ops/services/core/internal/connector"

// connectorLabel scopes the connector signals to their connector. P0 has one
// authenticated connector (DK Seller); a bounded label keeps the series shaped for
// a per-connector rollup without a schema change when more land.
const connectorLabel = "dk_seller"

// Catalog-sync initiation outcomes: the bounded vocabulary of the metric attribute
// emitted at the SyncCatalog boundary (issue #76). Every POST /connector/catalog/sync
// resolves to exactly one of these — no free text, no PII, no Persian copy.
const (
	// syncOutcomeEnqueued: the atomic claim won and a fresh sync job was enqueued.
	syncOutcomeEnqueued = "enqueued"
	// syncOutcomeIdempotentSkip: a run was already in-flight (best-effort fast path
	// OR the atomic claim lost the race) so nothing was enqueued — idempotent success.
	syncOutcomeIdempotentSkip = "idempotent_skip"
	// syncOutcomeCapabilityRefused: catalog_read was not Supported
	// (Unknown/Unsupported/Degraded) so the sync failed closed (§15.2).
	syncOutcomeCapabilityRefused = "capability_refused"
	// syncOutcomeUnavailable: no enqueuer is wired (503) so the sync could not be
	// initiated — the fail-closed default, never a healthy "queued".
	syncOutcomeUnavailable = "unavailable"
)

// syncInitMetrics emits the catalog-sync initiation counter. It is nil-safe: a
// metric-wiring hiccup must never break the sync path, so instrument construction
// errors fall back to a no-op instrument and every record on a nil receiver is a
// no-op (the telemetry seam fails open to no-op, the correct posture for
// observability).
type syncInitMetrics struct {
	initiations metric.Int64Counter
}

// newSyncInitMetrics builds the counter against the global OTel provider, falling
// back to a no-op instrument if construction fails.
func newSyncInitMetrics() *syncInitMetrics {
	m := otel.Meter(connInstrumentationName)
	c, err := m.Int64Counter(
		"connector.catalog_sync_initiations",
		metric.WithDescription("catalog sync initiation attempts by outcome (enqueued/idempotent_skip/capability_refused/unavailable)"),
	)
	if err != nil {
		c, _ = otel.Meter("noop").Int64Counter("connector.catalog_sync_initiations")
	}
	return &syncInitMetrics{initiations: c}
}

// record emits one initiation with a bounded outcome attribute. Nil-safe.
func (m *syncInitMetrics) record(ctx context.Context, outcome string) {
	if m == nil {
		return
	}
	m.initiations.Add(ctx, 1, metric.WithAttributes(
		attribute.String("outcome", outcome),
		attribute.String("connector", connectorLabel),
	))
}
