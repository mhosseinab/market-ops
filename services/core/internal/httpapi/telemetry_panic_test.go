package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// redTestHarness installs an isolated ManualReader meter provider and an
// in-memory span recorder so a single request's RED metric datapoints and its
// server span can be inspected together (span/metric AGREEMENT, issue #158).
type redTestHarness struct {
	reader *sdkmetric.ManualReader
	spans  *tracetest.SpanRecorder
	rm     *redMetrics
}

func newREDTestHarness(t *testing.T) *redTestHarness {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	prevM := otel.GetMeterProvider()
	otel.SetMeterProvider(mp)

	spans := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spans))
	prevT := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)

	t.Cleanup(func() {
		otel.SetMeterProvider(prevM)
		otel.SetTracerProvider(prevT)
	})

	// newREDMetrics reads the CURRENT global providers, so build it after the
	// test providers are installed.
	return &redTestHarness{reader: reader, spans: spans, rm: newREDMetrics()}
}

// durationPoints returns every recorded http.server.request.duration datapoint.
func (h *redTestHarness) durationPoints(t *testing.T) []metricdata.HistogramDataPoint[int64] {
	t.Helper()
	var rmData metricdata.ResourceMetrics
	if err := h.reader.Collect(context.Background(), &rmData); err != nil {
		t.Fatalf("collect: %v", err)
	}
	var pts []metricdata.HistogramDataPoint[int64]
	for _, sm := range rmData.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != "http.server.request.duration" {
				continue
			}
			hist, ok := m.Data.(metricdata.Histogram[int64])
			if !ok {
				t.Fatalf("http.server.request.duration is not an int64 histogram")
			}
			pts = append(pts, hist.DataPoints...)
		}
	}
	return pts
}

func attrString(dp metricdata.HistogramDataPoint[int64], key string) (string, bool) {
	v, ok := dp.Attributes.Value(attribute.Key(key))
	if !ok {
		return "", false
	}
	return v.AsString(), true
}

// serveExpectingPanic drives one request through the wrapped handler and asserts
// the panic RE-PROPAGATES past the RED middleware (never swallowed) — the outer
// recovery boundary (Go's net/http server) still runs unchanged.
func serveExpectingPanic(t *testing.T, h http.Handler, req *http.Request) {
	t.Helper()
	panicked := false
	func() {
		defer func() {
			if r := recover(); r != nil {
				panicked = true
			}
		}()
		h.ServeHTTP(httptest.NewRecorder(), req)
	}()
	if !panicked {
		t.Fatal("panic was swallowed by the RED middleware; the outer recovery boundary would never run (issue #158 re-panic)")
	}
}

// TestRED_PanicRecordsExactlyOneServerError proves a panicking handler produces
// EXACTLY ONE RED datapoint classified 5xx AND re-panics to the recovery
// boundary. Before #158 the datapoint vanished and panics reduced observed
// traffic instead of raising the error rate.
func TestRED_PanicRecordsExactlyOneServerError(t *testing.T) {
	h := newREDTestHarness(t)
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom: sensitive detail that must never enter a label")
	})
	wrapped := h.rm.wrap(next)

	serveExpectingPanic(t, wrapped, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	pts := h.durationPoints(t)
	if len(pts) != 1 {
		t.Fatalf("expected exactly one RED datapoint for a panic, got %d", len(pts))
	}
	if class, _ := attrString(pts[0], "http.status_class"); class != "5xx" {
		t.Fatalf("panic RED datapoint classified %q, want 5xx", class)
	}
	if code, _ := attrString(pts[0], "http.status_code"); code != "500" {
		t.Fatalf("panic RED status_code %q, want 500", code)
	}

	// Free-text containment: the raw panic value must NOT appear in any label.
	for _, kv := range pts[0].Attributes.ToSlice() {
		if got := kv.Value.AsString(); got != "" && containsSubstr(got, "sensitive detail") {
			t.Fatalf("panic value leaked into metric label %q=%q", kv.Key, got)
		}
	}
}

