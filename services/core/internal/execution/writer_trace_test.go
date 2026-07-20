package execution

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// TestHTTPWriter_PropagatesTraceAndPreservesHeaders asserts the core → DK write
// path carries a W3C traceparent continuing the caller's trace, while preserving
// the bearer credential AND the idempotency key (EXE-002, issue #152).
func TestHTTPWriter_PropagatesTraceAndPreservesHeaders(t *testing.T) {
	prevTP := otel.GetTracerProvider()
	prevProp := otel.GetTextMapPropagator()
	tp := sdktrace.NewTracerProvider()
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{},
	))
	t.Cleanup(func() {
		otel.SetTracerProvider(prevTP)
		otel.SetTextMapPropagator(prevProp)
		_ = tp.Shutdown(context.Background())
	})

	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":{"batch_id":42}}`))
	}))
	defer srv.Close()

	ctx, parent := otel.Tracer("test").Start(context.Background(), "server")
	defer parent.End()

	w := NewHTTPWriter(srv.URL, "dk-token", nil)
	res := w.WritePrice(ctx, WriteRequest{
		IdempotencyKey:  "idem-123",
		VariantNativeID: 7,
		PriceMantissa:   1000,
		PriceCurrency:   "IRR",
		PriceExponent:   0,
	})
	if res.Outcome != OutcomeAccepted {
		t.Fatalf("unexpected outcome %v (%s)", res.Outcome, res.Detail)
	}

	tpHeader := got.Get("traceparent")
	if tpHeader == "" {
		t.Fatalf("outbound write carried no traceparent")
	}
	parts := strings.Split(tpHeader, "-")
	if len(parts) != 4 || parts[1] != parent.SpanContext().TraceID().String() {
		t.Errorf("trace not continued to DK write path: %q", tpHeader)
	}
	if got.Get("Authorization") != "Bearer dk-token" {
		t.Errorf("bearer credential not preserved: %q", got.Get("Authorization"))
	}
	if got.Get("Idempotency-Key") != "idem-123" {
		t.Errorf("idempotency key not preserved: %q", got.Get("Idempotency-Key"))
	}
}
