package connector

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/mockdk"
)

// fakeStore is an in-memory Store for exercising the Service without a database.
// The DB-backed path is covered separately against native PG16 (service_db_test).
type fakeStore struct {
	conn map[uuid.UUID]db.ConnectorConnection
	caps map[uuid.UUID]map[string]db.ConnectorCapability
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		conn: map[uuid.UUID]db.ConnectorConnection{},
		caps: map[uuid.UUID]map[string]db.ConnectorCapability{},
	}
}

func (f *fakeStore) UpsertConnectorConnection(_ context.Context, arg db.UpsertConnectorConnectionParams) (db.ConnectorConnection, error) {
	c := db.ConnectorConnection{
		ID:                   uuid.New(),
		MarketplaceAccountID: arg.MarketplaceAccountID,
		ConnectionState:      "connected",
		AccessTokenSealed:    arg.AccessTokenSealed,
		RefreshTokenSealed:   arg.RefreshTokenSealed,
		AccessExpiresAt:      arg.AccessExpiresAt,
		RefreshExpiresAt:     arg.RefreshExpiresAt,
		KeyVersion:           arg.KeyVersion,
	}
	f.conn[arg.MarketplaceAccountID] = c
	return c, nil
}

func (f *fakeStore) GetConnectorConnection(_ context.Context, id uuid.UUID) (db.ConnectorConnection, error) {
	c, ok := f.conn[id]
	if !ok {
		return db.ConnectorConnection{}, pgxNoRows
	}
	return c, nil
}

func (f *fakeStore) DisconnectConnectorConnection(_ context.Context, id uuid.UUID) (db.ConnectorConnection, error) {
	c := f.conn[id]
	c.MarketplaceAccountID = id
	c.ConnectionState = "disconnected"
	c.AccessTokenSealed = nil
	c.RefreshTokenSealed = nil
	c.KeyVersion = 0
	f.conn[id] = c
	return c, nil
}

func (f *fakeStore) SeedConnectorCapability(_ context.Context, arg db.SeedConnectorCapabilityParams) error {
	m := f.caps[arg.MarketplaceAccountID]
	if m == nil {
		m = map[string]db.ConnectorCapability{}
		f.caps[arg.MarketplaceAccountID] = m
	}
	if _, ok := m[arg.Capability]; !ok {
		m[arg.Capability] = db.ConnectorCapability{
			MarketplaceAccountID: arg.MarketplaceAccountID,
			Capability:           arg.Capability,
			Status:               "unknown",
		}
	}
	return nil
}

func (f *fakeStore) SetConnectorCapabilityStatus(_ context.Context, arg db.SetConnectorCapabilityStatusParams) (db.ConnectorCapability, error) {
	m := f.caps[arg.MarketplaceAccountID]
	row := m[arg.Capability]
	row.MarketplaceAccountID = arg.MarketplaceAccountID
	row.Capability = arg.Capability
	row.Status = arg.Status
	row.Detail = arg.Detail
	row.LastVerifiedAt = arg.LastVerifiedAt
	m[arg.Capability] = row
	return row, nil
}

func (f *fakeStore) ResetConnectorCapability(_ context.Context, id uuid.UUID) error {
	for k, row := range f.caps[id] {
		row.Status = "unknown"
		row.Detail = pgtype.Text{}
		row.LastVerifiedAt = pgtype.Timestamptz{}
		f.caps[id][k] = row
	}
	return nil
}

func (f *fakeStore) ListConnectorCapabilities(_ context.Context, id uuid.UUID) ([]db.ConnectorCapability, error) {
	var out []db.ConnectorCapability
	for _, c := range AllCapabilities() {
		if row, ok := f.caps[id][string(c)]; ok {
			out = append(out, row)
		}
	}
	return out, nil
}

func newTestService(t *testing.T, store Store, baseURL string) *Service {
	t.Helper()
	cipher, err := NewCipherFromEnv(func(string) string { return testKeyB64() })
	if err != nil {
		t.Fatalf("cipher: %v", err)
	}
	dk, err := NewDKClient(baseURL, nil)
	if err != nil {
		t.Fatalf("dk client: %v", err)
	}
	return NewService(store, cipher, dk)
}

