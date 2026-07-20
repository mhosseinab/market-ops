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
	// accountFailed counts accounts whose digest delivery pass failed and was
	// ISOLATED (issue #124): the failure is contained to that account so every OTHER
	// account in the fan-out still delivers, and the failure is OBSERVABLE here —
	// never silently swallowed. The account id is NOT a label (high cardinality); it
	// travels on the warn log + typed observer instead.
	accountFailed metric.Int64Counter
	// idempotencyConflict counts deliveries that reused an (account, dedup_key) over a
	// DIFFERENT source event or materially changed payload (NOT-001, issue #123),
	// labeled by category. The collision fails closed with a typed conflict; this
	// series makes a lost distinct event distinguishable from a valid replay.
	idempotencyConflict metric.Int64Counter
	// urgentDelivered counts urgent (execution/safety) failure emails successfully
	// sent through the durable outbox (issue #122), labeled by category. It is the
	// positive side of the never-shed guarantee: an urgent failure DID reach mail.
	urgentDelivered metric.Int64Counter
	// urgentDeadLetter counts urgent emails that PERMANENTLY failed and were
	// dead-lettered (issue #122), labeled by category. This is the OBSERVABLE terminal
	// signal (§20.1 alert surface): the email is NOT marked delivered — no false
	// "delivered" — and the durable outbox row holds the dead_letter state for the
	// urgent-delivery runbook.
	urgentDeadLetter metric.Int64Counter
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
		metricsInst.accountFailed, _ = m.Int64Counter(
			"notify.digest.account_failed",
			metric.WithDescription("Accounts whose digest delivery failed and was isolated (contained, observed) so other accounts still deliver"),
		)
		metricsInst.idempotencyConflict, _ = m.Int64Counter(
			"notify.delivery.idempotency_conflict",
			metric.WithDescription("Notification deliveries that reused a dedup key over a different event/payload (fail-closed conflict)"),
		)
		metricsInst.urgentDelivered, _ = m.Int64Counter(
			"notify.urgent.delivered",
			metric.WithDescription("Urgent execution/safety failure emails successfully sent through the durable outbox (never shed)"),
		)
		metricsInst.urgentDeadLetter, _ = m.Int64Counter(
			"notify.urgent.dead_letter",
			metric.WithDescription("Urgent emails permanently failed and dead-lettered (observable terminal state; NOT marked delivered)"),
		)
	})
	return metricsInst
}

// recordUrgentDelivered emits the urgent-email delivered counter. The only label is
// the category — a bounded technical identifier, never rendered copy or a secret.
func recordUrgentDelivered(ctx context.Context, category string) {
	inst := instruments()
	if inst.urgentDelivered == nil {
		return
	}
	inst.urgentDelivered.Add(ctx, 1, metric.WithAttributes(
		attribute.String("category", category),
	))
}

// recordUrgentDeadLetter emits the urgent-email dead-letter counter (the OBSERVABLE
// permanent-failure signal). Labeled by category only; the notification id + bounded
// reason travel on the structured log and the durable outbox row, never as labels.
func recordUrgentDeadLetter(ctx context.Context, category string) {
	inst := instruments()
	if inst.urgentDeadLetter == nil {
		return
	}
	inst.urgentDeadLetter.Add(ctx, 1, metric.WithAttributes(
		attribute.String("category", category),
	))
}

// recordConflict emits the idempotency-conflict counter for a reused dedup key that is
// not an exact replay. The only label is the category — a bounded technical identifier,
// never rendered copy or a secret.
func recordConflict(ctx context.Context, category string) {
	inst := instruments()
	if inst.idempotencyConflict == nil {
		return
	}
	inst.idempotencyConflict.Add(ctx, 1, metric.WithAttributes(
		attribute.String("category", category),
	))
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

// recordAccountFailure emits the per-account digest-failure counter for one account
// isolated out of the fan-out (issue #124). The account id is intentionally NOT a
// label (high cardinality / PII posture) — it is carried on the warn log and the
// typed observer; this counter answers only "how many accounts failed this pass".
func recordAccountFailure(ctx context.Context) {
	inst := instruments()
	if inst.accountFailed == nil {
		return
	}
	inst.accountFailed.Add(ctx, 1)
}
