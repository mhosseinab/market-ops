// This file holds S32 cross-plane SYSTEM tests: they drive the REAL mounted
// HTTP gateway (NewServer, the same constructor production wires), a REAL
// Postgres-backed service layer, and a REAL mockdk HTTP server — never a
// direct in-process call into a single package's exported function bypassing
// the wire. It lives in package httpapi (not a separate tests/integration
// package) because the gen-go-boundary depguard rule
// (dk-p0-monorepo.md §5 — "only internal/httpapi may import gen/go") confines
// gateway.* request/response types to this package; a system test that drives
// real HTTP traffic necessarily constructs those types.
//
// Each DB-backed test skips (not fails) when DATABASE_URL is unset, so the
// suite degrades gracefully outside the compose-based `task test:integration`
// CI job while still running wherever a Postgres instance is reachable
// (including a native local one — see docs/implementation/dk-p0-progress.md).
package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	gateway "github.com/mhosseinab/market-ops/gen/go"
	"github.com/mhosseinab/market-ops/services/core/internal/approval"
	"github.com/mhosseinab/market-ops/services/core/internal/audit"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/execution"
	"github.com/mhosseinab/market-ops/services/core/internal/mockdk"
	"github.com/mhosseinab/market-ops/services/core/internal/perm"
	"github.com/mhosseinab/market-ops/services/core/internal/recommendation"
)

// newSystemPool connects to DATABASE_URL (schema applied via `task db:reset`).
// Skips when unset, exactly like the S18 execution package's own DB-backed
// suite. Named distinctly from other packages' newPool since this is the same
// package as auth_test.go/approval_test.go and must not collide.
func newSystemPool(t *testing.T) (*pgxpool.Pool, *db.Queries) {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set; skipping system integration test")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("connect pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool, db.New(pool)
}

// countingMockDK wraps the offline mockdk handler and counts external price
// writes. Reused from the S18 proof pattern, at the HTTP layer this time.
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

