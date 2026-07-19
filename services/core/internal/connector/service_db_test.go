package connector_test

import (
	"bytes"
	"context"
	"errors"
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

// seedAccount creates an organization + marketplace account, returning the
// organization id and the account id — both are required now that every connector
// query is org-scoped (S8-AUTHZ-001).
func seedAccount(t *testing.T, q *db.Queries) (org, account uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	o, err := q.CreateOrganization(ctx, "connector-test-"+uuid.NewString())
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	acct, err := q.CreateMarketplaceAccount(ctx, db.CreateMarketplaceAccountParams{
		OrganizationID:  o.ID,
		NativeAccountID: "native-" + uuid.NewString(),
		DisplayName:     "Test Seller",
	})
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	return o.ID, acct.ID
}

// TestServiceConnectPersistsSealedTokensAndProbes proves the full DB-backed
// lifecycle: connect seals tokens (no plaintext in the row), probes persist per
// capability, and disconnect purges tokens + resets capabilities to Unknown.
func TestServiceConnectPersistsSealedTokensAndProbes(t *testing.T) {
	q, pool := newDBQueries(t)
	cipher := newCipher(t)
	org, acct := seedAccount(t, q)

	srv := mockdk.NewServer(mockdk.DefaultConfig())
	defer srv.Close()
	dk, err := connector.NewDKClient(srv.URL, nil)
	if err != nil {
		t.Fatalf("dk client: %v", err)
	}
	svc := connector.NewService(q, cipher, dk)
	ctx := context.Background()

	snap, err := svc.Connect(ctx, org, acct, "auth-code")
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
	conn, err := q.GetConnectorConnection(ctx, db.GetConnectorConnectionParams{MarketplaceAccountID: acct, OrganizationID: org})
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
	rows, err := q.ListConnectorCapabilities(ctx, db.ListConnectorCapabilitiesParams{MarketplaceAccountID: acct, OrganizationID: org})
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
	dsnap, err := svc.Disconnect(ctx, org, acct)
	if err != nil {
		t.Fatalf("disconnect: %v", err)
	}
	if dsnap.Connection != connector.Disconnected {
		t.Fatalf("state = %s, want disconnected", dsnap.Connection)
	}
	conn2, _ := q.GetConnectorConnection(ctx, db.GetConnectorConnectionParams{MarketplaceAccountID: acct, OrganizationID: org})
	if len(conn2.AccessTokenSealed) != 0 || len(conn2.RefreshTokenSealed) != 0 {
		t.Fatal("tokens not purged on disconnect")
	}
	rows2, _ := q.ListConnectorCapabilities(ctx, db.ListConnectorCapabilitiesParams{MarketplaceAccountID: acct, OrganizationID: org})
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
	org, acct := seedAccount(t, q)

	srv := mockdk.NewServer(mockdk.DefaultConfig())
	defer srv.Close()
	dk, err := connector.NewDKClient(srv.URL, nil)
	if err != nil {
		t.Fatalf("dk client: %v", err)
	}
	svc := connector.NewService(q, cipher, dk)
	ctx := context.Background()

	if _, err := svc.Connect(ctx, org, acct, "auth-code"); err != nil {
		t.Fatalf("connect: %v", err)
	}
	before, _ := q.GetConnectorConnection(ctx, db.GetConnectorConnectionParams{MarketplaceAccountID: acct, OrganizationID: org})

	snap, err := svc.Refresh(ctx, org, acct)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if snap.Connection != connector.Connected {
		t.Fatalf("state after refresh = %s", snap.Connection)
	}
	after, _ := q.GetConnectorConnection(ctx, db.GetConnectorConnectionParams{MarketplaceAccountID: acct, OrganizationID: org})
	if bytes.Equal(before.AccessTokenSealed, after.AccessTokenSealed) {
		t.Fatal("access token was not rotated on refresh")
	}
}

// TestServiceCrossOrgIsolationDB is the DB-backed reproduction of S8-AUTHZ-001:
// two real organizations, A and B, with a connector account owned by B. A caller
// authenticated as A supplies B's account UUID to every connector operation. Each
// must be rejected against the real org-scoped SQL predicate, and B's persisted
// connection + capabilities must be entirely unchanged (no cross-tenant read,
// token overwrite, refresh, or disconnect).
func TestServiceCrossOrgIsolationDB(t *testing.T) {
	q, pool := newDBQueries(t)
	cipher := newCipher(t)
	ctx := context.Background()

	// Organization B owns a connected account.
	orgB, acctB := seedAccount(t, q)
	// Organization A is a separate, otherwise-authorized tenant.
	orgA, _ := seedAccount(t, q)

	srv := mockdk.NewServer(mockdk.DefaultConfig())
	defer srv.Close()
	dk, err := connector.NewDKClient(srv.URL, nil)
	if err != nil {
		t.Fatalf("dk client: %v", err)
	}
	svc := connector.NewService(q, cipher, dk)

	if _, err := svc.Connect(ctx, orgB, acctB, "auth-code"); err != nil {
		t.Fatalf("seed connect (org B): %v", err)
	}
	before, err := q.GetConnectorConnection(ctx, db.GetConnectorConnectionParams{MarketplaceAccountID: acctB, OrganizationID: orgB})
	if err != nil {
		t.Fatalf("read org B connection: %v", err)
	}

	// (3) Supply B's UUID to A's read, token-update (connect), refresh, and
	//     disconnect. (4) Observe every operation is contained.

	// Connect (token overwrite) under org A: rejected, no DK token exchange.
	if _, err := svc.Connect(ctx, orgA, acctB, "auth-code"); !errors.Is(err, connector.ErrAccountNotFound) {
		t.Fatalf("cross-org Connect err = %v, want ErrAccountNotFound", err)
	}
	// Refresh under org A: rejected before any DK refresh.
	if _, err := svc.Refresh(ctx, orgA, acctB); !errors.Is(err, connector.ErrAccountNotFound) {
		t.Fatalf("cross-org Refresh err = %v, want ErrAccountNotFound", err)
	}
	// Status under org A: reveals nothing — default disconnected/all-Unknown.
	snap, err := svc.Status(ctx, orgA, acctB)
	if err != nil {
		t.Fatalf("cross-org Status err = %v", err)
	}
	if snap.Connection != connector.Disconnected {
		t.Fatalf("cross-org Status leaked state %s", snap.Connection)
	}
	for _, c := range connector.AllCapabilities() {
		if st := snap.Registry.Status(c); st.State != connector.Unknown {
			t.Fatalf("cross-org Status leaked capability %s = %s", c, st.State)
		}
	}
	// Disconnect under org A: idempotent no-op snapshot, no mutation to B.
	if _, err := svc.Disconnect(ctx, orgA, acctB); err != nil {
		t.Fatalf("cross-org Disconnect err = %v", err)
	}

	// B's persisted connection is byte-for-byte unchanged.
	after, err := q.GetConnectorConnection(ctx, db.GetConnectorConnectionParams{MarketplaceAccountID: acctB, OrganizationID: orgB})
	if err != nil {
		t.Fatalf("re-read org B connection: %v", err)
	}
	if after.ConnectionState != "connected" ||
		!bytes.Equal(before.AccessTokenSealed, after.AccessTokenSealed) ||
		!bytes.Equal(before.RefreshTokenSealed, after.RefreshTokenSealed) ||
		after.KeyVersion != before.KeyVersion {
		t.Fatal("org B's connection was mutated by a cross-org caller")
	}
	// B still sees its own account fully connected + supported.
	bsnap, err := svc.Status(ctx, orgB, acctB)
	if err != nil {
		t.Fatalf("org B Status err = %v", err)
	}
	if bsnap.Connection != connector.Connected || !bsnap.Registry.IsSupported(connector.CatalogRead) {
		t.Fatal("org B's own connector state was disturbed by cross-org calls")
	}
	_ = pool
}
