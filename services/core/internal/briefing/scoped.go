// Tenant-scoping seam (issue #131, mirroring issue #67/#113/#237/#102): the
// transport-facing daily-briefing read resolves the authenticated principal's
// marketplace account from its organization and predicates the read on that account.
// A briefing belonging to another account is indistinguishable from a missing one
// (uniform not-found) and is never disclosed. Ownership is derived from the
// principal's organization, NEVER from the caller-supplied marketplaceAccountId —
// that is a selector, not authorization. Notification/briefing feeds are personal
// data, so cross-tenant disclosure here is a §4.6 never-cut breach.
package briefing

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mhosseinab/market-ops/services/core/internal/db"
)

// ErrAccountNotFound is returned when a request names a marketplace account the
// caller's organization does not own, or when the caller resolves to no account at
// all (an org-less principal, OrganizationID == uuid.Nil). The transport maps it to
// the SAME 404 a genuinely-missing briefing returns, so a foreign account is never
// revealed and there is no existence oracle (issue #131).
var ErrAccountNotFound = errors.New("briefing: account not found")

// accountForOrg resolves the single marketplace account owned by organizationID
// (org ↔ account is 1:1). A nil/unknown organization yields ErrAccountNotFound, so a
// caller with no resolvable account fails closed before any read.
func (s *Service) accountForOrg(ctx context.Context, organizationID uuid.UUID) (uuid.UUID, error) {
	acct, err := db.New(s.pool).GetMarketplaceAccountByOrganization(ctx, organizationID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, ErrAccountNotFound
		}
		return uuid.Nil, fmt.Errorf("briefing: resolve account for org: %w", err)
	}
	return acct.ID, nil
}

// GetForOrg returns the caller's OWN account stored daily briefing (issue #131). The
// requested account MUST equal the caller's resolved account; a foreign or org-less
// caller yields ErrAccountNotFound (uniform not-found, no existence oracle), never
// another tenant's briefing. Only after ownership is confirmed does the account-scoped
// Get run.
func (s *Service) GetForOrg(ctx context.Context, organizationID, requestedAccount uuid.UUID, day time.Time) (Briefing, error) {
	account, err := s.accountForOrg(ctx, organizationID)
	if err != nil {
		return Briefing{}, err
	}
	if requestedAccount != account {
		return Briefing{}, ErrAccountNotFound
	}
	return s.Get(ctx, account, day)
}

// LatestBeforeForOrg applies the same tenant-derived account scope as GetForOrg
// before reading historical briefing provenance. A foreign selector remains
// indistinguishable from an account with no readable briefing.
func (s *Service) LatestBeforeForOrg(ctx context.Context, organizationID, requestedAccount uuid.UUID, before time.Time) (Briefing, error) {
	account, err := s.accountForOrg(ctx, organizationID)
	if err != nil {
		return Briefing{}, err
	}
	if requestedAccount != account {
		return Briefing{}, ErrAccountNotFound
	}
	return s.LatestBefore(ctx, account, before)
}
