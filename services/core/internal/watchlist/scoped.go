// Tenant-scoping seam (issue #237, mirroring issue #102): the transport-facing
// watchlist read + add resolve the authenticated principal's marketplace account
// from its organization and predicate the operation on that account. A watchlist
// belonging to another account is indistinguishable from a missing one (uniform
// not-found) and is never read or mutated. Ownership is derived from the principal's
// organization, NEVER from a request body.
package watchlist

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
// a genuinely missing resource returns, so a foreign account is never revealed and
// there is no existence oracle (issue #237).
var ErrAccountNotFound = errors.New("watchlist: account not found")

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

// ListForOrg returns the caller's OWN account watchlist (issue #237). The requested
// account MUST equal the caller's resolved account; a foreign or org-less caller
// yields ErrAccountNotFound, never another tenant's list.
func (s *Service) ListForOrg(ctx context.Context, organizationID, requestedAccount uuid.UUID) ([]db.WatchlistEntry, error) {
	account, err := s.accountForOrg(ctx, organizationID)
	if err != nil {
		return nil, err
	}
	if requestedAccount != account {
		return nil, ErrAccountNotFound
	}
	return s.List(ctx, account)
}

// AddForOrg adds one Confirmed owned product to the caller's OWN account watchlist
// (issue #237). Ownership is resolved and enforced BEFORE any state change or audit
// append: a foreign or org-less caller returns ErrAccountNotFound with NO insert and
// NO audit row for the foreign account. Only after the account is confirmed owned
// does the capped, audited insert run under the resolved account.
func (s *Service) AddForOrg(ctx context.Context, organizationID, requestedAccount, variant uuid.UUID, actor audit.Actor) (db.WatchlistEntry, error) {
	account, err := s.accountForOrg(ctx, organizationID)
	if err != nil {
		return db.WatchlistEntry{}, err
	}
	if requestedAccount != account {
		return db.WatchlistEntry{}, ErrAccountNotFound
	}
	return s.Add(ctx, account, variant, actor)
}
