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

// Connect exchanges an authorization code for tokens, seals and persists them,
// seeds the capability registry at Unknown, then probes every capability. It is
// the connect half of ACC-001.
func (s *Service) Connect(ctx context.Context, accountID uuid.UUID, authCode string) (Snapshot, error) {
	if authCode == "" {
		return Snapshot{}, ErrInvalidAuthCode
	}
	tokens, err := s.dk.ExchangeToken(ctx, authCode)
	if err != nil {
		return Snapshot{}, err
	}
	if err := s.persistTokens(ctx, accountID, tokens); err != nil {
		return Snapshot{}, err
	}
	if err := s.seedCapabilities(ctx, accountID); err != nil {
		return Snapshot{}, err
	}
	if err := s.probeAndPersist(ctx, accountID, tokens.AccessToken); err != nil {
		return Snapshot{}, err
	}
	return s.Status(ctx, accountID)
}

// Refresh rotates the access token from the stored refresh token, re-seals it,
// and re-probes so status/last-verified stay current (ACC-001/ACC-003).
func (s *Service) Refresh(ctx context.Context, accountID uuid.UUID) (Snapshot, error) {
	conn, err := s.store.GetConnectorConnection(ctx, accountID)
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
	if err := s.persistTokens(ctx, accountID, tokens); err != nil {
		return Snapshot{}, err
	}
	if err := s.probeAndPersist(ctx, accountID, tokens.AccessToken); err != nil {
		return Snapshot{}, err
	}
	return s.Status(ctx, accountID)
}

// Disconnect severs the connection, purges the sealed tokens, and resets every
// capability to Unknown so nothing dependent can run afterwards (ACC-001).
func (s *Service) Disconnect(ctx context.Context, accountID uuid.UUID) (Snapshot, error) {
	if _, err := s.store.DisconnectConnectorConnection(ctx, accountID); err != nil {
		return Snapshot{}, fmt.Errorf("connector: disconnect: %w", err)
	}
	if err := s.store.ResetConnectorCapability(ctx, accountID); err != nil {
		return Snapshot{}, fmt.Errorf("connector: reset capabilities: %w", err)
	}
	return s.Status(ctx, accountID)
}

// Status returns the current connection + capability snapshot. An account with
// no connection row reads as Disconnected with every capability Unknown — the
// fail-closed default.
func (s *Service) Status(ctx context.Context, accountID uuid.UUID) (Snapshot, error) {
	snap := Snapshot{AccountID: accountID, Connection: Disconnected, Registry: NewRegistry()}

	conn, err := s.store.GetConnectorConnection(ctx, accountID)
	switch {
	case err == nil:
		snap.Connection = ConnectionState(conn.ConnectionState)
	case errors.Is(err, pgxNoRows):
		return snap, nil
	default:
		return Snapshot{}, fmt.Errorf("connector: load connection: %w", err)
	}

	rows, err := s.store.ListConnectorCapabilities(ctx, accountID)
	if err != nil {
		return Snapshot{}, fmt.Errorf("connector: list capabilities: %w", err)
	}
	statuses := make([]CapabilityStatus, 0, len(rows))
	for _, r := range rows {
		statuses = append(statuses, capabilityStatusFrom(r))
	}
	snap.Registry = NewRegistryFrom(statuses)
	return snap, nil
}

func (s *Service) persistTokens(ctx context.Context, accountID uuid.UUID, tokens TokenSet) error {
	access, refresh, err := s.cipher.SealTokens(tokens)
	if err != nil {
		return err
	}
	_, err = s.store.UpsertConnectorConnection(ctx, db.UpsertConnectorConnectionParams{
		MarketplaceAccountID: accountID,
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

func (s *Service) seedCapabilities(ctx context.Context, accountID uuid.UUID) error {
	for _, c := range AllCapabilities() {
		if err := s.store.SeedConnectorCapability(ctx, db.SeedConnectorCapabilityParams{
			MarketplaceAccountID: accountID,
			Capability:           string(c),
		}); err != nil {
			return fmt.Errorf("connector: seed capability %s: %w", c, err)
		}
	}
	return nil
}

func (s *Service) probeAndPersist(ctx context.Context, accountID uuid.UUID, accessToken string) error {
	for _, r := range s.dk.Probe(ctx, accessToken, s.opts) {
		if _, err := s.store.SetConnectorCapabilityStatus(ctx, db.SetConnectorCapabilityStatusParams{
			MarketplaceAccountID: accountID,
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
