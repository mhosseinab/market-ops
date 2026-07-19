package recommendation_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mhosseinab/market-ops/services/core/internal/approval"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/identity"
	"github.com/mhosseinab/market-ops/services/core/internal/recommendation"
)

// newPool connects to DATABASE_URL (schema applied via `task db:reset`). Skips
// when unset so the suite still runs where no Postgres is provisioned.
func newPool(t *testing.T) (*pgxpool.Pool, *db.Queries) {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set; skipping recommendation DB test")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("connect pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool, db.New(pool)
}

func seedVariant(t *testing.T, q *db.Queries) (account, variant uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	org, err := q.CreateOrganization(ctx, "rec-test-"+uuid.NewString())
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	acct, err := q.CreateMarketplaceAccount(ctx, db.CreateMarketplaceAccountParams{
		OrganizationID:  org.ID,
		NativeAccountID: "native-" + uuid.NewString(),
		DisplayName:     "Rec Seller",
	})
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	nativeProduct := int64(uuid.New().ID())
	nativeVariant := int64(uuid.New().ID())
	prod, err := q.UpsertProduct(ctx, db.UpsertProductParams{
		MarketplaceAccountID: acct.ID,
		NativeProductID:      nativeProduct,
		Title:                "Widget",
	})
	if err != nil {
		t.Fatalf("upsert product: %v", err)
	}
	v, err := q.UpsertVariant(ctx, db.UpsertVariantParams{
		MarketplaceAccountID: acct.ID,
		ProductID:            prod.ID,
		NativeVariantID:      nativeVariant,
		NativeProductID:      nativeProduct,
		SupplierCode:         "SKU-" + uuid.NewString()[:8],
		Title:                "Widget - Red",
	})
	if err != nil {
		t.Fatalf("upsert variant: %v", err)
	}
	return acct.ID, v.ID
}

// persistApprovableCard seeds an approvable recommendation + its Draft card.
func persistApprovableCard(t *testing.T, svc *recommendation.Service, account, variant uuid.UUID) db.ApprovalCard {
	t.Helper()
	ctx := context.Background()
	in := baseValidInput(t)
	in.AccountID = account
	in.VariantID = variant
	in.EventID = uuid.Nil // avoid an event FK; not-event-driven is explicit.
	rec := recommendation.Assemble(in)
	lineage := uuid.New()
	persisted, err := svc.Persist(ctx, lineage, rec)
	if err != nil {
		t.Fatalf("persist recommendation: %v", err)
	}
	card, err := svc.CreateCard(ctx, persisted.ID, uuid.New(), account, rec)
	if err != nil {
		t.Fatalf("create card: %v", err)
	}
	return card
}

// TestStateHistory_AppendOnlyAndReconstructable walks a card through the §8.4
// happy path and asserts the append-only history reconstructs the lifecycle
// (AUD-001) with no in-place mutation.
func TestStateHistory_AppendOnlyAndReconstructable(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account, variant := seedVariant(t, q)
	svc := recommendation.NewService(pool)
	card := persistApprovableCard(t, svc, account, variant)

	steps := []struct{ from, to approval.State }{
		{approval.StateDraft, approval.StateReadyForReview},
		{approval.StateReadyForReview, approval.StateAwaitingConfirmation},
		{approval.StateAwaitingConfirmation, approval.StateApproved},
		{approval.StateApproved, approval.StateRevalidating},
	}
	for _, s := range steps {
		if _, err := svc.Advance(ctx, card.ID, s.from, s.to, "test"); err != nil {
			t.Fatalf("advance %s→%s: %v", s.from, s.to, err)
		}
	}

	hist, err := svc.History(ctx, card.ID)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	// Initial [*]→draft plus four advances = 5 append-only rows, in order.
	wantTo := []string{"draft", "ready_for_review", "awaiting_confirmation", "approved", "revalidating"}
	if len(hist) != len(wantTo) {
		t.Fatalf("history has %d rows; want %d", len(hist), len(wantTo))
	}
	for i, row := range hist {
		if row.ToState != wantTo[i] {
			t.Fatalf("history[%d].to = %s; want %s", i, row.ToState, wantTo[i])
		}
	}
}

// TestAdvance_RejectsUndefinedTransition proves the store fails closed on a move
// the §8.4 machine does not permit (no state change, no history row).
func TestAdvance_RejectsUndefinedTransition(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account, variant := seedVariant(t, q)
	svc := recommendation.NewService(pool)
	card := persistApprovableCard(t, svc, account, variant)

	// Draft → Approved is undefined (§8.4). It must be rejected.
	if _, err := svc.Advance(ctx, card.ID, approval.StateDraft, approval.StateApproved, "bad"); err != recommendation.ErrRejectedTransition {
		t.Fatalf("undefined transition: want ErrRejectedTransition, got %v", err)
	}
	hist, err := svc.History(ctx, card.ID)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(hist) != 1 {
		t.Fatalf("undefined transition wrote history: %d rows", len(hist))
	}
}

