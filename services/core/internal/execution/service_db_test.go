package execution

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mhosseinab/market-ops/services/core/internal/approval"
	"github.com/mhosseinab/market-ops/services/core/internal/audit"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/mockdk"
	"github.com/mhosseinab/market-ops/services/core/internal/recommendation"
)

// newPool connects to DATABASE_URL (schema applied via task db:reset). Skips when
// unset so the suite still runs where no Postgres is provisioned.
func newPool(t *testing.T) (*pgxpool.Pool, *db.Queries) {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set; skipping execution DB test")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("connect pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool, db.New(pool)
}

// countingMockDK wraps the offline mockdk handler and counts external price
// writes (POST /open-api/v1/batch/variant/update). The counter is the EXE-002
// proof surface: a duplicate request must never increment it twice.
func countingMockDK(t *testing.T) (*httptest.Server, *int32) {
	t.Helper()
	var writes int32
	base := mockdk.Handler(mockdk.DefaultConfig())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/open-api/v1/batch/variant/update" {
			atomic.AddInt32(&writes, 1)
		}
		base.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, &writes
}

// seedApprovedCard seeds account/variant/recommendation/card and advances the card
// to Approved through the legal §8.4 path, returning the card and its native
// variant id.
func seedApprovedCard(t *testing.T, pool *pgxpool.Pool, q *db.Queries) (db.ApprovalCard, int64) {
	t.Helper()
	ctx := context.Background()

	org, err := q.CreateOrganization(ctx, "exec-test-"+uuid.NewString())
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	acct, err := q.CreateMarketplaceAccount(ctx, db.CreateMarketplaceAccountParams{
		OrganizationID: org.ID, NativeAccountID: "native-" + uuid.NewString(), DisplayName: "Exec Seller",
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
		VALUES ($1,$2,$3,1,'maximize_contribution',100000,'IRR',0,'complete','verified',
			1,1,1,1)
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
		{approval.StateAwaitingConfirmation, approval.StateApproved},
	} {
		if _, err := svc.Advance(ctx, card.ID, step.from, step.to, "seed"); err != nil {
			t.Fatalf("advance %s→%s: %v", step.from, step.to, err)
		}
	}
	approved, err := q.GetApprovalCard(ctx, card.ID)
	if err != nil {
		t.Fatalf("get approved card: %v", err)
	}
	return approved, nativeVariant
}

// fakeResolver returns a fixed RevalidationContext (the server-resolved state a
// real resolver would read from authoritative sources).
type fakeResolver struct{ ctx RevalidationContext }

func (f fakeResolver) Resolve(_ context.Context, card db.ApprovalCard) (RevalidationContext, error) {
	rc := f.ctx
	// Default the current binding to the card's bound binding (all gates pass)
	// unless the test overrode it.
	if rc.Inputs.Current.ActionID == uuid.Nil {
		b, _ := bindingOf(card)
		b.EvidenceVersions = nil
		rc.Inputs.Current = b
	}
	if rc.Inputs.Bound.ActionID == uuid.Nil {
		b, _ := bindingOf(card)
		b.EvidenceVersions = nil
		rc.Inputs.Bound = b
	}
	return rc, nil
}

// enabledContext builds a fully-passing, write-ENABLED revalidation context.
func enabledContext(card db.ApprovalCard, nativeVariant int64) RevalidationContext {
	return RevalidationContext{
		Inputs: RevalidationInputs{
			Now: time.Now(), IdentityConfirmed: true, CurrentPriceMatches: true,
			BoundaryKnown: true, PermissionGranted: true, JITFresh: true,
		},
		Enablement:      WriteEnablement{CapabilitySupported: true, RegionWriteVerified: true},
		Actor:           audit.Actor{ID: "owner-1", Role: "owner", Surface: "screen"},
		AccountID:       card.MarketplaceAccountID,
		VariantNativeID: nativeVariant,
	}
}

// TestExecute_DuplicateRequestZeroDuplicateWrites is the EXE-002 never-cut proof:
// two Execute calls for the same approved action perform EXACTLY ONE external
// write against mockdk. The second call replays the recorded result.
func TestExecute_DuplicateRequestZeroDuplicateWrites(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	card, native := seedApprovedCard(t, pool, q)
	srv, writes := countingMockDK(t)

	writer := NewHTTPWriter(srv.URL, "tok", srv.Client())
	svc := NewService(pool, recommendation.NewService(pool), writer, fakeResolver{ctx: enabledContext(card, native)})

	first, err := svc.Execute(ctx, card.ID, audit.Actor{ID: "owner-1", Role: "owner", Surface: "screen"})
	if err != nil {
		t.Fatalf("first execute: %v", err)
	}
	if !first.DidWrite || first.ExternalState != StateAccepted {
		t.Fatalf("first execute: didWrite=%v state=%q; want write+accepted", first.DidWrite, first.ExternalState)
	}
	second, err := svc.Execute(ctx, card.ID, audit.Actor{ID: "owner-1", Role: "owner", Surface: "screen"})
	if err != nil {
		t.Fatalf("second execute: %v", err)
	}
	if second.DidWrite {
		t.Fatalf("second execute performed a duplicate write")
	}
	if got := atomic.LoadInt32(writes); got != 1 {
		t.Fatalf("external writes = %d; want exactly 1 (EXE-002)", got)
	}
}

// TestClaimAndWrite_ConcurrentSingleWrite is the EXE-002 concurrency proof at the
// claim mechanism: N goroutines racing the SAME idempotency key produce EXACTLY
// ONE external write and EXACTLY ONE action_executions row — the UI-double-click /
// retry-races-original scenario the web review confirmed is reachable.
func TestClaimAndWrite_ConcurrentSingleWrite(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	card, native := seedApprovedCard(t, pool, q)
	srv, writes := countingMockDK(t)

	writer := NewHTTPWriter(srv.URL, "tok", srv.Client())
	svc := NewService(pool, recommendation.NewService(pool), writer, fakeResolver{ctx: enabledContext(card, native)})
	req := WriteRequest{IdempotencyKey: card.IdempotencyKey, VariantNativeID: native, PriceMantissa: card.PriceMantissa, PriceCurrency: card.PriceCurrency, PriceExponent: int8(card.PriceExponent)}

	const n = 12
	var wrote int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start // release all goroutines simultaneously.
			_, _, didWrite, err := svc.claimAndWrite(ctx, card, req)
			if err != nil {
				t.Errorf("claimAndWrite: %v", err)
				return
			}
			if didWrite {
				atomic.AddInt32(&wrote, 1)
			}
		}()
	}
	close(start)
	wg.Wait()

	if got := atomic.LoadInt32(&wrote); got != 1 {
		t.Fatalf("claims that wrote = %d; want exactly 1", got)
	}
	if got := atomic.LoadInt32(writes); got != 1 {
		t.Fatalf("external writes = %d; want exactly 1 (EXE-002 concurrent)", got)
	}
	var rows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM action_executions WHERE action_id = $1`, card.ActionID).Scan(&rows); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if rows != 1 {
		t.Fatalf("action_executions rows = %d; want exactly 1", rows)
	}
}

// TestExecute_ConcurrentExecuteSingleWrite is the end-to-end concurrency proof: N
// goroutines firing full Execute for the SAME approved card produce exactly one
// external write and one execution row. The §8.4 FROM-guard serialises the card
// while the idempotency claim guards the write; either way, zero duplicates.
func TestExecute_ConcurrentExecuteSingleWrite(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	card, native := seedApprovedCard(t, pool, q)
	srv, writes := countingMockDK(t)

	writer := NewHTTPWriter(srv.URL, "tok", srv.Client())
	svc := NewService(pool, recommendation.NewService(pool), writer, fakeResolver{ctx: enabledContext(card, native)})

	const n = 10
	var succeeded int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			res, err := svc.Execute(ctx, card.ID, audit.Actor{ID: "owner-1", Role: "owner", Surface: "screen"})
			if err == nil && res.Mode == ModeWrite {
				atomic.AddInt32(&succeeded, 1)
			}
		}()
	}
	close(start)
	wg.Wait()

	if atomic.LoadInt32(&succeeded) < 1 {
		t.Fatalf("no Execute call succeeded")
	}
	if got := atomic.LoadInt32(writes); got != 1 {
		t.Fatalf("external writes = %d; want exactly 1 (concurrent Execute)", got)
	}
	var rows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM action_executions WHERE action_id = $1`, card.ActionID).Scan(&rows); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if rows != 1 {
		t.Fatalf("action_executions rows = %d; want exactly 1", rows)
	}
}

