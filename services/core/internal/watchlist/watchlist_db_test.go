package watchlist_test

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mhosseinab/market-ops/services/core/internal/audit"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/watchlist"
)

func newPool(t *testing.T) (*pgxpool.Pool, *db.Queries) {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set; skipping watchlist DB test")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("connect pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool, db.New(pool)
}

func seedAccount(t *testing.T, q *db.Queries) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	org, err := q.CreateOrganization(ctx, "watchlist-test-"+uuid.NewString())
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	acct, err := q.CreateMarketplaceAccount(ctx, db.CreateMarketplaceAccountParams{
		OrganizationID:  org.ID,
		NativeAccountID: "native-" + uuid.NewString(),
		DisplayName:     "Watchlist Seller",
	})
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	return acct.ID
}

// seedVariant creates a bare variant row (no identity mapping).
func seedVariant(t *testing.T, q *db.Queries, account uuid.UUID) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	nativeProduct := int64(uuid.New().ID())
	nativeVariant := int64(uuid.New().ID())
	prod, err := q.UpsertProduct(ctx, db.UpsertProductParams{
		MarketplaceAccountID: account,
		NativeProductID:      nativeProduct,
		Title:                "Widget",
	})
	if err != nil {
		t.Fatalf("upsert product: %v", err)
	}
	v, err := q.UpsertVariant(ctx, db.UpsertVariantParams{
		MarketplaceAccountID: account,
		ProductID:            prod.ID,
		NativeVariantID:      nativeVariant,
		NativeProductID:      nativeProduct,
		SupplierCode:         "SKU-" + uuid.NewString()[:8],
		Title:                "Widget - Red",
	})
	if err != nil {
		t.Fatalf("upsert variant: %v", err)
	}
	return v.ID
}

// seedConfirmedVariant creates a variant with an active Confirmed Market
// Product Identity (CAT-002) — the EXT-007 add precondition.
func seedConfirmedVariant(t *testing.T, q *db.Queries, account uuid.UUID) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	variant := seedVariant(t, q, account)
	cand, err := q.CreateIdentityCandidate(ctx, db.CreateIdentityCandidateParams{
		MarketplaceAccountID: account,
		VariantID:            variant,
		NativeVariantID:      int64(uuid.New().ID()),
		NativeProductID:      int64(uuid.New().ID()),
	})
	if err != nil {
		t.Fatalf("create identity candidate: %v", err)
	}
	if _, err := q.ConfirmIdentity(ctx, cand.ID); err != nil {
		t.Fatalf("confirm identity: %v", err)
	}
	return variant
}

// TestWatchlistAddRequiresConfirmedIdentity is the EXT-007 precondition: only a
// Confirmed owned product may be added — never inferred, never bypassed.
func TestWatchlistAddRequiresConfirmedIdentity(t *testing.T) {
	pool, q := newPool(t)
	account := seedAccount(t, q)
	variant := seedVariant(t, q, account) // NOT confirmed.
	svc := watchlist.NewService(pool)
	actor := audit.Actor{ID: "owner@x.io", Role: "owner", Surface: "screens"}

	_, err := svc.Add(context.Background(), account, variant, actor)
	if err != watchlist.ErrNotConfirmed {
		t.Fatalf("err = %v, want ErrNotConfirmed", err)
	}
	entries, err := svc.List(context.Background(), account)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("an unconfirmed add must not have persisted an entry, got %d", len(entries))
	}
}

// TestWatchlistAddAppendsAuditAtomicallyAndIsIdempotent proves a fresh add
// appends an AUD-001 audit record ATOMICALLY with the insert, and that adding
// the same variant twice is idempotent (no duplicate entry, no error).
func TestWatchlistAddAppendsAuditAtomicallyAndIsIdempotent(t *testing.T) {
	pool, q := newPool(t)
	account := seedAccount(t, q)
	variant := seedConfirmedVariant(t, q, account)
	svc := watchlist.NewService(pool)
	actor := audit.Actor{ID: "owner@x.io", Role: "owner", Surface: "screens"}

	entry, err := svc.Add(context.Background(), account, variant, actor)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if entry.VariantID != variant || entry.MarketplaceAccountID != account {
		t.Fatalf("entry mismatch: %+v", entry)
	}

	rows, err := pool.Query(context.Background(),
		`SELECT count(*) FROM audit_records WHERE marketplace_account_id = $1 AND event_type = 'watchlist_change'`,
		account)
	if err != nil {
		t.Fatalf("query audit count: %v", err)
	}
	var auditCount int
	if rows.Next() {
		if err := rows.Scan(&auditCount); err != nil {
			t.Fatalf("scan audit count: %v", err)
		}
	}
	rows.Close()
	if auditCount != 1 {
		t.Fatalf("audit_records watchlist_change count = %d, want 1", auditCount)
	}

	// Idempotent re-add: same entry, no error, NO second audit row.
	again, err := svc.Add(context.Background(), account, variant, actor)
	if err != nil {
		t.Fatalf("second Add: %v", err)
	}
	if again.ID != entry.ID {
		t.Fatalf("second Add minted a NEW entry (not idempotent): %+v vs %+v", again, entry)
	}
	entries, err := svc.List(context.Background(), account)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("List after duplicate add = %d entries, want 1", len(entries))
	}

	rows2, err := pool.Query(context.Background(),
		`SELECT count(*) FROM audit_records WHERE marketplace_account_id = $1 AND event_type = 'watchlist_change'`,
		account)
	if err != nil {
		t.Fatalf("query audit count after duplicate: %v", err)
	}
	var auditCount2 int
	if rows2.Next() {
		if err := rows2.Scan(&auditCount2); err != nil {
			t.Fatalf("scan audit count: %v", err)
		}
	}
	rows2.Close()
	if auditCount2 != 1 {
		t.Fatalf("duplicate add appended a second audit row: count = %d, want 1", auditCount2)
	}
}

// TestWatchlistAddEnforcesServerCap is the EXT-007 hard requirement: the
// SERVER enforces the cap, never the client. This drives the cap without
// paying for watchlist.MaxEntries real Confirmed-identity inserts: it fills
// the account to the cap via direct SQL (bypassing the confirmation
// precondition on purpose, since CountWatchlistEntries — the gate Add checks —
// counts rows regardless of how they got there), then proves the NEXT Add,
// through the real service with a genuinely Confirmed variant, is rejected.
func TestWatchlistAddEnforcesServerCap(t *testing.T) {
	pool, q := newPool(t)
	account := seedAccount(t, q)
	svc := watchlist.NewService(pool)
	actor := audit.Actor{ID: "owner@x.io", Role: "owner", Surface: "screens"}

	for i := 0; i < watchlist.MaxEntries; i++ {
		v := seedVariant(t, q, account)
		if _, err := q.InsertWatchlistEntry(context.Background(), db.InsertWatchlistEntryParams{
			MarketplaceAccountID: account,
			VariantID:            v,
			AddedBy:              "seed",
		}); err != nil {
			t.Fatalf("seed watchlist entry %d: %v", i, err)
		}
	}

	overflow := seedConfirmedVariant(t, q, account)
	_, err := svc.Add(context.Background(), account, overflow, actor)
	if err != watchlist.ErrCapExceeded {
		t.Fatalf("err = %v, want ErrCapExceeded", err)
	}
}
