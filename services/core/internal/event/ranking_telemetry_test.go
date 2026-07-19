package event_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/event"
)

// TestRankerEmitsQuarantineTelemetry is the mandatory observability-field-emission
// test (CLAUDE.md: "observability field emission" is mandatory-TDD) for the money-
// correctness quarantine seam (issue #71). Ranking a set with N non-canonical-
// currency known events must increment the OTel counter
// `event.ranking.quarantined_currency` by EXACTLY N and emit a slog.Warn carrying
// the stable key `canonical_currency` plus the quarantined count. Its absence was
// the sole prior review blocker — it must exist.
func TestRankerEmitsQuarantineTelemetry(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	prev := otel.GetMeterProvider()
	otel.SetMeterProvider(provider)
	t.Cleanup(func() { otel.SetMeterProvider(prev) })

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))

	// Construct the Ranker AFTER installing the manual-reader provider so its
	// counter is registered against the reader.
	ranker := event.NewRanker(logger)

	base := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	// Canonical = IRR (3 events). USD (2) + EUR (1) are quarantined ⇒ N = 3.
	const wantQuarantined = 3
	events := []db.MarketEvent{
		mkEventCE(uuid.New(), "warning", 100, "IRR", 0, 5000, 5000, base),
		mkEventCE(uuid.New(), "warning", 200, "IRR", 0, 5000, 5000, base),
		mkEventCE(uuid.New(), "warning", 300, "IRR", 0, 5000, 5000, base),
		mkEventCE(uuid.New(), "critical", 900, "USD", 0, 5000, 5000, base),
		mkEventCE(uuid.New(), "critical", 800, "USD", 0, 5000, 5000, base),
		mkEventCE(uuid.New(), "critical", 700, "EUR", 0, 5000, 5000, base),
	}

	ranker.Rank(context.Background(), events)

	// (1) Counter incremented by exactly N.
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}
	var got int64 = -1
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != "event.ranking.quarantined_currency" {
				continue
			}
			sum, ok := m.Data.(metricdata.Sum[int64])
			if !ok {
				t.Fatalf("counter %q is not an int64 sum", m.Name)
			}
			var total int64
			for _, dp := range sum.DataPoints {
				total += dp.Value
			}
			got = total
		}
	}
	if got != wantQuarantined {
		t.Fatalf("event.ranking.quarantined_currency = %d, want %d (observability seam incomplete)", got, wantQuarantined)
	}

	// (2) Warn record carries the canonical currency and the quarantined count.
	// Decode with UseNumber so the JSON count stays a json.Number (no float64 on a
	// money-adjacent path — the forbidigo guard bans it, even in tests).
	dec := json.NewDecoder(bytes.NewReader(bytes.TrimSpace(buf.Bytes())))
	dec.UseNumber()
	var rec map[string]any
	if err := dec.Decode(&rec); err != nil {
		t.Fatalf("expected a JSON warn record, got %q (err %v)", buf.String(), err)
	}
	if rec["level"] != "WARN" {
		t.Fatalf("expected a WARN record, got level %v", rec["level"])
	}
	if rec["canonical_currency"] != "IRR" {
		t.Fatalf("warn record must carry canonical_currency=IRR, got %v", rec["canonical_currency"])
	}
	if n, ok := rec["quarantined"].(json.Number); !ok || n.String() != strconv.Itoa(wantQuarantined) {
		t.Fatalf("warn record must carry quarantined=%d, got %v", wantQuarantined, rec["quarantined"])
	}
}
