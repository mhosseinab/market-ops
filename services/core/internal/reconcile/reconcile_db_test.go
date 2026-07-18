package reconcile_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mhosseinab/market-ops/services/core/internal/approval"
	"github.com/mhosseinab/market-ops/services/core/internal/audit"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/execution"
	"github.com/mhosseinab/market-ops/services/core/internal/recommendation"
	"github.com/mhosseinab/market-ops/services/core/internal/reconcile"
)

func newPool(t *testing.T) (*pgxpool.Pool, *db.Queries) {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set; skipping reconcile DB test")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("connect pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool, db.New(pool)
}

// seedApprovedCard seeds a card advanced to Approved plus its variant id.
func seedApprovedCard(t *testing.T, pool *pgxpool.Pool, q *db.Queries) (db.ApprovalCard, uuid.UUID) {
	t.Helper()
	return seedCard(t, pool, q, approval.StateApproved)
}

// seedCard seeds a card advanced through the legal §8.4 path up to target.
func seedCard(t *testing.T, pool *pgxpool.Pool, q *db.Queries, target approval.State) (db.ApprovalCard, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	org, err := q.CreateOrganization(ctx, "recon-"+uuid.NewString())
	if err != nil {
		t.Fatalf("org: %v", err)
	}
	acct, err := q.CreateMarketplaceAccount(ctx, db.CreateMarketplaceAccountParams{
		OrganizationID: org.ID, NativeAccountID: "native-" + uuid.NewString(), DisplayName: "Recon Seller",
	})
	if err != nil {
		t.Fatalf("account: %v", err)
	}
	np, nv := int64(uuid.New().ID()), int64(uuid.New().ID())
	prod, err := q.UpsertProduct(ctx, db.UpsertProductParams{MarketplaceAccountID: acct.ID, NativeProductID: np, Title: "W"})
	if err != nil {
		t.Fatalf("product: %v", err)
	}
	variant, err := q.UpsertVariant(ctx, db.UpsertVariantParams{
		MarketplaceAccountID: acct.ID, ProductID: prod.ID, NativeVariantID: nv, NativeProductID: np,
		SupplierCode: "SKU-" + uuid.NewString()[:8], Title: "W-Red",
	})
	if err != nil {
		t.Fatalf("variant: %v", err)
	}
	var recID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO recommendations (marketplace_account_id, variant_id, lineage_id, version, objective,
			current_price_mantissa, current_price_currency, current_price_exponent, readiness, evidence_quality)
		VALUES ($1,$2,$3,1,'maximize_contribution',100000,'IRR',0,'complete','verified') RETURNING id`,
		acct.ID, variant.ID, uuid.New()).Scan(&recID); err != nil {
		t.Fatalf("recommendation: %v", err)
	}
	actionID := uuid.New()
	binding := approval.Binding{ActionID: actionID, ParameterVersion: 1, ContextVersion: 1, PolicyVersion: 1, CostProfileVersion: 1, Expiry: time.Now().Add(30 * time.Minute)}
	card, err := q.InsertApprovalCard(ctx, db.InsertApprovalCardParams{
		RecommendationID: recID, MarketplaceAccountID: acct.ID, LineageID: uuid.New(),
		ActionID: actionID, ParameterVersion: 1, ContextVersion: 1, PolicyVersion: 1, CostProfileVersion: 1,
		EvidenceVersions: []byte("{}"), IdempotencyKey: binding.IdempotencyKey(),
		State: string(approval.StateDraft), PriceMantissa: 95000, PriceCurrency: "IRR", PriceExponent: 0, ExpiresAt: binding.Expiry,
	})
	if err != nil {
		t.Fatalf("card: %v", err)
	}
	svc := recommendation.NewService(pool)
	steps := []struct{ from, to approval.State }{
		{approval.StateDraft, approval.StateReadyForReview},
		{approval.StateReadyForReview, approval.StateAwaitingConfirmation},
		{approval.StateAwaitingConfirmation, approval.StateApproved},
	}
	for _, s := range steps {
		if _, err := svc.Advance(ctx, card.ID, s.from, s.to, "seed"); err != nil {
			t.Fatalf("advance: %v", err)
		}
		if s.to == target {
			break
		}
	}
	final, _ := q.GetApprovalCard(ctx, card.ID)
	return final, variant.ID
}

type unknownWriter struct{}

func (unknownWriter) WritePrice(_ context.Context, _ execution.WriteRequest) execution.WriteResult {
	return execution.WriteResult{Outcome: execution.OutcomeUnknown}
}

type fakeResolver struct {
	account, variant uuid.UUID
	native           int64
}

func (f fakeResolver) Resolve(_ context.Context, card db.ApprovalCard) (execution.RevalidationContext, error) {
	b := approval.Binding{ActionID: card.ActionID, ParameterVersion: card.ParameterVersion, ContextVersion: card.ContextVersion, PolicyVersion: card.PolicyVersion, CostProfileVersion: card.CostProfileVersion, Expiry: card.ExpiresAt}
	return execution.RevalidationContext{
		Inputs: execution.RevalidationInputs{
			Bound: b, Current: b, Now: time.Now(),
			IdentityConfirmed: true, CurrentPriceMatches: true, BoundaryKnown: true, PermissionGranted: true, JITFresh: true,
		},
		Enablement:      execution.WriteEnablement{CapabilitySupported: true, RegionWriteVerified: true},
		AccountID:       f.account,
		VariantID:       f.variant,
		VariantNativeID: f.native,
	}, nil
}

// TestReconcilePending_ResolvesToTerminalAndOpensWindow proves the §16 / EXE-003
// reconciliation: a Pending Reconciliation action resolves to Accepted, the card
// advances PendingReconciliation → Accepted, and a seven-day outcome window opens.
func TestReconcilePending_ResolvesToTerminalAndOpensWindow(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	card, variant := seedApprovedCard(t, pool, q)

	rec := recommendation.NewService(pool)
	exec := execution.NewService(pool, rec, unknownWriter{}, fakeResolver{account: card.MarketplaceAccountID, variant: variant, native: 1})
	res, err := exec.Execute(ctx, card.ID, audit.Actor{ID: "owner-1"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.ExternalState != execution.StatePendingReconciliation {
		t.Fatalf("want pending; got %q", res.ExternalState)
	}

	rc := reconcile.NewService(pool, rec, rec)
	if err := rc.ReconcilePending(ctx, card.ActionID, reconcile.ResolveAccepted, "read-back matched"); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	after, _ := q.GetApprovalCard(ctx, card.ID)
	if after.State != string(approval.StateAccepted) {
		t.Fatalf("card state = %q; want accepted", after.State)
	}
	if _, err := q.GetOutcomeWindowByAction(ctx, card.ActionID); err != nil {
		t.Fatalf("outcome window not opened: %v", err)
	}
	// A second reconcile of the now-terminal action is refused (not pending).
	if err := rc.ReconcilePending(ctx, card.ActionID, reconcile.ResolveAccepted, "again"); err != reconcile.ErrNotPending {
		t.Fatalf("re-reconcile: want ErrNotPending, got %v", err)
	}
}

// TestInvalidateStaleCardsForVariant proves the §16 manual-DK-change consumer:
// a live control-bearing card for the variant is invalidated so no stale write
// lands. Per §8.4 the invalidatable live states are AwaitingConfirmation and
// Revalidating (Approved must revalidate first), so this seeds AwaitingConfirmation.
func TestInvalidateStaleCardsForVariant(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	card, variant := seedCard(t, pool, q, approval.StateAwaitingConfirmation)

	rec := recommendation.NewService(pool)
	rc := reconcile.NewService(pool, rec, rec)
	n, err := rc.InvalidateStaleCardsForVariant(ctx, variant, "owned offer changed")
	if err != nil {
		t.Fatalf("invalidate: %v", err)
	}
	if n < 1 {
		t.Fatalf("expected at least one card invalidated; got %d", n)
	}
	after, _ := q.GetApprovalCard(ctx, card.ID)
	if after.State != string(approval.StateInvalidated) {
		t.Fatalf("card state = %q; want invalidated", after.State)
	}
}
