package recommendation_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/recommendation"
)

// stubEventSource yields a fixed set of eligible events (the real EventSource is a thin
// DB read exercised separately; here we drive the producer's persistence path).
type stubEventSource struct {
	events []recommendation.EligibleEvent
}

func (s stubEventSource) Eligible(context.Context) ([]recommendation.EligibleEvent, error) {
	return s.events, nil
}

// stubResolver returns a fixed approvable input for every event.
type stubResolver struct{ in recommendation.AssembleInput }

func (r stubResolver) Resolve(context.Context, recommendation.EligibleEvent) (recommendation.AssembleInput, error) {
	return r.in, nil
}

// seedOpenEvent inserts one open market event for the variant and returns its id.
func seedOpenEvent(t *testing.T, q *db.Queries, account, variant uuid.UUID) uuid.UUID {
	t.Helper()
	now := time.Now().UTC()
	ev, err := q.RecordEvent(context.Background(), db.RecordEventParams{
		MarketplaceAccountID: account,
		VariantID:            variant,
		EventType:            "winning_state",
		Severity:             "warning",
		DedupKey:             "rec-producer-" + uuid.NewString(),
		ConfidenceBp:         5000,
		UrgencyBp:            5000,
		EvidenceQuality:      "verified",
		EvidenceDetail:       []byte("{}"),
		FirstDetectedAt:      now,
		ExpiresAt:            now.Add(24 * time.Hour),
	})
	if err != nil {
		t.Fatalf("record event: %v", err)
	}
	return ev.ID
}

// The real EventSource reads open|updated events account-wide, and the producer then
// persists an approvable recommendation + its Draft card. A replay pass at the same
// evidence version creates NO duplicate version (idempotency, never-cut).
func TestProducer_DB_ProducesCardAndDedupesReplay(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account, variant := seedVariant(t, q)
	eventID := seedOpenEvent(t, q, account, variant)

	in := baseValidInput(t)
	in.AccountID = account
	in.VariantID = variant

	svc := recommendation.NewService(pool)
	src := stubEventSource{events: []recommendation.EligibleEvent{
		{EventID: eventID, AccountID: account, VariantID: variant, EvidenceVersion: 0},
	}}
	producer := recommendation.NewProducer(svc, src, stubResolver{in: in}, nil)

	// First pass: one approvable recommendation + one Draft card.
	m, err := producer.RunOnce(ctx)
	if err != nil {
		t.Fatalf("first RunOnce: %v", err)
	}
	if m.Produced != 1 {
		t.Fatalf("metrics = %+v, want Produced=1", m)
	}
	recs, err := q.ListRecommendationsForVariant(ctx, db.ListRecommendationsForVariantParams{
		MarketplaceAccountID: account, VariantID: variant,
	})
	if err != nil {
		t.Fatalf("list recommendations: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("want 1 recommendation, got %d", len(recs))
	}
	if !recs[0].Approvable {
		t.Fatal("produced recommendation should be approvable")
	}
	if !recs[0].EventID.Valid || recs[0].EventID.Bytes != eventID {
		t.Fatal("recommendation should cite the driving event id")
	}
	cards, err := q.ListLiveCardsForVariant(ctx, variant)
	if err != nil {
		t.Fatalf("list cards: %v", err)
	}
	if len(cards) != 1 || cards[0].State != "draft" {
		t.Fatalf("want 1 draft card, got %+v", cards)
	}

	// Second pass over the same event at the same evidence version: deduped, no new
	// version and no new card.
	m2, err := producer.RunOnce(ctx)
	if err != nil {
		t.Fatalf("replay RunOnce: %v", err)
	}
	if m2.Deduped != 1 || m2.Produced != 0 {
		t.Fatalf("replay metrics = %+v, want Deduped=1", m2)
	}
	recs2, err := q.ListRecommendationsForVariant(ctx, db.ListRecommendationsForVariantParams{
		MarketplaceAccountID: account, VariantID: variant,
	})
	if err != nil {
		t.Fatalf("list recommendations (replay): %v", err)
	}
	if len(recs2) != 1 {
		t.Fatalf("replay must not create a duplicate version, got %d", len(recs2))
	}
}

// The real DB-backed EventSource returns the seeded open event account-wide.
func TestProducer_DB_EventSourceListsOpenEvents(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account, variant := seedVariant(t, q)
	eventID := seedOpenEvent(t, q, account, variant)

	events, err := recommendation.NewEventSource(pool).Eligible(ctx)
	if err != nil {
		t.Fatalf("eligible: %v", err)
	}
	found := false
	for _, e := range events {
		if e.EventID == eventID {
			found = true
			if e.AccountID != account || e.VariantID != variant {
				t.Fatalf("event fields mismatch: %+v", e)
			}
		}
	}
	if !found {
		t.Fatalf("seeded open event %s not returned by the source", eventID)
	}
}
