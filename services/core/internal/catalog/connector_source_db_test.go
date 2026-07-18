package catalog_test

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mhosseinab/market-ops/services/core/internal/catalog"
	"github.com/mhosseinab/market-ops/services/core/internal/connector"
	"github.com/mhosseinab/market-ops/services/core/internal/mockdk"
)

// TestSyncThroughConnectorAndMockDK exercises the full Route A seam: connect
// (seals tokens), then sync a paginated variant fixture from the mock DK server
// through the generated client — including a one-shot pagination fault — and
// assert the owned-offer price is stored ONLY as quarantined raw evidence.
func TestSyncThroughConnectorAndMockDK(t *testing.T) {
	pool, q := newPool(t)
	account := seedAccount(t, q)
	ctx := context.Background()

	// Cipher (env-provided key) so Connect can seal tokens.
	t.Setenv(connector.EncryptionKeyEnv, "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	cipher, err := connector.NewCipherFromEnv(os.Getenv)
	if err != nil {
		t.Fatalf("cipher: %v", err)
	}

	fx := &mockdk.CatalogFixture{
		Items: []map[string]any{
			mockdk.VariantItem(100, 1000, 1, 111000, 7),
			mockdk.VariantItem(100, 1001, 2, 222000, 3),
			mockdk.VariantItem(101, 1002, 3, 333000, 0),
		},
		PageSize:   1,
		PageFaults: map[int]mockdk.Mode{2: mockdk.ModeRateLimited},
	}
	cfg := mockdk.DefaultConfig()
	cfg.Catalog = fx
	srv := mockdk.NewServer(cfg)
	defer srv.Close()

	dk, err := connector.NewDKClient(srv.URL, nil)
	if err != nil {
		t.Fatalf("dk client: %v", err)
	}
	svc := connector.NewService(q, cipher, dk)
	if _, err := svc.Connect(ctx, account, "auth-code"); err != nil {
		t.Fatalf("connect: %v", err)
	}

	src := catalog.NewConnectorSource(svc, account)
	s := catalog.NewSyncer(pool, src, 1)
	runID, err := s.Start(ctx, account, catalog.KindInitial)
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	// First attempt hits the 429 on page 2 → retryable error, run stays running.
	if err := s.Resume(ctx, account, runID); err == nil {
		t.Fatal("expected rate-limit fault on page 2")
	}
	faulted, _ := q.GetCatalogSyncRun(ctx, runID)
	if faulted.Status != "running" || faulted.NextPage != 2 {
		t.Fatalf("after fault: status=%s next_page=%d, want running/2", faulted.Status, faulted.NextPage)
	}

	// Clear the fault and resume to completion.
	delete(fx.PageFaults, 2)
	if err := s.Resume(ctx, account, runID); err != nil {
		t.Fatalf("resume: %v", err)
	}
	done, _ := q.GetCatalogSyncRun(ctx, runID)
	if done.Status != "completed" {
		t.Fatalf("status=%s, want completed", done.Status)
	}
	if _, v, _, o, _ := counts(t, q, account); v != 3 || o != 3 {
		t.Fatalf("variants=%d offers=%d, want 3/3", v, o)
	}

	// MONEY QUARANTINE: the price is stored verbatim as raw evidence with an
	// EMPTY unit (DK sends no unit token) — never a Money, never an inferred unit.
	text, value, unit := readOwnedOfferPrice(t, pool, account, 1000)
	if value != "111000" || text != "111000" {
		t.Fatalf("price raw value=%q text=%q, want verbatim 111000", value, text)
	}
	if unit != "" {
		t.Fatalf("price raw unit=%q, want empty (quarantined, never inferred)", unit)
	}
}

// readOwnedOfferPrice reads the raw price evidence columns for a variant.
func readOwnedOfferPrice(t *testing.T, pool *pgxpool.Pool, account uuid.UUID, nativeVariantID int64) (text, value, unit string) {
	t.Helper()
	// Query directly: there is deliberately no code path turning these into Money.
	row := pool.QueryRow(context.Background(),
		`SELECT price_raw_text, price_raw_value, price_raw_unit FROM owned_offers
		 WHERE marketplace_account_id=$1 AND native_variant_id=$2`, account, nativeVariantID)
	if err := row.Scan(&text, &value, &unit); err != nil {
		t.Fatalf("read owned offer: %v", err)
	}
	return
}
