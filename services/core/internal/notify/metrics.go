package notify

import (
	"context"
	"sync"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// instrumentationName is the stable telemetry scope for the notification delivery
// plane. The same field names are emitted by tests and prod (CLAUDE.md observability
// pillars) so a fail-closed rejection and a digest-row isolation are visible series.
const instrumentationName = "github.com/mhosseinab/market-ops/services/core/internal/notify"

// metrics holds the notification delivery-layer counters. They are initialized
// lazily from the OTel GLOBAL meter provider, which is a no-op provider until
// obs.Init installs one (behind OTEL_ENABLED) — so recording is always safe,
// never panics, and never depends on collector availability (fails open for the
// telemetry hop only; the delivery decision itself always fails closed).
type notifyMetrics struct {
	// rejected counts deliveries refused by the closed message-schema contract,
	// labeled by reason and surface. Free-text/invalid copy never reaches storage.
	rejected metric.Int64Counter
	// itemIsolated counts persisted digest rows skipped because they violate the
	// closed schema (a legacy/invalid row), labeled by reason. The skip is
	// OBSERVABLE here — never a silent drop — while the rest of the digest renders.
	itemIsolated metric.Int64Counter
}

var (
	metricsOnce sync.Once
	metricsInst notifyMetrics
)

func instruments() notifyMetrics {
	metricsOnce.Do(func() {
		m := otel.GetMeterProvider().Meter(instrumentationName)
		metricsInst.rejected, _ = m.Int64Counter(
			"notify.delivery.rejected",
			metric.WithDescription("Notification deliveries rejected by the closed message-catalog contract"),
		)
		metricsInst.itemIsolated, _ = m.Int64Counter(
			"notify.digest.item_isolated",
			metric.WithDescription("Persisted digest rows isolated (skipped, observed) for violating the closed message schema"),
		)
	})
	return metricsInst
}

// recordRejection emits the delivery-rejection counter for a schema violation.
func recordRejection(ctx context.Context, e *MessageValidationError) {
	if e == nil {
		return
	}
	inst := instruments()
	if inst.rejected == nil {
		return
	}
	inst.rejected.Add(ctx, 1, metric.WithAttributes(
		attribute.String("reason", string(e.Reason)),
		attribute.String("surface", e.Surface),
	))
}

// recordIsolation emits the digest-row isolation counter for a skipped legacy/
// invalid row.
func recordIsolation(ctx context.Context, e *MessageValidationError) {
	if e == nil {
		return
	}
	inst := instruments()
	if inst.itemIsolated == nil {
		return
	}
	inst.itemIsolated.Add(ctx, 1, metric.WithAttributes(
		attribute.String("reason", string(e.Reason)),
		attribute.String("surface", e.Surface),
	))
}
