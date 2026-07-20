package identity_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mhosseinab/market-ops/services/core/internal/identity"
)

// orgForAccount resolves the organization that owns a marketplace account, so the
// tenant-scoped NeedsReviewQueueForOrg read can be exercised with a real
// principal→org context.
func orgForAccount(t *testing.T, pool *pgxpool.Pool, account uuid.UUID) uuid.UUID {
	t.Helper()
	var org uuid.UUID
	if err := pool.QueryRow(context.Background(),
		`SELECT organization_id FROM marketplace_accounts WHERE id=$1`, account).Scan(&org); err != nil {
		t.Fatalf("resolve org for account: %v", err)
	}
	return org
}

// TestNeedsReviewQueueForOrgIsTenantScoped is the issue #346 tenant-quarantine
// proof: organization A's Needs Review queue (supplier codes / native product ids)
// can NEVER be read by organization B naming A's account id. A foreign account
// yields the SAME uniform not-found (ErrAccountNotFound) a genuinely-absent account
// would, so it is not an existence oracle. An org-less caller (OrganizationID ==
// uuid.Nil) fails closed BEFORE any read. Organization A's own read succeeds
// (positive control). Cross-tenant disclosure here is a §4.6 identity-quarantine
// breach.
func TestNeedsReviewQueueForOrgIsTenantScoped(t *testing.T) {
	pool, q := newPool(t)
	svc := identity.NewService(pool, nil)
	ctx := context.Background()

	// A owns a variant with a pending (NeedsReview) candidate → a non-empty queue.
	accountA, variantA, _, _ := seedVariant(t, q)
	orgA := orgForAccount(t, pool, accountA)
	candidateFor(t, svc, accountA, variantA)

	// B is a separate tenant.
	accountB, _, _, _ := seedVariant(t, q)
	orgB := orgForAccount(t, pool, accountB)

	// Organization B naming A's account id → uniform not-found, never A's queue.
	if _, err := svc.NeedsReviewQueueForOrg(ctx, orgB, accountA); !errors.Is(err, identity.ErrAccountNotFound) {
		t.Fatalf("B reading A's queue: err=%v, want ErrAccountNotFound (uniform not-found, no oracle)", err)
	}

	// Org-less principal (no resolvable account) fails closed before any read.
	if _, err := svc.NeedsReviewQueueForOrg(ctx, uuid.Nil, accountA); !errors.Is(err, identity.ErrAccountNotFound) {
		t.Fatalf("org-less caller: err=%v, want ErrAccountNotFound (fails closed)", err)
	}

	// Organization A reading a DIFFERENT account than its own (even a real one) is
	// still rejected — ownership is asserted, not just resolvability.
	if _, err := svc.NeedsReviewQueueForOrg(ctx, orgA, accountB); !errors.Is(err, identity.ErrAccountNotFound) {
		t.Fatalf("A naming B's account: err=%v, want ErrAccountNotFound", err)
	}

	// Positive control: organization A reads its OWN queue.
	items, err := svc.NeedsReviewQueueForOrg(ctx, orgA, accountA)
	if err != nil {
		t.Fatalf("A reading its own queue: err=%v, want ok", err)
	}
	if len(items) != 1 || items[0].VariantID != variantA {
		t.Fatalf("A's own queue = %+v, want the one pending candidate for %v", items, variantA)
	}
}
