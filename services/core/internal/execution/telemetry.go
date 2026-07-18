package execution

import (
	"context"
	"log/slog"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/mhosseinab/market-ops/services/core/internal/db"
)

// instrumentationName is the stable telemetry scope for the execution plane. The
// same field names are emitted by tests and prod (CLAUDE.md observability).
const instrumentationName = "github.com/mhosseinab/market-ops/services/core/internal/execution"

// telemetry is the execution-path observability seam: metrics + traces +
// structured logs on the never-cut boundaries (write attempt, dedup hit,
// gate-block, pending-reconciliation park, recommend-only fallback, audit-write
// failure). When no OTel provider is installed the global meter/tracer are no-ops,
// so this is always safe to call and never a hard dependency.
type telemetry struct {
	tracer trace.Tracer
	logger *slog.Logger

	writeAttempts   metric.Int64Counter
	dedupHits       metric.Int64Counter
	gateBlocks      metric.Int64Counter
	pendingParks    metric.Int64Counter
	recommendOnly   metric.Int64Counter
	terminals       metric.Int64Counter
	auditFailures   metric.Int64Counter
	enablementDenie metric.Int64Counter
}

// newTelemetry builds the execution telemetry against the global OTel provider. A
// nil logger degrades to slog.Default. Counter construction errors are swallowed
// into no-op counters (a metric wiring hiccup must never break the write path);
// the counters themselves fail open to no-op, which is the correct posture for a
// telemetry seam (unlike the audit record, which fails CLOSED).
func newTelemetry(logger *slog.Logger) *telemetry {
	if logger == nil {
		logger = slog.Default()
	}
	m := otel.Meter(instrumentationName)
	ctr := func(name, desc string) metric.Int64Counter {
		c, err := m.Int64Counter(name, metric.WithDescription(desc))
		if err != nil {
			c, _ = noopMeter.Int64Counter(name)
		}
		return c
	}
	return &telemetry{
		tracer:          otel.Tracer(instrumentationName),
		logger:          logger.With("component", "execution"),
		writeAttempts:   ctr("execution.write_attempts", "external price-write attempts (EXE-002)"),
		dedupHits:       ctr("execution.dedup_hits", "idempotency-key claim conflicts; a duplicate request wrote nothing (EXE-002)"),
		gateBlocks:      ctr("execution.gate_blocks", "EXE-001 revalidation gate blocks (by gate)"),
		pendingParks:    ctr("execution.pending_reconciliation", "unknown write results parked pending (EXE-003)"),
		recommendOnly:   ctr("execution.recommend_only", "recommend-only fallbacks (writes OFF, EXE-005)"),
		terminals:       ctr("execution.terminal_results", "reconciled terminal external results (by state)"),
		auditFailures:   ctr("execution.audit_write_failures", "audit-record append failures that rolled the state change back (AUD-001)"),
		enablementDenie: ctr("execution.enablement_denied", "write enablement denied (capability/region)"),
	}
}

// noopMeter backs a counter when the real meter errors, so a counter is never nil.
var noopMeter = otel.Meter("noop")

// bindingAttrs is the trace/log context an approval control is reconstructable
// from (CLAUDE.md: propagate action ID + parameter version + context version).
func bindingAttrs(card db.ApprovalCard) []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.String("action_id", card.ActionID.String()),
		attribute.Int64("parameter_version", card.ParameterVersion),
		attribute.Int64("context_version", card.ContextVersion),
		attribute.String("card_id", card.ID.String()),
	}
}

// startSpan opens a span carrying the approval-control identity.
func (t *telemetry) startSpan(ctx context.Context, name string, card db.ApprovalCard) (context.Context, trace.Span) {
	return t.tracer.Start(ctx, name, trace.WithAttributes(bindingAttrs(card)...))
}

func (t *telemetry) gateBlocked(ctx context.Context, card db.ApprovalCard, gate Gate, mode Mode) {
	t.gateBlocks.Add(ctx, 1, metric.WithAttributes(attribute.String("gate", string(gate)), attribute.String("mode", string(mode))))
	t.logger.WarnContext(ctx, "execution gate blocked write", "gate", gate, "mode", mode,
		"action_id", card.ActionID.String(), "parameter_version", card.ParameterVersion, "context_version", card.ContextVersion)
}

func (t *telemetry) dedupHit(ctx context.Context, key string) {
	t.dedupHits.Add(ctx, 1)
	t.logger.InfoContext(ctx, "idempotency claim conflict; duplicate request wrote nothing", "idempotency_key", key)
}

func (t *telemetry) wroteExternal(ctx context.Context, card db.ApprovalCard, state ExternalState) {
	t.writeAttempts.Add(ctx, 1, metric.WithAttributes(attribute.String("external_state", string(state))))
	if state == StatePendingReconciliation {
		t.pendingParks.Add(ctx, 1)
		t.logger.WarnContext(ctx, "unknown write result parked pending reconciliation", "action_id", card.ActionID.String())
	}
}

func (t *telemetry) terminal(ctx context.Context, state ExternalState) {
	t.terminals.Add(ctx, 1, metric.WithAttributes(attribute.String("external_state", string(state))))
}

func (t *telemetry) recommendOnlyFallback(ctx context.Context, card db.ApprovalCard) {
	t.recommendOnly.Add(ctx, 1)
	t.enablementDenie.Add(ctx, 1)
	t.logger.InfoContext(ctx, "writes OFF; approved action tracked recommend-only", "action_id", card.ActionID.String())
}

// auditWriteFailed records an audit-append failure that forced a rollback. This is
// the never-cut boundary: an audit loss is an incident, not a swallowed error.
func (t *telemetry) auditWriteFailed(ctx context.Context, actionID uuid.UUID, err error) {
	t.auditFailures.Add(ctx, 1)
	t.logger.ErrorContext(ctx, "audit append failed; state change rolled back (fail closed)",
		"action_id", actionID.String(), "error", err.Error())
}
