package jobs

import (
	"context"
	"log/slog"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// dispatchInstrumentationName is the stable telemetry scope for the durable
// execution-intent dispatch seam (issue #92). Tests and prod emit the same field
// names (CLAUDE.md observability: shared schema).
const dispatchInstrumentationName = "github.com/mhosseinab/market-ops/services/core/internal/jobs/dispatch"

// dispatchTelemetry is the execute_approved lifecycle observability seam: metrics +
// traces + structured logs on the durable-intent boundary. It emits at enqueue
// (dispatched), worker claim (claimed), success (completed), dark park (snoozed),
// and failure (failed; flagged terminal on the final attempt). The intent BACKLOG
// is observable as dispatched minus completed; TERMINAL FAILURES are the failed
// counter with terminal=true (issue #92 acceptance: both observable). Counter
// construction errors degrade to no-op — a telemetry hiccup never breaks dispatch.
type dispatchTelemetry struct {
	tracer trace.Tracer
	logger *slog.Logger

	dispatchedCtr metric.Int64Counter
	claimedCtr    metric.Int64Counter
	completedCtr  metric.Int64Counter
	snoozedCtr    metric.Int64Counter
	failedCtr     metric.Int64Counter
}

// dispatchTel is the process-wide instance the transactional enqueue helper emits
// through (the worker builds its own with its logger). Both share the same OTel
// instruments (deduplicated by name within the meter).
var dispatchTel = newDispatchTelemetry(nil)

// newDispatchTelemetry builds the dispatch telemetry against the global OTel
// provider. A nil logger degrades to slog.Default. Counter wiring errors fall back
// to no-op counters (fail open; observability must never break the durable path).
func newDispatchTelemetry(logger *slog.Logger) *dispatchTelemetry {
	if logger == nil {
		logger = slog.Default()
	}
	m := otel.Meter(dispatchInstrumentationName)
	ctr := func(name, desc string) metric.Int64Counter {
		c, err := m.Int64Counter(name, metric.WithDescription(desc))
		if err != nil {
			c, _ = otel.Meter("noop").Int64Counter(name)
		}
		return c
	}
	return &dispatchTelemetry{
		tracer:        otel.Tracer(dispatchInstrumentationName),
		logger:        logger.With("component", "execution_dispatch"),
		dispatchedCtr: ctr("execution_dispatch.intents_dispatched", "durable execution intents enqueued at Approved commit (issue #92)"),
		claimedCtr:    ctr("execution_dispatch.intents_claimed", "durable execution intents claimed by a worker"),
		completedCtr:  ctr("execution_dispatch.intents_completed", "durable execution intents processed successfully (exactly-once effect)"),
		snoozedCtr:    ctr("execution_dispatch.intents_snoozed", "durable execution intents parked (dark posture, writes OFF pre-S35)"),
		failedCtr:     ctr("execution_dispatch.intents_failed", "durable execution intent processing failures (terminal=true on final attempt)"),
	}
}

// intentAttrs is the trace/log context an approval control is reconstructable from
// (CLAUDE.md: propagate action ID + parameter version + context version).
func intentAttrs(args ExecuteApprovedArgs) []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.String("action_id", args.ActionID.String()),
		attribute.Int64("parameter_version", args.ParameterVersion),
		attribute.Int64("context_version", args.ContextVersion),
		attribute.String("card_id", args.CardID.String()),
	}
}

func (t *dispatchTelemetry) dispatched(ctx context.Context, args ExecuteApprovedArgs, jobID int64, dedup bool) {
	t.dispatchedCtr.Add(ctx, 1, metric.WithAttributes(attribute.Bool("dedup_skipped", dedup)))
	t.logger.InfoContext(ctx, "execution intent dispatched", "job_id", jobID, "dedup_skipped", dedup,
		"action_id", args.ActionID.String(), "parameter_version", args.ParameterVersion,
		"context_version", args.ContextVersion, "card_id", args.CardID.String())
}

func (t *dispatchTelemetry) claimed(ctx context.Context, args ExecuteApprovedArgs, jobID int64, attempt int) (context.Context, trace.Span) {
	t.claimedCtr.Add(ctx, 1)
	ctx, span := t.tracer.Start(ctx, "execution_dispatch.claim", trace.WithAttributes(intentAttrs(args)...))
	t.logger.InfoContext(ctx, "execution intent claimed", "job_id", jobID, "attempt", attempt,
		"action_id", args.ActionID.String(), "card_id", args.CardID.String())
	return ctx, span
}

func (t *dispatchTelemetry) completed(ctx context.Context, args ExecuteApprovedArgs, jobID int64) {
	t.completedCtr.Add(ctx, 1)
	t.logger.InfoContext(ctx, "execution intent processed", "job_id", jobID,
		"action_id", args.ActionID.String(), "card_id", args.CardID.String())
}

func (t *dispatchTelemetry) snoozed(ctx context.Context, args ExecuteApprovedArgs, jobID int64) {
	t.snoozedCtr.Add(ctx, 1)
	t.logger.InfoContext(ctx, "execution intent parked (dark; writes OFF until S35)", "job_id", jobID,
		"action_id", args.ActionID.String(), "card_id", args.CardID.String())
}

// failed records a processing failure. On the final attempt the intent is
// discarded by River, so it is flagged terminal — the observable terminal-failure
// signal (issue #92 acceptance). Earlier attempts are retryable.
func (t *dispatchTelemetry) failed(ctx context.Context, args ExecuteApprovedArgs, jobID int64, attempt, maxAttempts int, err error) {
	terminal := attempt >= maxAttempts
	t.failedCtr.Add(ctx, 1, metric.WithAttributes(attribute.Bool("terminal", terminal)))
	msg := "execution intent processing failed (will retry)"
	level := slog.LevelWarn
	if terminal {
		msg = "execution intent processing failed terminally (discarded after final attempt)"
		level = slog.LevelError
	}
	t.logger.Log(ctx, level, msg, "job_id", jobID, "attempt", attempt, "max_attempts", maxAttempts,
		"terminal", terminal, "action_id", args.ActionID.String(), "card_id", args.CardID.String(),
		"error", err.Error())
}