// TestAdvance_RejectsStaleFromState proves the FROM-guard: an advance from a
// state the card already left matches no row and is rejected (no blind overwrite).
func TestAdvance_RejectsStaleFromState(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account, variant := seedVariant(t, q)
	svc := recommendation.NewService(pool)
	card := persistApprovableCard(t, svc, account, variant)

	if _, err := svc.Advance(ctx, card.ID, approval.StateDraft, approval.StateReadyForReview, "ok"); err != nil {
		t.Fatalf("advance: %v", err)
	}
	// The card is now ReadyForReview; a second Draft→ReadyForReview is stale.
	if _, err := svc.Advance(ctx, card.ID, approval.StateDraft, approval.StateReadyForReview, "stale"); err != recommendation.ErrRejectedTransition {
		t.Fatalf("stale from-state: want ErrRejectedTransition, got %v", err)
	}
}

// TestSelectionSet_ChangeInvalidatesBulkPreview proves CHAT-051/052: a bulk
// preview bound to a selection-set version is invalidated by a set change. Under
// the immutable-membership model (#91) a set change is NEVER an in-place append —
// it mints a NEW version through PreviewBulkSelection on the same lineage.
func TestSelectionSet_ChangeInvalidatesBulkPreview(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account, variant := seedVariant(t, q)
	svc := recommendation.NewService(pool)
	recID := persistRecommendation(t, svc, account, variant)

	// Version N: a bulk preview over a single member. Membership is sealed here.
	set1, err := svc.PreviewBulkSelection(ctx, account, uuid.Nil, "priority",
		nil, []recommendation.PreviewMemberInput{{VariantID: variant, RecommendationID: recID}})
	if err != nil {
		t.Fatalf("preview v1: %v", err)
	}
	lineage := set1.Set.LineageID

	// A preview bound to set1.Version is currently valid.
	ok, err := svc.BulkPreviewValid(ctx, lineage, set1.Set.Version)
	if err != nil {
		t.Fatalf("bulk valid: %v", err)
	}
	if !ok {
		t.Fatalf("bound preview should be valid before the set changes")
	}

	// A scope change ⇒ N+1 with its own membership snapshot (here the membership
	// shrinks to zero). The old-bound preview is now invalid.
	set2, err := svc.PreviewBulkSelection(ctx, account, lineage, "priority", nil, nil)
	if err != nil {
		t.Fatalf("preview v2: %v", err)
	}
	if set2.Set.Version == set1.Set.Version {
		t.Fatalf("selection-set change did not mint a new version")
	}
	stillValid, err := svc.BulkPreviewValid(ctx, lineage, set1.Set.Version)
	if err != nil {
		t.Fatalf("bulk valid: %v", err)
	}
	if stillValid {
		t.Fatalf("bulk preview bound to the old set version is still valid after a set change (CHAT-052 violated)")
	}
}

