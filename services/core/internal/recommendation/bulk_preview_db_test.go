package recommendation_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/mhosseinab/market-ops/services/core/internal/money"
	"github.com/mhosseinab/market-ops/services/core/internal/recommendation"
)

// TestPreviewBulkSelection_ServerMintsVersion_NeverClientSupplied is the S37
// hard safety precondition (PD-3 item 4): the selection-set VERSION is minted
// ENTIRELY server-side. This test proves it by construction — the request
// input to PreviewBulkSelection carries no version at all — and by behavior: a
// second preview call against the SAME lineage mints a STRICTLY GREATER
// version, with no way for the caller to influence the number.
func TestPreviewBulkSelectionServerMintsVersionNeverClientSupplied(t *testing.T) {
	pool, q := newPool(t)
	account, variant := seedVariant(t, q)
	svc := recommendation.NewService(pool)
	recID := persistRecommendation(t, svc, account, variant)

	members := []recommendation.PreviewMemberInput{{VariantID: variant, RecommendationID: recID}}

	first, err := svc.PreviewBulkSelection(context.Background(), account, uuid.Nil, "bulk-test", nil, members)
	if err != nil {
		t.Fatalf("first preview: %v", err)
	}
	if first.Set.Version != 1 {
		t.Fatalf("first preview version = %d, want 1 (server-minted, first in a fresh lineage)", first.Set.Version)
	}
	if first.Set.LineageID == uuid.Nil {
		t.Fatal("PreviewBulkSelection must mint a lineage id when none is supplied")
	}

	// A second preview against the SAME lineage mints the NEXT version — the
	// server, never the caller, advances it.
	second, err := svc.PreviewBulkSelection(context.Background(), account, first.Set.LineageID, "bulk-test-refresh", nil, members)
	if err != nil {
		t.Fatalf("second preview: %v", err)
	}
	if second.Set.LineageID != first.Set.LineageID {
		t.Fatalf("second preview lineage = %s, want same lineage %s", second.Set.LineageID, first.Set.LineageID)
	}
	if second.Set.Version != first.Set.Version+1 {
		t.Fatalf("second preview version = %d, want %d (server-minted next version)", second.Set.Version, first.Set.Version+1)
	}

	// A stale bound version (the first preview's) is no longer current — the
	// EXACT invariant confirmBulkApproval depends on (CHAT-051/052).
	valid, err := svc.BulkPreviewValid(context.Background(), first.Set.LineageID, first.Set.Version)
	if err != nil {
		t.Fatalf("BulkPreviewValid: %v", err)
	}
	if valid {
		t.Fatal("the FIRST preview's version must be invalidated once a second version was minted")
	}
	current, err := svc.CurrentSelectionSetVersion(context.Background(), first.Set.LineageID)
	if err != nil {
		t.Fatalf("CurrentSelectionSetVersion: %v", err)
	}
	if current != second.Set.Version {
		t.Fatalf("current version = %d, want %d (the second, server-minted preview)", current, second.Set.Version)
	}
}

// TestPreviewBulkSelection_ResolvesDispositionServerSide proves the member
// disposition is resolved from the NAMED recommendation's own persisted state
// — never taken as a client-asserted value (there is no disposition field on
// PreviewMemberInput at all).
func TestPreviewBulkSelectionResolvesDispositionServerSide(t *testing.T) {
	pool, q := newPool(t)
	account, variant := seedVariant(t, q)
	svc := recommendation.NewService(pool)
	recID := persistRecommendation(t, svc, account, variant)

	result, err := svc.PreviewBulkSelection(context.Background(), account, uuid.Nil, "bulk-disposition",
		nil, []recommendation.PreviewMemberInput{{VariantID: variant, RecommendationID: recID}})
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if len(result.Members) != 1 {
		t.Fatalf("members = %d, want 1", len(result.Members))
	}
	if result.Members[0].Disposition != recommendation.DispositionExecutable {
		t.Fatalf("disposition = %s, want executable (the seeded recommendation is approvable)", result.Members[0].Disposition)
	}
	if result.AggregateImpact == nil {
		t.Fatal("aggregate impact should be known (the recommendation has a proposed contribution)")
	}
}

