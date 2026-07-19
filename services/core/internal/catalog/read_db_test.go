package catalog_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/mhosseinab/market-ops/services/core/internal/catalog"
	"github.com/mhosseinab/market-ops/services/core/internal/connector"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
)

// seedVariant upserts a product + variant for the account and returns the variant
// id. It is the minimal canonical seed the Products read model reads from.
func seedVariant(t *testing.T, q *db.Queries, account uuid.UUID, nativeProduct, nativeVariant int64, supplierCode string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	p, err := q.UpsertProduct(ctx, db.UpsertProductParams{
		MarketplaceAccountID: account,
		NativeProductID:      nativeProduct,
		Title:                "product",
	})
	if err != nil {
		t.Fatalf("upsert product: %v", err)
	}
	v, err := q.UpsertVariant(ctx, db.UpsertVariantParams{
		MarketplaceAccountID: account,
		ProductID:            p.ID,
		NativeVariantID:      nativeVariant,
		NativeProductID:      nativeProduct,
		SupplierCode:         supplierCode,
		Title:                "variant",
	})
	if err != nil {
		t.Fatalf("upsert variant: %v", err)
	}
	return v.ID
}

func seedOwnedOfferCapability(t *testing.T, q *db.Queries, org, account uuid.UUID, status connector.State) {
	t.Helper()
	ctx := context.Background()
	if err := q.SeedConnectorCapability(ctx, db.SeedConnectorCapabilityParams{
		Capability:           string(connector.OwnedOfferRead),
		MarketplaceAccountID: account,
		OrganizationID:       org,
	}); err != nil {
		t.Fatalf("seed capability: %v", err)
	}
	if _, err := q.SetConnectorCapabilityStatus(ctx, db.SetConnectorCapabilityStatusParams{
		Status:               string(status),
		MarketplaceAccountID: account,
		Capability:           string(connector.OwnedOfferRead),
		OrganizationID:       org,
	}); err != nil {
		t.Fatalf("set capability: %v", err)
	}
}

// TestReadModelCrossAccountFailsClosed_DB is the authoritative DB proof of the
// never-cut cross-account guarantee: account A's ListProducts NEVER returns account
// B's variant, and a foreign account request fails closed. Deferred to CI (needs
// Postgres); skipped when DATABASE_URL is unset.
func TestReadModelCrossAccountFailsClosed_DB(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()

	orgA, accountA := seedOrgAccount(t, q)
	orgB, accountB := seedOrgAccount(t, q)
	seedVariant(t, q, accountA, 100, 1000, "A-SKU")
	seedVariant(t, q, accountB, 200, 2000, "B-SKU")

	svc := catalog.NewReadService(pool)

	pageA, err := svc.ListProducts(ctx, orgA, accountA, 0, 50)
	if err != nil {
		t.Fatalf("list A: %v", err)
	}
	if len(pageA.Rows) != 1 || pageA.Rows[0].SupplierCode != "A-SKU" {
		t.Fatalf("account A must see only its own variant, got %+v", pageA.Rows)
	}
	for _, r := range pageA.Rows {
		if r.NativeVariantID == 2000 {
			t.Fatal("cross-account leak: account B's variant returned for account A")
		}
	}

	// A request for account B under org A's authority fails closed (not owned).
	if _, err := svc.ListProducts(ctx, orgA, accountB, 0, 50); err != catalog.ErrAccountNotFound {
		t.Fatalf("cross-org request must fail closed, got %v", err)
	}
	_ = orgB
}

// TestReadModelCapabilityGating_DB proves owned-offer data renders only when
// owned_offer_read is Supported (else a reason, never fabricated data). Deferred to CI.
func TestReadModelCapabilityGating_DB(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()

	org, account := seedOrgAccount(t, q)
	variant := seedVariant(t, q, account, 100, 1000, "SKU-1")
	if _, err := q.UpsertOwnedOffer(ctx, db.UpsertOwnedOfferParams{
		MarketplaceAccountID: account,
		VariantID:            variant,
		NativeVariantID:      1000,
		PriceRawText:         "1,000",
		PriceRawValue:        "1000",
		PriceRawUnit:         "T",
	}); err != nil {
		t.Fatalf("upsert owned offer: %v", err)
	}

	svc := catalog.NewReadService(pool)

	// No capability row ⇒ Unknown ⇒ gated (Unknown never enables).
	page, err := svc.ListProducts(ctx, org, account, 0, 50)
	if err != nil {
		t.Fatalf("list (unknown cap): %v", err)
	}
	if page.Rows[0].Owned.Present || page.Rows[0].Owned.HasPrice {
		t.Fatalf("unknown capability must not present owned data: %+v", page.Rows[0].Owned)
	}
	if page.Rows[0].Owned.UnavailableReason != catalog.ReasonCapabilityNotSupported {
		t.Fatalf("want capability_not_supported reason, got %q", page.Rows[0].Owned.UnavailableReason)
	}

	// Supported capability ⇒ raw price evidence renders.
	seedOwnedOfferCapability(t, q, org, account, connector.Supported)
	page2, err := svc.ListProducts(ctx, org, account, 0, 50)
	if err != nil {
		t.Fatalf("list (supported cap): %v", err)
	}
	owned := page2.Rows[0].Owned
	if !owned.Present || owned.Price.Value != "1000" {
		t.Fatalf("supported capability must render raw price, got %+v", owned)
	}
}
