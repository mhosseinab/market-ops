package observation

import (
	"context"
	"log/slog"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// UnsupportedParserEvent is the BOUNDED parser-drift signal raised when a capture's
// parser identity is not in the server-owned registry (#154, §10.4). Every field is
// a technical identifier — account/target UUIDs, the route, the source discriminator,
// and the version tokens. It carries NO raw marketplace free text, NO price tokens,
// and NO Persian copy: a rejected version is surfaced as an attributable identifier,
// never as user-facing content.
type UnsupportedParserEvent struct {
	Account          uuid.UUID
	TargetID         uuid.UUID
	Route            string
	SourceType       string
	ParserVersion    string
	ConnectorVersion string
}

// ParserDriftSink receives the bounded parser-drift signal so an unsupported parser
// version is an observed, audited outcome (§10.4) rather than a swallowed exception.
// It is injectable so the ingest path can be tested without OTel wiring and so a
// future durable drift queue can be substituted without touching the derivation.
type ParserDriftSink interface {
	UnsupportedParser(ctx context.Context, ev UnsupportedParserEvent)
}

// telemetryDriftSink is the default sink: a stable-key structured warning plus an
// OTel counter, both scoped to the observation plane. It fails open to no-op
// instruments — a metric wiring hiccup must never break the fail-closed ingest path.
type telemetryDriftSink struct {
	logger     *slog.Logger
	rejections metric.Int64Counter
}

const driftInstrumentationName = "github.com/mhosseinab/market-ops/services/core/internal/observation"

// newTelemetryDriftSink builds the default parser-drift sink. A nil logger degrades
// to slog.Default; an instrument construction error degrades to a no-op counter.
func newTelemetryDriftSink(logger *slog.Logger) *telemetryDriftSink {
	if logger == nil {
		logger = slog.Default()
	}
	m := otel.Meter(driftInstrumentationName)
	c, err := m.Int64Counter(
		"observation.unsupported_parser_rejections",
		metric.WithDescription("captures quarantined because their parser identity is not in the server-owned registry (§10.4 parser drift)"),
	)
	if err != nil {
		c, _ = otel.Meter("noop").Int64Counter("observation.unsupported_parser_rejections")
	}
	return &telemetryDriftSink{logger: logger.With("component", "observation_parser_registry"), rejections: c}
}

// UnsupportedParser emits the bounded rejection to metrics and the structured log.
func (s *telemetryDriftSink) UnsupportedParser(ctx context.Context, ev UnsupportedParserEvent) {
	s.rejections.Add(ctx, 1, metric.WithAttributes(
		attribute.String("route", ev.Route),
		attribute.String("source_type", ev.SourceType),
		attribute.String("parser_version", ev.ParserVersion),
		attribute.String("connector_version", ev.ConnectorVersion),
	))
	s.logger.WarnContext(ctx, "capture parser identity not registered; quarantined to Unverified",
		slog.String("event", "unsupported_parser_quarantined"),
		slog.String("account_id", ev.Account.String()),
		slog.String("target_id", ev.TargetID.String()),
		slog.String("route", ev.Route),
		slog.String("source_type", ev.SourceType),
		slog.String("parser_version", ev.ParserVersion),
		slog.String("connector_version", ev.ConnectorVersion),
	)
}
