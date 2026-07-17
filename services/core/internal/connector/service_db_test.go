package connector_test

import (
	"bytes"
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mhosseinab/market-ops/services/core/internal/connector"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/mockdk"
)

// newDBQueries connects to DATABASE_URL and returns sqlc queries. It skips when
// DATABASE_URL is unset (CI provides Postgres from S6; locally native PG16 with
// the connector migration applied via `task db:reset`).
func newDBQueries(t *testing.T) (*db.Queries, *pgxpool.Pool) {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set; skipping connector DB test")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("connect pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return db.New(pool), pool
}

func newCipher(t *testing.T) *connector.Cipher {
	t.Helper()
	t.Setenv(connector.EncryptionKeyEnv, "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=") // 32 zero bytes
	c, err := connector.NewCipherFromEnv(os.Getenv)
	if err != nil {
		t.Fatalf("cipher: %v", err)
	}
	return c
}

// seedAccount creates an organization + marketplace account, returning its id.
func seedAccount(t *testing.T, q *db.Queries) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	org, err := q.CreateOrganization(ctx, "connector-test-"+uuid.NewString())
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	acct, err := q.CreateMarketplaceAccount(ctx, db.CreateMarketplaceAccountParams{
		OrganizationID:  org.ID,
		NativeAccountID: "native-" + uuid.NewString(),
		DisplayName:     "Test Seller",
	})
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	return acct.ID
}

// TestServiceConnectPersistsSealedTokensAndProbes proves the full DB-backed
// lifecycle: connect seals tokens (no plaintext in the row), probes persist per
// capability, and disconnect purges tokens + resets capabilities to Unknown.
func TestServiceConnectPersistsSealedTokensAndProbes(t *testing.T) {
	q, pool := newDBQueries(t)
	cipher := newCipher(t)
	acct := seedAccount(t, q)

	srv := mockdk.NewServer(mockdk.DefaultConfig())
	defer srv.Close()
	dk, err := connector.NewDKClient(srv.URL, nil)
	if err != nil {
		t.Fatalf("dk client: %v", err)
	}
	svc := connector.NewService(q, cipher, dk)
	ctx := context.Background()

	snap, err := svc.Connect(ctx, acct, "auth-code")
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if snap.Connection != connector.Connected {
		t.Fatalf("state = %s, want connected", snap.Connection)
	}
	if !snap.Registry.IsSupported(connector.CatalogRead) {
		t.Fatal("catalog_read should be supported after happy probe")
	}

	// The persisted row holds sealed bytes, never the plaintext access token.
	conn, err := q.GetConnectorConnection(ctx, acct)
	if err != nil {
		t.Fatalf("get connection: %v", err)
	}
	if len(conn.AccessTokenSealed) == 0 {
		t.Fatal("no sealed access token persisted")
	}
	if bytes.Contains(conn.AccessTokenSealed, []byte("mock-access-token")) {
		t.Fatal("plaintext token found in DB — encryption at rest violated")
	}
	if conn.KeyVersion == 0 {
		t.Fatal("key_version not stamped")
	}

	// All nine capability rows exist with a last-verified stamp.
	rows, err := q.ListConnectorCapabilities(ctx, acct)
	if err != nil {
		t.Fatalf("list caps: %v", err)
	}
	if len(rows) != 9 {
		t.Fatalf("expected 9 capability rows, got %d", len(rows))
	}
	for _, r := range rows {
		if r.Status == "unknown" {
			t.Errorf("%s still unknown after probe", r.Capability)
		}
		if !r.LastVerifiedAt.Valid {
			t.Errorf("%s has no last-verified time after probe", r.Capability)
		}
	}

	// Disconnect purges tokens and resets every capability to Unknown.
	dsnap, err := svc.Disconnect(ctx, acct)
	if err != nil {
		t.Fatalf("disconnect: %v", err)
	}
	if dsnap.Connection != connector.Disconnected {
		t.Fatalf("state = %s, want disconnected", dsnap.Connection)
	}
	conn2, _ := q.GetConnectorConnection(ctx, acct)
	if len(conn2.AccessTokenSealed) != 0 || len(conn2.RefreshTokenSealed) != 0 {
		t.Fatal("tokens not purged on disconnect")
	}
	rows2, _ := q.ListConnectorCapabilities(ctx, acct)
	for _, r := range rows2 {
		if r.Status != "unknown" || r.LastVerifiedAt.Valid {
			t.Errorf("%s not reset to unknown/unverified: status=%s verified=%v", r.Capability, r.Status, r.LastVerifiedAt.Valid)
		}
	}
	_ = pool
}

// TestServiceRefreshRotatesToken proves refresh rotates the persisted token and
// re-probes.
func TestServiceRefreshRotatesToken(t *testing.T) {
	q, _ := newDBQueries(t)
	cipher := newCipher(t)
	acct := seedAccount(t, q)

	srv := mockdk.NewServer(mockdk.DefaultConfig())
	defer srv.Close()
	dk, err := connector.NewDKClient(srv.URL, nil)
	if err != nil {
		t.Fatalf("dk client: %v", err)
	}
	svc := connector.NewService(q, cipher, dk)
	ctx := context.Background()

	if _, err := svc.Connect(ctx, acct, "auth-code"); err != nil {
		t.Fatalf("connect: %v", err)
	}
	before, _ := q.GetConnectorConnection(ctx, acct)

	snap, err := svc.Refresh(ctx, acct)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if snap.Connection != connector.Connected {
		t.Fatalf("state after refresh = %s", snap.Connection)
	}
	after, _ := q.GetConnectorConnection(ctx, acct)
	if bytes.Equal(before.AccessTokenSealed, after.AccessTokenSealed) {
		t.Fatal("access token was not rotated on refresh")
	}
}
