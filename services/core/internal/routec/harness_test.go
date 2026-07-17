package routec_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mhosseinab/market-ops/services/core/internal/routec"
)

// scriptedFetcher returns results from a fixed sequence, cycling. It generates NO
// live traffic — the harness never opens a connection itself.
type scriptedFetcher struct {
	seq []routec.FetchResult
	i   int
}

func (s *scriptedFetcher) Fetch(context.Context, routec.FetchRequest) (routec.FetchResult, error) {
	r := s.seq[s.i%len(s.seq)]
	s.i++
	return r, nil
}

// TestHarnessMeasuresBlockRateAndThroughput asserts the harness aggregates block
// rate, byte cost, and throughput from a scripted fetcher (offline).
func TestHarnessMeasuresBlockRateAndThroughput(t *testing.T) {
	f := &scriptedFetcher{seq: []routec.FetchResult{
		{Signal: routec.SignalOK, Bytes: 1000, Latency: time.Millisecond},
		{Signal: routec.SignalOK, Bytes: 1000, Latency: 2 * time.Millisecond},
		{Signal: routec.Signal429, Bytes: 50, Latency: time.Millisecond},
		{Signal: routec.SignalChallenge, Bytes: 40, Latency: time.Millisecond},
	}}
	h := routec.NewHarness(f, 2, 4)
	rep, err := h.Run(context.Background(), routec.FetchRequest{URL: "http://fixture", Account: uuid.New()}, 8)
	if err != nil {
		t.Fatalf("harness run: %v", err)
	}
	if rep.Requests != 8 {
		t.Fatalf("requests: got %d want 8", rep.Requests)
	}
	// 2 of every 4 are blocked (429 + challenge) => 5000 bp block rate.
	if rep.BlockRateBP != 5000 {
		t.Fatalf("block rate: got %d bp want 5000", rep.BlockRateBP)
	}
	if rep.TotalBytes != (1000+1000+50+40)*2 {
		t.Fatalf("total bytes: got %d", rep.TotalBytes)
	}
	if rep.ThroughputMilliRPS <= 0 {
		t.Fatalf("throughput must be positive, got %d", rep.ThroughputMilliRPS)
	}
}

// TestHarnessRecordMode asserts -record writes the measurement report file for
// the S35 baseline (still offline — the fetcher is scripted).
func TestHarnessRecordMode(t *testing.T) {
	f := &scriptedFetcher{seq: []routec.FetchResult{{Signal: routec.SignalOK, Bytes: 500, Latency: time.Millisecond}}}
	path := filepath.Join(t.TempDir(), "s35_baseline.json")
	h := routec.NewHarness(f, 1, 1)
	h.Record = true
	h.RecordPath = path
	if _, err := h.Run(context.Background(), routec.FetchRequest{URL: "http://fixture", Account: uuid.New()}, 5); err != nil {
		t.Fatalf("harness run: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("record file not written: %v", err)
	}
	var rep routec.MeasurementReport
	if err := json.Unmarshal(b, &rep); err != nil {
		t.Fatalf("record file not valid json: %v", err)
	}
	if rep.Requests != 5 {
		t.Fatalf("recorded requests: got %d want 5", rep.Requests)
	}
}
