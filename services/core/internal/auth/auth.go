// Package auth is the authentication plane for the core service: argon2id
// credential verification and server-side session issuance/resolution (PRD §2.2,
// §8, §12.3, ACC-002). It produces a Principal — the authenticated identity plus
// role — that the permission middleware feeds to the single perm matrix.
//
// Session security posture (PRD §8, §12.3):
//   - The session token is a 256-bit cryptographically random value returned to
//     the client ONLY in a secure httpOnly cookie; it is never placed in the
//     response body and never in client-side storage.
//   - The database stores only the SHA-256 of the token, so a database read
//     alone cannot mint a valid cookie.
//   - Resolution is fail closed: an absent, unknown, or expired token yields no
//     Principal and therefore no authorization.
package auth

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

	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/normalize"
	"github.com/mhosseinab/market-ops/services/core/internal/perm"
)

// ErrInvalidCredentials is the single failure returned for any bad login: an
// unknown email, a missing credential, or a wrong password all map here, so the
// response never leaks which field was wrong (fail closed, no user enumeration).
var ErrInvalidCredentials = errors.New("auth: invalid credentials")

// ErrNoSession is returned when a token resolves to no live session.
var ErrNoSession = errors.New("auth: no valid session")

// DefaultSessionTTL is how long a new session stays valid.
const DefaultSessionTTL = 12 * time.Hour

// Principal is an authenticated identity. Role drives every authorization
// decision through perm.Can; it is the ONLY authority source (never inferred
// from the token's shape).
type Principal struct {
	UserID         uuid.UUID
	OrganizationID uuid.UUID
	Email          string
	Role           perm.Role
	ExpiresAt      time.Time
}

// Session is a freshly issued session: the raw token (to set as a cookie, once)
// and the principal it authenticates.
type Session struct {
	Token     string // raw opaque token — set as a cookie, never persisted
	Principal Principal
}

// Store is the persistence surface auth depends on: exactly the auth queries.
// *db.Queries satisfies it; tests substitute a fake. Kept minimal (interface
// segregation) so auth never reaches for unrelated queries.
type Store interface {
	GetUserByEmail(ctx context.Context, email string) (db.User, error)
	GetUserCredential(ctx context.Context, userID uuid.UUID) (db.UserCredential, error)
	CreateSession(ctx context.Context, arg db.CreateSessionParams) (db.Session, error)
	GetSessionUser(ctx context.Context, tokenHash string) (db.GetSessionUserRow, error)
	DeleteSession(ctx context.Context, tokenHash string) error
	UpsertUserCredential(ctx context.Context, arg db.UpsertUserCredentialParams) error
	ListUsersByOrganization(ctx context.Context, organizationID uuid.UUID) ([]db.User, error)
}

// Clock supplies the current time; overridable in tests.
type Clock func() time.Time

// Service issues and resolves sessions and verifies credentials.
type Service struct {
	store Store
	ttl   time.Duration
	now   Clock
}

// NewService wires an auth Service with the default TTL and wall clock.
func NewService(store Store) *Service {
	return &Service{store: store, ttl: DefaultSessionTTL, now: time.Now}
}

// WithTTL overrides the session lifetime.
func (s *Service) WithTTL(ttl time.Duration) *Service { s.ttl = ttl; return s }

// WithClock overrides the clock (tests).
func (s *Service) WithClock(now Clock) *Service { s.now = now; return s }

// SetPassword hashes plain with argon2id and stores it for userID (initial set
// or rotation). Used by provisioning/admin flows and tests; never logs the
// plaintext.
func (s *Service) SetPassword(ctx context.Context, userID uuid.UUID, plain string) error {
	hash, err := HashPassword(plain)
	if err != nil {
		return err
	}
	if err := s.store.UpsertUserCredential(ctx, db.UpsertUserCredentialParams{
		UserID:       userID,
		PasswordHash: hash,
	}); err != nil {
		return fmt.Errorf("auth: store credential: %w", err)
	}
	return nil
}

