package connector

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/mockdk"
)

// fakeStore is an in-memory Store for exercising the Service without a database.
// The DB-backed path is covered separately against native PG16 (service_db_test).
//
// It is ORG-AWARE (S8-AUTHZ-001): every lookup and mutation is predicated on the
// account belonging to the caller's organization, mirroring the org-scoped SQL.
// `owner` maps an account id to its owning organization (a marketplace_accounts
// row). A request whose organization does not own the account resolves to the
// same fail-closed result as an unknown account — no row, no mutation — so the
// fake can prove cross-organization containment exactly as the database does.
type fakeStore struct {
	owner map[uuid.UUID]uuid.UUID // account -> owning organization
	conn  map[uuid.UUID]db.ConnectorConnection
	caps  map[uuid.UUID]map[string]db.ConnectorCapability
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		owner: map[uuid.UUID]uuid.UUID{},
		conn:  map[uuid.UUID]db.ConnectorConnection{},
		caps:  map[uuid.UUID]map[string]db.ConnectorCapability{},
	}
}

// own registers account as belonging to org, mirroring a marketplace_accounts row.
func (f *fakeStore) own(org, account uuid.UUID) { f.owner[account] = org }

// ownedBy reports whether account belongs to org (the org predicate every query
// carries). An unregistered or foreign account is not owned — fails closed.
func (f *fakeStore) ownedBy(account, org uuid.UUID) bool {
	o, ok := f.owner[account]
	return ok && o == org
}

func (f *fakeStore) GetOrgMarketplaceAccountID(_ context.Context, arg db.GetOrgMarketplaceAccountIDParams) (uuid.UUID, error) {
	if f.ownedBy(arg.ID, arg.OrganizationID) {
		return arg.ID, nil
	}
	return uuid.Nil, pgxNoRows
}

