package httpapi

import (
	"net/http"
	"strconv"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// instrumentationName is the stable telemetry scope for the gateway transport.
// Test fixtures and prod telemetry share these field names (CLAUDE.md observability):
// the §18 dashboards and the §17.2 P95 panels query exactly these series.
const instrumentationName = "github.com/mhosseinab/market-ops/services/core/internal/httpapi"

// redMetrics is the RED (Rate / Errors / Duration) instrument set for every
// gateway endpoint. The duration histogram's count is the request RATE, its
// http_status_class label carries the ERROR rate, and the histogram buckets are
// the latency DISTRIBUTION the §17.2 P95 targets are measured against (common
// product views < 2000ms, approval card < 5000ms, chat completion < 10000ms).
// Duration is recorded in INTEGER milliseconds — no float on any path (PRD §9.1),
// consistent with the S18/S19 integer-only telemetry. A metrics-wiring hiccup
// degrades to no-op instruments — telemetry never breaks a request.
type redMetrics struct {
	duration metric.Int64Histogram
	tracer   trace.Tracer
}

var noopMeter = otel.Meter("noop")

func newREDMetrics() *redMetrics {
	m := otel.Meter(instrumentationName)
	h, err := m.Int64Histogram(
		"http.server.request.duration",
		metric.WithUnit("ms"),
		metric.WithDescription("gateway request duration in integer milliseconds, labeled by route/method/status_class (RED + §17.2 P95)"),
	)
	if err != nil {
		h, _ = noopMeter.Int64Histogram("http.server.request.duration")
	}
	return &redMetrics{duration: h, tracer: otel.Tracer(instrumentationName)}
}

// routeLabel bounds metric/trace cardinality: the gateway mounts EXACT paths, so
// a known (method, path) is emitted verbatim; anything else (a scan, a 404) folds
// into a single "other" bucket. No query string, id, or free text ever becomes a
// label (locale/PII stay out of telemetry).
func routeLabel(method, path string) string {
	if _, ok := lookupPolicy(method, path); ok {
		return path
	}
	return "other"
}

// statusClass reduces a status code to its RED error class (2xx/3xx/4xx/5xx) so a
// dashboard slices success vs client vs server error without per-code cardinality.
func statusClass(code int) string {
	switch {
	case code >= 500:
		return "5xx"
	case code >= 400:
		return "4xx"
	case code >= 300:
		return "3xx"
	default:
		return "2xx"
	}
}

// statusRecorder captures the response status for the RED label without buffering.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	return s.ResponseWriter.Write(b)
}

// wrap is the outermost transport middleware: it extracts any inbound W3C trace
// context (web → gateway), opens a server span carrying the normalized route, and
// records the RED duration histogram on completion. It is safe with a no-op
// provider (dev without OTEL_ENABLED) and never alters the response.
func (rm *redMetrics) wrap(next http.Handler) http.Handler {
	propagator := otel.GetTextMapPropagator()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := propagator.Extract(r.Context(), propagation.HeaderCarrier(r.Header))
		route := routeLabel(r.Method, r.URL.Path)
		ctx, span := rm.tracer.Start(ctx, r.Method+" "+route,
			trace.WithSpanKind(trace.SpanKindServer),
			trace.WithAttributes(
				attribute.String("http.request.method", r.Method),
				attribute.String("http.route", route),
			),
		)
		defer span.End()

		rec := &statusRecorder{ResponseWriter: w, status: 0}
		start := time.Now()
		next.ServeHTTP(rec, r.WithContext(ctx))
		if rec.status == 0 {
			rec.status = http.StatusOK
		}

		class := statusClass(rec.status)
		span.SetAttributes(attribute.Int("http.response.status_code", rec.status))
		rm.duration.Record(ctx, time.Since(start).Milliseconds(), metric.WithAttributes(
			attribute.String("http.route", route),
			attribute.String("http.request.method", r.Method),
			attribute.String("http.status_class", class),
			attribute.String("http.status_code", strconv.Itoa(rec.status)),
		))
	})
}
