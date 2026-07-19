package jobs

import (
	"context"
	"log/slog"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// reopenDispatchInstrumentationName is the stable telemetry scope for the durable
// identity-reopen dispatch seam (issue #49). Tests and prod emit the same field names
// (CLAUDE.md observability: shared schema).
const reopenDispatchInstrumentationName = "github.com/mhosseinab/market-ops/services/core/internal/jobs/reopen_dispatch"

// reopenDispatchTelemetry is the mapping_reopened lifecycle observability seam:
// metrics + traces + structured logs on the durable-intent boundary. It emits at
// enqueue (dispatched), worker claim (claimed), success (completed), park (snoozed),
// and failure (failed; flagged terminal on the final attempt). The intent BACKLOG is
// observable as dispatched minus completed; TERMINAL FAILURES are the failed counter
// with terminal=true. Counter construction errors degrade to no-op — a telemetry
// hiccup never breaks the durable dispatch path.
type reopenDispatchTelemetry struct {
	tracer trace.Tracer
	logger *slog.Logger

	dispatchedCtr metric.Int64Counter
	claimedCtr    metric.Int64Counter
	completedCtr  metric.Int64Counter
	snoozedCtr    metric.Int64Counter
	failedCtr     metric.Int64Counter
}

// reopenDispatchTel is the process-wide instance the transactional enqueue helper
// emits through (the worker builds its own with its logger). Both share the same OTel
// instruments (deduplicated by name within the meter).
var reopenDispatchTel = newReopenDispatchTelemetry(nil)

// newReopenDispatchTelemetry builds the reopen dispatch telemetry against the global
// OTel provider. A nil logger degrades to slog.Default. Counter wiring errors fall
// back to no-op counters (observability must never break the durable path).
func newReopenDispatchTelemetry(logger *slog.Logger) *reopenDispatchTelemetry {
	if logger == nil {
		logger = slog.Default()
	}
	m := otel.Meter(reopenDispatchInstrumentationName)
	ctr := func(name, desc string) metric.Int64Counter {
		c, err := m.Int64Counter(name, metric.WithDescription(desc))
		if err != nil {
			c, _ = otel.Meter("noop").Int64Counter(name)
		}
		return c
	}
	return &reopenDispatchTelemetry{
		tracer:        otel.Tracer(reopenDispatchInstrumentationName),
		logger:        logger.With("component", "reopen_dispatch"),
		dispatchedCtr: ctr("reopen_dispatch.intents_dispatched", "durable identity-reopen intents enqueued at reopen commit (issue #49)"),
		claimedCtr:    ctr("reopen_dispatch.intents_claimed", "durable identity-reopen intents claimed by a worker"),
		completedCtr:  ctr("reopen_dispatch.intents_completed", "durable identity-reopen intents processed successfully (exactly-once effect)"),
		snoozedCtr:    ctr("reopen_dispatch.intents_snoozed", "durable identity-reopen intents parked"),
		failedCtr:     ctr("reopen_dispatch.intents_failed", "durable identity-reopen intent processing failures (terminal=true on final attempt)"),
	}
}

// reopenIntentAttrs is the trace/log context a reopen event is reconstructable from.
func reopenIntentAttrs(args MappingReopenedArgs) []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.String("identity_id", args.IdentityID.String()),
		attribute.String("variant_id", args.VariantID.String()),
		attribute.String("account_id", args.AccountID.String()),
		attribute.String("reason", args.Reason),
		attribute.String("dedup_key", args.DedupKey),
	}
}

func (t *reopenDispatchTelemetry) dispatched(ctx context.Context, args MappingReopenedArgs, jobID int64, dedup bool) {
	t.dispatchedCtr.Add(ctx, 1, metric.WithAttributes(attribute.Bool("dedup_skipped", dedup)))
	t.logger.InfoContext(ctx, "reopen intent dispatched", "job_id", jobID, "dedup_skipped", dedup,
		"identity_id", args.IdentityID.String(), "variant_id", args.VariantID.String(),
		"reason", args.Reason, "dedup_key", args.DedupKey)
}

func (t *reopenDispatchTelemetry) claimed(ctx context.Context, args MappingReopenedArgs, jobID int64, attempt int) (context.Context, trace.Span) {
	t.claimedCtr.Add(ctx, 1)
	ctx, span := t.tracer.Start(ctx, "reopen_dispatch.claim", trace.WithAttributes(reopenIntentAttrs(args)...))
	t.logger.InfoContext(ctx, "reopen intent claimed", "job_id", jobID, "attempt", attempt,
		"identity_id", args.IdentityID.String(), "variant_id", args.VariantID.String())
	return ctx, span
}

func (t *reopenDispatchTelemetry) completed(ctx context.Context, args MappingReopenedArgs, jobID int64) {
	t.completedCtr.Add(ctx, 1)
	t.logger.InfoContext(ctx, "reopen intent processed", "job_id", jobID,
		"identity_id", args.IdentityID.String(), "variant_id", args.VariantID.String())
}

func (t *reopenDispatchTelemetry) snoozed(ctx context.Context, args MappingReopenedArgs, jobID int64) {
	t.snoozedCtr.Add(ctx, 1)
	t.logger.InfoContext(ctx, "reopen intent parked", "job_id", jobID,
		"identity_id", args.IdentityID.String(), "variant_id", args.VariantID.String())
}

// failed records a processing failure. On the final attempt the intent is discarded by
// River, so it is flagged terminal — the observable terminal-failure signal. Earlier
// attempts are retryable.
func (t *reopenDispatchTelemetry) failed(ctx context.Context, args MappingReopenedArgs, jobID int64, attempt, maxAttempts int, err error) {
	terminal := attempt >= maxAttempts
	t.failedCtr.Add(ctx, 1, metric.WithAttributes(attribute.Bool("terminal", terminal)))
	msg := "reopen intent processing failed (will retry)"
	level := slog.LevelWarn
	if terminal {
		msg = "reopen intent processing failed terminally (discarded after final attempt)"
		level = slog.LevelError
	}
	t.logger.Log(ctx, level, msg, "job_id", jobID, "attempt", attempt, "max_attempts", maxAttempts,
		"terminal", terminal, "identity_id", args.IdentityID.String(),
		"variant_id", args.VariantID.String(), "reason", args.Reason, "error", err.Error())
}
