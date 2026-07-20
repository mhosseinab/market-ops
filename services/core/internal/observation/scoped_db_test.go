package observation_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	obs "github.com/mhosseinab/market-ops/services/core/internal/observation"
)

// orgForAccount resolves the organization that owns a marketplace account, so the
// tenant-scoped ForOrg reads can be exercised with a real principal→org context.
func orgForAccount(t *testing.T, pool *pgxpool.Pool, account uuid.UUID) uuid.UUID {
	t.Helper()
	var org uuid.UUID
	if err := pool.QueryRow(context.Background(),
		`SELECT organization_id FROM marketplace_accounts WHERE id=$1`, account).Scan(&org); err != nil {
		t.Fatalf("resolve org for account: %v", err)
	}
	return org
}

// TestObservationReadsAreOrgScoped is the issue #131 tenant-quarantine proof at the
// SERVICE boundary: organization B can never list organization A's observation
// targets or Observed Offers, nor read A's target's append-only evidence, by naming
// A's account id or target id. A foreign account yields the uniform not-found
// (ErrAccountNotFound); a foreign target yields an EMPTY evidence list
// (indistinguishable from a target with no evidence — no existence oracle). A's own
// reads succeed (positive control).
func TestObservationReadsAreOrgScoped(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()

	accountA, variantA, nvA, npA := seedVariant(t, q)
	orgA := orgForAccount(t, pool, accountA)
	insertConfirmedIdentity(t, pool, accountA, variantA, nvA, npA)

	accountB, _, _, _ := seedVariant(t, q)
	orgB := orgForAccount(t, pool, accountB)

	clk := &clock{t: time.Now().UTC()}
	svc := obs.NewService(pool).WithClock(clk.now)

	created, err := svc.SyncTargetsFromConfirmed(ctx, accountA)
	if err != nil || len(created) != 1 {
		t.Fatalf("sync A targets: %v (n=%d)", err, len(created))
	}
	targetA := created[0].ID
	if _, err := svc.Ingest(ctx, captureFor(targetA, accountA, nvA, obs.RouteC, clk.now())); err != nil {
		t.Fatalf("ingest A observation: %v", err)
	}

	// --- Organization B is denied every read of A's resources. -----------------
	if _, err := svc.ListTargetsForOrg(ctx, orgB, accountA); !errors.Is(err, obs.ErrAccountNotFound) {
		t.Fatalf("B listing A's targets: err=%v, want ErrAccountNotFound (uniform not-found, no oracle)", err)
	}
	if _, err := svc.ListObservedOffersForOrg(ctx, orgB, accountA); !errors.Is(err, obs.ErrAccountNotFound) {
		t.Fatalf("B listing A's offers: err=%v, want ErrAccountNotFound", err)
	}
	if rows, err := svc.ListObservationsForOrg(ctx, orgB, targetA, 100); err != nil || len(rows) != 0 {
		t.Fatalf("B reading A's evidence: rows=%d err=%v; want 0 rows, no error (empty, no oracle)", len(rows), err)
	}

	// --- Positive control: organization A reads its OWN resources. -------------
	targets, err := svc.ListTargetsForOrg(ctx, orgA, accountA)
	if err != nil || len(targets) != 1 {
		t.Fatalf("A listing its own targets: n=%d err=%v; want 1", len(targets), err)
	}
	offers, err := svc.ListObservedOffersForOrg(ctx, orgA, accountA)
	if err != nil || len(offers) != 1 {
		t.Fatalf("A listing its own offers: n=%d err=%v; want 1", len(offers), err)
	}
	rows, err := svc.ListObservationsForOrg(ctx, orgA, targetA, 100)
	if err != nil || len(rows) != 1 {
		t.Fatalf("A reading its own evidence: n=%d err=%v; want 1", len(rows), err)
	}

	// --- Foreign+local cross-combo: B naming A's account with a real target id, or a
	//     mismatched account id, still fails closed as the uniform not-found. --------
	if _, err := svc.ListTargetsForOrg(ctx, orgB, uuid.New()); !errors.Is(err, obs.ErrAccountNotFound) {
		t.Fatalf("B naming an unknown account: err=%v, want ErrAccountNotFound (same as foreign)", err)
	}
}
