// Package httpx centralizes construction of OUTBOUND HTTP clients for the core
// so that W3C trace context (traceparent/tracestate) is propagated across every
// process boundary the core initiates: core → LLM plane and core → DK Seller
// (read and write). Installing a global propagator (internal/obs) alone does NOT
// mutate outbound requests — the header injection has to happen per request, on
// the client transport. Routing every production outbound client through this
// package guarantees no client can silently omit propagation (issue #152).
//
// Design (issue #152, "explicit inject round-tripper" option): a RoundTripper
// starts a CLIENT span per request (a child of the caller's span, same trace),
// injects the globally-configured propagator into the request headers from that
// child span's context, then delegates. This reuses the single W3C propagator
// installed by obs.Init and adds NO new OpenTelemetry dependency (it pins to the
// go.opentelemetry.io/otel v1.44.0 module already in go.mod, avoiding the
// separate contrib/otelhttp module and its independent version line).
//
// It preserves existing behavior: authentication headers set by the caller are
// untouched (only trace/baggage keys are added), and the request context governs
// cancellation exactly as before (the child span context derives from it), so the
// long-lived SSE stream's write-deadline / disconnect seam still works.
//
// Baggage is ALLOWLISTED, never copied blindly: baggage crosses the boundary as
// request headers, so only non-sensitive technical correlators may propagate.
// Approval-control identity (action id / parameter version / context version)
// rides on the SPAN as attributes per the observability contract, not in baggage.
package httpx

import (
	"context"
	"net/http"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

// tracerName identifies the instrumentation scope for outbound client spans.
const tracerName = "github.com/mhosseinab/market-ops/services/core/internal/httpx"

// allowedBaggageKeys is the ALLOWLIST of baggage members that may cross a process
// boundary. Baggage is serialized verbatim into a downstream request header, so
// this set must contain ONLY non-sensitive technical correlators. It must NEVER
// contain a secret, token, credential, PII, raw marketplace text, an
// approval-control secret, or any Persian/localized copy — those are never-cut
// containment and localization-boundary violations. The approval-control identity
// (action id / parameter version / context version) travels on the span as
// attributes so an approval control is reconstructable from telemetry, and is
// deliberately NOT placed in baggage.
var allowedBaggageKeys = map[string]struct{}{
	"service.version": {}, // release/build version, safe to correlate across hops
	"schema.version":  {}, // contract/schema version, a technical identifier
}

// propagatingTransport wraps a base RoundTripper. For every outbound request it
// starts a client span, injects the configured W3C propagator (trace context +
// allowlisted baggage) from that child span's context into the request headers,
// delegates to base, and ends the span with the outcome. It never mutates the
// caller's *http.Request in place (it clones), so a retried request is not
// double-injected and the caller's headers are preserved.
type propagatingTransport struct {
	base http.RoundTripper
}

// RoundTrip implements http.RoundTripper.
func (t *propagatingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Start a client span as a child of the inbound context's span. When no
	// TracerProvider/propagator is installed (tracing disabled) the tracer is a
	// no-op and Inject writes nothing — outbound behavior is unchanged, matching
	// the fail-closed "off is off" posture of obs.Init.
	ctx, span := otel.Tracer(tracerName).Start(req.Context(), "HTTP "+req.Method,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			semconv.HTTPRequestMethodKey.String(req.Method),
			semconv.URLFull(req.URL.String()),
			semconv.ServerAddress(req.URL.Hostname()),
		),
	)

	// Clone so we never mutate the caller's request; the clone carries the child
	// span context, which derives from req.Context() and therefore preserves the
	// caller's deadline and cancellation (the SSE disconnect seam).
	outReq := req.Clone(ctx)

	// Inject trace context + ONLY allowlisted baggage from the child span context.
	injectCtx := allowlistBaggage(ctx)
	otel.GetTextMapPropagator().Inject(injectCtx, propagation.HeaderCarrier(outReq.Header))

	resp, err := t.base.RoundTrip(outReq)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		span.End()
		return resp, err
	}
	span.SetAttributes(semconv.HTTPResponseStatusCode(resp.StatusCode))
	if resp.StatusCode >= 400 {
		span.SetStatus(codes.Error, http.StatusText(resp.StatusCode))
	}
	// End the span once response headers are available. For a streaming (SSE)
	// response this is correct: ending the span neither closes the body nor
	// cancels the request; the body streams on under the caller's context.
	span.End()
	return resp, err
}

// allowlistBaggage returns a context whose baggage contains ONLY allowlisted
// members; every other member is dropped so it can never be injected into an
// outbound header. On any reconstruction error it fails closed by propagating NO
// baggage rather than risk leaking a member.
func allowlistBaggage(ctx context.Context) context.Context {
	members := baggage.FromContext(ctx).Members()
	if len(members) == 0 {
		return ctx
	}
	kept := make([]baggage.Member, 0, len(members))
	for _, m := range members {
		if _, ok := allowedBaggageKeys[m.Key()]; ok {
			kept = append(kept, m)
		}
	}
	filtered, err := baggage.New(kept...)
	if err != nil {
		empty, _ := baggage.New()
		return baggage.ContextWithBaggage(ctx, empty)
	}
	return baggage.ContextWithBaggage(ctx, filtered)
}

// WrapTransport returns a RoundTripper that injects trace context around base.
// A nil base uses http.DefaultTransport. It never double-wraps an already
// instrumented transport.
func WrapTransport(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	if _, ok := base.(*propagatingTransport); ok {
		return base
	}
	return &propagatingTransport{base: base}
}

// NewClient returns a fresh *http.Client whose transport propagates trace
// context on every request. A timeout of 0 leaves the client with no overall
// deadline (required for long-lived SSE streams, whose cancellation is governed
// by the request context).
func NewClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout:   timeout,
		Transport: WrapTransport(nil),
	}
}

// Instrument ensures c propagates trace context and returns it. A nil client
// gets a fresh instrumented client with no timeout. Instrument is idempotent: a
// client already carrying the propagating transport is returned unchanged, so a
// caller-supplied client (e.g. a recording client for snapshots, or a test
// client) can be routed through it without double-wrapping.
func Instrument(c *http.Client) *http.Client {
	if c == nil {
		return NewClient(0)
	}
	c.Transport = WrapTransport(c.Transport)
	return c
}
