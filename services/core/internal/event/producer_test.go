package event_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mhosseinab/market-ops/services/core/internal/cost"
	"github.com/mhosseinab/market-ops/services/core/internal/event"
	"github.com/mhosseinab/market-ops/services/core/internal/money"
)

// fakeRecorder is an in-memory event.Recorder: it resolves a per-type threshold
// from a map and records candidates, deduping on dedup key (mirroring the real
// service's EVT-003 behaviour) so the producer's idempotency can be tested without
// a database.
type fakeRecorder struct {
	thresholds map[event.Type]event.Threshold
	seen       map[string]bool
	recorded   []event.Candidate
	recErr     error
	thrErr     error
}

func newFakeRecorder() *fakeRecorder {
	return &fakeRecorder{
		thresholds: map[event.Type]event.Threshold{},
		seen:       map[string]bool{},
	}
}

func (f *fakeRecorder) ThresholdAsOf(_ context.Context, _ uuid.UUID, _ string, t event.Type, _ time.Time) (event.Threshold, error) {
	if f.thrErr != nil {
		return event.Threshold{}, f.thrErr
	}
	return f.thresholds[t], nil
}

func (f *fakeRecorder) RecordFor(_ context.Context, _ uuid.UUID, c event.Candidate) (event.RecordResult, error) {
	if f.recErr != nil {
		return event.RecordResult{}, f.recErr
	}
	f.recorded = append(f.recorded, c)
	deduped := f.seen[c.DedupKey]
	f.seen[c.DedupKey] = true
	return event.RecordResult{Deduped: deduped}, nil
}

// fiveTransitions returns one materialising transition per detector type, all for
// the same account/variant so a full pass exercises every detector routing.
func fiveTransitions(t *testing.T, account, variant uuid.UUID, now time.Time) []event.Transition {
	t.Helper()
	ev := event.Evidence{Quality: event.QualitySupported, Ref: "r"}
	return []event.Transition{
		{Account: account, Category: "*", Type: event.TypeWinningState, WinningState: &event.WinningStateInput{
			Variant: variant, WasWinning: true, IsWinning: false,
			Exposure: event.UnknownExposure(), Evidence: ev, Now: now, TTL: time.Hour,
		}},
		{Account: account, Category: "*", Type: event.TypeCompetitorPrice, CompetitorPrice: &event.CompetitorPriceInput{
			Variant: variant, OfferIdentity: "seller-1", Unit: "IRR",
			PrevValue: "1000000", CurrValue: "1200000",
			Exposure: event.UnknownExposure(), Evidence: ev, Now: now, TTL: time.Hour,
		}},
		{Account: account, Category: "*", Type: event.TypeSellerCount, SellerCount: &event.SellerCountInput{
			Variant: variant, PrevCount: 2, CurrCount: 5,
			Exposure: event.UnknownExposure(), Evidence: ev, Now: now, TTL: time.Hour,
		}},
		{Account: account, Category: "*", Type: event.TypeSuppressionBoundary, SuppressionBoundary: &event.SuppressionBoundaryInput{
			Variant: variant, WasSuppressed: false, IsSuppressed: true,
			Exposure: event.UnknownExposure(), Evidence: ev, Now: now, TTL: time.Hour,
		}},
		{Account: account, Category: "*", Type: event.TypeContributionFloor, ContributionFloor: &event.ContributionFloorInput{
			Variant: variant, Readiness: cost.StateComplete, HasContribution: true,
			Contribution: irr(t, 60), Floor: irr(t, 100),
			Evidence: ev, Now: now, TTL: time.Hour,
		}},
	}
}

// staticSource returns a fixed transition set (a test double for the DB-backed
// ObservationSource) so the producer can be driven without a database.
type staticSource struct {
	transitions []event.Transition
	err         error
}

func (s staticSource) Transitions(context.Context) ([]event.Transition, error) {
	return s.transitions, s.err
}

