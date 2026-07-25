package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	gateway "github.com/mhosseinab/market-ops/gen/go"
	"github.com/mhosseinab/market-ops/services/core/internal/approval"
	"github.com/mhosseinab/market-ops/services/core/internal/auth"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/execution"
	"github.com/mhosseinab/market-ops/services/core/internal/perm"
	"github.com/mhosseinab/market-ops/services/core/internal/recommendation"
)

// ── PD-4 rule (1) HTTP-consumer regression for issue #106 ────────────────────
//
// The authoritative Actions projection is: current pre-execution lineage heads
// UNION execution-bearing historical card versions, each execution overlay bound
// to its EXACT (actionId, cardId). These tests build that shape in a REAL
// Postgres through production queries only — an older recommend-only-tracked
// Approved card version and a newer Draft SHARING ONE action ID — and assert it
// through the real HTTP consumer (GET /actions), never through a fake that
// supplies rows the SQL cannot produce.

// projectionFixture is the seeded (older executed version, newer Draft head) pair
// plus the identities needed to authenticate as its owner.
type projectionFixture struct {
	org     uuid.UUID
	account uuid.UUID
	variant uuid.UUID
	// executed is the OLDER card version that carries the recommend-only action.
	executed db.ApprovalCard
	// head is the NEWER Draft minted on the SAME lineage and SAME action id.
	head db.ApprovalCard
}

