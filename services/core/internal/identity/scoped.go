// Tenant-scoping seam (issue #346, mirroring issue #131): the transport-facing
// Needs Review read resolves the authenticated principal's marketplace account
// from its organization and predicates the read on that account. Another account's
// identity-mapping queue (supplier codes / native product ids) is indistinguishable
// from an empty/missing result (uniform not-found) and is never disclosed.
// Ownership is derived from the principal's organization, NEVER from a request param.
package identity

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
// there is no existence oracle (issue #346, mirroring issues #131/#67/#113).
var ErrAccountNotFound = errors.New("identity: account not found")

// accountForOrg resolves the single marketplace account owned by organizationID
// (org ↔ account is 1:1). A nil/unknown organization yields ErrAccountNotFound, so
// a caller with no resolvable account fails closed. This is the SAME
// GetMarketplaceAccountByOrganization linkage the observation/cost/briefing scoping
// (issue #131) uses — one authorization rule, not a second surface.
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

// NeedsReviewQueueForOrg returns the caller's OWN account Needs Review queue
// (issue #346). The account is resolved from the authenticated organization and the
// requested account MUST equal it; a foreign or org-less caller yields
// ErrAccountNotFound (uniform not-found, no existence oracle), never another
// tenant's queue. Ownership is derived from the principal's organization, NEVER from
// the request param. The read is a SELECT already SQL-bound to the resolved account
// (ListNeedsReviewQueue filters on marketplace_account_id), so this adds the
// org→account resolution and ownership assertion in front of it.
func (s *Service) NeedsReviewQueueForOrg(ctx context.Context, organizationID, requestedAccount uuid.UUID) ([]QueueItem, error) {
	account, err := s.accountForOrg(ctx, organizationID)
	if err != nil {
		return nil, err
	}
	if requestedAccount != account {
		return nil, ErrAccountNotFound
	}
	return s.NeedsReviewQueue(ctx, account)
}
