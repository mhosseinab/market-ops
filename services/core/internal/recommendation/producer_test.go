package recommendation

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mhosseinab/market-ops/services/core/internal/cost"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/money"
	"github.com/mhosseinab/market-ops/services/core/internal/policy"
)

// --- Test doubles -------------------------------------------------------------

// fakeSource yields a fixed slice of eligible events (and an optional error).
type fakeSource struct {
	events []EligibleEvent
	err    error
}

func (f fakeSource) Eligible(context.Context) ([]EligibleEvent, error) {
	return f.events, f.err
}

// fakeResolver maps an event id to a resolution outcome (input or error).
type fakeResolver struct {
	byEvent map[uuid.UUID]resolveOutcome
}

type resolveOutcome struct {
	in  AssembleInput
	err error
}

func (f fakeResolver) Resolve(_ context.Context, ev EligibleEvent) (AssembleInput, error) {
	o, ok := f.byEvent[ev.EventID]
	if !ok {
		return AssembleInput{}, ErrInputsUnavailable
	}
	return o.in, o.err
}

// fakeStore records produced versions per lineage and every ProduceVersion call.
type fakeStore struct {
	current    map[uuid.UUID]db.Recommendation // lineage -> current row
	produced   []producedRec
	cards      int
	produceErr error
}

type producedRec struct {
	lineage uuid.UUID
	rec     Recommendation
	card    bool
}

func newFakeStore() *fakeStore {
	return &fakeStore{current: map[uuid.UUID]db.Recommendation{}}
}

func (s *fakeStore) CurrentRecommendationForLineage(_ context.Context, lineage uuid.UUID) (db.Recommendation, bool, error) {
	row, ok := s.current[lineage]
	return row, ok, nil
}

func (s *fakeStore) ProduceVersion(_ context.Context, lineage, _ uuid.UUID, rec Recommendation) (db.Recommendation, bool, error) {
	if s.produceErr != nil {
		return db.Recommendation{}, false, s.produceErr
	}
	card := rec.Approvable()
	row := db.Recommendation{
		ID:             uuid.New(),
		LineageID:      lineage,
		ContextVersion: rec.binding.ContextVersion,
		Approvable:     rec.Approvable(),
	}
	s.current[lineage] = row
	s.produced = append(s.produced, producedRec{lineage: lineage, rec: rec, card: card})
	if card {
		s.cards++
	}
	return row, card, nil
}

// approvableInput builds an AssembleInput that clears every PRC-002 gate, so the
// assembled recommendation is Approvable and mints a card.
func approvableInput(t *testing.T, account, variant uuid.UUID, now time.Time) AssembleInput {
	t.Helper()
	price := mustMoney(t, 100000, "IRR", 0)
	proposed := mustMoney(t, 110000, "IRR", 0)
	contribution := mustMoney(t, 20000, "IRR", 0)
	return AssembleInput{
		AccountID:           account,
		VariantID:           variant,
		Objective:           policy.ObjectiveMaximizeContribution,
		CurrentPrice:        price,
		CurrentContribution: contribution,
		Policy: policy.Result{
			Proposed: &policy.Proposal{Price: proposed, Contribution: contribution},
		},
		Boundary:          policy.Boundary{Known: true, Min: price, Max: proposed},
		Evidence:          Evidence{Quality: "verified", AsOf: now.Add(-time.Minute)},
		IdentityConfirmed: true,
		BoundaryKnown:     true,
		PermissionGranted: true,
		Readiness:         cost.StateComplete,
		EvidenceQuality:   "verified",
		Now:               now,
		Expiry:            now.Add(time.Hour),
		ActionID:          uuid.New(),
		ParameterVersion:  1,
	}
}

