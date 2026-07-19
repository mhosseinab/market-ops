package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	gateway "github.com/mhosseinab/market-ops/gen/go"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/execution"
	"github.com/mhosseinab/market-ops/services/core/internal/outcome"
	"github.com/mhosseinab/market-ops/services/core/internal/recommendation"
)

// This file is the issue #102 LAYER 1 proof at the real HTTP boundary: an authorized
// operator (Owner) in account B, presenting a valid session, cannot read or mutate a
// card / recommendation / execution / outcome owned by account A. Every cross-account
// request returns a uniform not-found with NO disclosure and NO side effect, while
// the SAME request from account A's operator succeeds (positive control). Scope is
// derived from the session principal's organization, never from the request body.

// seedOrgAccount creates a fresh org + marketplace account (a distinct tenant).
func seedOrgAccount(t *testing.T, q *db.Queries) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	org, err := q.CreateOrganization(ctx, "tenant-102-"+uuid.NewString())
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	if _, err := q.CreateMarketplaceAccount(ctx, db.CreateMarketplaceAccountParams{
		OrganizationID: org.ID, NativeAccountID: "native-" + uuid.NewString(), DisplayName: "Tenant B",
	}); err != nil {
		t.Fatalf("create account: %v", err)
	}
	return org.ID
}

func postJSON(t *testing.T, srv *http.Server, tok, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: tok})
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)
	return rec
}

// TestTenantScoping_CrossAccountReadsAndMutationsAreNotFound is the headline Layer-1
// acceptance test: cross-account GET card / recommendation-detail / execution /
// outcome and cross-account CONFIRM / EXECUTE / RETRY / EDIT-PRICE all fail with a
// uniform not-found and leave NO state row / NO execution record behind.
func TestTenantScoping_CrossAccountReadsAndMutationsAreNotFound(t *testing.T) {
	pool, q := newSystemPool(t)
	ctx := context.Background()

	// Account A owns an Approved card + recommendation; open an outcome window on the
	// action too (so the outcome read has a real, owned target to hide from B).
	card, _, orgA := seedApprovedCardSystem(t, pool, q)
	outSvc := outcome.NewService(pool)
	if _, err := outSvc.OpenWindow(ctx, card.ActionID, card.ID); err != nil {
		t.Fatalf("open outcome window: %v", err)
	}

	// Account B is a fully-provisioned, authorized tenant — the realistic attacker.
	orgB := seedOrgAccount(t, q)

	apprSvc := recommendation.NewService(pool)
	execSvc := execution.NewService(pool, apprSvc, denyWriter{}, denyResolver{})

	opts := []Option{WithApproval(apprSvc), WithExecution(execSvc), WithOutcome(outSvc)}
	srvB, tokB := systemOwnerServerForOrg(t, orgB, opts...)
	srvA, tokA := systemOwnerServerForOrg(t, orgA, opts...)

	cardID := card.ID.String()
	recID := card.RecommendationID.String()
	actionID := card.ActionID.String()

	// --- Cross-account READS from B → uniform 404, no disclosure. --------------
	for _, tc := range []struct{ name, path string }{
		{"approval card", "/approvals/card?cardId=" + cardID},
		{"recommendation detail", "/recommendations/detail?recommendationId=" + recID},
		{"action execution", "/actions/execution?actionId=" + actionID},
		{"outcome", "/outcomes?actionId=" + actionID},
	} {
		rec := getJSON(t, srvB, tokB, tc.path, nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("cross-account GET %s: status=%d, want 404 (uniform not-found, no disclosure); body=%s", tc.name, rec.Code, rec.Body.String())
		}
	}

	// --- Cross-account MUTATIONS from B → 404/no-side-effect. ------------------
	confirm := postJSON(t, srvB, tokB, "/approvals/confirm", confirmBody(t, card.ID, card.ActionID, 1))
	if confirm.Code != http.StatusNotFound {
		t.Fatalf("cross-account confirm: status=%d, want 404; body=%s", confirm.Code, confirm.Body.String())
	}

	execBody, _ := json.Marshal(gateway.ExecuteActionRequest{CardId: card.ID})
	exec := postJSON(t, srvB, tokB, "/actions/execute", string(execBody))
	if exec.Code != http.StatusNotFound {
		t.Fatalf("cross-account execute: status=%d, want 404; body=%s", exec.Code, exec.Body.String())
	}

	retryBody, _ := json.Marshal(gateway.RetryActionRequest{ActionId: card.ActionID})
	retry := postJSON(t, srvB, tokB, "/actions/retry", string(retryBody))
	if retry.Code != http.StatusNotFound {
		t.Fatalf("cross-account retry: status=%d, want 404; body=%s", retry.Code, retry.Body.String())
	}

	editBody := `{"cardId":"` + cardID + `","newPrice":{"mantissa":"90000","currency":"IRR","exponent":0}}`
	edit := postJSON(t, srvB, tokB, "/approvals/card/edit-price", editBody)
	if edit.Code != http.StatusNotFound {
		t.Fatalf("cross-account edit-price: status=%d, want 404; body=%s", edit.Code, edit.Body.String())
	}

	// --- No side effects: A's card is untouched and no execution record exists. -
	after, err := q.GetApprovalCard(ctx, card.ID)
	if err != nil {
		t.Fatalf("reload card: %v", err)
	}
	if after.State != card.State {
		t.Fatalf("cross-account mutation changed A's card state %q -> %q (must be untouched)", card.State, after.State)
	}
	if after.Version != card.Version {
		t.Fatalf("cross-account edit-price minted a new version for A's card (must be untouched): %d -> %d", card.Version, after.Version)
	}
	if _, err := q.GetActionExecutionByAction(ctx, card.ActionID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("cross-account execute wrote an execution record for A's action (must be none): err=%v", err)
	}

	// --- Positive control: account A's own operator can read + still no leak. ---
	recA := getJSON(t, srvA, tokA, "/approvals/card?cardId="+cardID, nil)
	if recA.Code != http.StatusOK {
		t.Fatalf("account A's own card read: status=%d, want 200; body=%s", recA.Code, recA.Body.String())
	}
}