// Login verifies email/password and, on success, opens a server-side session.
// Every failure mode returns ErrInvalidCredentials without distinction. To keep
// timing uniform against user enumeration, a wrong email still runs an argon2id
// verification against a dummy hash before failing.
func (s *Service) Login(ctx context.Context, email, password string) (Session, error) {
	// Normalize the login identifier to the same canonical form the write path
	// stores (issue #12, #201): normalize.Email matches the SQL email_canonical()
	// used by the write, the unique index, and this lookup, so a padded identifier
	// (tab/newline included) resolves exactly one principal and never another org's
	// row. The DB re-applies email_canonical to both sides, so it stays the
	// enforcement authority even if this pre-normalization ever drifted.
	email = normalize.Email(email)
	user, err := s.store.GetUserByEmail(ctx, email)
	if errors.Is(err, pgx.ErrNoRows) {
		// Constant-work path: verify against a throwaway hash so a missing user
		// and a wrong password take comparable time.
		_, _ = VerifyPassword(dummyHash, password)
		return Session{}, ErrInvalidCredentials
	}
	if err != nil {
		return Session{}, fmt.Errorf("auth: load user: %w", err)
	}

	cred, err := s.store.GetUserCredential(ctx, user.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		_, _ = VerifyPassword(dummyHash, password)
		return Session{}, ErrInvalidCredentials
	}
	if err != nil {
		return Session{}, fmt.Errorf("auth: load credential: %w", err)
	}

	ok, err := VerifyPassword(cred.PasswordHash, password)
	if err != nil {
		// Malformed stored hash: fail closed, do not authenticate.
		return Session{}, ErrInvalidCredentials
	}
	if !ok {
		return Session{}, ErrInvalidCredentials
	}

	return s.issue(ctx, user)
}

// issue mints a random token, persists only its hash, and returns the session.
func (s *Service) issue(ctx context.Context, user db.User) (Session, error) {
	token, err := newToken()
	if err != nil {
		return Session{}, err
	}
	expires := s.now().Add(s.ttl).UTC()
	row, err := s.store.CreateSession(ctx, db.CreateSessionParams{
		TokenHash: hashToken(token),
		UserID:    user.ID,
		ExpiresAt: expires,
	})
	if err != nil {
		return Session{}, fmt.Errorf("auth: create session: %w", err)
	}
	return Session{
		Token: token,
		Principal: Principal{
			UserID:         user.ID,
			OrganizationID: user.OrganizationID,
			Email:          user.Email,
			Role:           perm.Role(user.Role),
			ExpiresAt:      row.ExpiresAt.UTC(),
		},
	}, nil
}

// Resolve maps a raw session token to its Principal. It fails closed: an empty,
// unknown, or expired token returns ErrNoSession (the query already excludes
// expired rows).
func (s *Service) Resolve(ctx context.Context, token string) (Principal, error) {
	if token == "" {
		return Principal{}, ErrNoSession
	}
	row, err := s.store.GetSessionUser(ctx, hashToken(token))
	if errors.Is(err, pgx.ErrNoRows) {
		return Principal{}, ErrNoSession
	}
	if err != nil {
		return Principal{}, fmt.Errorf("auth: resolve session: %w", err)
	}
	return Principal{
		UserID:         row.UserID,
		OrganizationID: row.OrganizationID,
		Email:          row.Email,
		Role:           perm.Role(row.Role),
		ExpiresAt:      row.ExpiresAt.UTC(),
	}, nil
}

// Logout deletes the server-side session for token. Idempotent: closing an
// absent session is not an error.
func (s *Service) Logout(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	if err := s.store.DeleteSession(ctx, hashToken(token)); err != nil {
		return fmt.Errorf("auth: delete session: %w", err)
	}
	return nil
}

// ListUsers returns every user in organizationID, in a stable order (PD-3 item
// 7, S37 read). Reading is L1 — the transport boundary (perm) restricts nothing
// further here; scoping to the caller's OWN organization is the caller's
// responsibility (the gateway handler passes the authenticated principal's
// OrganizationID, never a client-supplied one).
func (s *Service) ListUsers(ctx context.Context, organizationID uuid.UUID) ([]db.User, error) {
	users, err := s.store.ListUsersByOrganization(ctx, organizationID)
	if err != nil {
		return nil, fmt.Errorf("auth: list users: %w", err)
	}
	return users, nil
}

// newToken returns a 256-bit random token as URL-safe hex.
func newToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("auth: read token entropy: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// hashToken returns the SHA-256 (hex) of a raw token — the value stored and
// looked up. The raw token never touches the database.
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// dummyHash is an argon2id hash of a random value, computed once at startup. It
// is verified against on the missing-user / missing-credential login paths so
// those take work comparable to a real password check, denying an attacker a
// timing oracle for user enumeration. It is never a credential for any account.
var dummyHash = func() string {
	tok, err := newToken()
	if err != nil {
		return ""
	}
	h, err := HashPassword(tok)
	if err != nil {
		return ""
	}
	return h
}()