// TestConnectLifecycleHappy proves the Unknown -> probe -> Supported/Degraded
// transition: before Connect everything is Unknown; after Connect against a
// happy mock, reads are Supported and price_write is Degraded (never Supported
// without the gated write probe).
func TestConnectLifecycleHappy(t *testing.T) {
	srv := mockdk.NewServer(mockdk.DefaultConfig())
	defer srv.Close()

	store := newFakeStore()
	svc := newTestService(t, store, srv.URL)
	acct := uuid.New()

	// Before connect: fail-closed Unknown, dependents blocked.
	pre, err := svc.Status(context.Background(), acct)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if pre.Connection != Disconnected {
		t.Fatalf("pre-connect state = %s, want disconnected", pre.Connection)
	}
	if err := pre.Registry.Require(CatalogRead); err == nil {
		t.Fatal("catalog dependent should be blocked before connect")
	}

	snap, err := svc.Connect(context.Background(), acct, "auth-code-123")
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if snap.Connection != Connected {
		t.Fatalf("post-connect state = %s, want connected", snap.Connection)
	}

	wantSupported := []Capability{CatalogRead, OwnedOfferRead, StockRead, BuyboxRead, BoundaryRead, CommissionRead, SalesContextRead, ChangeFeed}
	for _, c := range wantSupported {
		st := snap.Registry.Status(c)
		if st.State != Supported {
			t.Errorf("%s = %s, want supported", c, st.State)
		}
		if st.LastVerified == nil {
			t.Errorf("%s supported but has no last-verified time", c)
		}
		if err := snap.Registry.Require(c); err != nil {
			t.Errorf("Require(%s) after probe = %v, want nil", c, err)
		}
	}
	// price_write is verified-but-capped at Degraded (reconciliation gated to S35).
	if st := snap.Registry.Status(PriceWrite); st.State != Degraded {
		t.Errorf("price_write = %s, want degraded (gated write probe)", st.State)
	}
	if err := snap.Registry.Require(PriceWrite); err == nil {
		t.Error("price_write must still block dependents while degraded")
	}
}

// TestConnectFaultModes proves each fault maps to the correct state.
func TestConnectFaultModes(t *testing.T) {
	tests := []struct {
		name string
		cap  Capability
		mode mockdk.Mode
		want State
	}{
		{"unauthorized -> unsupported", CatalogRead, mockdk.ModeUnauthorized, Unsupported},
		{"forbidden -> unsupported", StockRead, mockdk.ModeForbidden, Unsupported},
		{"rate limited -> degraded", CommissionRead, mockdk.ModeRateLimited, Degraded},
		{"malformed -> degraded", OwnedOfferRead, mockdk.ModeMalformed, Degraded},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := mockdk.DefaultConfig()
			cfg.PerCap[string(tc.cap)] = tc.mode
			srv := mockdk.NewServer(cfg)
			defer srv.Close()

			store := newFakeStore()
			svc := newTestService(t, store, srv.URL)
			acct := uuid.New()

			snap, err := svc.Connect(context.Background(), acct, "auth-code")
			if err != nil {
				t.Fatalf("connect: %v", err)
			}
			if st := snap.Registry.Status(tc.cap); st.State != tc.want {
				t.Fatalf("%s under %s = %s, want %s", tc.cap, tc.mode, st.State, tc.want)
			}
			// A faulted capability never opens its gate.
			if err := snap.Registry.Require(tc.cap); err == nil {
				t.Fatalf("%s faulted but Require returned nil", tc.cap)
			}
			// Detail carries a recovery-oriented reason (ACC-003).
			if snap.Registry.Status(tc.cap).Detail == "" {
				t.Errorf("%s faulted without a recovery detail", tc.cap)
			}
		})
	}
}

// TestDisconnectResetsToUnknown proves disconnect purges tokens and returns every
// capability to Unknown so nothing dependent can run afterwards.
func TestDisconnectResetsToUnknown(t *testing.T) {
	srv := mockdk.NewServer(mockdk.DefaultConfig())
	defer srv.Close()

	store := newFakeStore()
	svc := newTestService(t, store, srv.URL)
	acct := uuid.New()

	if _, err := svc.Connect(context.Background(), acct, "auth"); err != nil {
		t.Fatalf("connect: %v", err)
	}
	snap, err := svc.Disconnect(context.Background(), acct)
	if err != nil {
		t.Fatalf("disconnect: %v", err)
	}
	if snap.Connection != Disconnected {
		t.Fatalf("state = %s, want disconnected", snap.Connection)
	}
	for _, c := range AllCapabilities() {
		if st := snap.Registry.Status(c); st.State != Unknown {
			t.Errorf("%s after disconnect = %s, want unknown", c, st.State)
		}
	}
	if conn := store.conn[acct]; len(conn.AccessTokenSealed) != 0 || len(conn.RefreshTokenSealed) != 0 {
		t.Error("tokens not purged on disconnect")
	}
}

// TestConnectRejectsEmptyAuthCode proves connect validates its input.
func TestConnectRejectsEmptyAuthCode(t *testing.T) {
	svc := newTestService(t, newFakeStore(), "http://127.0.0.1:0")
	if _, err := svc.Connect(context.Background(), uuid.New(), ""); err == nil {
		t.Fatal("empty auth code should be rejected")
	}
}