// TestProducerRunsEveryDetector proves each of the five production input
// transitions creates an event candidate THROUGH the running producer (not a
// direct RecordFor call) — EVT-001 acceptance test "all five types produce".
func TestProducerRunsEveryDetector(t *testing.T) {
	account, variant := uuid.New(), uuid.New()
	now := time.Now().UTC()
	rec := newFakeRecorder()
	// A competitor-price / seller-count threshold must be in force for those
	// detectors to fire (EVT-002).
	rec.thresholds[event.TypeCompetitorPrice] = event.Threshold{ID: uuid.New(), Version: 1, MoveBp: money.NewBasisPoints(1000)}
	rec.thresholds[event.TypeSellerCount] = event.Threshold{ID: uuid.New(), Version: 1, SellerCountDelta: 2}

	p := event.NewProducer(rec, staticSource{transitions: fiveTransitions(t, account, variant, now)}, nil)
	m, err := p.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if m.Produced != 5 {
		t.Fatalf("want 5 produced (one per detector), got %d (scanned=%d dormant=%d errors=%d): %+v",
			m.Produced, m.Scanned, m.Dormant, m.Errors, recordedTypes(rec))
	}
	// Every distinct type was recorded exactly once.
	got := map[event.Type]int{}
	for _, c := range rec.recorded {
		got[c.Type]++
	}
	for _, ty := range []event.Type{
		event.TypeWinningState, event.TypeCompetitorPrice, event.TypeSellerCount,
		event.TypeSuppressionBoundary, event.TypeContributionFloor,
	} {
		if got[ty] != 1 {
			t.Errorf("type %s recorded %d times, want 1", ty, got[ty])
		}
	}
}

func recordedTypes(f *fakeRecorder) []event.Type {
	out := make([]event.Type, 0, len(f.recorded))
	for _, c := range f.recorded {
		out = append(out, c.Type)
	}
	return out
}

// TestProducerReplayProducesNoDuplicate is the EVT-003/§16 never-cut idempotency
// test: running the SAME committed input twice records the candidate but the
// second pass DEDUPS — zero new Today items.
func TestProducerReplayProducesNoDuplicate(t *testing.T) {
	account, variant := uuid.New(), uuid.New()
	now := time.Now().UTC()
	rec := newFakeRecorder()
	src := staticSource{transitions: []event.Transition{
		{Account: account, Category: "*", Type: event.TypeWinningState, WinningState: &event.WinningStateInput{
			Variant: variant, WasWinning: true, IsWinning: false,
			Exposure: event.UnknownExposure(),
			Evidence: event.Evidence{Quality: event.QualitySupported, Ref: "r"},
			Now:      now, TTL: time.Hour,
		}},
	}}
	p := event.NewProducer(rec, src, nil)

	first, err := p.RunOnce(context.Background())
	if err != nil || first.Produced != 1 || first.Deduped != 0 {
		t.Fatalf("first pass: produced=%d deduped=%d err=%v", first.Produced, first.Deduped, err)
	}
	second, err := p.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if second.Produced != 0 || second.Deduped != 1 {
		t.Fatalf("replay must DEDUP (0 produced, 1 deduped), got produced=%d deduped=%d", second.Produced, second.Deduped)
	}
}

// TestProducerNonMaterialIsDormant proves a below-threshold / non-transition input
// records nothing (dormant), never a fabricated event.
func TestProducerNonMaterialIsDormant(t *testing.T) {
	account, variant := uuid.New(), uuid.New()
	now := time.Now().UTC()
	rec := newFakeRecorder()
	rec.thresholds[event.TypeCompetitorPrice] = event.Threshold{ID: uuid.New(), Version: 1, MoveBp: money.NewBasisPoints(5000)}
	src := staticSource{transitions: []event.Transition{
		// A 2% move against a 50% threshold: not material.
		{Account: account, Category: "*", Type: event.TypeCompetitorPrice, CompetitorPrice: &event.CompetitorPriceInput{
			Variant: variant, OfferIdentity: "s", Unit: "IRR",
			PrevValue: "1000000", CurrValue: "1020000",
			Exposure: event.UnknownExposure(),
			Evidence: event.Evidence{Quality: event.QualitySupported, Ref: "r"},
			Now:      now, TTL: time.Hour,
		}},
	}}
	m, err := event.NewProducer(rec, src, nil).RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if m.Produced != 0 || m.Dormant != 1 {
		t.Fatalf("non-material input must be dormant, got produced=%d dormant=%d", m.Produced, m.Dormant)
	}
	if len(rec.recorded) != 0 {
		t.Fatalf("dormant input must record nothing, recorded %d", len(rec.recorded))
	}
}