// TestExecute_DefaultConfigCannotWrite is the OFF-by-default proof: with the
// default enablement (capability Unsupported, region unverified) an Approved card
// performs NO external write and is tracked recommend-only instead.
func TestExecute_DefaultConfigCannotWrite(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	card, native := seedApprovedCard(t, pool, q)
	srv, writes := countingMockDK(t)

	rc := enabledContext(card, native)
	rc.Enablement = WriteEnablement{} // default OFF (both keys false)
	rc.VariantID = variantOfCard(t, pool, card)

	writer := NewHTTPWriter(srv.URL, "tok", srv.Client())
	svc := NewService(pool, recommendation.NewService(pool), writer, fakeResolver{ctx: rc})

	res, err := svc.Execute(ctx, card.ID, audit.Actor{ID: "owner-1", Role: "owner", Surface: "screen"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Mode != ModeRecommendOnly || res.RecommendOnlyState != StateAwaitingExternalExecution {
		t.Fatalf("default config: mode=%q state=%q; want recommend_only/awaiting", res.Mode, res.RecommendOnlyState)
	}
	if got := atomic.LoadInt32(writes); got != 0 {
		t.Fatalf("default config performed %d external writes; want 0", got)
	}
	// The card must NOT have advanced past Approved.
	after, err := q.GetApprovalCard(ctx, card.ID)
	if err != nil {
		t.Fatalf("get card: %v", err)
	}
	if after.State != string(approval.StateApproved) {
		t.Fatalf("default config advanced card to %q; want it to remain approved", after.State)
	}
}

// TestExecute_UnknownResultParksPendingAndRetryRejected proves EXE-003: an unknown
// write result parks in Pending Reconciliation and the retry endpoint refuses it.
func TestExecute_UnknownResultParksPendingAndRetryRejected(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	card, native := seedApprovedCard(t, pool, q)

	svc := NewService(pool, recommendation.NewService(pool), unknownWriter{}, fakeResolver{ctx: enabledContext(card, native)})

	res, err := svc.Execute(ctx, card.ID, audit.Actor{ID: "owner-1", Role: "owner", Surface: "screen"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.ExternalState != StatePendingReconciliation {
		t.Fatalf("unknown result: state=%q; want pending_reconciliation", res.ExternalState)
	}
	if _, err := svc.Retry(ctx, card.ActionID, audit.Actor{ID: "owner-1"}); err != ErrUnreconciled {
		t.Fatalf("retry of pending action: want ErrUnreconciled, got %v", err)
	}
}

// unknownWriter always reports an ambiguous result (timeout-like) to exercise the
// fail-closed Pending Reconciliation path.
type unknownWriter struct{}

func (unknownWriter) WritePrice(_ context.Context, _ WriteRequest) WriteResult {
	return WriteResult{Outcome: OutcomeUnknown, Detail: "simulated timeout"}
}

// variantOfCard reads the variant id for a card's recommendation (for recommend-
// only seeding).
func variantOfCard(t *testing.T, pool *pgxpool.Pool, card db.ApprovalCard) uuid.UUID {
	t.Helper()
	var variant uuid.UUID
	if err := pool.QueryRow(context.Background(),
		`SELECT variant_id FROM recommendations WHERE id = $1`, card.RecommendationID).Scan(&variant); err != nil {
		t.Fatalf("variant of card: %v", err)
	}
	return variant
}
