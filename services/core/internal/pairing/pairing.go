// Package pairing implements the browser-extension pairing plane (PRD §14
// EXT-001). A logged-in human mints a short-lived, single-use pairing code; the
// extension exchanges it for a SCOPED capture/overlay credential bound to one
// marketplace account. The extension never holds a seller-API token — the only
// credential this plane ever issues is the capture credential.
//
// Security posture mirrors the session plane (internal/auth):
//   - The raw pairing code and raw capture credential are 256-bit random values.
//     Only their SHA-256 hashes are stored, so a database read alone can neither
//     reconstruct a live code nor a usable credential.
//   - A code is strictly single-use and short-lived; claiming clears its hash.
//   - Resolution is fail closed: an absent, unknown, expired, or revoked
//     credential yields no account and therefore no authorization (401 upstream).
package pairing

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mhosseinab/market-ops/services/core/internal/db"
)

// ErrNoAccount is returned when the caller's organization has no marketplace
// account to scope a pairing to.
var ErrNoAccount = errors.New("pairing: no marketplace account for organization")

// ErrInvalidCode is returned when a pairing code is unknown, expired, revoked, or
// already claimed. It never distinguishes which, so a code cannot be probed.
var ErrInvalidCode = errors.New("pairing: invalid or expired pairing code")

// ErrInvalidCredential is returned when a presented capture credential resolves
// to no live pairing (unknown, expired, or revoked).
var ErrInvalidCredential = errors.New("pairing: invalid or revoked capture credential")

// DefaultCodeTTL is how long a freshly minted pairing code stays claimable. It is
// deliberately short: a code is a one-time, hand-carried secret.
const DefaultCodeTTL = 5 * time.Minute

// DefaultCredentialTTL is how long a claimed capture credential stays valid
// before the extension must re-pair. Bounded so a leaked credential ages out.
const DefaultCredentialTTL = 30 * 24 * time.Hour

// Code is a freshly minted pairing code: the raw code (displayed once) and its
// scope + expiry.
type Code struct {
	Code                 string
	MarketplaceAccountID uuid.UUID
	ExpiresAt            time.Time
}

// Credential is a freshly issued capture credential: the raw value (returned to
// the extension once) plus its record id, scope, and expiry.
type Credential struct {
	Credential           string
	CredentialID         uuid.UUID
	MarketplaceAccountID uuid.UUID
	ExpiresAt            time.Time
}

// Store is the persistence surface pairing depends on: exactly the pairing
// queries plus the account lookup. *db.Queries satisfies it; tests substitute a
// fake. Kept minimal (interface segregation).
type Store interface {
	GetMarketplaceAccountByOrganization(ctx context.Context, organizationID uuid.UUID) (db.MarketplaceAccount, error)
	CreatePairingCode(ctx context.Context, arg db.CreatePairingCodeParams) (db.ExtensionPairing, error)
	ClaimPairingCode(ctx context.Context, arg db.ClaimPairingCodeParams) (db.ExtensionPairing, error)
	ResolveCaptureCredential(ctx context.Context, credentialHash pgtype.Text) (db.ResolveCaptureCredentialRow, error)
	RevokePairingsForAccount(ctx context.Context, marketplaceAccountID uuid.UUID) error
}

// Clock supplies the current time; overridable in tests.
type Clock func() time.Time

// Service mints pairing codes, claims them into capture credentials, resolves a
// presented credential to its scoped account, and revokes credentials.
type Service struct {
	store   Store
	codeTTL time.Duration
	credTTL time.Duration
	now     Clock
}

// NewService wires a pairing Service with default TTLs and the wall clock.
func NewService(store Store) *Service {
	return &Service{store: store, codeTTL: DefaultCodeTTL, credTTL: DefaultCredentialTTL, now: time.Now}
}

// WithClock overrides the clock (tests).
func (s *Service) WithClock(now Clock) *Service { s.now = now; return s }

// WithTTLs overrides the code and credential lifetimes (tests).
func (s *Service) WithTTLs(code, cred time.Duration) *Service {
	s.codeTTL, s.credTTL = code, cred
	return s
}

