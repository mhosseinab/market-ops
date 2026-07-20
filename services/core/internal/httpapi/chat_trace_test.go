package httpapi

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// TestHTTPLLMChat_PropagatesTraceContext asserts the core → LLM plane outbound
// request carries a W3C traceparent continuing the caller's trace with a child
// span, while preserving the read/Draft-only bearer credential (issue #152).
func TestHTTPLLMChat_PropagatesTraceContext(t *testing.T) {
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
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "data: {}\n\n")
	}))
	defer srv.Close()

	ctx, parent := otel.Tracer("test").Start(context.Background(), "server")
	defer parent.End()

	svc := NewHTTPLLMChat(srv.URL, "draft-only-token")
	body, err := svc.StartTurn(ctx, ChatTurn{
		UserID:         uuid.New(),
		OrganizationID: uuid.New(),
		Message:        "hi",
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = body.Close()

	tpHeader := got.Get("traceparent")
	if tpHeader == "" {
		t.Fatalf("outbound LLM request carried no traceparent")
	}
	parts := strings.Split(tpHeader, "-")
	if len(parts) != 4 || parts[1] != parent.SpanContext().TraceID().String() {
		t.Errorf("trace not continued to LLM plane: %q", tpHeader)
	}
	if parts[2] == parent.SpanContext().SpanID().String() {
		t.Errorf("LLM outbound span id equals server span id; expected a child")
	}
	if got.Get("Authorization") != "Bearer draft-only-token" {
		t.Errorf("draft-only credential not preserved: %q", got.Get("Authorization"))
	}
}