// seedActionsProjection seeds org → account → product → variant → recommendation
// → approval card v1, drives v1 to Approved through the REAL §8.4 state machine,
// tracks it recommend-only with the SAME production query the execution service
// uses (InsertRecommendOnlyAction — which leaves the card Approved), then mints a
// newer Draft v2 in the SAME lineage preserving the action id, exactly as
// recommendation.EditPrice does.
func seedActionsProjection(t *testing.T, pool *pgxpool.Pool, q *db.Queries) projectionFixture {
	t.Helper()
	ctx := context.Background()

	org, err := q.CreateOrganization(ctx, "actions-proj-"+uuid.NewString())
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	acct, err := q.CreateMarketplaceAccount(ctx, db.CreateMarketplaceAccountParams{
		OrganizationID:  org.ID,
		NativeAccountID: "native-" + uuid.NewString(),
		DisplayName:     "Projection Seller",
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

	cardLineage := uuid.New()
	actionID := uuid.New()
	binding := approval.Binding{
		ActionID: actionID, ParameterVersion: 1, ContextVersion: 1,
		PolicyVersion: 1, CostProfileVersion: 1, Expiry: time.Now().Add(30 * time.Minute),
	}
	v1, err := q.InsertApprovalCard(ctx, db.InsertApprovalCardParams{
		RecommendationID: recID, MarketplaceAccountID: acct.ID, LineageID: cardLineage,
		ActionID: actionID, ParameterVersion: 1, ContextVersion: 1, PolicyVersion: 1, CostProfileVersion: 1,
		EvidenceVersions: []byte("{}"), IdempotencyKey: binding.IdempotencyKey(),
		State: string(approval.StateDraft), PriceMantissa: 95000, PriceCurrency: "IRR", PriceExponent: 0,
		ExpiresAt: binding.Expiry,
	})
	if err != nil {
		t.Fatalf("insert card v1: %v", err)
	}
	rec := recommendation.NewService(pool)
	for _, step := range []struct{ from, to approval.State }{
		{approval.StateDraft, approval.StateReadyForReview},
		{approval.StateReadyForReview, approval.StateAwaitingConfirmation},
		{approval.StateAwaitingConfirmation, approval.StateApproved},
	} {
		if _, err := rec.Advance(ctx, v1.ID, step.from, step.to, "seed"); err != nil {
			t.Fatalf("advance %s→%s: %v", step.from, step.to, err)
		}
	}
	v1, err = q.GetApprovalCard(ctx, v1.ID)
	if err != nil {
		t.Fatalf("reload v1: %v", err)
	}

	// Track it recommend-only — the DEFAULT production execution mode while writes
	// are dark. The card stays Approved (execution.Service.recordRecommendOnly).
	now := time.Now().UTC()
	if _, err := q.InsertRecommendOnlyAction(ctx, db.InsertRecommendOnlyActionParams{
		CardID: v1.ID, ActionID: v1.ActionID, MarketplaceAccountID: acct.ID, VariantID: variant.ID,
		ApprovedPriceMantissa: v1.PriceMantissa, ApprovedPriceCurrency: v1.PriceCurrency,
		ApprovedPriceExponent: v1.PriceExponent,
		ApprovedAt:            now, WindowExpiresAt: now.Add(24 * time.Hour),
	}); err != nil {
		t.Fatalf("insert recommend-only action: %v", err)
	}

	// A NEWER Draft in the SAME lineage, SAME action id, NEW parameter version —
	// exactly the shape recommendation.EditPrice mints.
	editBinding := approval.Binding{
		ActionID: actionID, ParameterVersion: v1.ParameterVersion + 1, ContextVersion: 1,
		PolicyVersion: 1, CostProfileVersion: 1, Expiry: time.Now().Add(time.Hour),
	}
	v2, err := q.InsertApprovalCard(ctx, db.InsertApprovalCardParams{
		RecommendationID: recID, MarketplaceAccountID: acct.ID, LineageID: cardLineage,
		ActionID: actionID, ParameterVersion: editBinding.ParameterVersion, ContextVersion: 1,
		PolicyVersion: 1, CostProfileVersion: 1,
		EvidenceVersions: []byte("{}"), IdempotencyKey: editBinding.IdempotencyKey(),
		State: string(approval.StateDraft), PriceMantissa: 96000, PriceCurrency: "IRR", PriceExponent: 0,
		ExpiresAt: editBinding.Expiry,
	})
	if err != nil {
		t.Fatalf("insert card v2: %v", err)
	}
	if v2.Version <= v1.Version {
		t.Fatalf("v2 version %d must exceed v1 version %d", v2.Version, v1.Version)
	}

	return projectionFixture{org: org.ID, account: acct.ID, variant: variant.ID, executed: v1, head: v2}
}

// actionsServer wires the REAL approval + execution services over the real pool,
// with the fake auth resolving a session principal for the fixture's org.
func actionsServer(t *testing.T, pool *pgxpool.Pool, f projectionFixture, token string) *http.Server {
	t.Helper()
	fa := newFakeAuth()
	fa.principals[token] = auth.Principal{
		UserID:         uuid.New(),
		OrganizationID: f.org,
		Email:          "owner@x.io",
		Role:           perm.RoleOwner,
		ExpiresAt:      time.Now().Add(time.Hour).UTC(),
	}
	rec := recommendation.NewService(pool)
	// A read-only list needs no writer/resolver: ListUnifiedByCardIDsForOrg reads
	// the pool only. Leaving them nil keeps this test incapable of any write.
	exec := execution.NewService(pool, rec, nil, nil)
	return NewServer(":0", BuildInfo{}, testLogger(),
		WithAuth(fa), WithApproval(rec), WithExecution(exec), WithCookieSecure(false))
}

func listActions(t *testing.T, srv *http.Server, token, account, state string) gateway.ActionList {
	t.Helper()
	path := "/actions?marketplaceAccountId=" + account
	if state != "" {
		path += "&state=" + state
	}
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: token})
	res := httptest.NewRecorder()
	srv.Handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("GET %s: status = %d, body = %s", path, res.Code, res.Body.String())
	}
	var out gateway.ActionList
	if err := json.Unmarshal(res.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode ActionList: %v", err)
	}
	return out
}

