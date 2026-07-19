package connector

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/mhosseinab/market-ops/services/core/internal/db"
)

// ErrInvalidAuthCode is returned when a connect request carries no auth code.
var ErrInvalidAuthCode = errors.New("connector: authorization code is required")

// ErrNotConnected is returned by operations that need an established connection.
var ErrNotConnected = errors.New("connector: account is not connected")

// ErrAccountNotFound is returned when the account id is not owned by the
// authenticated organization (S8-AUTHZ-001). It is returned identically for a
// genuinely-absent account and for one owned by a DIFFERENT organization, so the
// response never reveals whether a foreign account UUID exists. It carries no
// side effect: the guard runs before any DK call or write.
var ErrAccountNotFound = errors.New("connector: account not found")

// ConnectionState mirrors the persisted connection_state values.
type ConnectionState string

const (
	Connected    ConnectionState = "connected"
	Disconnected ConnectionState = "disconnected"
)

// Snapshot is the reconciled connector view returned by every Service operation:
// the connection state plus the capability registry. It is what the gateway maps
// onto ConnectorStatus.
type Snapshot struct {
	AccountID  uuid.UUID
	Connection ConnectionState
	Registry   *Registry
}

// Service is the connector orchestration seam: it exchanges/refreshes tokens
// (sealing them at rest), seeds the nine capabilities at Unknown, runs probes,
// and reconciles the persisted status. It is the producer the gateway consumes.
type Service struct {
	store  Store
	cipher *Cipher
	dk     *DKClient
	opts   ProbeOptions
}

// NewService wires a Service. The cipher fails closed on a missing key at
// construction time upstream (NewCipherFromEnv), so a Service can never be built
// without an encryption key — plaintext tokens are impossible by construction.
func NewService(store Store, cipher *Cipher, dk *DKClient) *Service {
	return &Service{store: store, cipher: cipher, dk: dk}
}

// WithProbeOptions sets sample identifiers used by per-variant probes.
func (s *Service) WithProbeOptions(o ProbeOptions) *Service { s.opts = o; return s }

// assertOwned is the organization ownership guard (S8-AUTHZ-001). It resolves the
// account id ONLY when it belongs to organizationID; a foreign or unknown account
// returns ErrAccountNotFound. Callers run it before any DK call or write so a
// cross-organization request produces no side effect and reveals nothing.
func (s *Service) assertOwned(ctx context.Context, organizationID, accountID uuid.UUID) error {
	_, err := s.store.GetOrgMarketplaceAccountID(ctx, db.GetOrgMarketplaceAccountIDParams{
		ID:             accountID,
		OrganizationID: organizationID,
	})
	if errors.Is(err, pgxNoRows) {
		return ErrAccountNotFound
	}
	if err != nil {
		return fmt.Errorf("connector: resolve account owner: %w", err)
	}
	return nil
}

// Connect exchanges an authorization code for tokens, seals and persists them,
// seeds the capability registry at Unknown, then probes every capability. It is
// the connect half of ACC-001. organizationID is the authenticated caller's org;
// a foreign account never reaches the token exchange (S8-AUTHZ-001).
func (s *Service) Connect(ctx context.Context, organizationID, accountID uuid.UUID, authCode string) (Snapshot, error) {
	if authCode == "" {
		return Snapshot{}, ErrInvalidAuthCode
	}
	// Ownership guard BEFORE any DK call or write: a cross-organization account
	// yields ErrAccountNotFound with no token exchange and no mutation.
	if err := s.assertOwned(ctx, organizationID, accountID); err != nil {
		return Snapshot{}, err
	}
	tokens, err := s.dk.ExchangeToken(ctx, authCode)
	if err != nil {
		return Snapshot{}, err
	}
	if err := s.persistTokens(ctx, organizationID, accountID, tokens); err != nil {
		return Snapshot{}, err
	}
	if err := s.seedCapabilities(ctx, organizationID, accountID); err != nil {
		return Snapshot{}, err
	}
	if err := s.probeAndPersist(ctx, organizationID, accountID, tokens.AccessToken); err != nil {
		return Snapshot{}, err
	}
	return s.Status(ctx, organizationID, accountID)
}

