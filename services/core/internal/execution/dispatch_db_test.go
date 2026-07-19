package execution

import (
	"context"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/mhosseinab/market-ops/services/core/internal/approval"
	"github.com/mhosseinab/market-ops/services/core/internal/audit"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/jobs"
	"github.com/mhosseinab/market-ops/services/core/internal/recommendation"
)

// seedAwaitingCard seeds account/variant/recommendation/card and drives the card to
// AwaitingConfirmation (a live, control-bearing state), returning the reloaded card,
// its native variant id, and the presented APR-001 binding a client would echo.
func seedAwaitingCard(t *testing.T, pool *pgxpool.Pool, q *db.Queries) (db.ApprovalCard, int64, approval.Binding) {
	t.Helper()
	ctx := context.Background()

	org, err := q.CreateOrganization(ctx, "dispatch-test-"+uuid.NewString())
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	acct, err := q.CreateMarketplaceAccount(ctx, db.CreateMarketplaceAccountParams{
		OrganizationID: org.ID, NativeAccountID: "native-" + uuid.NewString(), DisplayName: "Dispatch Seller",
	})
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	nativeProduct := int64(uuid.New().ID())
	nativeVariant := int64(uuid.New().ID())
	prod, err := q.UpsertProduct(ctx, db.UpsertProductParams{
		MarketplaceAccountID: acct.ID, NativeProductID: nativeProduct, Title: "Widget",
	})
	if err != nil {
		t.Fatalf("upsert product: %v", err)
	}
	variant, err := q.UpsertVariant(ctx, db.UpsertVariantParams{
		MarketplaceAccountID: acct.ID, ProductID: prod.ID,
		NativeVariantID: nativeVariant, NativeProductID: nativeProduct,
		SupplierCode: "SKU-" + uuid.NewString()[:8], Title: "Widget - Red",
	})
	if err != nil {
		t.Fatalf("upsert variant: %v", err)
	}

	lineage := uuid.New()
	var recID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO recommendations (
			marketplace_account_id, variant_id, lineage_id, version, objective,
			current_price_mantissa, current_price_currency, current_price_exponent,
			readiness, evidence_quality,
			cost_profile_version, policy_version, context_version, parameter_version)
		VALUES ($1,$2,$3,1,'maximize_contribution',100000,'IRR',0,'complete','verified',1,1,1,1)
		RETURNING id`, acct.ID, variant.ID, lineage).Scan(&recID); err != nil {
		t.Fatalf("insert recommendation: %v", err)
	}

	actionID := uuid.New()
	binding := approval.Binding{
		ActionID: actionID, ParameterVersion: 1, ContextVersion: 1,
		PolicyVersion: 1, CostProfileVersion: 1, Expiry: time.Now().Add(30 * time.Minute),
	}
	card, err := q.InsertApprovalCard(ctx, db.InsertApprovalCardParams{
		RecommendationID: recID, MarketplaceAccountID: acct.ID, LineageID: uuid.New(),
		ActionID: actionID, ParameterVersion: 1, ContextVersion: 1, PolicyVersion: 1, CostProfileVersion: 1,
		EvidenceVersions: []byte("{}"), IdempotencyKey: binding.IdempotencyKey(),
		State: string(approval.StateDraft), PriceMantissa: 95000, PriceCurrency: "IRR", PriceExponent: 0,
		ExpiresAt: binding.Expiry,
	})
	if err != nil {
		t.Fatalf("insert card: %v", err)
	}

	svc := recommendation.NewService(pool)
	for _, step := range []struct{ from, to approval.State }{
		{approval.StateDraft, approval.StateReadyForReview},
		{approval.StateReadyForReview, approval.StateAwaitingConfirmation},
	} {
		if _, err := svc.Advance(ctx, card.ID, step.from, step.to, "seed"); err != nil {
			t.Fatalf("advance %s→%s: %v", step.from, step.to, err)
		}
	}
	awaiting, err := q.GetApprovalCard(ctx, card.ID)
	if err != nil {
		t.Fatalf("get awaiting card: %v", err)
	}
	presented := bindingOf(awaiting)
	presented.EvidenceVersions = nil
	return awaiting, nativeVariant, presented
}

// startDispatchClient migrates River, registers the execute-approved worker over the
// injected runner, and starts the client, returning it plus a JobCompleted channel.
func startDispatchClient(t *testing.T, pool *pgxpool.Pool, runner jobs.ExecuteApprovedFunc) (*jobs.Client, <-chan *river.Event) {
	t.Helper()
	ctx := context.Background()
	if err := jobs.Migrate(ctx, pool); err != nil {
		t.Fatalf("river migrate: %v", err)
	}
	workers, err := jobs.NewWorkers(nil, jobs.ExecutionRunners{ExecuteApproved: runner})
	if err != nil {
		t.Fatalf("workers: %v", err)
	}
	client, err := jobs.NewClient(pool, workers, nil)
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	completed, cancel := client.Subscribe(river.EventKindJobCompleted)
	t.Cleanup(cancel)
	if err := client.Start(ctx); err != nil {
		t.Fatalf("start client: %v", err)
	}
	t.Cleanup(func() {
		stopCtx, cancelStop := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancelStop()
		_ = client.Stop(stopCtx)
	})
	return client, completed
}

// TestConfirmDispatch_ClientDeathStillExecutes is the core issue #92 acceptance
// proof: after a confirmation is acknowledged, execution proceeds durably WITHOUT a
// second client request. The test confirms the card and then does NOT call Execute
// (simulating the client/process dying immediately after confirm); the durable
// worker claims the enqueued intent and drives exactly one external write.
func TestConfirmDispatch_ClientDeathStillExecutes(t *testing.T) {
	if os.Getenv("DATABASE_URL") == "" {
		t.Skip("DATABASE_URL not set; skipping dispatch DB test")
	}
	pool, q := newPool(t)
	ctx := context.Background()
	card, native, presented := seedAwaitingCard(t, pool, q)

	srv, writes := countingMockDK(t)
	writer := NewHTTPWriter(srv.URL, "tok", srv.Client())
	execSvc := NewService(pool, recommendation.NewService(pool), writer, fakeResolver{ctx: enabledContext(card, native)})

	// Scope the runner to THIS card: the DB is shared, so the client may also drain
	// execute_approved intents enqueued by other tests. Foreign intents are acked as
	// a no-op (they are driven by their own test), so this test's write counter and
	// card state reflect ONLY this card — the assertion stays deterministic.
	runner := func(c context.Context, args jobs.ExecuteApprovedArgs) error {
		if args.CardID != card.ID {
			return nil
		}
		_, err := execSvc.Execute(c, args.CardID, audit.Actor{ID: "execution_worker", Role: "system", Surface: "system"})
		return err
	}
	client, _ := startDispatchClient(t, pool, runner)

	// The recommendation service that OWNS confirmation dispatches onto this client.
	recSvc := recommendation.NewService(pool).SetExecutionDispatcher(recommendation.NewJobDispatcher(client))

	outcome, err := recSvc.ConfirmIndividual(ctx, card.ID, presented, time.Now().UTC(), audit.Actor{ID: "test-user", Role: "owner", Surface: "screen"})
	if err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if outcome.State != approval.StateApproved {
		t.Fatalf("confirm state = %s; want approved", outcome.State)
	}
	// Deliberately DO NOT call execSvc.Execute here — the client is "dead" after confirm.

	// The durable worker must reach execution without a second client request. Poll
	// the card until the worker drives it to a terminal state (accepted).
	deadline := time.Now().Add(20 * time.Second)
	var after db.ApprovalCard
	for {
		after, err = q.GetApprovalCard(ctx, card.ID)
		if err != nil {
			t.Fatalf("reload card: %v", err)
		}
		if after.State == string(approval.StateAccepted) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("durable execution intent was not processed within 20s (card state=%s)", after.State)
		}
		time.Sleep(50 * time.Millisecond)
	}

	if got := atomic.LoadInt32(writes); got != 1 {
		t.Fatalf("external writes = %d; want exactly 1 (durable dispatch executed once)", got)
	}
}

// TestConfirmDispatch_DuplicateDeliveryNoDuplicateWrite proves crash/restart safety:
// the SAME durable intent delivered twice (e.g. a worker crash after the external
// write but before River acked the job, so River redelivers on restart) resumes the
// SAME action with ZERO additional external writes — execution.Execute replays the
// recorded result idempotently.
func TestConfirmDispatch_DuplicateDeliveryNoDuplicateWrite(t *testing.T) {
	if os.Getenv("DATABASE_URL") == "" {
		t.Skip("DATABASE_URL not set; skipping dispatch DB test")
	}
	pool, q := newPool(t)
	ctx := context.Background()
	card, native, presented := seedAwaitingCard(t, pool, q)

	srv, writes := countingMockDK(t)
	writer := NewHTTPWriter(srv.URL, "tok", srv.Client())
	execSvc := NewService(pool, recommendation.NewService(pool), writer, fakeResolver{ctx: enabledContext(card, native)})

	// Confirm to reach Approved (no worker running here; we drive the runner directly
	// to simulate redelivery of the same intent deterministically).
	recSvc := recommendation.NewService(pool)
	if _, err := recSvc.ConfirmIndividual(ctx, card.ID, presented, time.Now().UTC(), audit.Actor{ID: "test-user", Role: "owner", Surface: "screen"}); err != nil {
		t.Fatalf("confirm: %v", err)
	}

	args := jobs.ExecuteApprovedArgs{CardID: card.ID, ActionID: card.ActionID, ParameterVersion: card.ParameterVersion, ContextVersion: card.ContextVersion}
	runner := func(c context.Context, a jobs.ExecuteApprovedArgs) error {
		_, err := execSvc.Execute(c, a.CardID, audit.Actor{ID: "execution_worker", Role: "system", Surface: "system"})
		return err
	}
	// First delivery performs the write.
	if err := runner(ctx, args); err != nil {
		t.Fatalf("first delivery: %v", err)
	}
	// Redelivery after a simulated crash: must NOT write again (idempotent replay).
	if err := runner(ctx, args); err != nil {
		t.Fatalf("redelivery: %v", err)
	}

	if got := atomic.LoadInt32(writes); got != 1 {
		t.Fatalf("external writes = %d; want exactly 1 across redelivery (EXE-002)", got)
	}
	var rows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM action_executions WHERE action_id = $1`, card.ActionID).Scan(&rows); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if rows != 1 {
		t.Fatalf("action_executions rows = %d; want exactly 1", rows)
	}
}
