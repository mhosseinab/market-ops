// Tenant-scoping seam (issue #113, mirroring issue #237/#102): the transport-facing
// notification feed, unread badge, and acknowledgement resolve the authenticated
// principal's marketplace account from its organization and predicate every query on
// that account. A feed or notification belonging to another account is
// indistinguishable from a missing one (uniform not-found) and is never read or
// acknowledged. Ownership is derived from the principal's organization, NEVER from a
// caller-supplied marketplaceAccountId or notificationId — those are selectors, not
// authorization.
package notify

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
// there is no existence oracle (issue #113). A foreign notification id under a valid
// own account is NOT an ErrAccountNotFound: it flows through the account-scoped
// query and returns an empty feed / idempotent no-op, so it too discloses nothing.
var ErrAccountNotFound = errors.New("notify: account not found")

// accountForOrg resolves the single marketplace account owned by organizationID
// (org ↔ account is 1:1). A nil/unknown organization yields ErrAccountNotFound, so
// a caller with no resolvable account fails closed.
func (s *Store) accountForOrg(ctx context.Context, organizationID uuid.UUID) (uuid.UUID, error) {
	acct, err := db.New(s.pool).GetMarketplaceAccountByOrganization(ctx, organizationID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, ErrAccountNotFound
		}
		return uuid.Nil, err
	}
	return acct.ID, nil
}

// ListForOrg returns the caller's OWN account notification feed (issue #113). The
// requested account MUST equal the caller's resolved account; a foreign or org-less
// caller yields ErrAccountNotFound, never another tenant's feed.
func (s *Store) ListForOrg(ctx context.Context, organizationID, requestedAccount uuid.UUID) ([]Notification, error) {
	account, err := s.accountForOrg(ctx, organizationID)
	if err != nil {
		return nil, err
	}
	if requestedAccount != account {
		return nil, ErrAccountNotFound
	}
	return s.List(ctx, account)
}

// UnreadCountForOrg returns the caller's OWN account unread badge (issue #113). The
// requested account MUST equal the caller's resolved account; a foreign or org-less
// caller yields ErrAccountNotFound, never another tenant's count.
func (s *Store) UnreadCountForOrg(ctx context.Context, organizationID, requestedAccount uuid.UUID) (int64, error) {
	account, err := s.accountForOrg(ctx, organizationID)
	if err != nil {
		return 0, err
	}
	if requestedAccount != account {
		return 0, ErrAccountNotFound
	}
	return s.UnreadCount(ctx, account)
}

// FeedForOrg returns the caller's OWN account notification feed AND its account-wide
// unread badge from ONE consistent database snapshot (issue #129), preserving the
// issue #113 tenant scoping EXACTLY: the account is resolved FROM the authenticated
// organization and the requested account MUST equal it — a foreign or org-less caller
// yields ErrAccountNotFound (uniform not-found, no existence oracle), never another
// tenant's feed or count. Ownership is enforced BEFORE the snapshot read; the read
// itself observes the feed items and the badge under a single MVCC snapshot, so the
// two can never describe different database states, and any component failure returns
// NO partial feed (fail closed).
func (s *Store) FeedForOrg(ctx context.Context, organizationID, requestedAccount uuid.UUID) (Feed, error) {
	account, err := s.accountForOrg(ctx, organizationID)
	if err != nil {
		return Feed{}, err
	}
	if requestedAccount != account {
		return Feed{}, ErrAccountNotFound
	}
	return s.snapshotFeed(ctx, account)
}

// MarkReadForOrg acknowledges one notification under the caller's OWN account (issue
// #113). Ownership is resolved and enforced BEFORE any state change: a foreign or
// org-less caller — including one supplying another tenant's account id — returns
// ErrAccountNotFound with NO update to the append-only read-state projection. Only
// after the account is confirmed owned does the FROM-guarded MarkRead run under the
// resolved account: a foreign notification id under the own account matches nothing
// and returns an idempotent changed=false (never a cross-tenant read-state write and
// never an existence oracle).
func (s *Store) MarkReadForOrg(ctx context.Context, organizationID, requestedAccount, id uuid.UUID) (Notification, bool, error) {
	account, err := s.accountForOrg(ctx, organizationID)
	if err != nil {
		return Notification{}, false, err
	}
	if requestedAccount != account {
		return Notification{}, false, ErrAccountNotFound
	}
	return s.MarkRead(ctx, account, id)
}