// Refresh rotates the access token from the stored refresh token, re-seals it,
// and re-probes so status/last-verified stay current (ACC-001/ACC-003). The
// connection is loaded ORG-SCOPED: a foreign account (or one never connected)
// resolves to no row and fails closed with ErrAccountNotFound before any DK call.
func (s *Service) Refresh(ctx context.Context, organizationID, accountID uuid.UUID) (Snapshot, error) {
	conn, err := s.store.GetConnectorConnection(ctx, db.GetConnectorConnectionParams{
		MarketplaceAccountID: accountID,
		OrganizationID:       organizationID,
	})
	if errors.Is(err, pgxNoRows) {
		// Foreign account, or an owned account with no connection row. Identical
		// not-found shape either way — reveals nothing and makes no DK call.
		return Snapshot{}, ErrAccountNotFound
	}
	if err != nil {
		return Snapshot{}, fmt.Errorf("connector: load connection: %w", err)
	}
	if ConnectionState(conn.ConnectionState) != Connected {
		return Snapshot{}, ErrNotConnected
	}
	accessTok, refreshTok, err := s.cipher.OpenTokens(conn.AccessTokenSealed, conn.RefreshTokenSealed)
	if err != nil {
		return Snapshot{}, err
	}
	prev := TokenSet{AccessToken: accessTok, RefreshToken: refreshTok}
	if conn.AccessExpiresAt.Valid {
		prev.AccessExpiresAt = conn.AccessExpiresAt.Time
	}
	if conn.RefreshExpiresAt.Valid {
		prev.RefreshExpiresAt = conn.RefreshExpiresAt.Time
	}
	tokens, err := s.dk.Refresh(ctx, prev)
	if err != nil {
		return Snapshot{}, err
	}
	if err := s.persistTokens(ctx, organizationID, accountID, tokens); err != nil {
		return Snapshot{}, err
	}
	if err := s.probeAndPersist(ctx, organizationID, accountID, tokens.AccessToken); err != nil {
		return Snapshot{}, err
	}
	return s.Status(ctx, organizationID, accountID)
}

// Disconnect severs the connection, purges the sealed tokens, and resets every
// capability to Unknown so nothing dependent can run afterwards (ACC-001). Both
// mutations are ORG-SCOPED: a foreign account matches zero rows, so another
// organization's connection is never touched. The caller receives the same
// fail-closed disconnected snapshot as for an unknown account.
func (s *Service) Disconnect(ctx context.Context, organizationID, accountID uuid.UUID) (Snapshot, error) {
	// Disconnect is idempotent: a never-connected (or foreign) account has no row
	// to update, which is not an error — the desired end state (disconnected)
	// already holds and no cross-organization mutation occurs.
	if _, err := s.store.DisconnectConnectorConnection(ctx, db.DisconnectConnectorConnectionParams{
		MarketplaceAccountID: accountID,
		OrganizationID:       organizationID,
	}); err != nil && !errors.Is(err, pgxNoRows) {
		return Snapshot{}, fmt.Errorf("connector: disconnect: %w", err)
	}
	if err := s.store.ResetConnectorCapability(ctx, db.ResetConnectorCapabilityParams{
		MarketplaceAccountID: accountID,
		OrganizationID:       organizationID,
	}); err != nil {
		return Snapshot{}, fmt.Errorf("connector: reset capabilities: %w", err)
	}
	return s.Status(ctx, organizationID, accountID)
}

// Status returns the current connection + capability snapshot. It is ORG-SCOPED:
// an account with no connection row visible to the organization — including a
// foreign account owned by a DIFFERENT organization — reads as Disconnected with
// every capability Unknown. This is the fail-closed default and reveals nothing
// about whether a foreign account exists or is connected.
func (s *Service) Status(ctx context.Context, organizationID, accountID uuid.UUID) (Snapshot, error) {
	snap := Snapshot{AccountID: accountID, Connection: Disconnected, Registry: NewRegistry()}

	conn, err := s.store.GetConnectorConnection(ctx, db.GetConnectorConnectionParams{
		MarketplaceAccountID: accountID,
		OrganizationID:       organizationID,
	})
	switch {
	case err == nil:
		snap.Connection = ConnectionState(conn.ConnectionState)
	case errors.Is(err, pgxNoRows):
		return snap, nil
	default:
		return Snapshot{}, fmt.Errorf("connector: load connection: %w", err)
	}

	reg, err := s.capabilityRegistry(ctx, organizationID, accountID)
	if err != nil {
		return Snapshot{}, err
	}
	snap.Registry = reg
	return snap, nil
}