func summaryByID(items []gateway.ActionSummary) map[uuid.UUID]gateway.ActionSummary {
	out := make(map[uuid.UUID]gateway.ActionSummary, len(items))
	for _, i := range items {
		out[i.Id] = i
	}
	return out
}

// TestListActions_ExecutedCardVisibleAndOverlayBoundToExactCard is the PD-4
// regression through the production HTTP consumer (EXE-005 / OUT-001 / AUD-001).
//
// With an older recommend-only-tracked card version and a newer Draft sharing ONE
// action id, GET /actions must:
//   - return BOTH rows (the terminal executed version stays addressable forever);
//   - bind the execution overlay to the EXACT (actionId, cardId) — the executed
//     version carries it, the pre-execution Draft head carries NONE of it;
//   - never give a recommend-only action a write externalState (a false write
//     claim is a never-cut violation).
func TestListActions_ExecutedCardVisibleAndOverlayBoundToExactCard(t *testing.T) {
	pool, q := newIntegrationPool(t)
	f := seedActionsProjection(t, pool, q)
	srv := actionsServer(t, pool, f, "tok-owner")

	got := summaryByID(listActions(t, srv, "tok-owner", f.account.String(), "").Items)

	executed, ok := got[f.executed.ID]
	if !ok {
		t.Fatalf("GET /actions omitted the execution-bearing card version %s — it must remain addressable through the common action API (PD-4 rule 1)", f.executed.ID)
	}
	head, ok := got[f.head.ID]
	if !ok {
		t.Fatalf("GET /actions omitted the current Draft head %s", f.head.ID)
	}

	// The executed version carries its own overlay, faithfully mirroring the
	// durable recommend-only row (and NEVER a write claim — never-cut).
	assertRecommendOnlyOverlay(t, q, executed, f.executed.ActionID)

	// The pre-execution Draft head carries NO overlay at all: the overlay is bound
	// to the exact card version, not to the shared action lineage.
	if head.ExecutionMode != nil || head.CanonicalState != nil ||
		head.ExternalState != nil || head.RecommendOnlyState != nil {
		t.Fatalf("pre-execution Draft head %s wrongly inherited the executed version's overlay (mode=%v canonical=%v external=%v recommendOnly=%v) — the overlay must bind to the EXACT (actionId, cardId)",
			f.head.ID, head.ExecutionMode, head.CanonicalState, head.ExternalState, head.RecommendOnlyState)
	}

	// Approval versioning is never-cut: each row keeps its OWN card version.
	if executed.Version != int64(f.executed.Version) || head.Version != int64(f.head.Version) {
		t.Fatalf("projected versions = executed %d / head %d; want %d / %d (versions must never be collapsed or re-stamped)",
			executed.Version, head.Version, f.executed.Version, f.head.Version)
	}
}

// TestListActions_ExecutedCardVisibleUnderStateFilter proves the §8.4 state
// filter stays authoritative over the UNIONED projection (issue #142 must not
// regress) and still finds the executed version: it is Approved while its lineage
// head is Draft, so state=approved returns exactly the executed version — with its
// overlay — and state=draft returns exactly the head, with none.
func TestListActions_ExecutedCardVisibleUnderStateFilter(t *testing.T) {
	pool, q := newIntegrationPool(t)
	f := seedActionsProjection(t, pool, q)
	srv := actionsServer(t, pool, f, "tok-owner")

	approved := listActions(t, srv, "tok-owner", f.account.String(), string(approval.StateApproved)).Items
	if len(approved) != 1 || approved[0].Id != f.executed.ID {
		t.Fatalf("GET /actions?state=approved returned %d rows (%v); want exactly the executed version %s",
			len(approved), summaryIDs(approved), f.executed.ID)
	}
	if approved[0].RecommendOnlyState == nil {
		t.Fatalf("executed version lost its recommend-only overlay under the state filter")
	}

	drafts := listActions(t, srv, "tok-owner", f.account.String(), string(approval.StateDraft)).Items
	if len(drafts) != 1 || drafts[0].Id != f.head.ID {
		t.Fatalf("GET /actions?state=draft returned %d rows (%v); want exactly the head %s",
			len(drafts), summaryIDs(drafts), f.head.ID)
	}
	if drafts[0].ExecutionMode != nil || drafts[0].RecommendOnlyState != nil {
		t.Fatalf("Draft head carries an execution overlay under the state filter")
	}
}

