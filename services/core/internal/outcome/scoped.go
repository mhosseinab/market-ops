// Tenant-scoping seam (issue #102): the transport-facing outcome reads resolve the
// authenticated principal's marketplace account from its organization and predicate
// every read on that account. An outcome window whose action/card belongs to
// another account is indistinguishable from a missing one (uniform not-found) and
// is never disclosed. Ownership is derived from the principal's organization,
// NEVER from a request body.
package outcome

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mhosseinab/market-ops/services/core/internal/db"
)

// ErrAccountNotFound is returned when a request names a marketplace account the
// caller's organization does not own (issue #102). It maps to the same 404 a
// missing resource returns, so a foreign account is never revealed.
var ErrAccountNotFound = errors.New("outcome: account not found")

// accountForOrg resolves the single marketplace account owned by organizationID
// (org ↔ account is 1:1). A nil/unknown organization yields pgx.ErrNoRows.
func (s *Service) accountForOrg(ctx context.Context, organizationID uuid.UUID) (uuid.UUID, error) {
	acct, err := db.New(s.pool).GetMarketplaceAccountByOrganization(ctx, organizationID)
	if err != nil {
		return uuid.Nil, err
	}
	return acct.ID, nil
}

// GetForOrg returns an action's outcome window and result ONLY when its card
// belongs to the caller's account (issue #102). A foreign action returns
// ErrNoWindow — the same fail-closed not-found a genuinely windowless action
// returns.
func (s *Service) GetForOrg(ctx context.Context, organizationID, actionID uuid.UUID) (View, error) {
	account, err := s.accountForOrg(ctx, organizationID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return View{}, ErrNoWindow
		}
		return View{}, err
	}
	q := db.New(s.pool)
	win, err := q.GetOutcomeWindowByActionForAccount(ctx, db.GetOutcomeWindowByActionForAccountParams{
		ActionID:             actionID,
		MarketplaceAccountID: account,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return View{}, ErrNoWindow
	}
	if err != nil {
		return View{}, err
	}
	res, err := q.GetOutcomeResult(ctx, win.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return View{Window: win}, nil
	}
	if err != nil {
		return View{}, err
	}
	return View{Window: win, HasResult: true, Result: res}, nil
}

// ListByAccountForOrg returns the outcome windows for the caller's own account only
// (issue #102). The requested account MUST equal the caller's resolved account; a
// foreign account id yields ErrAccountNotFound.
func (s *Service) ListByAccountForOrg(ctx context.Context, organizationID, requestedAccount uuid.UUID, limit int32) ([]db.ListOutcomeWindowsByAccountRow, error) {
	account, err := s.accountForOrg(ctx, organizationID)
	if err != nil {
		return nil, err
	}
	if requestedAccount != account {
		return nil, ErrAccountNotFound
	}
	return s.ListByAccount(ctx, account, limit)
}
