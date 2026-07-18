package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// TestRED_EmitsRequestDurationSeries proves the gateway RED seam actually EMITS
// the http.server.request.duration histogram the §17.2 P95 panels and the §18
// chat-latency dashboard query. Observability field emission is mandatory-TDD
// (CLAUDE.md): the series NAME and the label keys here must match the dashboards.
func TestRED_EmitsRequestDurationSeries(t *testing.T) {
	reader := metric.NewManualReader()
	provider := metric.NewMeterProvider(metric.WithReader(reader))
	prev := otel.GetMeterProvider()
	otel.SetMeterProvider(provider)
	t.Cleanup(func() { otel.SetMeterProvider(prev) })

	rm := newREDMetrics()
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := rm.wrap(next)

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	h.ServeHTTP(httptest.NewRecorder(), req)

	var rmData metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rmData); err != nil {
		t.Fatalf("collect: %v", err)
	}

	var found bool
	for _, sm := range rmData.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != "http.server.request.duration" {
				continue
			}
			found = true
			hist, ok := m.Data.(metricdata.Histogram[int64])
			if !ok {
				t.Fatalf("http.server.request.duration is not an int64 histogram")
			}
			if len(hist.DataPoints) == 0 {
				t.Fatalf("no data points recorded on the RED histogram")
			}
			// The route/status labels the dashboards slice by MUST be present.
			attrs := hist.DataPoints[0].Attributes
			for _, key := range []string{"http.route", "http.status_class"} {
				if _, present := attrs.Value(attribute.Key(key)); !present {
					t.Fatalf("RED histogram missing label %q; dashboards would break", key)
				}
			}
		}
	}
	if !found {
		t.Fatalf("http.server.request.duration not emitted; §17.2 P95 seam incomplete")
	}
}