// TestPreviewBulkSelection_AggregateImpactUnknownOnCurrencyMismatch is the
// money-correctness / quarantine-over-inference fix: when two members'
// proposed contributions cannot be summed (a currency mismatch), the WHOLE
// aggregate must flip to unknown/unavailable — NEVER an understated partial
// sum silently presented as complete to a bulk-approving Owner/Operator.
func TestPreviewBulkSelectionAggregateImpactUnknownOnCurrencyMismatch(t *testing.T) {
	pool, q := newPool(t)
	account, variantA := seedVariant(t, q)
	variantB := seedSecondVariant(t, q, account)
	svc := recommendation.NewService(pool)

	irrContribution, err := money.New(300, "IRR", 0)
	if err != nil {
		t.Fatalf("money.New(IRR): %v", err)
	}
	usdContribution, err := money.New(300, "USD", 0)
	if err != nil {
		t.Fatalf("money.New(USD): %v", err)
	}
	recA := persistRecommendationWithContribution(t, svc, account, variantA, irrContribution)
	recB := persistRecommendationWithContribution(t, svc, account, variantB, usdContribution)

	result, err := svc.PreviewBulkSelection(context.Background(), account, uuid.Nil, "bulk-mismatch",
		nil, []recommendation.PreviewMemberInput{
			{VariantID: variantA, RecommendationID: recA},
			{VariantID: variantB, RecommendationID: recB},
		})
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if len(result.Members) != 2 {
		t.Fatalf("members = %d, want 2 (the mismatch must not drop a member from the SET, only the aggregate)", len(result.Members))
	}
	if result.AggregateImpact != nil {
		t.Fatalf("AggregateImpact = %+v, want nil/unknown — a currency mismatch must never yield a silent partial sum", *result.AggregateImpact)
	}
}

// TestPreviewBulkSelection_AggregateUnavailableWhenMemberContributionMissing is
// the #141 end-to-end proof: one member with UNAVAILABLE contribution evidence
// makes the WHOLE aggregate unavailable (never a partial known total), and the
// operator-facing preview response and the persisted, immutable selection-set
// version bind the IDENTICAL aggregate + fingerprint. Deferred to CI (needs a
// database).
func TestPreviewBulkSelectionAggregateUnavailableWhenMemberContributionMissing(t *testing.T) {
	pool, q := newPool(t)
	account, variantA := seedVariant(t, q)
	variantB := seedSecondVariant(t, q, account)
	svc := recommendation.NewService(pool)

	available, err := money.New(300, "IRR", 0)
	if err != nil {
		t.Fatalf("money.New(IRR): %v", err)
	}
	recA := persistRecommendationWithContribution(t, svc, account, variantA, available)
	recB := persistRecommendationUnavailableContribution(t, svc, account, variantB)

	result, err := svc.PreviewBulkSelection(context.Background(), account, uuid.Nil, "bulk-missing-contribution",
		nil, []recommendation.PreviewMemberInput{
			{VariantID: variantA, RecommendationID: recA},
			{VariantID: variantB, RecommendationID: recB},
		})
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if len(result.Members) != 2 {
		t.Fatalf("members = %d, want 2 (a missing contribution must not drop a member from the SET, only the aggregate)", len(result.Members))
	}
	if result.AggregateImpact != nil {
		t.Fatalf("AggregateImpact = %+v, want nil/unknown — one member without contribution evidence must never yield a partial known total of 300", *result.AggregateImpact)
	}

	// Response and the persisted, sealed version must bind the SAME aggregate state
	// and the SAME membership fingerprint — they can never disagree (#141).
	persisted, err := svc.GetSelectionSet(context.Background(), result.Set.ID)
	if err != nil {
		t.Fatalf("get persisted selection set: %v", err)
	}
	if persisted.AggregateImpactKnown {
		t.Fatalf("persisted AggregateImpactKnown = true, want false — the stored version must record the SAME unavailable aggregate the response reports")
	}
	if !bytes.Equal(persisted.MembershipFingerprint, recommendation.MembershipFingerprint(result.Members, result.AggregateImpact)) {
		t.Fatal("persisted membership fingerprint differs from the response's (membership+aggregate) fingerprint — confirmation would bind a version that disagrees with the preview")
	}
}

// TestPreviewBulkSelection_RejectsUnknownOrForeignMember proves a member naming
// a recommendation that does not exist, or that belongs to a different
// account/variant, is refused — never a fabricated member (fail closed).
func TestPreviewBulkSelectionRejectsUnknownOrForeignMember(t *testing.T) {
	pool, q := newPool(t)
	account, variant := seedVariant(t, q)
	svc := recommendation.NewService(pool)

	// Unknown recommendation id.
	_, err := svc.PreviewBulkSelection(context.Background(), account, uuid.Nil, "bulk-unknown",
		nil, []recommendation.PreviewMemberInput{{VariantID: variant, RecommendationID: uuid.New()}})
	if err != recommendation.ErrUnknownMember {
		t.Fatalf("err = %v, want ErrUnknownMember (unknown recommendation)", err)
	}

	// Real recommendation, but a foreign variant id in the request.
	recID := persistRecommendation(t, svc, account, variant)
	_, err = svc.PreviewBulkSelection(context.Background(), account, uuid.Nil, "bulk-mismatch",
		nil, []recommendation.PreviewMemberInput{{VariantID: uuid.New(), RecommendationID: recID}})
	if err != recommendation.ErrUnknownMember {
		t.Fatalf("err = %v, want ErrUnknownMember (variant mismatch)", err)
	}
}
