package outcome

import (
	"context"
	"log/slog"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// instrumentationName is the stable telemetry scope for the outcome close plane.
const instrumentationName = "github.com/mhosseinab/market-ops/services/core/internal/outcome"

// noopMeter backs a counter when the real meter errors, so a counter is never nil.
var noopMeter = otel.Meter("noop")

// telemetry is the OUT-001 close-job observability seam. The classification
// boundary is a never-cut seam: a MEASURABLE close (per §15.3 class), a
// NotMeasurable-due-to-absence close, an Incomplete (unclosed) resolution, and a
// SOURCE ERROR are each emitted as distinct signals, so telemetry can never
// confuse a source failure or an incomplete window with a real close. Counters
// fail open to no-ops (a metric hiccup must never break the close path).
type telemetry struct {
	logger       *slog.Logger
	closes       metric.Int64Counter // labelled by §15.3 result class
	incompletes  metric.Int64Counter
	sourceErrors metric.Int64Counter
}

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
		logger:       logger.With("component", "outcome"),
		closes:       ctr("outcome.window_closes", "OUT-001 windows closed, labelled by §15.3 result class"),
		incompletes:  ctr("outcome.window_incomplete", "due windows left unclosed because evidence is not yet available (retryable)"),
		sourceErrors: ctr("outcome.source_errors", "evidence-source failures; the window is left unclosed and NEVER NotMeasurable"),
	}
}

// closed records a window closed with a resolved §15.3 result. NotMeasurable is
// tagged distinctly so a genuine-absence close is separable from a measurable one.
func (t *telemetry) closed(ctx context.Context, actionID uuid.UUID, result Result) {
	t.closes.Add(ctx, 1, metric.WithAttributes(attribute.String("result", string(result))))
	t.logger.InfoContext(ctx, "outcome window closed",
		"action_id", actionID.String(), "result", string(result))
}

// incomplete records a due window left unclosed (retryable), distinct from a close.
func (t *telemetry) incomplete(ctx context.Context, actionID uuid.UUID) {
	t.incompletes.Add(ctx, 1)
	t.logger.DebugContext(ctx, "outcome window not yet measurable; left unclosed",
		"action_id", actionID.String())
}

// sourceError records an evidence-source failure. The window is left unclosed and
// is NEVER closed as NotMeasurable; the error is actionable (carries the action id).
func (t *telemetry) sourceError(ctx context.Context, actionID uuid.UUID, err error) {
	t.sourceErrors.Add(ctx, 1)
	t.logger.WarnContext(ctx, "outcome evidence source failed; window left unclosed (not NotMeasurable)",
		"action_id", actionID.String(), "error", err.Error())
}
