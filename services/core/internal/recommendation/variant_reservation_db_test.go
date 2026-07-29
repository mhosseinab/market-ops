package recommendation_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mhosseinab/market-ops/services/core/internal/approval"
	"github.com/mhosseinab/market-ops/services/core/internal/recommendation"
	"github.com/mhosseinab/market-ops/services/core/internal/reservation"
)

// BULK-PROTOCOL DESIGN RECORD (a), wired end to end (issue #87, prior finding 2).
//
// The reservation is ACQUIRED BEFORE DISPATCH, on the SAME transaction that commits
// the authorization. These tests assert that at the seam that matters: two DIFFERENT
// approval cards on ONE owned variant — the sibling-offer / two-selection-lineages
// case — cannot both reach a durable execution intent.

// TestConfirmIndividual_SecondCardOnOneVariantCannotDispatch is the core of prior
// finding 2. Two independent recommendation lineages exist for one owned variant (two
// sibling offers each producing their own recommendation). The first confirmation
// authorizes and dispatches; the SECOND must fail closed BEFORE dispatch, leaving its
// card a live control that a resume can retry once the first write settles.
func TestConfirmIndividual_SecondCardOnOneVariantCannotDispatch(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	svc := recommendation.NewService(pool).SetExecutionDispatcher(realDispatcherFor(t, pool))
	account, variant := seedVariant(t, q)

	cardA := awaitingCard(t, svc, account, variant)
	cardB := awaitingCard(t, svc, account, variant)
	now := time.Now().UTC()

	if _, err := svc.ConfirmIndividual(ctx, cardA.ID, bindingOf(t, cardA), now, testActor()); err != nil {
		t.Fatalf("first confirmation: %v", err)
	}
	if n := countIntents(t, pool, cardA.ID); n != 1 {
		t.Fatalf("first card execution intents = %d; want 1", n)
	}

	_, err := svc.ConfirmIndividual(ctx, cardB.ID, bindingOf(t, cardB), now, testActor())
	if !errors.Is(err, reservation.ErrVariantReserved) {
		t.Fatalf("second card on the same variant: err=%v; want ErrVariantReserved (fail closed BEFORE dispatch)", err)
	}
	// Nothing half-committed: NO intent, and the card is still a live control.
	if n := countIntents(t, pool, cardB.ID); n != 0 {
		t.Fatalf("second card enqueued %d execution intents; want 0 — two cards must never write one variant", n)
	}
	if got := reloadState(t, svc, cardB.ID); got != approval.StateAwaitingConfirmation {
		t.Fatalf("second card state %q; want awaiting_confirmation (still a live control, resume-safe)", got)
	}
	// And the first card's authorization is untouched.
	if got := reloadState(t, svc, cardA.ID); got != approval.StateApproved {
		t.Fatalf("first card state %q; want approved", got)
	}
}

// TestConfirmBulkSelection_SiblingSelectionCannotDispatchForAReservedVariant is the
// same invariant through the BULK seam, in the exact shape the record names: two
// SEPARATE one-member selection lineages for sibling offers on ONE owned variant. The
// second selection's member is reported `failed` with an explicit reason — resume-safe,
// nothing half-committed — and enqueues no execution intent.
func TestConfirmBulkSelection_SiblingSelectionCannotDispatchForAReservedVariant(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	svc := recommendation.NewService(pool).SetExecutionDispatcher(realDispatcherFor(t, pool))
	account, variant := seedVariant(t, q)

	obsA := seedEvidenceOffer(t, pool, q, account, variant, "sibling-a")
	obsB := seedEvidenceOffer(t, pool, q, account, variant, "sibling-b")
	cardA := awaitingCardWithEvidence(t, svc, account, variant, obsA)
	cardB := awaitingCardWithEvidence(t, svc, account, variant, obsB)

	setA, err := svc.PreviewBulkSelection(ctx, account, uuid.Nil, "sibling-set-a", nil,
		[]recommendation.PreviewMemberInput{{VariantID: variant, RecommendationID: cardA.RecommendationID}})
	if err != nil {
		t.Fatalf("preview A: %v", err)
	}
	setB, err := svc.PreviewBulkSelection(ctx, account, uuid.Nil, "sibling-set-b", nil,
		[]recommendation.PreviewMemberInput{{VariantID: variant, RecommendationID: cardB.RecommendationID}})
	if err != nil {
		t.Fatalf("preview B: %v", err)
	}
	// The two selections carry DIFFERENT sealed offer identities — the whole point of
	// #87: sibling offers stay individually attributable all the way to the wire.
	if setA.Members[0].OfferIdentity == setB.Members[0].OfferIdentity {
		t.Fatalf("sibling selections collapsed to one offer identity: %q", setA.Members[0].OfferIdentity)
	}

	now := time.Now().UTC()
	outA, err := svc.ConfirmBulkSelection(ctx, account, setA.Set.LineageID, setA.Set.Version, now, testActor())
	if err != nil {
		t.Fatalf("confirm A: %v", err)
	}
	if outA.Items[0].State != recommendation.BulkItemAuthorized {
		t.Fatalf("selection A item %q; want authorized", outA.Items[0].State)
	}

	outB, err := svc.ConfirmBulkSelection(ctx, account, setB.Set.LineageID, setB.Set.Version, now, testActor())
	if err != nil {
		t.Fatalf("confirm B: %v", err)
	}
	if got := outB.Items[0].State; got != recommendation.BulkItemFailed {
		t.Fatalf("sibling selection item %q; want failed (resume-safe, nothing half-committed)", got)
	}
	if got := outB.Items[0].Reason; got != "variant_reservation_held" {
		t.Fatalf("sibling selection reason %q; want variant_reservation_held (an actionable, non-localized diagnostic)", got)
	}
	if n := countIntents(t, pool, cardB.ID); n != 0 {
		t.Fatalf("sibling selection enqueued %d execution intents; want 0 (prior finding 2)", n)
	}
	if got := reloadState(t, svc, cardB.ID); got != approval.StateAwaitingConfirmation {
		t.Fatalf("sibling member card %q; want awaiting_confirmation (still a live control)", got)
	}
	// No provenance was fabricated for a member this selection did not authorize.
	if n := countBindings(t, pool, setB.Set.ID); n != 0 {
		t.Fatalf("sibling selection wrote %d provenance rows without authorizing; want 0", n)
	}
}
