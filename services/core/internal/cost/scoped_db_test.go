package cost_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mhosseinab/market-ops/services/core/internal/cost"
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

// TestCostReadsAreOrgScoped is the issue #131 tenant-quarantine proof at the SERVICE
// boundary: organization B can never read organization A's cost profiles, margin
// readiness, or CSV import preview by naming A's variant/batch id. A foreign resource
// is indistinguishable from a genuinely-unknown one (uniform not-found, no existence
// oracle), while organization A's own reads succeed (positive control).
func TestCostReadsAreOrgScoped(t *testing.T) {
	pool, q := newPool(t)
	svc := cost.NewService(pool)
	ctx := context.Background()

	accountA, variantA := seedVariant(t, q, "SKU-131-A")
	orgA := orgForAccount(t, pool, accountA)
	// A distinct, fully-provisioned tenant B — the realistic attacker.
	accountB, _ := seedVariant(t, q, "SKU-131-B")
	orgB := orgForAccount(t, pool, accountB)

	// A owns a cost profile + readiness for variantA.
	if _, err := svc.EnterSingleCost(ctx, cost.SingleCostInput{
		Account: accountA, VariantID: variantA, Component: cost.ComponentCOGS, RawValue: "1500",
	}); err != nil {
		t.Fatalf("seed A cost value: %v", err)
	}
	// A owns a preview batch.
	preview, err := svc.PreviewImport(ctx, cost.PreviewInput{
		Account: accountA, Filename: "a.csv", Content: "sku,cogs\nSKU-131-A,1500\n",
	})
	if err != nil {
		t.Fatalf("seed A preview: %v", err)
	}
	batchA := preview.Batch.ID
	now := time.Now()

	// --- Organization B is denied every read of A's resources. -----------------
	if rows, err := svc.CostProfileAtForOrg(ctx, orgB, variantA, now); err != nil || len(rows) != 0 {
		t.Fatalf("B reading A's cost profiles: rows=%d err=%v; want 0 rows, no error (uniform empty, no oracle)", len(rows), err)
	}
	if _, err := svc.GetReadinessForOrg(ctx, orgB, variantA); !errors.Is(err, cost.ErrVariantNotFound) {
		t.Fatalf("B reading A's readiness: err=%v, want ErrVariantNotFound (indistinguishable from unknown variant)", err)
	}
	if _, err := svc.GetPreviewForOrg(ctx, orgB, batchA); !errors.Is(err, cost.ErrBatchNotFound) {
		t.Fatalf("B reading A's preview: err=%v, want ErrBatchNotFound (indistinguishable from unknown batch)", err)
	}

	// --- Positive control: organization A reads its OWN resources. -------------
	if rows, err := svc.CostProfileAtForOrg(ctx, orgA, variantA, now); err != nil || len(rows) == 0 {
		t.Fatalf("A reading its own cost profiles: rows=%d err=%v; want >0 rows", len(rows), err)
	}
	if _, err := svc.GetReadinessForOrg(ctx, orgA, variantA); err != nil {
		t.Fatalf("A reading its own readiness: err=%v, want ok", err)
	}
	if _, err := svc.GetPreviewForOrg(ctx, orgA, batchA); err != nil {
		t.Fatalf("A reading its own preview: err=%v, want ok", err)
	}

	// --- Foreign+local cross-combo: B naming an unknown variant/batch is the SAME
	//     uniform not-found as B naming A's real ids (no oracle). --------------------
	if _, err := svc.GetReadinessForOrg(ctx, orgB, uuid.New()); !errors.Is(err, cost.ErrVariantNotFound) {
		t.Fatalf("B reading an unknown variant: err=%v, want ErrVariantNotFound (same as foreign)", err)
	}
	if _, err := svc.GetPreviewForOrg(ctx, orgB, uuid.New()); !errors.Is(err, cost.ErrBatchNotFound) {
		t.Fatalf("B reading an unknown batch: err=%v, want ErrBatchNotFound (same as foreign)", err)
	}
}