// TestListActions_ForeignAccountIsUniformNotFound is the issue #102 negative over
// the widened projection: another organization's account id is a uniform 404
// through GET /actions — never another tenant's executed cards.
func TestListActions_ForeignAccountIsUniformNotFound(t *testing.T) {
	pool, q := newIntegrationPool(t)
	mine := seedActionsProjection(t, pool, q)
	theirs := seedActionsProjection(t, pool, q)
	srv := actionsServer(t, pool, mine, "tok-owner")

	req := httptest.NewRequest(http.MethodGet, "/actions?marketplaceAccountId="+theirs.account.String(), nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "tok-owner"})
	res := httptest.NewRecorder()
	srv.Handler.ServeHTTP(res, req)
	if res.Code != http.StatusNotFound {
		t.Fatalf("foreign account: status = %d, want 404 (uniform not-found), body = %s", res.Code, res.Body.String())
	}

	// And the own-account queue never contains the foreign account's rows.
	own := summaryByID(listActions(t, srv, "tok-owner", mine.account.String(), "").Items)
	if _, leaked := own[theirs.executed.ID]; leaked {
		t.Fatalf("foreign executed card %s leaked into account %s's queue", theirs.executed.ID, mine.account)
	}
}

// TestListActions_FailsClosedWithoutExecutionPlane is the observability/
// fail-closed negative for the widened projection (§4.6: no silent fallback).
//
// The list now contains execution-bearing card versions whose mode and canonical
// state come ENTIRELY from the execution overlay. With the execution plane
// unwired, every executed row would render with no overlay — indistinguishable
// from a pre-execution card, i.e. a terminal executed action silently displayed
// as "not executed yet". That is exactly the misleading degradation the never-cut
// rules forbid, so the route fails closed with the same structured 503 every other
// execution-dependent route returns, rather than serving a half-truthful queue.
func TestListActions_FailsClosedWithoutExecutionPlane(t *testing.T) {
	pool, q := newIntegrationPool(t)
	f := seedActionsProjection(t, pool, q)

	fa := newFakeAuth()
	fa.principals["tok-owner"] = auth.Principal{
		UserID: uuid.New(), OrganizationID: f.org, Email: "owner@x.io",
		Role: perm.RoleOwner, ExpiresAt: time.Now().Add(time.Hour).UTC(),
	}
	// Approval wired, execution DELIBERATELY absent.
	srv := NewServer(":0", BuildInfo{}, testLogger(),
		WithAuth(fa), WithApproval(recommendation.NewService(pool)), WithCookieSecure(false))

	req := httptest.NewRequest(http.MethodGet, "/actions?marketplaceAccountId="+f.account.String(), nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "tok-owner"})
	res := httptest.NewRecorder()
	srv.Handler.ServeHTTP(res, req)

	if res.Code != http.StatusServiceUnavailable {
		t.Fatalf("unwired execution plane: status = %d, want 503 (fail closed) — a queue that cannot label execution state must not be served, body = %s",
			res.Code, res.Body.String())
	}
	// NEGATIVE: no action rows are disclosed on the fail-closed path.
	var list gateway.ActionList
	if err := json.Unmarshal(res.Body.Bytes(), &list); err == nil && len(list.Items) > 0 {
		t.Fatalf("fail-closed response leaked %d action rows", len(list.Items))
	}
}

func summaryIDs(items []gateway.ActionSummary) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(items))
	for _, i := range items {
		out = append(out, i.Id)
	}
	return out
}
