package httpx

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// withTracingGlobals installs a real SDK tracer provider and the W3C
// TraceContext+Baggage propagator for the duration of a test, restoring the
// prior globals afterwards. Without a real provider the tracer is a no-op and no
// span context exists to inject; without the propagator Inject writes nothing.
func withTracingGlobals(t *testing.T) *sdktrace.TracerProvider {
	t.Helper()
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
	return tp
}

// recordingServer captures the headers of the first request it receives.
func recordingServer(t *testing.T, hdr *http.Header) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*hdr = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestNewClient_InjectsChildSpanSameTrace(t *testing.T) {
	withTracingGlobals(t)
	var got http.Header
	srv := recordingServer(t, &got)

	// A parent (server) span whose context governs the outbound call.
	ctx, parent := otel.Tracer("test").Start(context.Background(), "server")
	defer parent.End()
	parentSC := parent.SpanContext()

	client := NewClient(5 * time.Second)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	tp := got.Get("traceparent")
	if tp == "" {
		t.Fatalf("outbound request carried no traceparent header")
	}
	// traceparent: version-traceid-spanid-flags
	parts := strings.Split(tp, "-")
	if len(parts) != 4 {
		t.Fatalf("malformed traceparent %q", tp)
	}
	if parts[1] != parentSC.TraceID().String() {
		t.Errorf("trace id not continued: got %s want %s", parts[1], parentSC.TraceID())
	}
	if parts[2] == parentSC.SpanID().String() {
		t.Errorf("outbound span id equals the server span id %s; expected a distinct CHILD span", parts[2])
	}
	if parts[2] == "0000000000000000" {
		t.Errorf("outbound span id is the zero span id")
	}
}

func TestNewClient_NoInboundContext_StartsValidTrace(t *testing.T) {
	withTracingGlobals(t)
	var got http.Header
	srv := recordingServer(t, &got)

	client := NewClient(5 * time.Second)
	// No parent span: a plain background context.
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	tp := got.Get("traceparent")
	if tp == "" {
		t.Fatalf("outbound request without inbound context carried no traceparent")
	}
	parts := strings.Split(tp, "-")
	if len(parts) != 4 || parts[1] == "00000000000000000000000000000000" {
		t.Fatalf("expected a valid new trace, got %q", tp)
	}
}

func TestNewClient_BaggageAllowlist_DropsNonAllowlistedKey(t *testing.T) {
	withTracingGlobals(t)
	var got http.Header
	srv := recordingServer(t, &got)

	allowed, err := baggage.NewMember("service.version", "v1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	// A key NOT on the allowlist — must never cross the boundary.
	secret, err := baggage.NewMember("secret_note", "do-not-leak")
	if err != nil {
		t.Fatal(err)
	}
	bag, err := baggage.New(allowed, secret)
	if err != nil {
		t.Fatal(err)
	}
	ctx := baggage.ContextWithBaggage(context.Background(), bag)

	client := NewClient(5 * time.Second)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	outBaggage := got.Get("baggage")
	if !strings.Contains(outBaggage, "service.version") {
		t.Errorf("allowlisted baggage key was dropped: %q", outBaggage)
	}
	if strings.Contains(outBaggage, "secret_note") || strings.Contains(outBaggage, "do-not-leak") {
		t.Errorf("non-allowlisted baggage leaked outbound: %q", outBaggage)
	}
}

func TestNewClient_PreservesAuthHeader(t *testing.T) {
	withTracingGlobals(t)
	var got http.Header
	srv := recordingServer(t, &got)

	client := NewClient(5 * time.Second)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer secret-token")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	if got.Get("Authorization") != "Bearer secret-token" {
		t.Errorf("Authorization header not preserved: %q", got.Get("Authorization"))
	}
}

func TestNewClient_PreservesContextCancellation(t *testing.T) {
	withTracingGlobals(t)
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done() // hold until the client cancels
		close(block)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	client := NewClient(0) // no client timeout: cancellation must come from the context
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	_, err = client.Do(req)
	if err == nil {
		t.Fatal("expected a cancellation error from the request context")
	}
	select {
	case <-block:
	case <-time.After(2 * time.Second):
		t.Fatal("server never observed request-context cancellation")
	}
}

func TestInstrument_Idempotent(t *testing.T) {
	base := &http.Client{}
	once := Instrument(base)
	twice := Instrument(once)
	if _, ok := once.Transport.(*propagatingTransport); !ok {
		t.Fatalf("Instrument did not install propagatingTransport")
	}
	if inner, ok := twice.Transport.(*propagatingTransport); ok {
		if _, doubled := inner.base.(*propagatingTransport); doubled {
			t.Fatalf("Instrument double-wrapped the transport")
		}
	}
}

func TestInstrument_PropagatesThroughProvidedClient(t *testing.T) {
	withTracingGlobals(t)
	var got http.Header
	srv := recordingServer(t, &got)

	// A caller-supplied client (e.g. a recording client for snapshots) must still
	// get propagation once routed through Instrument.
	provided := &http.Client{Timeout: 5 * time.Second}
	client := Instrument(provided)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if got.Get("traceparent") == "" {
		t.Errorf("provided client did not propagate trace context after Instrument")
	}
}

var _ = trace.SpanKindClient