// TestRED_PanicSpanAndMetricAgree proves the span status/events and the metric
// AGREE for a panicking request: span status is Error, its status_code attribute
// matches the metric's status_code, and no raw panic text is on the span.
func TestRED_PanicSpanAndMetricAgree(t *testing.T) {
	h := newREDTestHarness(t)
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom: secret stack frame")
	})
	wrapped := h.rm.wrap(next)

	serveExpectingPanic(t, wrapped, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	pts := h.durationPoints(t)
	if len(pts) != 1 {
		t.Fatalf("expected one datapoint, got %d", len(pts))
	}
	metricCode, _ := attrString(pts[0], "http.status_code")

	ended := h.spans.Ended()
	if len(ended) != 1 {
		t.Fatalf("expected exactly one ended span, got %d", len(ended))
	}
	sp := ended[0]
	if sp.Status().Code != codes.Error {
		t.Fatalf("span status code = %v, want Error for a panic", sp.Status().Code)
	}
	var spanCode string
	for _, a := range sp.Attributes() {
		if string(a.Key) == "http.response.status_code" {
			spanCode = strconv.FormatInt(a.Value.AsInt64(), 10)
		}
		if got := a.Value.AsString(); got != "" && containsSubstr(got, "secret stack frame") {
			t.Fatalf("panic value leaked into span attribute %q", a.Key)
		}
	}
	if spanCode != metricCode {
		t.Fatalf("span status_code %q disagrees with metric status_code %q", spanCode, metricCode)
	}
	if got := sp.Status().Description; containsSubstr(got, "secret stack frame") {
		t.Fatalf("panic value leaked into span status description %q", got)
	}
}

// TestRED_PanicAfterWriteHeader documents status classification when the handler
// wrote a header before panicking. A <500 header still escalates to 500 so the
// crash raises the RED error rate; an already-server-error header is preserved.
func TestRED_PanicAfterWriteHeader(t *testing.T) {
	cases := []struct {
		name     string
		written  int
		wantCode string
	}{
		{"panic after 200 escalates to 500", http.StatusOK, "500"},
		{"panic after 400 escalates to 500", http.StatusBadRequest, "500"},
		{"panic after 503 preserves 503", http.StatusServiceUnavailable, "503"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newREDTestHarness(t)
			next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.written)
				panic("boom after header")
			})
			wrapped := h.rm.wrap(next)

			serveExpectingPanic(t, wrapped, httptest.NewRequest(http.MethodGet, "/healthz", nil))

			pts := h.durationPoints(t)
			if len(pts) != 1 {
				t.Fatalf("expected exactly one datapoint, got %d", len(pts))
			}
			if code, _ := attrString(pts[0], "http.status_code"); code != tc.wantCode {
				t.Fatalf("status_code = %q, want %q", code, tc.wantCode)
			}
			if class, _ := attrString(pts[0], "http.status_class"); class != "5xx" {
				t.Fatalf("status_class = %q, want 5xx", class)
			}
		})
	}
}

// TestRED_NormalPathsExactlyOnce proves the non-panic paths still record exactly
// one datapoint with the correct class — no double-count regression from moving
// finalization into a defer.
func TestRED_NormalPathsExactlyOnce(t *testing.T) {
	cases := []struct {
		name      string
		status    int
		wantClass string
		wantCode  string
	}{
		{"implicit 200", 0, "2xx", "200"},
		{"explicit 404", http.StatusNotFound, "4xx", "404"},
		{"explicit 500", http.StatusInternalServerError, "5xx", "500"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newREDTestHarness(t)
			next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if tc.status == 0 {
					_, _ = w.Write([]byte("ok"))
					return
				}
				w.WriteHeader(tc.status)
			})
			wrapped := h.rm.wrap(next)
			wrapped.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/healthz", nil))

			pts := h.durationPoints(t)
			if len(pts) != 1 {
				t.Fatalf("expected exactly one datapoint, got %d", len(pts))
			}
			if class, _ := attrString(pts[0], "http.status_class"); class != tc.wantClass {
				t.Fatalf("status_class = %q, want %q", class, tc.wantClass)
			}
			if code, _ := attrString(pts[0], "http.status_code"); code != tc.wantCode {
				t.Fatalf("status_code = %q, want %q", code, tc.wantCode)
			}

			ended := h.spans.Ended()
			if len(ended) != 1 {
				t.Fatalf("expected exactly one ended span, got %d", len(ended))
			}
			if ended[0].Status().Code == codes.Error {
				t.Fatalf("non-panic %s marked span as Error", tc.name)
			}
		})
	}
}

func containsSubstr(s, sub string) bool {
	if sub == "" {
		return false
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
