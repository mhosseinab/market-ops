package recommendation_test

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/money"
	"github.com/mhosseinab/market-ops/services/core/internal/recommendation"
)

// persistRecommendation persists a fresh recommendation for account/variant and
// returns its persisted id. Reuses baseValidInput (recommendation_test.go) so
// the recommendation is approvable, with a proposed contribution available.
func persistRecommendation(t *testing.T, svc *recommendation.Service, account, variant uuid.UUID) uuid.UUID {
	t.Helper()
	in := baseValidInput(t)
	in.AccountID = account
	in.VariantID = variant
	in.EventID = uuid.Nil
	rec := recommendation.Assemble(in)
	row, err := svc.Persist(context.Background(), uuid.New(), rec)
	if err != nil {
		t.Fatalf("persist recommendation: %v", err)
	}
	return row.ID
}

// persistRecommendationWithContribution is persistRecommendation but with a
// caller-chosen proposed contribution — used to construct a cross-currency
// aggregate-impact mismatch across two members of one bulk preview.
func persistRecommendationWithContribution(t *testing.T, svc *recommendation.Service, account, variant uuid.UUID, contribution money.Money) uuid.UUID {
	t.Helper()
	in := baseValidInput(t)
	in.AccountID = account
	in.VariantID = variant
	in.EventID = uuid.Nil
	in.Policy.Proposed.Contribution = contribution
	rec := recommendation.Assemble(in)
	row, err := svc.Persist(context.Background(), uuid.New(), rec)
	if err != nil {
		t.Fatalf("persist recommendation: %v", err)
	}
	return row.ID
}

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

// seedSecondVariant adds another variant to an EXISTING account (seedVariant in
// service_db_test.go always mints a fresh account too; this lets a test build
// a multi-member bulk preview within one account).
func seedSecondVariant(t *testing.T, q *db.Queries, account uuid.UUID) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	nativeProduct := int64(uuid.New().ID())
	nativeVariant := int64(uuid.New().ID())
	prod, err := q.UpsertProduct(ctx, db.UpsertProductParams{
		MarketplaceAccountID: account,
		NativeProductID:      nativeProduct,
		Title:                "Widget 2",
	})
	if err != nil {
		t.Fatalf("upsert product: %v", err)
	}
	v, err := q.UpsertVariant(ctx, db.UpsertVariantParams{
		MarketplaceAccountID: account,
		ProductID:            prod.ID,
		NativeVariantID:      nativeVariant,
		NativeProductID:      nativeProduct,
		SupplierCode:         "SKU-" + uuid.NewString()[:8],
		Title:                "Widget 2 - Blue",
	})
	if err != nil {
		t.Fatalf("upsert variant: %v", err)
	}
	return v.ID
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

// persistRecommendationUnavailableContribution persists a recommendation whose
// proposed contribution is UNAVAILABLE (the policy engine produced no proposal),
// so its ProposedContributionAvailable column is false — the #141 fixture.
func persistRecommendationUnavailableContribution(t *testing.T, svc *recommendation.Service, account, variant uuid.UUID) uuid.UUID {
	t.Helper()
	in := baseValidInput(t)
	in.AccountID = account
	in.VariantID = variant
	in.EventID = uuid.Nil
	in.Policy.Proposed = nil // no proposal ⇒ proposed contribution is Unavailable
	rec := recommendation.Assemble(in)
	row, err := svc.Persist(context.Background(), uuid.New(), rec)
	if err != nil {
		t.Fatalf("persist recommendation (unavailable contribution): %v", err)
	}
	return row.ID
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

// TestEditPrice_MintsNewCardVersionAndNewParameterVersion is CHAT-044 / PD-3
// item 2 realized end to end through the store: a price edit mints a NEW card
// version in the SAME lineage, with a STRICTLY GREATER parameter version, reset
// to Draft — the price is never mutated in place.
func TestEditPriceMintsNewCardVersionAndNewParameterVersion(t *testing.T) {
	pool, q := newPool(t)
	account, variant := seedVariant(t, q)
	svc := recommendation.NewService(pool)
	original := persistApprovableCard(t, svc, account, variant)

	newPrice, err := money.New(999900, "IRR", -2)
	if err != nil {
		t.Fatalf("money.New: %v", err)
	}
	edited, err := svc.EditPrice(context.Background(), original.ID, newPrice, time.Now().UTC())
	if err != nil {
		t.Fatalf("EditPrice: %v", err)
	}
	if edited.LineageID != original.LineageID {
		t.Fatalf("edited lineage = %s, want same lineage %s", edited.LineageID, original.LineageID)
	}
	if edited.Version <= original.Version {
		t.Fatalf("edited version = %d, want > %d", edited.Version, original.Version)
	}
	if edited.ParameterVersion <= original.ParameterVersion {
		t.Fatalf("edited parameter version = %d, want > %d", edited.ParameterVersion, original.ParameterVersion)
	}
	if edited.State != "draft" {
		t.Fatalf("edited state = %s, want draft (reset)", edited.State)
	}
	if edited.PriceMantissa != newPrice.Mantissa() || edited.PriceCurrency != newPrice.Currency() {
		t.Fatalf("edited price = %d %s, want %d %s", edited.PriceMantissa, edited.PriceCurrency, newPrice.Mantissa(), newPrice.Currency())
	}
	if original.ID == edited.ID {
		t.Fatal("EditPrice must mint a NEW card row, never mutate the original in place")
	}

	// The original card row is untouched (append-only: the price on the OLD row
	// never changes).
	stillOriginal, err := svc.GetCard(context.Background(), original.ID)
	if err != nil {
		t.Fatalf("GetCard(original): %v", err)
	}
	if stillOriginal.PriceMantissa != original.PriceMantissa {
		t.Fatal("EditPrice mutated the original card's price in place — append-only violation")
	}
}

// TestListActions_ReturnsCurrentVersionPerLineage_NewestFirst proves the
// actions queue read groups by lineage (current version only) and orders
// newest first (PD-3 item 5).
func TestListActionsReturnsCurrentVersionPerLineageNewestFirst(t *testing.T) {
	pool, q := newPool(t)
	account, variant := seedVariant(t, q)
	svc := recommendation.NewService(pool)
	first := persistApprovableCard(t, svc, account, variant)

	newPrice, err := money.New(123400, "IRR", -2)
	if err != nil {
		t.Fatalf("money.New: %v", err)
	}
	edited, err := svc.EditPrice(context.Background(), first.ID, newPrice, time.Now().UTC())
	if err != nil {
		t.Fatalf("EditPrice: %v", err)
	}

	rows, err := svc.ListActions(context.Background(), account, "", 0)
	if err != nil {
		t.Fatalf("ListActions: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("ListActions = %d rows, want 1 (one lineage, current version only)", len(rows))
	}
	if rows[0].ID != edited.ID {
		t.Fatalf("ListActions returned card %s, want the CURRENT (edited) version %s", rows[0].ID, edited.ID)
	}

	// State filter narrows to the exact state.
	filtered, err := svc.ListActions(context.Background(), account, "draft", 0)
	if err != nil {
		t.Fatalf("ListActions(draft): %v", err)
	}
	if len(filtered) != 1 {
		t.Fatalf("ListActions(draft) = %d, want 1", len(filtered))
	}
	none, err := svc.ListActions(context.Background(), account, "approved", 0)
	if err != nil {
		t.Fatalf("ListActions(approved): %v", err)
	}
	if len(none) != 0 {
		t.Fatalf("ListActions(approved) = %d, want 0 (card is in draft)", len(none))
	}
}