// capabilityRegistry loads the account's persisted capability snapshot into a
// fail-closed Registry. Any capability missing from storage stays Unknown
// (NewRegistryFrom preserves the Unknown default), so an absent row can never
// read as Supported. It is the single loader shared by Status and every
// capability guard, so there is exactly one path from persisted state to an
// enforcement decision (§15.2 capability-gating invariant).
func (s *Service) capabilityRegistry(ctx context.Context, organizationID, accountID uuid.UUID) (*Registry, error) {
	rows, err := s.store.ListConnectorCapabilities(ctx, db.ListConnectorCapabilitiesParams{
		MarketplaceAccountID: accountID,
		OrganizationID:       organizationID,
	})
	if err != nil {
		return nil, fmt.Errorf("connector: list capabilities: %w", err)
	}
	statuses := make([]CapabilityStatus, 0, len(rows))
	for _, r := range rows {
		statuses = append(statuses, capabilityStatusFrom(r))
	}
	return NewRegistryFrom(statuses), nil
}

// requireCapabilities loads the persisted registry and requires every listed
// capability to be Supported. It is the centralized enforcement point dependent
// operations call BEFORE decrypting a token or making any DK request: a single
// non-Supported capability returns ErrCapabilityNotSupported and the operation
// fails closed (§15.2, "Unknown never enables dependent logic").
func (s *Service) requireCapabilities(ctx context.Context, organizationID, accountID uuid.UUID, caps ...Capability) error {
	reg, err := s.capabilityRegistry(ctx, organizationID, accountID)
	if err != nil {
		return err
	}
	for _, c := range caps {
		if err := reg.Require(c); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) persistTokens(ctx context.Context, organizationID, accountID uuid.UUID, tokens TokenSet) error {
	access, refresh, err := s.cipher.SealTokens(tokens)
	if err != nil {
		return err
	}
	_, err = s.store.UpsertConnectorConnection(ctx, db.UpsertConnectorConnectionParams{
		MarketplaceAccountID: accountID,
		OrganizationID:       organizationID,
		AccessTokenSealed:    access,
		RefreshTokenSealed:   refresh,
		AccessExpiresAt:      timestamptz(tokens.AccessExpiresAt),
		RefreshExpiresAt:     timestamptz(tokens.RefreshExpiresAt),
		KeyVersion:           s.cipher.Version(),
	})
	if err != nil {
		return fmt.Errorf("connector: persist tokens: %w", err)
	}
	return nil
}

func (s *Service) seedCapabilities(ctx context.Context, organizationID, accountID uuid.UUID) error {
	for _, c := range AllCapabilities() {
		if err := s.store.SeedConnectorCapability(ctx, db.SeedConnectorCapabilityParams{
			MarketplaceAccountID: accountID,
			OrganizationID:       organizationID,
			Capability:           string(c),
		}); err != nil {
			return fmt.Errorf("connector: seed capability %s: %w", c, err)
		}
	}
	return nil
}

func (s *Service) probeAndPersist(ctx context.Context, organizationID, accountID uuid.UUID, accessToken string) error {
	for _, r := range s.dk.Probe(ctx, accessToken, s.opts) {
		if _, err := s.store.SetConnectorCapabilityStatus(ctx, db.SetConnectorCapabilityStatusParams{
			MarketplaceAccountID: accountID,
			OrganizationID:       organizationID,
			Capability:           string(r.Capability),
			Status:               string(r.State),
			Detail:               text(r.Detail),
			LastVerifiedAt:       timestamptz(r.VerifiedAt),
		}); err != nil {
			return fmt.Errorf("connector: persist capability %s: %w", r.Capability, err)
		}
	}
	return nil
}
