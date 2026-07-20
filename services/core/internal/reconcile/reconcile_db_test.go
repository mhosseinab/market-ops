package reconcile_test

import (
	"context"
	"os"
	"sync/atomic"
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

// countingWriter fails the test if the marketplace is ever written to and counts
// the attempts, so a gate-blocked park + its drain can assert ZERO external writes.
type countingWriter struct{ n *int32 }

func (w countingWriter) WritePrice(_ context.Context, _ execution.WriteRequest) execution.WriteResult {
	atomic.AddInt32(w.n, 1)
	return execution.WriteResult{Outcome: execution.OutcomeUnknown}
}

// gateFailResolver resolves writes ENABLED but the EXE-001 gate FAILING (permission
// revoked after approval), so a resume-from-Executing card blocks without writing.
type gateFailResolver struct {
	account, variant uuid.UUID
	native           int64
}

func (f gateFailResolver) Resolve(_ context.Context, card db.ApprovalCard) (execution.RevalidationContext, error) {
	b := approval.Binding{ActionID: card.ActionID, ParameterVersion: card.ParameterVersion, ContextVersion: card.ContextVersion, PolicyVersion: card.PolicyVersion, CostProfileVersion: card.CostProfileVersion, Expiry: card.ExpiresAt}
	return execution.RevalidationContext{
		Inputs: execution.RevalidationInputs{
			Bound: b, Current: b, Now: time.Now(),
			IdentityConfirmed: true, CurrentPriceMatches: true, BoundaryKnown: true,
			PermissionGranted: false, JITFresh: true,
		},
		Enablement:      execution.WriteEnablement{CapabilitySupported: true, RegionWriteVerified: true},
		AccountID:       f.account,
		VariantID:       f.variant,
		VariantNativeID: f.native,
	}, nil
}

// TestReconcile_GateBlockedResumePark_DrainsToFailed is the issue #105 fix-cycle-1
// safety regression: a card a crash left in Executing whose EXE-001 gate FAILS on
// resume parks in PendingReconciliation with a gate-blocked, NO-WRITE
// action_executions marker. That marker makes the park VISIBLE (OPS-002 queue +
// backlog gauge) and DRAINABLE, and — because no write happened — it can resolve
// ONLY to Failed via authoritative read-back, never Accepted (EXE-003). The whole
// lifecycle performs ZERO external writes.
func TestReconcile_GateBlockedResumePark_DrainsToFailed(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	card, variant := seedApprovedCard(t, pool, q)

	// Simulate a crash that stranded the card mid-write in Executing.
	rec := recommendation.NewService(pool)
	for _, s := range []struct{ from, to approval.State }{
		{approval.StateApproved, approval.StateRevalidating},
		{approval.StateRevalidating, approval.StateExecuting},
	} {
		if _, err := rec.Advance(ctx, card.ID, s.from, s.to, "simulate crash mid-write"); err != nil {
			t.Fatalf("advance %s→%s: %v", s.from, s.to, err)
		}
	}

	var writes int32
	exec := execution.NewService(pool, rec, countingWriter{n: &writes},
		gateFailResolver{account: card.MarketplaceAccountID, variant: variant, native: 1})

	res, err := exec.Execute(ctx, card.ID, audit.Actor{ID: "owner-1"})
	if err != nil {
		t.Fatalf("resume execute (gate fail): %v", err)
	}
	if res.DidWrite || !res.Blocked || res.ExternalState != execution.StatePendingReconciliation {
		t.Fatalf("resume: didWrite=%v blocked=%v state=%q; want blocked+pending, no write", res.DidWrite, res.Blocked, res.ExternalState)
	}
	if got := atomic.LoadInt32(&writes); got != 0 {
		t.Fatalf("external writes after blocked resume = %d; want 0", got)
	}

	// The parked action is VISIBLE: a gate-blocked marker enumerated by the OPS-002
	// queue and the backlog gauge.
	rowExec, err := q.GetActionExecutionByAction(ctx, card.ActionID)
	if err != nil {
		t.Fatalf("get execution marker: %v", err)
	}
	if !rowExec.GateBlocked || rowExec.ExternalState != string(execution.StatePendingReconciliation) {
		t.Fatalf("marker gate_blocked=%v state=%q; want true + pending_reconciliation", rowExec.GateBlocked, rowExec.ExternalState)
	}
	pending, err := q.ListPendingReconciliationByAccount(ctx, db.ListPendingReconciliationByAccountParams{
		MarketplaceAccountID: card.MarketplaceAccountID, Limit: 10,
	})
	if err != nil {
		t.Fatalf("list pending: %v", err)
	}
	var visible bool
	for _, p := range pending {
		if p.ActionID == card.ActionID {
			visible = true
		}
	}
	if !visible {
		t.Fatalf("parked action not visible in OPS-002 pending-reconciliation queue")
	}
	agg, err := q.AggregatePendingReconciliation(ctx)
	if err != nil {
		t.Fatalf("aggregate pending: %v", err)
	}
	var counted bool
	for _, a := range agg {
		if a.AccountID == card.MarketplaceAccountID && a.PendingCount >= 1 {
			counted = true
		}
	}
	if !counted {
		t.Fatalf("parked action not counted by pending-reconciliation backlog gauge")
	}

	// A no-write marker may NEVER resolve to Accepted: reconciliation cannot infer a
	// success from a write that never happened. It stays visibly pending (fail closed).
	rc := reconcile.NewService(pool, rec, rec)
	if err := rc.ReconcilePending(ctx, card.ActionID, reconcile.ResolveAccepted, "read-back"); err != reconcile.ErrGateBlockedNoWrite {
		t.Fatalf("reconcile Accepted on gate-blocked marker: err=%v; want ErrGateBlockedNoWrite", err)
	}
	if mid, _ := q.GetApprovalCard(ctx, card.ID); mid.State != string(approval.StatePendingReconciliation) {
		t.Fatalf("card state after refused Accepted = %q; want still pending_reconciliation", mid.State)
	}

	// Authoritative read-back resolves it to the ONLY terminal it can reach: Failed.
	if err := rc.ReconcilePending(ctx, card.ActionID, reconcile.ResolveFailed, "read-back: our price never landed"); err != nil {
		t.Fatalf("reconcile Failed: %v", err)
	}
	after, _ := q.GetApprovalCard(ctx, card.ID)
	if after.State != string(approval.StateFailed) {
		t.Fatalf("card state = %q; want failed after drain", after.State)
	}
	resolved, _ := q.GetActionExecutionByAction(ctx, card.ActionID)
	if resolved.ExternalState != string(execution.StateFailed) {
		t.Fatalf("execution state = %q; want failed after drain", resolved.ExternalState)
	}
	if _, err := q.GetOutcomeWindowByAction(ctx, card.ActionID); err != nil {
		t.Fatalf("outcome window not opened on drain: %v", err)
	}
	if got := atomic.LoadInt32(&writes); got != 0 {
		t.Fatalf("external writes across park+drain = %d; want 0 throughout", got)
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