// blockedInput builds an AssembleInput that trips blockers (no confirmed identity,
// incomplete cost), so the assembled recommendation is NOT approvable.
func blockedInput(t *testing.T, account, variant uuid.UUID, now time.Time) AssembleInput {
	t.Helper()
	price := mustMoney(t, 100000, "IRR", 0)
	return AssembleInput{
		AccountID:         account,
		VariantID:         variant,
		Objective:         policy.ObjectiveMaximizeContribution,
		CurrentPrice:      price,
		Evidence:          Evidence{Quality: "unavailable", AsOf: now.Add(-time.Minute)},
		IdentityConfirmed: false,
		BoundaryKnown:     false,
		PermissionGranted: false,
		Readiness:         cost.StateMissing,
		EvidenceQuality:   "unavailable",
		Now:               now,
	}
}

func mustMoney(t *testing.T, mantissa int64, cur string, exp int8) money.Money {
	t.Helper()
	m, err := money.New(mantissa, cur, exp)
	if err != nil {
		t.Fatalf("money.New: %v", err)
	}
	return m
}

func newTestProducer(src EventSource, res InputResolver, store Store, now time.Time) *Producer {
	return NewProducer(store, src, res, nil).WithClock(func() time.Time { return now })
}

// --- Acceptance tests ---------------------------------------------------------

// Each eligible production event creates the expected recommendation AND card
// (acceptance test 1) when its inputs are approvable.
func TestProducer_EligibleEventCreatesRecommendationAndCard(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	account, variant, event := uuid.New(), uuid.New(), uuid.New()
	src := fakeSource{events: []EligibleEvent{{EventID: event, AccountID: account, VariantID: variant, EvidenceVersion: 0}}}
	res := fakeResolver{byEvent: map[uuid.UUID]resolveOutcome{event: {in: approvableInput(t, account, variant, now)}}}
	store := newFakeStore()

	m, err := newTestProducer(src, res, store, now).RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if m.Produced != 1 || m.Blocked != 0 || m.Deduped != 0 {
		t.Fatalf("metrics = %+v, want Produced=1", m)
	}
	if len(store.produced) != 1 || !store.produced[0].card {
		t.Fatalf("expected one produced recommendation with a card, got %+v", store.produced)
	}
	// The card lineage is deterministic per event, so a re-run maps to the same lineage.
	if store.produced[0].lineage != lineageForEvent(event) {
		t.Fatalf("lineage = %v, want deterministic lineageForEvent", store.produced[0].lineage)
	}
}

// Blocked/incomplete inputs create NO control and retain exact blocker reasons
// (acceptance test 2).
func TestProducer_BlockedInputsPersistNoControl(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	account, variant, event := uuid.New(), uuid.New(), uuid.New()
	src := fakeSource{events: []EligibleEvent{{EventID: event, AccountID: account, VariantID: variant}}}
	res := fakeResolver{byEvent: map[uuid.UUID]resolveOutcome{event: {in: blockedInput(t, account, variant, now)}}}
	store := newFakeStore()

	m, err := newTestProducer(src, res, store, now).RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if m.Blocked != 1 || m.Produced != 0 {
		t.Fatalf("metrics = %+v, want Blocked=1", m)
	}
	if store.cards != 0 {
		t.Fatalf("blocked recommendation must mint NO card, got %d", store.cards)
	}
	if len(store.produced) != 1 {
		t.Fatalf("blocked recommendation must still be persisted, got %d", len(store.produced))
	}
	if len(store.produced[0].rec.Blockers) == 0 {
		t.Fatal("persisted blocked recommendation must retain its blocker reasons")
	}
	if store.produced[0].rec.Approvable() {
		t.Fatal("blocked recommendation must not be approvable")
	}
}

// Replayed input creates no duplicate version (acceptance test 3): a second pass over
// the same event at the same evidence version persists nothing new.
func TestProducer_ReplayCreatesNoDuplicateVersion(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	account, variant, event := uuid.New(), uuid.New(), uuid.New()
	src := fakeSource{events: []EligibleEvent{{EventID: event, AccountID: account, VariantID: variant, EvidenceVersion: 3}}}
	res := fakeResolver{byEvent: map[uuid.UUID]resolveOutcome{event: {in: approvableInput(t, account, variant, now)}}}
	store := newFakeStore()
	p := newTestProducer(src, res, store, now)

	if _, err := p.RunOnce(context.Background()); err != nil {
		t.Fatalf("first RunOnce: %v", err)
	}
	m, err := p.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("second RunOnce: %v", err)
	}
	if m.Deduped != 1 || m.Produced != 0 {
		t.Fatalf("replay metrics = %+v, want Deduped=1 Produced=0", m)
	}
	if len(store.produced) != 1 {
		t.Fatalf("replay must not persist a new version, produced=%d", len(store.produced))
	}
}

