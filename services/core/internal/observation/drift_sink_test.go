package observation

import (
	"context"
	"strconv"
	"testing"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// TestUnsupportedParserMetricLabelCardinalityIsBounded is the #154 REOPEN
// regression (observability integrity, §8 SRE). The parser-drift counter must
// carry only BOUNDED, closed-set label values. The rejected connector/parser
// VERSION strings are attacker-influenced and unvalidated (Capture.Validate only
// checks parser version non-empty; connector version is unchecked), so using them
// as raw metric labels lets N distinct rejected versions create N distinct label
// sets — an unbounded metric-cardinality DoS. Feeding many distinct rejected
// versions must therefore yield a bounded number of distinct metric label sets,
// and the raw version must NEVER appear as a metric attribute key.
func TestUnsupportedParserMetricLabelCardinalityIsBounded(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	prev := otel.GetMeterProvider()
	otel.SetMeterProvider(provider)
	t.Cleanup(func() { otel.SetMeterProvider(prev) })

	// Construct the sink AFTER installing the manual-reader provider so its counter
	// registers against the reader.
	sink := newTelemetryDriftSink(nil)

	const n = 500
	acct := uuid.New()
	target := uuid.New()
	for i := 0; i < n; i++ {
		sink.UnsupportedParser(context.Background(), UnsupportedParserEvent{
			Account:    acct,
			TargetID:   target,
			Route:      string(RouteC),
			SourceType: string(SourcePublicWebEndpoint),
			// Distinct, attacker-influenced version strings on every call.
			ParserVersion:    "evil-parser@" + strconv.Itoa(i),
			ConnectorVersion: "evil-connector@" + strconv.Itoa(i),
			Reason:           RejectionUnknownParser,
		})
	}

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}

	var dps []metricdata.DataPoint[int64]
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != "observation.unsupported_parser_rejections" {
				continue
			}
			sum, ok := m.Data.(metricdata.Sum[int64])
			if !ok {
				t.Fatalf("counter %q is not an int64 sum", m.Name)
			}
			dps = sum.DataPoints
		}
	}
	if dps == nil {
		t.Fatal("counter observation.unsupported_parser_rejections not found")
	}

	// (1) Bounded distinct label sets: route(3) x source_type(5) x reason(<=4) is a
	// small closed universe. N=500 distinct versions must NOT create ~N label sets.
	const maxLabelSets = 60
	if len(dps) > maxLabelSets {
		t.Fatalf("metric cardinality unbounded: %d distinct label sets for %d distinct rejected versions (want <= %d)", len(dps), n, maxLabelSets)
	}

	// With a single (route, source_type, reason) triple across all N calls, the
	// bounded universe collapses to exactly one label set.
	if len(dps) != 1 {
		t.Fatalf("expected exactly 1 label set for a constant (route,source_type,reason), got %d", len(dps))
	}

	// (2) The raw version must never be a metric attribute key; the bounded reason
	// label must be present with a value from the closed set.
	var total int64
	sawReason := false
	for _, dp := range dps {
		for _, kv := range dp.Attributes.ToSlice() {
			key := string(kv.Key)
			if key == "parser_version" || key == "connector_version" {
				t.Fatalf("raw version leaked as metric label key %q (value %q) — unbounded cardinality", key, kv.Value.AsString())
			}
			if key == "reason" {
				sawReason = true
				switch ParserRejectionReason(kv.Value.AsString()) {
				case RejectionBlankParserVersion, RejectionUnknownParser, RejectionConnectorIncompatible:
				default:
					t.Fatalf("reason label %q is outside the bounded closed set", kv.Value.AsString())
				}
			}
		}
		total += dp.Value
	}
	if !sawReason {
		t.Fatal("bounded reason label absent — drift attribution lost")
	}

	// (3) Signal intact: every rejection is still counted.
	if total != n {
		t.Fatalf("rejection count = %d, want %d (drift signal must stay intact)", total, n)
	}
}

// TestClassifyRejectionIsBoundedClosedSet locks the registry classifier to the closed
// reason set (#154 REOPEN). It never echoes the raw version and partitions the
// rejection space into stable buckets so the metric label stays bounded.
func TestClassifyRejectionIsBoundedClosedSet(t *testing.T) {
	reg := NewParserRegistry(ParserSupport{
		SourceType:        SourcePublicWebEndpoint,
		ParserVersion:     "dk-product@1.0.0",
		ConnectorVersions: []string{"market-ops-ext@0.1.0"},
	})

	cases := []struct {
		name      string
		source    SourceType
		parser    string
		connector string
		want      ParserRejectionReason
	}{
		{"blank version", SourcePublicWebEndpoint, "   ", "x", RejectionBlankParserVersion},
		{"unknown tuple", SourcePublicWebEndpoint, "attacker@9.9.9", "x", RejectionUnknownParser},
		{"connector mismatch", SourcePublicWebEndpoint, "dk-product@1.0.0", "rogue-ext@6.6.6", RejectionConnectorIncompatible},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := reg.ClassifyRejection(tc.source, tc.parser, tc.connector)
			if got != tc.want {
				t.Fatalf("ClassifyRejection = %q, want %q", got, tc.want)
			}
			if string(got) == tc.parser || string(got) == tc.connector {
				t.Fatalf("classification %q echoed the raw version input", got)
			}
		})
	}
}