// TestProducerContributionFloorDormantWithoutReadiness proves the floor detector
// stays dormant unless cost readiness is Complete — it never fabricates a floor
// breach (EVT-001 / EVT-005).
func TestProducerContributionFloorDormantWithoutReadiness(t *testing.T) {
	account, variant := uuid.New(), uuid.New()
	now := time.Now().UTC()
	rec := newFakeRecorder()
	src := staticSource{transitions: []event.Transition{
		{Account: account, Category: "*", Type: event.TypeContributionFloor, ContributionFloor: &event.ContributionFloorInput{
			Variant: variant, Readiness: cost.StatePartial, HasContribution: true,
			Contribution: irr(t, 60), Floor: irr(t, 100),
			Evidence: event.Evidence{Quality: event.QualityVerified, Ref: "r"},
			Now:      now, TTL: time.Hour,
		}},
	}}
	m, err := event.NewProducer(rec, src, nil).RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if m.Produced != 0 || m.Dormant != 1 || len(rec.recorded) != 0 {
		t.Fatalf("floor must stay dormant when readiness != Complete: produced=%d dormant=%d recorded=%d",
			m.Produced, m.Dormant, len(rec.recorded))
	}
}

// TestProducerSurfacesRecordError proves a record failure is surfaced (returned to
// River for retry), counted, and does not abort the remaining transitions.
func TestProducerSurfacesRecordError(t *testing.T) {
	account, variant := uuid.New(), uuid.New()
	now := time.Now().UTC()
	rec := newFakeRecorder()
	rec.recErr = errors.New("db down")
	src := staticSource{transitions: []event.Transition{
		{Account: account, Category: "*", Type: event.TypeWinningState, WinningState: &event.WinningStateInput{
			Variant: variant, WasWinning: true, IsWinning: false,
			Exposure: event.UnknownExposure(),
			Evidence: event.Evidence{Quality: event.QualitySupported, Ref: "r"},
			Now:      now, TTL: time.Hour,
		}},
	}}
	m, err := event.NewProducer(rec, src, nil).RunOnce(context.Background())
	if err == nil {
		t.Fatal("a record failure must be surfaced for retry, got nil error")
	}
	if m.Errors != 1 {
		t.Fatalf("errors metric = %d, want 1", m.Errors)
	}
}

// TestProducerSurfacesSourceError proves a source failure is surfaced (River retry)
// rather than swallowed into an empty pass.
func TestProducerSurfacesSourceError(t *testing.T) {
	src := staticSource{err: errors.New("source unavailable")}
	if _, err := event.NewProducer(newFakeRecorder(), src, nil).RunOnce(context.Background()); err == nil {
		t.Fatal("a source failure must be surfaced, got nil error")
	}
}

// TestProducerEmitsObservabilityFields proves the producer logs a structured
// summary with the agreed field schema shared by tests and prod telemetry.
func TestProducerEmitsObservabilityFields(t *testing.T) {
	account, variant := uuid.New(), uuid.New()
	now := time.Now().UTC()
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	rec := newFakeRecorder()
	src := staticSource{transitions: []event.Transition{
		{Account: account, Category: "*", Type: event.TypeWinningState, WinningState: &event.WinningStateInput{
			Variant: variant, WasWinning: true, IsWinning: false,
			Exposure: event.UnknownExposure(),
			Evidence: event.Evidence{Quality: event.QualitySupported, Ref: "r"},
			Now:      now, TTL: time.Hour,
		}},
	}}
	if _, err := event.NewProducer(rec, src, logger).RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	var found bool
	for _, line := range bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n")) {
		var m map[string]any
		if json.Unmarshal(line, &m) != nil {
			continue
		}
		if _, ok := m["produced"]; !ok {
			continue
		}
		found = true
		for _, k := range []string{"scanned", "produced", "deduped", "dormant", "errors"} {
			if _, ok := m[k]; !ok {
				t.Errorf("summary log missing field %q", k)
			}
		}
	}
	if !found {
		t.Fatalf("no producer summary log line emitted; got: %s", buf.String())
	}
}