// A newer evidence version produces a new recommendation version (not deduped).
func TestProducer_NewerEvidenceVersionProducesNewVersion(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	account, variant, event := uuid.New(), uuid.New(), uuid.New()
	res := fakeResolver{byEvent: map[uuid.UUID]resolveOutcome{event: {in: approvableInput(t, account, variant, now)}}}
	store := newFakeStore()

	p1 := newTestProducer(fakeSource{events: []EligibleEvent{{EventID: event, AccountID: account, VariantID: variant, EvidenceVersion: 1}}}, res, store, now)
	if _, err := p1.RunOnce(context.Background()); err != nil {
		t.Fatalf("first RunOnce: %v", err)
	}
	p2 := newTestProducer(fakeSource{events: []EligibleEvent{{EventID: event, AccountID: account, VariantID: variant, EvidenceVersion: 2}}}, res, store, now)
	m, err := p2.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("second RunOnce: %v", err)
	}
	if m.Produced != 1 || m.Deduped != 0 {
		t.Fatalf("metrics = %+v, want Produced=1 for newer evidence version", m)
	}
	if len(store.produced) != 2 {
		t.Fatalf("newer evidence version must persist a new version, produced=%d", len(store.produced))
	}
}

// The dark resolver fails closed: an event whose authoritative inputs are unavailable
// is PARKED (no recommendation, no fabrication).
func TestProducer_DarkResolverParksWithoutFabricating(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	event := uuid.New()
	src := fakeSource{events: []EligibleEvent{{EventID: event, AccountID: uuid.New(), VariantID: uuid.New()}}}
	// Empty resolver -> ErrInputsUnavailable for every event.
	res := fakeResolver{byEvent: map[uuid.UUID]resolveOutcome{}}
	store := newFakeStore()

	m, err := newTestProducer(src, res, store, now).RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if m.Parked != 1 || m.Produced != 0 || m.Blocked != 0 {
		t.Fatalf("metrics = %+v, want Parked=1", m)
	}
	if len(store.produced) != 0 {
		t.Fatal("dark posture must persist nothing")
	}
}

// A resolver error is surfaced for River retry (so producer failure survives restart
// and is retried durably — acceptance test 4), and does not abort the other events.
func TestProducer_ResolverErrorSurfacedForRetry(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	account, variant := uuid.New(), uuid.New()
	bad, good := uuid.New(), uuid.New()
	src := fakeSource{events: []EligibleEvent{
		{EventID: bad, AccountID: account, VariantID: variant},
		{EventID: good, AccountID: account, VariantID: variant},
	}}
	res := fakeResolver{byEvent: map[uuid.UUID]resolveOutcome{
		bad:  {err: errors.New("boom")},
		good: {in: approvableInput(t, account, variant, now)},
	}}
	store := newFakeStore()

	m, err := newTestProducer(src, res, store, now).RunOnce(context.Background())
	if err == nil {
		t.Fatal("expected RunOnce to surface the resolver error for retry")
	}
	if m.Errors != 1 {
		t.Fatalf("metrics = %+v, want Errors=1", m)
	}
	if m.Produced != 1 {
		t.Fatalf("the healthy event must still be produced, metrics = %+v", m)
	}
}

// A Source failure aborts the pass and is surfaced for retry.
func TestProducer_SourceErrorSurfaced(t *testing.T) {
	src := fakeSource{err: errors.New("db down")}
	store := newFakeStore()
	_, err := newTestProducer(src, fakeResolver{}, store, time.Now()).RunOnce(context.Background())
	if err == nil {
		t.Fatal("expected source error to surface")
	}
	if len(store.produced) != 0 {
		t.Fatal("no production on a source failure")
	}
}
