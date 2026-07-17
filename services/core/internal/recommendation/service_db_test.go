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
// preview bound to a selection-set version is invalidated by a set change (a new
// version).
func TestSelectionSet_ChangeInvalidatesBulkPreview(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account, variant := seedVariant(t, q)
	svc := recommendation.NewService(pool)
	lineage := uuid.New()

	set1, err := svc.CreateSelectionSet(ctx, recommendation.SelectionSetInput{
		Account: account, Lineage: lineage, Name: "priority", MemberCount: 1,
	})
	if err != nil {
		t.Fatalf("create set: %v", err)
	}
	if _, err := svc.AddMember(ctx, set1.ID, variant, uuid.Nil, recommendation.DispositionExecutable); err != nil {
		t.Fatalf("add member: %v", err)
	}
	// A preview bound to set1.Version is currently valid.
	ok, err := svc.BulkPreviewValid(ctx, lineage, set1.Version)
	if err != nil {
		t.Fatalf("bulk valid: %v", err)
	}
	if !ok {
		t.Fatalf("bound preview should be valid before the set changes")
	}
	// The set changes → a new version. The old-bound preview is now invalid.
	set2, err := svc.CreateSelectionSet(ctx, recommendation.SelectionSetInput{
		Account: account, Lineage: lineage, Name: "priority", MemberCount: 2,
	})
	if err != nil {
		t.Fatalf("create set v2: %v", err)
	}
	if set2.Version == set1.Version {
		t.Fatalf("selection-set change did not mint a new version")
	}
	stillValid, err := svc.BulkPreviewValid(ctx, lineage, set1.Version)
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