// seedApprovedCardSystem seeds account/variant/recommendation/card and
// advances the card to Approved through the legal §8.4 path (mirrors
// internal/execution/service_db_test.go's seedApprovedCard — duplicated here
// rather than exported cross-package, since it is test-only fixture setup,
// not product logic; named distinctly to avoid any future collision in this
// package).
func seedApprovedCardSystem(t *testing.T, pool *pgxpool.Pool, q *db.Queries) (db.ApprovalCard, int64, uuid.UUID) {
	t.Helper()
	ctx := context.Background()

	org, err := q.CreateOrganization(ctx, "s32-dup-write-"+uuid.NewString())
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	acct, err := q.CreateMarketplaceAccount(ctx, db.CreateMarketplaceAccountParams{
		OrganizationID: org.ID, NativeAccountID: "native-" + uuid.NewString(), DisplayName: "S32 Seller",
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
			readiness, evidence_quality)
		VALUES ($1,$2,$3,1,'maximize_contribution',100000,'IRR',0,'complete','verified')
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
	return approved, nativeVariant, org.ID
}

// fixedResolver returns an always-write-enabled RevalidationContext bound to
// the seeded card, exactly like internal/execution/service_db_test.go's
// fakeResolver.
type fixedResolver struct {
	card          db.ApprovalCard
	nativeVariant int64
}

func (f fixedResolver) Resolve(_ context.Context, card db.ApprovalCard) (execution.RevalidationContext, error) {
	inputs := execution.RevalidationInputs{
		Now: time.Now(), IdentityConfirmed: true, CurrentPriceMatches: true,
		BoundaryKnown: true, PermissionGranted: true, JITFresh: true,
	}
	current := approval.Binding{
		ActionID: card.ActionID, ParameterVersion: card.ParameterVersion,
		ContextVersion: card.ContextVersion, PolicyVersion: card.PolicyVersion,
		CostProfileVersion: card.CostProfileVersion, Expiry: card.ExpiresAt,
	}
	inputs.Current = current
	inputs.Bound = current
	return execution.RevalidationContext{
		Inputs:          inputs,
		Enablement:      execution.WriteEnablement{CapabilitySupported: true, RegionWriteVerified: true},
		Actor:           audit.Actor{ID: "owner-1", Role: "owner", Surface: "screen"},
		AccountID:       card.MarketplaceAccountID,
		VariantNativeID: f.nativeVariant,
	}, nil
}

// systemOwnerServerForOrg builds a real gateway.NewServer with the Owner session bound to a
// SPECIFIC organization (issue #102): tenant-scoped routes resolve the principal's
// organization to its marketplace account, so a card/action/outcome read or write
// only succeeds when the session's org owns the seeded resource. Passing the
// seeded account's org id is what makes the happy-path tenant checks pass; a
// mismatched org would (correctly) return a uniform not-found.
func systemOwnerServerForOrg(t *testing.T, organizationID uuid.UUID, opts ...Option) (*http.Server, string) {
	t.Helper()
	fa := newFakeAuth()
	owner := principal(perm.RoleOwner)
	owner.OrganizationID = organizationID
	const tok = "s32-owner-session"
	fa.principals[tok] = owner
	base := []Option{WithAuth(fa), WithCookieSecure(false)}
	srv := NewServer(":0", BuildInfo{}, testLogger(), append(base, opts...)...)
	return srv, tok
}

// TestSystemDuplicateWrite_ConcurrentDoubleConfirmExecute is the EXE-002
// SYSTEM proof (dk-p0-implementation-steps.md S32 item 5): unlike the S18
// service-level proof (internal/execution/service_db_test.go, which calls
// Execute() directly), this test fires TEN CONCURRENT HTTP POST
// /actions/execute requests for the SAME card — the real wire boundary a
// double-click or a retried request reaches — at the REAL mounted gateway
// router, backed by a REAL Postgres-backed execution.Service and a REAL
// mockdk HTTP server. It asserts exactly ONE external write reaches mockdk
// and exactly one response reports DidWrite=true.
func TestSystemDuplicateWrite_ConcurrentDoubleConfirmExecute(t *testing.T) {
	pool, q := newSystemPool(t)
	card, native, org := seedApprovedCardSystem(t, pool, q)

	dk, writes := countingMockDK(t)
	writer := execution.NewHTTPWriter(dk.URL, "tok", dk.Client())
	execSvc := execution.NewService(pool, recommendation.NewService(pool), writer, fixedResolver{card: card, nativeVariant: native})

	srv, tok := systemOwnerServerForOrg(t, org, WithExecution(execSvc))

	body, err := json.Marshal(gateway.ExecuteActionRequest{CardId: card.ID})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	doExecute := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/actions/execute", strings.NewReader(string(body)))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: tok})
		rec := httptest.NewRecorder()
		srv.Handler.ServeHTTP(rec, req)
		return rec
	}

	// 10 concurrent double(+)-confirm requests for the SAME approved card,
	// released simultaneously — the same concurrency shape as the S18
	// service-level proof, but through the wire (HTTP) boundary a real
	// double-click or a client retry actually reaches. The §8.4 FROM-guarded
	// state transition serialises the race: exactly one caller can observe
	// Approved→Executing, so every OTHER concurrent request legitimately loses
	// the race rather than silently duplicating the write — that is the
	// correct, fail-closed outcome, not a test bug.
	const n = 10
	var wg sync.WaitGroup
	results := make([]*httptest.ResponseRecorder, n)
	start := make(chan struct{})
	for i := range results {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results[i] = doExecute()
		}()
	}
	close(start)
	wg.Wait()

	didWriteCount := 0
	okCount := 0
	for i, rec := range results {
		switch rec.Code {
		case http.StatusOK:
			okCount++
			var out gateway.ExecuteActionResult
			if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
				t.Fatalf("execute #%d: decode: %v", i, err)
			}
			if out.DidWrite {
				didWriteCount++
			}
		case http.StatusConflict:
			// Lost the §8.4 FROM-guarded race — no state mutated, no write attempted.
			// Two race-losses both surface here as 409 (issue #138, now resolved):
			//   - execution.ErrNotApproved: lost on the Approved gate check itself.
			//   - recommendation.ErrRejectedTransition: read the card as Approved but
			//     lost the FROM-guarded Approved→Revalidating advance.
			// Both are legitimate client-side conflicts, not bugs (matches S18's
			// TestExecute_ConcurrentExecuteSingleWrite tolerance).
		default:
			t.Fatalf("execute #%d: unexpected status = %d, body=%s", i, rec.Code, rec.Body.String())
		}
	}

	if okCount < 1 {
		t.Fatalf("concurrent double-confirm: no request observed the approved card (all %d lost the race) — infra bug, not a safety proof", n)
	}
	if didWriteCount != 1 {
		t.Fatalf("concurrent double-confirm: %d responses reported DidWrite=true; want exactly 1 (EXE-002)", didWriteCount)
	}
	if got := atomic.LoadInt32(writes); got != 1 {
		t.Fatalf("concurrent double-confirm: mockdk saw %d external writes; want exactly 1 (EXE-002)", got)
	}
	// Third, independent measurement (matching the S18 service-level sibling,
	// internal/execution/service_db_test.go): the persisted row count. This
	// closes the residual gap between "what the API reported" (DidWrite) / "what
	// mockdk observed" (write counter) and "what is actually persisted" —
	// reinforcing the EXE-002 exactly-one-write invariant at the wire boundary.
	var rows int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM action_executions WHERE action_id = $1`, card.ActionID).Scan(&rows); err != nil {
		t.Fatalf("count action_executions rows: %v", err)
	}
	if rows != 1 {
		t.Fatalf("concurrent double-confirm: action_executions rows = %d; want exactly 1 (EXE-002)", rows)
	}
}
