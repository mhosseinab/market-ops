// Tenant-scoping seam (issue #237, mirroring issue #102): the transport-facing
// guardrail read + write resolve the authenticated principal's marketplace account
// from its organization and predicate the operation on that account. A guardrail
// config owned by another account is indistinguishable from a missing one (uniform
// not-found) and is never read or written. Ownership is derived from the principal's
// organization, NEVER from a request body — doubly binding here because SetForOrg is
// a money/policy write (contribution floor, movement cap, cooldown), a §4.6
// tenant-integrity surface.
package guardrail

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mhosseinab/market-ops/services/core/internal/audit"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
)

// ErrAccountNotFound is returned when a request names a marketplace account the
// caller's organization does not own, or when the caller resolves to no account at
// all (an org-less principal, OrganizationID == uuid.Nil). It maps to the same 404
// a genuinely missing/unconfigured resource returns, so a foreign account is never
// revealed and there is no existence oracle (issue #237).
var ErrAccountNotFound = errors.New("guardrail: account not found")

// accountForOrg resolves the single marketplace account owned by organizationID
// (org ↔ account is 1:1). A nil/unknown organization yields ErrAccountNotFound, so
// a caller with no resolvable account fails closed.
func (s *Service) accountForOrg(ctx context.Context, organizationID uuid.UUID) (uuid.UUID, error) {
	acct, err := db.New(s.pool).GetMarketplaceAccountByOrganization(ctx, organizationID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, ErrAccountNotFound
		}
		return uuid.Nil, err
	}
	return acct.ID, nil
}

// GetForOrg reads the caller's OWN account guardrails (issue #237). The requested
// account MUST equal the caller's resolved account; a foreign or org-less caller
// yields ErrAccountNotFound (a uniform not-found), never another tenant's config.
func (s *Service) GetForOrg(ctx context.Context, organizationID, requestedAccount uuid.UUID) (ConfigView, error) {
	account, err := s.accountForOrg(ctx, organizationID)
	if err != nil {
		return ConfigView{}, err
	}
	if requestedAccount != account {
		return ConfigView{}, ErrAccountNotFound
	}
	return s.Get(ctx, account)
}

// SetForOrg writes the caller's OWN account guardrails (issue #237). Ownership is
// resolved and enforced BEFORE any state change or audit append: a foreign or
// org-less caller returns ErrAccountNotFound with NO mutation and NO audit row for
// the foreign account. Only after the account is confirmed owned does the write
// (with its atomic AUD-001 audit append) run under the resolved account.
func (s *Service) SetForOrg(ctx context.Context, organizationID, requestedAccount uuid.UUID, actor audit.Actor, settings Settings) (ConfigView, error) {
	account, err := s.accountForOrg(ctx, organizationID)
	if err != nil {
		return ConfigView{}, err
	}
	if requestedAccount != account {
		return ConfigView{}, ErrAccountNotFound
	}
	return s.Set(ctx, account, actor, settings)
}
