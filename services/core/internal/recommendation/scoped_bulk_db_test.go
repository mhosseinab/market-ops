package recommendation_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mhosseinab/market-ops/services/core/internal/approval"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/recommendation"
)

// seedTenant provisions a distinct tenant (org + marketplace account) and a variant
// under it, returning the organization id the caller authenticates as, the account
// id its bulk selection sets are owned by, and a variant id for seeding members.
func seedTenant(t *testing.T, q *db.Queries) (org, account, variant uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	o, err := q.CreateOrganization(ctx, "bulk-scope-"+uuid.NewString())
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	acct, err := q.CreateMarketplaceAccount(ctx, db.CreateMarketplaceAccountParams{
		OrganizationID:  o.ID,
		NativeAccountID: "native-" + uuid.NewString(),
		DisplayName:     "Bulk Scope Seller",
	})
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	return o.ID, acct.ID, seedSecondVariant(t, q, acct.ID)
}

// TestConfirmBulkSelectionForOrg_CrossTenantSetFailsClosed is the issue #102 negative
// test (never-cut tenant integrity, PRD §4.6) for the bulk path re-implemented on top
// of #90's authoritative ConfirmBulkSelection. #90 derives the tenant from the
// selection set itself and enforces per-MEMBER account_mismatch, but does NOT gate the
// SET against the CALLER (its lineage lookup is unscoped). ConfirmBulkSelectionForOrg
// closes that gap: a caller in tenant B, presenting a valid resolvable account, cannot
// confirm a bulk selection whose lineage belongs to tenant A. The foreign lineage is
// indistinguishable from a missing one (uniform pgx.ErrNoRows → 404, no existence
// oracle), authorizes NOTHING, and leaves A's member card + its lineage untouched.
func TestConfirmBulkSelectionForOrg_CrossTenantSetFailsClosed(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	svc := recommendation.NewService(pool).SetExecutionDispatcher(realDispatcherFor(t, pool))

	// Tenant A owns an executable selection set (one AwaitingConfirmation member card).
	orgA, accountA, variantA := seedTenant(t, q)
	cardA := awaitingCard(t, svc, accountA, variantA)
	lineageA, versionA := previewExecutableSet(t, svc, accountA, variantA, cardA)

	// Tenant B is a fully-provisioned, authorized attacker with its own account.
	orgB, _, _ := seedTenant(t, q)

	// B confirms A's lineage → fail closed (uniform not-found), authorize NOTHING.
	out, err := svc.ConfirmBulkSelectionForOrg(ctx, orgB, lineageA, versionA, time.Now().UTC())
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("cross-tenant bulk confirm: err=%v; want pgx.ErrNoRows (uniform not-found)", err)
	}
	if out.Valid || out.ExecutionPending || len(out.Items) != 0 {
		t.Fatalf("cross-tenant bulk confirm leaked an outcome: %+v", out)
	}
	// No side effect: A's member card stays a live control, no execution intent.
	if got := reloadState(t, svc, cardA.ID); got != approval.StateAwaitingConfirmation {
		t.Fatalf("A's member card advanced to %s under B's confirm; want awaiting_confirmation", got)
	}
	if got := countIntents(t, pool, cardA.ID); got != 0 {
		t.Fatalf("cross-tenant confirm enqueued %d execution intents; want 0", got)
	}

	// An org with NO resolvable account also fails closed (no bulk path exists).
	if _, err := svc.ConfirmBulkSelectionForOrg(ctx, uuid.New(), lineageA, versionA, time.Now().UTC()); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("org with no account: err=%v; want pgx.ErrNoRows", err)
	}

	// Positive control: A's OWN operator confirms A's lineage and authorizes the member,
	// proving the org gate does not break the owner's authoritative #90 flow.
	ownOut, err := svc.ConfirmBulkSelectionForOrg(ctx, orgA, lineageA, versionA, time.Now().UTC())
	if err != nil {
		t.Fatalf("owner bulk confirm: %v", err)
	}
	if !ownOut.Valid || !ownOut.ExecutionPending {
		t.Fatalf("owner bulk confirm: valid=%v pending=%v; want both true", ownOut.Valid, ownOut.ExecutionPending)
	}
	if st := itemFor(t, ownOut.Items, cardA.RecommendationID).State; st != recommendation.BulkItemAuthorized {
		t.Fatalf("owner member state = %s; want authorized", st)
	}
	if got := reloadState(t, svc, cardA.ID); got != approval.StateApproved {
		t.Fatalf("owner-confirmed member card = %s; want approved", got)
	}
}
