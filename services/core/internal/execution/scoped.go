// Tenant-scoping seam (issue #102): the transport-facing execution operations
// resolve the authenticated principal's marketplace account from its organization
// and predicate EVERY read and mutation on that account. An action/card/execution
// owned by another account is indistinguishable from a missing one (uniform
// not-found) and is never executed, retried, or disclosed. Ownership is derived
// from the principal's organization, NEVER from a request body.
package execution

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mhosseinab/market-ops/services/core/internal/audit"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
)

// ErrAccountNotFound is returned when a request names a marketplace account the
// caller's organization does not own (issue #102). It maps to the same 404 a
// missing resource returns, so a foreign account is never revealed.
var ErrAccountNotFound = errors.New("execution: account not found")

// accountForOrg resolves the single marketplace account owned by organizationID
// (org ↔ account is 1:1). A nil/unknown organization yields pgx.ErrNoRows, so a
// caller with no resolvable account fails closed.
func (s *Service) accountForOrg(ctx context.Context, organizationID uuid.UUID) (uuid.UUID, error) {
	acct, err := db.New(s.pool).GetMarketplaceAccountByOrganization(ctx, organizationID)
	if err != nil {
		return uuid.Nil, err
	}
	return acct.ID, nil
}

// ExecuteForOrg revalidates and executes an approved card ONLY when it belongs to
// the caller's account (issue #102). The ownership check reads the card scoped
// BEFORE any state change or external write, so a cross-account execution returns
// pgx.ErrNoRows with NO side effect (no state row, no execution record, no write).
func (s *Service) ExecuteForOrg(ctx context.Context, organizationID, cardID uuid.UUID, actor audit.Actor) (ExecuteResult, error) {
	account, err := s.accountForOrg(ctx, organizationID)
	if err != nil {
		return ExecuteResult{}, err
	}
	if _, err := db.New(s.pool).GetApprovalCardForAccount(ctx, db.GetApprovalCardForAccountParams{
		ID:                   cardID,
		MarketplaceAccountID: account,
	}); err != nil {
		return ExecuteResult{}, err // foreign/missing card → uniform not-found, no side effect.
	}
	return s.Execute(ctx, cardID, actor)
}

// RetryForOrg gates a retry ONLY when the action's card belongs to the caller's
// account (issue #102). A foreign or unknown action resolves to no execution record
// under the account scope and returns ErrNoExecution (404), never another account's
// retry.
func (s *Service) RetryForOrg(ctx context.Context, organizationID, actionID uuid.UUID, actor audit.Actor) (RetryResult, error) {
	account, err := s.accountForOrg(ctx, organizationID)
	if err != nil {
		return RetryResult{}, err
	}
	if _, err := db.New(s.pool).GetActionExecutionByActionForAccount(ctx, db.GetActionExecutionByActionForAccountParams{
		ActionID:             actionID,
		MarketplaceAccountID: account,
	}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return RetryResult{}, ErrNoExecution
		}
		return RetryResult{}, err
	}
	return s.Retry(ctx, actionID, actor)
}

// GetExecutionForOrg returns an action's execution record ONLY when its card
// belongs to the caller's account (issue #102). A foreign action returns
// pgx.ErrNoRows.
func (s *Service) GetExecutionForOrg(ctx context.Context, organizationID, actionID uuid.UUID) (db.ActionExecution, error) {
	account, err := s.accountForOrg(ctx, organizationID)
	if err != nil {
		return db.ActionExecution{}, err
	}
	return db.New(s.pool).GetActionExecutionByActionForAccount(ctx, db.GetActionExecutionByActionForAccountParams{
		ActionID:             actionID,
		MarketplaceAccountID: account,
	})
}

// GetUnifiedActionForOrg resolves an action across BOTH execution modes (issue
// #106) scoped to the caller's account (issue #102): a write execution OR a
// recommend-only action, each looked up under the caller's resolved account so a
// foreign-account action matches no row and returns pgx.ErrNoRows (a uniform
// not-found, never disclosed). A write record takes precedence (the two are
// mutually exclusive per action), preserving #106's rule that a stray
// recommend-only row never masks a real external write.
func (s *Service) GetUnifiedActionForOrg(ctx context.Context, organizationID, actionID uuid.UUID) (UnifiedAction, error) {
	account, err := s.accountForOrg(ctx, organizationID)
	if err != nil {
		return UnifiedAction{}, err
	}
	q := db.New(s.pool)
	if exec, err := q.GetActionExecutionByActionForAccount(ctx, db.GetActionExecutionByActionForAccountParams{
		ActionID:             actionID,
		MarketplaceAccountID: account,
	}); err == nil {
		return unifiedFromExecution(exec), nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return UnifiedAction{}, err
	}
	if ro, err := q.GetRecommendOnlyActionForAccount(ctx, db.GetRecommendOnlyActionForAccountParams{
		ActionID:             actionID,
		MarketplaceAccountID: account,
	}); err == nil {
		return unifiedFromRecommendOnly(ro), nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return UnifiedAction{}, err
	}
	return UnifiedAction{}, pgx.ErrNoRows
}

// ListUnifiedByAccountForOrg projects both execution modes for the caller's OWN
// account (issue #106) scoped to the caller (issue #102). The requested account
// MUST equal the caller's resolved account; a foreign account id yields
// ErrAccountNotFound, never another tenant's projection.
func (s *Service) ListUnifiedByAccountForOrg(ctx context.Context, organizationID, requestedAccount uuid.UUID, limit int32) ([]UnifiedAction, error) {
	account, err := s.accountForOrg(ctx, organizationID)
	if err != nil {
		return nil, err
	}
	if requestedAccount != account {
		return nil, ErrAccountNotFound
	}
	return s.ListUnifiedByAccount(ctx, account, limit)
}

// ListPendingReconciliationForOrg returns the pending-reconciliation queue for the
// caller's own account only (issue #102). The requested account MUST equal the
// caller's resolved account; a foreign account id yields ErrAccountNotFound.
func (s *Service) ListPendingReconciliationForOrg(ctx context.Context, organizationID, requestedAccount uuid.UUID, limit int32) ([]db.ActionExecution, error) {
	account, err := s.accountForOrg(ctx, organizationID)
	if err != nil {
		return nil, err
	}
	if requestedAccount != account {
		return nil, ErrAccountNotFound
	}
	return s.ListPendingReconciliation(ctx, account, limit)
}
