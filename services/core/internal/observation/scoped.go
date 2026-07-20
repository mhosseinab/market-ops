// Tenant-scoping seam (issue #237, mirroring issue #102): the transport-facing
// Market conflict read resolves the authenticated principal's marketplace account
// from its organization and predicates the read on that account. Another account's
// conflicted Observed Offers are indistinguishable from an empty/missing result
// (uniform not-found) and are never disclosed. Ownership is derived from the
// principal's organization, NEVER from a request param.
package observation

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mhosseinab/market-ops/services/core/internal/db"
)

// ErrAccountNotFound is returned when a request names a marketplace account the
// caller's organization does not own, or when the caller resolves to no account at
// all (an org-less principal, OrganizationID == uuid.Nil). It maps to the same 404
// a genuinely missing resource returns, so a foreign account is never revealed and
// there is no existence oracle (issue #237).
var ErrAccountNotFound = errors.New("observation: account not found")

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

// ListConflictedObservedOffersForOrg returns the caller's OWN account conflicted
// Observed Offers (issue #237). The requested account MUST equal the caller's
// resolved account; a foreign or org-less caller yields ErrAccountNotFound, never
// another tenant's Market conflict view.
func (s *Service) ListConflictedObservedOffersForOrg(ctx context.Context, organizationID, requestedAccount uuid.UUID) ([]db.ObservedOffer, error) {
	account, err := s.accountForOrg(ctx, organizationID)
	if err != nil {
		return nil, err
	}
	if requestedAccount != account {
		return nil, ErrAccountNotFound
	}
	return s.ListConflictedObservedOffers(ctx, account)
}