// TestTenantScoping_CrossAccountListsAreNotFound proves the account-parameter list
// reads (actions queue, outcomes list, ops queue) reject a foreign account id with a
// uniform not-found rather than returning another tenant's queue.
func TestTenantScoping_CrossAccountListsAreNotFound(t *testing.T) {
	pool, q := newSystemPool(t)
	ctx := context.Background()

	card, _, _ := seedApprovedCardSystem(t, pool, q)
	// The account that OWNS the seeded card (account A).
	accountA := card.MarketplaceAccountID
	_ = ctx

	orgB := seedOrgAccount(t, q)
	apprSvc := recommendation.NewService(pool)
	execSvc := execution.NewService(pool, apprSvc, denyWriter{}, denyResolver{})
	outSvc := outcome.NewService(pool)

	srvB, tokB := systemOwnerServerForOrg(t, orgB,
		WithApproval(apprSvc), WithExecution(execSvc), WithOutcome(outSvc))

	a := accountA.String()
	for _, tc := range []struct{ name, path string }{
		{"actions queue", "/actions?marketplaceAccountId=" + a},
		{"outcomes list", "/outcomes/list?marketplaceAccountId=" + a},
		{"ops queues", "/ops/queues?marketplaceAccountId=" + a},
	} {
		rec := getJSON(t, srvB, tokB, tc.path, nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("cross-account list %s for a foreign account id: status=%d, want 404 (never another tenant's queue); body=%s", tc.name, rec.Code, rec.Body.String())
		}
	}
}

// TestTenantScoping_IdempotencyKeysCannotLeakAcrossTenants proves the EXE-002
// idempotency key cannot be exploited cross-tenant (issue #102 acceptance test 5):
// the global UNIQUE(idempotency_key) is safe because (a) each account's card carries
// a DISTINCT key (derived from a per-card action id) and (b) a cross-account execute
// can never reach another account's card to claim or adopt its key — so account B's
// execute leaves account A's single execution record and its key untouched.
func TestTenantScoping_IdempotencyKeysCannotLeakAcrossTenants(t *testing.T) {
	pool, q := newSystemPool(t)
	ctx := context.Background()

	cardA, _, _ := seedApprovedCardSystem(t, pool, q)
	cardB, _, orgB := seedApprovedCardSystem(t, pool, q)

	// Distinct per-tenant keys: no cross-account idempotency collision by construction.
	if cardA.IdempotencyKey == cardB.IdempotencyKey {
		t.Fatal("two tenants' cards must not share an idempotency key (cross-tenant dedup collision)")
	}

	// Account B executes its OWN card for real (success), claiming its own key.
	execSvc := execution.NewService(pool, recommendation.NewService(pool), acceptWriter{}, fixedResolver{card: cardB, nativeVariant: 0})
	srvB, tokB := systemOwnerServerForOrg(t, orgB, WithExecution(execSvc))
	body, _ := json.Marshal(gateway.ExecuteActionRequest{CardId: cardB.ID})
	if rec := postJSON(t, srvB, tokB, "/actions/execute", string(body)); rec.Code != http.StatusOK {
		t.Fatalf("account B executing its OWN card: status=%d, body=%s", rec.Code, rec.Body.String())
	}

	// B now holds exactly one execution record keyed by B's key; A has none — B's
	// activity never reached, adopted, or collided with A's key space.
	bExec, err := q.GetActionExecutionByKey(ctx, cardB.IdempotencyKey)
	if err != nil {
		t.Fatalf("B's own execution record must exist: %v", err)
	}
	if bExec.CardID != cardB.ID {
		t.Fatalf("B's execution record bound to the wrong card: %s", bExec.CardID)
	}
	if _, err := q.GetActionExecutionByKey(ctx, cardA.IdempotencyKey); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("account A's key space must be untouched by B's execute: err=%v", err)
	}
}

// acceptWriter reports a definitively accepted write — used only for a same-account
// happy path (never a cross-account probe).
type acceptWriter struct{}

func (acceptWriter) WritePrice(context.Context, execution.WriteRequest) execution.WriteResult {
	return execution.WriteResult{Outcome: execution.OutcomeAccepted, ExternalRef: "ok"}
}

// denyWriter is a Writer that MUST never be called under a cross-account request
// (the ownership guard rejects the request before any write path is reached).
type denyWriter struct{}

func (denyWriter) WritePrice(context.Context, execution.WriteRequest) execution.WriteResult {
	panic("write attempted under a cross-account request — the tenant guard must reject before any external write (issue #102)")
}

// denyResolver is a Resolver that MUST never be called under a cross-account request
// (the ownership guard rejects before revalidation resolution).
type denyResolver struct{}

func (denyResolver) Resolve(context.Context, db.ApprovalCard) (execution.RevalidationContext, error) {
	panic("revalidation resolve attempted under a cross-account request — the tenant guard must reject first (issue #102)")
}
