package routec

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/google/uuid"
)

// Harness measures Route C safe throughput, block rate, and byte cost (PRD §0.1
// Gate 0a, §10.2, §17.3). It drives a Fetcher through a bounded number of
// requests and aggregates the outcome. It is deliberately transport-agnostic: it
// NEVER opens a connection itself — it calls the injected Fetcher. In tests that
// Fetcher hits an httptest server; the S35 measurement (a GATED, human-go
// operation) injects the real HTTPFetcher against real DK. This package never
// generates live DK traffic on its own.
type Harness struct {
	fetcher Fetcher
	limiter *Limiter
	// Record, when true, causes Run to write a MeasurementReport to RecordPath
	// (the -record mode used to capture S35 baselines). It is off by default.
	Record     bool
	RecordPath string
	now        func() time.Time
}

// NewHarness builds a measurement harness over a fetcher, throttled by the same
// concurrency limiter Route C uses in production so the measurement reflects the
// real envelope.
func NewHarness(fetcher Fetcher, perAccount, perHost int) *Harness {
	return &Harness{
		fetcher: fetcher,
		limiter: NewLimiter(perAccount, perHost),
		now:     time.Now,
	}
}

// MeasurementReport is the aggregated result of a harness run. It feeds the Gate
// 0a capacity decision (does measured cap clear ≥ 50 priority targets/account?).
// Rates are integer basis points and throughput is integer milli-requests/sec —
// no floats on any path (repo guard, PRD §9.1).
type MeasurementReport struct {
	Requests           int            `json:"requests"`
	Duration           time.Duration  `json:"duration_ns"`
	ThroughputMilliRPS int64          `json:"throughput_milli_rps"`
	BlockRateBP        int            `json:"block_rate_bp"`
	TransportRateBP    int            `json:"transport_rate_bp"`
	TotalBytes         int64          `json:"total_bytes"`
	AvgBytes           int64          `json:"avg_bytes"`
	P50Latency         time.Duration  `json:"p50_latency_ns"`
	P95Latency         time.Duration  `json:"p95_latency_ns"`
	SignalCounts       map[string]int `json:"signal_counts"`
	CapturedAt         time.Time      `json:"captured_at"`
}

// Run drives count fetches of the given request (all against the injected
// Fetcher) and returns the aggregated report. It respects ctx cancellation. When
// Record is set it also writes the report to RecordPath.
func (h *Harness) Run(ctx context.Context, req FetchRequest, count int) (MeasurementReport, error) {
	if count <= 0 {
		return MeasurementReport{}, fmt.Errorf("routec: harness count must be positive")
	}
	account := req.Account
	if account == uuid.Nil {
		account = uuid.New()
	}
	signals := map[string]int{}
	var totalBytes int64
	var blocked, transport int
	latencies := make([]time.Duration, 0, count)

	start := h.now()
	for i := 0; i < count; i++ {
		if err := ctx.Err(); err != nil {
			return MeasurementReport{}, err
		}
		release, err := h.limiter.Acquire(ctx, account)
		if err != nil {
			return MeasurementReport{}, err
		}
		res, ferr := h.fetcher.Fetch(ctx, req)
		release()
		signals[res.Signal.String()]++
		totalBytes += res.Bytes
		latencies = append(latencies, res.Latency)
		switch res.Signal {
		case Signal403, Signal429, SignalChallenge:
			blocked++
		case SignalTransport:
			transport++
		}
		_ = ferr // transport errors already reflected in the signal count
	}
	elapsed := h.now().Sub(start)

	report := MeasurementReport{
		Requests:     count,
		Duration:     elapsed,
		TotalBytes:   totalBytes,
		SignalCounts: signals,
		CapturedAt:   start,
	}
	if ns := elapsed.Nanoseconds(); ns > 0 {
		// milli-RPS = requests * 1000 * 1e9ns / elapsedNs (all integer).
		report.ThroughputMilliRPS = int64(count) * 1000 * int64(time.Second) / ns
	}
	report.BlockRateBP = blocked * 10000 / count
	report.TransportRateBP = transport * 10000 / count
	report.AvgBytes = totalBytes / int64(count)
	report.P50Latency = percentile(latencies, 50)
	report.P95Latency = percentile(latencies, 95)

	if h.Record {
		if err := writeReport(h.RecordPath, report); err != nil {
			return report, err
		}
	}
	return report, nil
}

// percentile returns the p-th percentile latency (nearest-rank).
func percentile(xs []time.Duration, p int) time.Duration {
	if len(xs) == 0 {
		return 0
	}
	sorted := append([]time.Duration(nil), xs...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	rank := (p * len(sorted)) / 100
	if rank >= len(sorted) {
		rank = len(sorted) - 1
	}
	return sorted[rank]
}

// writeReport persists a measurement report as JSON for the S35 baseline.
func writeReport(path string, report MeasurementReport) error {
	if path == "" {
		return fmt.Errorf("routec: -record set but no record path given")
	}
	b, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("routec: marshal measurement report: %w", err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return fmt.Errorf("routec: write measurement report: %w", err)
	}
	return nil
}