// TestReopenExpirer_InvalidatesLiveCard proves the S11 identity-reopen consumer:
// reopening a mapping expires the dependent live control (§16).
func TestReopenExpirer_InvalidatesLiveCard(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account, variant := seedVariant(t, q)
	svc := recommendation.NewService(pool)
	card := persistApprovableCard(t, svc, account, variant)

	// Drive the card to AwaitingConfirmation (a live control-bearing state).
	if _, err := svc.Advance(ctx, card.ID, approval.StateDraft, approval.StateReadyForReview, "ok"); err != nil {
		t.Fatalf("advance: %v", err)
	}
	if _, err := svc.Advance(ctx, card.ID, approval.StateReadyForReview, approval.StateAwaitingConfirmation, "ok"); err != nil {
		t.Fatalf("advance: %v", err)
	}

	expirer := recommendation.NewReopenExpirer(svc)
	if err := expirer.MappingReopened(ctx, identity.MappingReopenedEvent{
		AccountID: account, VariantID: variant, Reason: identity.ReasonMerge,
		EmittedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("reopen: %v", err)
	}

	got, err := svc.GetCard(ctx, card.ID)
	if err != nil {
		t.Fatalf("get card: %v", err)
	}
	if got.State != string(approval.StateInvalidated) {
		t.Fatalf("card state after reopen = %s; want invalidated", got.State)
	}
}

// driveToState advances a freshly-minted Draft card up the §8.4 happy path until
// it reaches target, using the real FROM-guarded advances.
func driveToState(t *testing.T, svc *recommendation.Service, cardID uuid.UUID, target approval.State) {
	t.Helper()
	ctx := context.Background()
	path := []struct{ from, to approval.State }{
		{approval.StateDraft, approval.StateReadyForReview},
		{approval.StateReadyForReview, approval.StateAwaitingConfirmation},
		{approval.StateAwaitingConfirmation, approval.StateApproved},
		{approval.StateApproved, approval.StateRevalidating},
	}
	for _, s := range path {
		if _, err := svc.Advance(ctx, cardID, s.from, s.to, "test-drive"); err != nil {
			t.Fatalf("advance %s→%s: %v", s.from, s.to, err)
		}
		if s.to == target {
			return
		}
	}
	t.Fatalf("target state %s not reached on the happy path", target)
}

// TestReopenExpirer_InvalidatesApprovedCard proves the §16 identity-reopen rule
// for an ALREADY-APPROVED dependent (issue #86): the §8.4 table has no direct
// Approved → Invalidated edge, so the consumer must compose Approved →
// Revalidating → Invalidated in one transaction. After reopen the card must be
// Invalidated (fail closed) — never left Approved/executable — and the
// append-only history must record BOTH hops.
func TestReopenExpirer_InvalidatesApprovedCard(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account, variant := seedVariant(t, q)
	svc := recommendation.NewService(pool)
	card := persistApprovableCard(t, svc, account, variant)

	driveToState(t, svc, card.ID, approval.StateApproved)

	n, err := svc.ExpireDependentForVariant(ctx, variant, "identity_reopen:test")
	if err != nil {
		t.Fatalf("expire: %v", err)
	}
	if n != 1 {
		t.Fatalf("invalidated count = %d; want 1", n)
	}

	got, err := svc.GetCard(ctx, card.ID)
	if err != nil {
		t.Fatalf("get card: %v", err)
	}
	if got.State != string(approval.StateInvalidated) {
		t.Fatalf("approved card after reopen = %s; want invalidated (fail closed)", got.State)
	}

	// The append-only history must show the composed Approved→Revalidating→Invalidated
	// hops (AUD-001): every hop writes its own row, none is skipped or overwritten.
	// The two hops share a transaction timestamp, so ordering is asserted by the
	// self-describing from→to linkage (which reconstructs the true order) rather than
	// by the occurred_at tiebreak.
	hist, err := svc.History(ctx, card.ID)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(hist) != 6 {
		t.Fatalf("history has %d rows; want 6 (%v)", len(hist), historyToStates(hist))
	}
	wantEdges := map[string]bool{
		"->draft":                 true, // [*] → Draft (NULL from_state)
		"draft->ready_for_review": true,
		"ready_for_review->awaiting_confirmation": true,
		"awaiting_confirmation->approved":         true,
		"approved->revalidating":                  true, // composed hop 1 (#86)
		"revalidating->invalidated":               true, // composed hop 2 (#86)
	}
	for _, row := range hist {
		var from string
		if row.FromState.Valid {
			from = row.FromState.String
		}
		edge := from + "->" + row.ToState
		if !wantEdges[edge] {
			t.Fatalf("unexpected history edge %q", edge)
		}
		delete(wantEdges, edge)
	}
	if len(wantEdges) != 0 {
		t.Fatalf("missing history edges: %v", wantEdges)
	}

	// Idempotency: a second delivery is a no-op — Invalidated is terminal, the live
	// query no longer returns it, count is 0, and no spurious history row is added.
	n2, err := svc.ExpireDependentForVariant(ctx, variant, "identity_reopen:test")
	if err != nil {
		t.Fatalf("second expire: %v", err)
	}
	if n2 != 0 {
		t.Fatalf("second expire invalidated %d; want 0 (idempotent)", n2)
	}
	hist2, err := svc.History(ctx, card.ID)
	if err != nil {
		t.Fatalf("history 2: %v", err)
	}
	if len(hist2) != 6 {
		t.Fatalf("second delivery added history rows: %d; want 6", len(hist2))
	}
}

// TestReopenExpirer_InvalidatesRevalidatingCard proves a Revalidating dependent is
// also invalidated (its single-hop Revalidating → Invalidated edge is unchanged).
func TestReopenExpirer_InvalidatesRevalidatingCard(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account, variant := seedVariant(t, q)
	svc := recommendation.NewService(pool)
	card := persistApprovableCard(t, svc, account, variant)

	driveToState(t, svc, card.ID, approval.StateRevalidating)

	n, err := svc.ExpireDependentForVariant(ctx, variant, "identity_reopen:test")
	if err != nil {
		t.Fatalf("expire: %v", err)
	}
	if n != 1 {
		t.Fatalf("invalidated count = %d; want 1", n)
	}
	got, err := svc.GetCard(ctx, card.ID)
	if err != nil {
		t.Fatalf("get card: %v", err)
	}
	if got.State != string(approval.StateInvalidated) {
		t.Fatalf("revalidating card after reopen = %s; want invalidated", got.State)
	}
}

func historyToStates(hist []db.ApprovalCardState) []string {
	out := make([]string, len(hist))
	for i, r := range hist {
		out[i] = r.ToState
	}
	return out
}