// MintCode issues a short-lived, single-use pairing code for the organization's
// marketplace account. The raw code is returned once (to display); only its hash
// is persisted.
func (s *Service) MintCode(ctx context.Context, organizationID uuid.UUID) (Code, error) {
	acct, err := s.store.GetMarketplaceAccountByOrganization(ctx, organizationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Code{}, ErrNoAccount
	}
	if err != nil {
		return Code{}, fmt.Errorf("pairing: load account: %w", err)
	}
	code, err := newSecret()
	if err != nil {
		return Code{}, err
	}
	expires := s.now().Add(s.codeTTL).UTC()
	if _, err := s.store.CreatePairingCode(ctx, db.CreatePairingCodeParams{
		MarketplaceAccountID: acct.ID,
		CodeHash:             text(hashSecret(code)),
		CodeExpiresAt:        expires,
	}); err != nil {
		return Code{}, fmt.Errorf("pairing: create code: %w", err)
	}
	return Code{Code: code, MarketplaceAccountID: acct.ID, ExpiresAt: expires}, nil
}

// Claim exchanges a raw pairing code for a scoped capture credential. The claim
// is atomic and single-use (the query clears the code hash and matches only an
// unclaimed, unexpired, unrevoked row). The raw credential is returned once.
func (s *Service) Claim(ctx context.Context, rawCode string) (Credential, error) {
	if rawCode == "" {
		return Credential{}, ErrInvalidCode
	}
	cred, err := newSecret()
	if err != nil {
		return Credential{}, err
	}
	expires := s.now().Add(s.credTTL).UTC()
	row, err := s.store.ClaimPairingCode(ctx, db.ClaimPairingCodeParams{
		CodeHash:            text(hashSecret(rawCode)),
		CredentialHash:      text(hashSecret(cred)),
		CredentialExpiresAt: pgtype.Timestamptz{Time: expires, Valid: true},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Credential{}, ErrInvalidCode
	}
	if err != nil {
		return Credential{}, fmt.Errorf("pairing: claim code: %w", err)
	}
	return Credential{
		Credential:           cred,
		CredentialID:         row.ID,
		MarketplaceAccountID: row.MarketplaceAccountID,
		ExpiresAt:            expires,
	}, nil
}

// Resolved is the scope a live capture credential authorizes.
type Resolved struct {
	CredentialID         uuid.UUID
	MarketplaceAccountID uuid.UUID
}

// ResolveCredential maps a presented raw capture credential to its scoped
// account. It fails closed: an empty, unknown, expired, or revoked credential
// returns ErrInvalidCredential (the query already excludes revoked/expired rows).
func (s *Service) ResolveCredential(ctx context.Context, rawCredential string) (Resolved, error) {
	if rawCredential == "" {
		return Resolved{}, ErrInvalidCredential
	}
	row, err := s.store.ResolveCaptureCredential(ctx, text(hashSecret(rawCredential)))
	if errors.Is(err, pgx.ErrNoRows) {
		return Resolved{}, ErrInvalidCredential
	}
	if err != nil {
		return Resolved{}, fmt.Errorf("pairing: resolve credential: %w", err)
	}
	return Resolved{CredentialID: row.ID, MarketplaceAccountID: row.MarketplaceAccountID}, nil
}

// RevokeForOrganization revokes every active capture credential for the
// organization's marketplace account (EXT-001 kill switch). Idempotent.
func (s *Service) RevokeForOrganization(ctx context.Context, organizationID uuid.UUID) error {
	acct, err := s.store.GetMarketplaceAccountByOrganization(ctx, organizationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNoAccount
	}
	if err != nil {
		return fmt.Errorf("pairing: load account: %w", err)
	}
	if err := s.store.RevokePairingsForAccount(ctx, acct.ID); err != nil {
		return fmt.Errorf("pairing: revoke: %w", err)
	}
	return nil
}

// newSecret returns a 256-bit random secret as hex. Used for both the pairing
// code and the capture credential.
func newSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("pairing: read entropy: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// hashSecret returns the SHA-256 (hex) of a raw secret — the value stored and
// looked up. The raw secret never touches the database.
func hashSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// text wraps a non-empty string as a valid pgtype.Text.
func text(s string) pgtype.Text {
	return pgtype.Text{String: s, Valid: true}
}