func (f *fakeStore) UpsertConnectorConnection(_ context.Context, arg db.UpsertConnectorConnectionParams) (db.ConnectorConnection, error) {
	if !f.ownedBy(arg.MarketplaceAccountID, arg.OrganizationID) {
		// INSERT ... SELECT sourced from marketplace_accounts yields zero rows.
		return db.ConnectorConnection{}, pgxNoRows
	}
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

func (f *fakeStore) GetConnectorConnection(_ context.Context, arg db.GetConnectorConnectionParams) (db.ConnectorConnection, error) {
	if !f.ownedBy(arg.MarketplaceAccountID, arg.OrganizationID) {
		return db.ConnectorConnection{}, pgxNoRows
	}
	c, ok := f.conn[arg.MarketplaceAccountID]
	if !ok {
		return db.ConnectorConnection{}, pgxNoRows
	}
	return c, nil
}

func (f *fakeStore) DisconnectConnectorConnection(_ context.Context, arg db.DisconnectConnectorConnectionParams) (db.ConnectorConnection, error) {
	if !f.ownedBy(arg.MarketplaceAccountID, arg.OrganizationID) {
		// Zero rows matched: no mutation to another organization's connection.
		return db.ConnectorConnection{}, pgxNoRows
	}
	c := f.conn[arg.MarketplaceAccountID]
	c.MarketplaceAccountID = arg.MarketplaceAccountID
	c.ConnectionState = "disconnected"
	c.AccessTokenSealed = nil
	c.RefreshTokenSealed = nil
	c.KeyVersion = 0
	f.conn[arg.MarketplaceAccountID] = c
	return c, nil
}

func (f *fakeStore) SeedConnectorCapability(_ context.Context, arg db.SeedConnectorCapabilityParams) error {
	if !f.ownedBy(arg.MarketplaceAccountID, arg.OrganizationID) {
		return nil // INSERT ... SELECT yields no row; nothing seeded.
	}
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
	if !f.ownedBy(arg.MarketplaceAccountID, arg.OrganizationID) {
		return db.ConnectorCapability{}, pgxNoRows
	}
	m := f.caps[arg.MarketplaceAccountID]
	if m == nil {
		m = map[string]db.ConnectorCapability{}
		f.caps[arg.MarketplaceAccountID] = m
	}
	row := m[arg.Capability]
	row.MarketplaceAccountID = arg.MarketplaceAccountID
	row.Capability = arg.Capability
	row.Status = arg.Status
	row.Detail = arg.Detail
	row.LastVerifiedAt = arg.LastVerifiedAt
	m[arg.Capability] = row
	return row, nil
}

func (f *fakeStore) ResetConnectorCapability(_ context.Context, arg db.ResetConnectorCapabilityParams) error {
	if !f.ownedBy(arg.MarketplaceAccountID, arg.OrganizationID) {
		return nil // Zero rows matched: no capability of another org is reset.
	}
	for k, row := range f.caps[arg.MarketplaceAccountID] {
		row.Status = "unknown"
		row.Detail = pgtype.Text{}
		row.LastVerifiedAt = pgtype.Timestamptz{}
		f.caps[arg.MarketplaceAccountID][k] = row
	}
	return nil
}

func (f *fakeStore) ListConnectorCapabilities(_ context.Context, arg db.ListConnectorCapabilitiesParams) ([]db.ConnectorCapability, error) {
	if !f.ownedBy(arg.MarketplaceAccountID, arg.OrganizationID) {
		return nil, nil
	}
	var out []db.ConnectorCapability
	for _, c := range AllCapabilities() {
		if row, ok := f.caps[arg.MarketplaceAccountID][string(c)]; ok {
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

// newAccount registers a fresh (organization, account) pair in the fake store and
// returns both — the equivalent of a seeded marketplace_accounts row.
func newAccount(store *fakeStore) (org, acct uuid.UUID) {
	org, acct = uuid.New(), uuid.New()
	store.own(org, acct)
	return org, acct
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
	org, acct := newAccount(store)

	// Before connect: fail-closed Unknown, dependents blocked.
	pre, err := svc.Status(context.Background(), org, acct)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if pre.Connection != Disconnected {
		t.Fatalf("pre-connect state = %s, want disconnected", pre.Connection)
	}
	if err := pre.Registry.Require(CatalogRead); err == nil {
		t.Fatal("catalog dependent should be blocked before connect")
	}

	snap, err := svc.Connect(context.Background(), org, acct, "auth-code-123")
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
			org, acct := newAccount(store)

			snap, err := svc.Connect(context.Background(), org, acct, "auth-code")
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
	org, acct := newAccount(store)

	if _, err := svc.Connect(context.Background(), org, acct, "auth"); err != nil {
		t.Fatalf("connect: %v", err)
	}
	snap, err := svc.Disconnect(context.Background(), org, acct)
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
	store := newFakeStore()
	org, acct := newAccount(store)
	svc := newTestService(t, store, "http://127.0.0.1:0")
	if _, err := svc.Connect(context.Background(), org, acct, ""); err == nil {
		t.Fatal("empty auth code should be rejected")
	}
}

// --- Cross-organization authorization (S8-AUTHZ-001, negative tests first) ---

// TestConnectRejectsForeignOrganization proves a caller in organization A cannot
// connect (create or overwrite tokens for) an account owned by organization B.
// A panic-on-use DK transport guarantees the ownership guard runs BEFORE any
// token exchange, and no connection row is written.
func TestConnectRejectsForeignOrganization(t *testing.T) {
	store := newFakeStore()
	cipher := newCipher(t)
	dk := newDKClientWithTransport(t, panicTransport{t: t})
	svc := NewService(store, cipher, dk)

	_, acctB := newAccount(store) // owned by B
	orgA := uuid.New()            // an otherwise-authorized caller in A

	_, err := svc.Connect(context.Background(), orgA, acctB, "auth-code")
	if !errors.Is(err, ErrAccountNotFound) {
		t.Fatalf("cross-org Connect err = %v, want ErrAccountNotFound", err)
	}
	if _, ok := store.conn[acctB]; ok {
		t.Fatal("cross-org Connect wrote a connection row for organization B's account")
	}
}

// TestForeignOrganizationHasNoSideEffect proves that once B's account is
// connected, a caller in A can neither read its real state, refresh/overwrite its
// tokens, nor disconnect it. Every foreign operation returns the same
// not-found-shaped result as an unknown account and causes ZERO mutation to B.
func TestForeignOrganizationHasNoSideEffect(t *testing.T) {
	srv := mockdk.NewServer(mockdk.DefaultConfig())
	defer srv.Close()

	store := newFakeStore()
	svc := newTestService(t, store, srv.URL)

	orgB, acctB := newAccount(store)
	if _, err := svc.Connect(context.Background(), orgB, acctB, "auth"); err != nil {
		t.Fatalf("seed connect (org B): %v", err)
	}
	before := store.conn[acctB]
	if before.ConnectionState != "connected" || len(before.AccessTokenSealed) == 0 {
		t.Fatalf("precondition: org B account must be connected with sealed tokens")
	}

	orgA := uuid.New()

	// Refresh: foreign account fails closed BEFORE any DK refresh; tokens intact.
	if _, err := svc.Refresh(context.Background(), orgA, acctB); !errors.Is(err, ErrAccountNotFound) {
		t.Fatalf("cross-org Refresh err = %v, want ErrAccountNotFound", err)
	}

	// Status: foreign account reveals nothing — default disconnected/all-Unknown.
	snap, err := svc.Status(context.Background(), orgA, acctB)
	if err != nil {
		t.Fatalf("cross-org Status err = %v", err)
	}
	if snap.Connection != Disconnected {
		t.Fatalf("cross-org Status leaked connection state %s", snap.Connection)
	}
	for _, c := range AllCapabilities() {
		if st := snap.Registry.Status(c); st.State != Unknown {
			t.Fatalf("cross-org Status leaked capability %s = %s", c, st.State)
		}
	}

	// Disconnect: foreign account returns the idempotent disconnected snapshot but
	// performs NO mutation to organization B's connection or capabilities.
	dsnap, err := svc.Disconnect(context.Background(), orgA, acctB)
	if err != nil {
		t.Fatalf("cross-org Disconnect err = %v", err)
	}
	if dsnap.Connection != Disconnected {
		t.Fatalf("cross-org Disconnect state = %s, want disconnected", dsnap.Connection)
	}
	after := store.conn[acctB]
	if after.ConnectionState != "connected" || !bytes.Equal(before.AccessTokenSealed, after.AccessTokenSealed) {
		t.Fatal("cross-org Disconnect mutated organization B's connection/tokens")
	}

	// Organization B still sees its own account fully connected and supported.
	bsnap, err := svc.Status(context.Background(), orgB, acctB)
	if err != nil {
		t.Fatalf("org B Status err = %v", err)
	}
	if bsnap.Connection != Connected {
		t.Fatal("org B's own connection was disturbed by cross-org calls")
	}
	if !bsnap.Registry.IsSupported(CatalogRead) {
		t.Fatal("org B's own capabilities were disturbed by cross-org calls")
	}
}
