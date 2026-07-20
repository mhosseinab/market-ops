package connector

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/mhosseinab/market-ops/services/core/internal/mockdk"
)

// headerCapture records the headers of the last request that passed through it,
// then delegates to base.
type headerCapture struct {
	base http.RoundTripper
	last http.Header
}

func (h *headerCapture) RoundTrip(req *http.Request) (*http.Response, error) {
	h.last = req.Header.Clone()
	return h.base.RoundTrip(req)
}

// TestDKClient_PropagatesTraceContext asserts the core → DK Seller (Route A)
// outbound request carries a W3C traceparent continuing the caller's trace, even
// when the caller supplies its own *http.Client (issue #152 centralization: a
// provided client is still instrumented via NewDKClient).
func TestDKClient_PropagatesTraceContext(t *testing.T) {
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

	srv := mockdk.NewServer(mockdk.DefaultConfig())
	defer srv.Close()

	cap := &headerCapture{base: srv.Client().Transport}
	httpClient := srv.Client()
	httpClient.Transport = cap

	dk, err := NewDKClient(srv.URL, httpClient)
	if err != nil {
		t.Fatalf("dk client: %v", err)
	}

	ctx, parent := otel.Tracer("test").Start(context.Background(), "server")
	defer parent.End()

	if _, err := dk.ExchangeToken(ctx, "auth-code"); err != nil {
		t.Fatalf("exchange token: %v", err)
	}

	tpHeader := cap.last.Get("traceparent")
	if tpHeader == "" {
		t.Fatalf("outbound DK request carried no traceparent")
	}
	parts := strings.Split(tpHeader, "-")
	if len(parts) != 4 || parts[1] != parent.SpanContext().TraceID().String() {
		t.Errorf("trace not continued to DK Seller: %q", tpHeader)
	}
	if parts[2] == parent.SpanContext().SpanID().String() {
		t.Errorf("DK outbound span id equals server span id; expected a child")
	}
}
