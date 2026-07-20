package briefing_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mhosseinab/market-ops/services/core/internal/briefing"
	"github.com/mhosseinab/market-ops/services/core/internal/event"
)

// orgForAccount resolves the organization that owns a marketplace account, so the
// tenant-scoped GetForOrg read can be exercised with a real principal→org context.
func orgForAccount(t *testing.T, pool *pgxpool.Pool, account uuid.UUID) uuid.UUID {
	t.Helper()
	var org uuid.UUID
	if err := pool.QueryRow(context.Background(),
		`SELECT organization_id FROM marketplace_accounts WHERE id=$1`, account).Scan(&org); err != nil {
		t.Fatalf("resolve org for account: %v", err)
	}
	return org
}

// TestBriefingGetForOrgIsTenantScoped is the issue #131 tenant-quarantine proof: a
// generated daily briefing for organization A's account can NEVER be read by
// organization B naming A's account id. A foreign account yields the SAME uniform
// not-found (ErrAccountNotFound) a genuinely-absent briefing would, so it is not an
// existence oracle — while organization A's own read succeeds (positive control).
// Briefing feeds are personal data, so cross-tenant disclosure here is a §4.6 breach.
func TestBriefingGetForOrgIsTenantScoped(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()

	accountA, variantsA := seedAccountWithVariants(t, q, 1)
	orgA := orgForAccount(t, pool, accountA)
	accountB, _ := seedAccountWithVariants(t, q, 1)
	orgB := orgForAccount(t, pool, accountB)

	eventSvc := event.NewService(pool)
	bsvc := briefing.NewService(pool, eventSvc)

	// A owns a briefing for today.
	openLostWinning(t, eventSvc, accountA, variantsA[0], 5_000, bsvc.BusinessDay())
	if _, err := bsvc.GenerateForAccount(ctx, accountA); err != nil {
		t.Fatalf("generate A briefing: %v", err)
	}
	day := bsvc.BusinessDay()

	// Organization B naming A's account id → uniform not-found, never A's briefing.
	if _, err := bsvc.GetForOrg(ctx, orgB, accountA, day); !errors.Is(err, briefing.ErrAccountNotFound) {
		t.Fatalf("B reading A's briefing: err=%v, want ErrAccountNotFound (uniform not-found, no oracle)", err)
	}

	// Positive control: organization A reads its OWN briefing.
	b, err := bsvc.GetForOrg(ctx, orgA, accountA, day)
	if err != nil {
		t.Fatalf("A reading its own briefing: err=%v, want ok", err)
	}
	if len(b.Events) == 0 {
		t.Fatal("A's own briefing must carry its events")
	}
}
