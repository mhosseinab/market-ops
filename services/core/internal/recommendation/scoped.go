// Tenant-scoping seam (issue #102): the transport-facing recommendation/approval
// operations resolve the authenticated principal's marketplace account from its
// organization and predicate EVERY read and mutation on that account. A card,
// recommendation, or selection set owned by another account is indistinguishable
// from a missing one (uniform pgx.ErrNoRows → 404) and is never disclosed or
// mutated. Ownership is derived from the principal's organization, NEVER from a
// request body — the same posture the connector plane established (S8-AUTHZ-001).
//
// Because org ↔ marketplace_account is 1:1 (marketplace_accounts.organization_id
// is NOT NULL UNIQUE, migration 0001), the organization resolves to exactly one
// account, which is the tenant predicate threaded into the scoped sqlc queries.
package recommendation

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/mhosseinab/market-ops/services/core/internal/approval"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/money"
)

// ErrAccountNotFound is returned when a request names a marketplace account the
// caller's organization does not own (issue #102). It is returned identically for
// a genuinely-absent account and one owned by a DIFFERENT organization, so the
// transport never reveals whether a foreign account exists (no existence oracle);
// the transport maps it to the same 404 a missing resource returns. It carries no
// side effect — the guard runs before any read of foreign rows or any mutation.
var ErrAccountNotFound = errors.New("recommendation: account not found")

// errNotOwnedAccount is the internal alias used by the request-account guards.
var errNotOwnedAccount = ErrAccountNotFound

// accountForOrg resolves the single marketplace account owned by organizationID
// (org ↔ account is 1:1). A nil/unknown organization (e.g. the org-less LLM machine
// principal) matches no account and yields pgx.ErrNoRows, so a caller with no
// resolvable account fails closed — the transport maps that to the same not-found
// a missing resource returns, revealing nothing.
func (s *Service) accountForOrg(ctx context.Context, organizationID uuid.UUID) (uuid.UUID, error) {
	acct, err := db.New(s.pool).GetMarketplaceAccountByOrganization(ctx, organizationID)
	if err != nil {
		return uuid.Nil, err // pgx.ErrNoRows propagates → uniform not-found.
	}
	return acct.ID, nil
}

// GetCardForOrg returns a card ONLY when it belongs to the caller's organization's
// marketplace account (issue #102). A foreign card returns pgx.ErrNoRows, the same
// as a missing card — no cross-tenant disclosure.
func (s *Service) GetCardForOrg(ctx context.Context, organizationID, id uuid.UUID) (db.ApprovalCard, error) {
	account, err := s.accountForOrg(ctx, organizationID)
	if err != nil {
		return db.ApprovalCard{}, err
	}
	return db.New(s.pool).GetApprovalCardForAccount(ctx, db.GetApprovalCardForAccountParams{
		ID:                   id,
		MarketplaceAccountID: account,
	})
}

// GetRecommendationForOrg returns a recommendation ONLY when it belongs to the
// caller's account (issue #102). A foreign recommendation returns pgx.ErrNoRows.
func (s *Service) GetRecommendationForOrg(ctx context.Context, organizationID, id uuid.UUID) (db.Recommendation, error) {
	account, err := s.accountForOrg(ctx, organizationID)
	if err != nil {
		return db.Recommendation{}, err
	}
	return db.New(s.pool).GetRecommendationForAccount(ctx, db.GetRecommendationForAccountParams{
		ID:                   id,
		MarketplaceAccountID: account,
	})
}

// ConfirmIndividualForOrg activates the structured control on a card ONLY when it
// belongs to the caller's account (issue #102). The ownership check reads the card
// scoped BEFORE any mutation, so a cross-account confirmation returns pgx.ErrNoRows
// with NO state change and NO execution intent. The account column on a card is
// immutable (append-only lineage), so this pre-check is airtight; the confirmation
// itself then runs its own serialized, FROM-guarded transaction unchanged.
func (s *Service) ConfirmIndividualForOrg(ctx context.Context, organizationID, cardID uuid.UUID, presented approval.Binding, now time.Time) (ConfirmOutcome, error) {
	account, err := s.accountForOrg(ctx, organizationID)
	if err != nil {
		return ConfirmOutcome{}, err
	}
	if _, err := db.New(s.pool).GetApprovalCardForAccount(ctx, db.GetApprovalCardForAccountParams{
		ID:                   cardID,
		MarketplaceAccountID: account,
	}); err != nil {
		return ConfirmOutcome{}, err // foreign/missing card → uniform not-found, no side effect.
	}
	return s.ConfirmIndividual(ctx, cardID, presented, now)
}

// EditPriceForOrg mints a new card version with the edited price ONLY when the
// card belongs to the caller's account (issue #102). The ownership check runs
// BEFORE the mint, so a cross-account edit returns pgx.ErrNoRows with no new
// version written.
func (s *Service) EditPriceForOrg(ctx context.Context, organizationID, cardID uuid.UUID, newPrice money.Money, now time.Time) (db.ApprovalCard, error) {
	account, err := s.accountForOrg(ctx, organizationID)
	if err != nil {
		return db.ApprovalCard{}, err
	}
	if _, err := db.New(s.pool).GetApprovalCardForAccount(ctx, db.GetApprovalCardForAccountParams{
		ID:                   cardID,
		MarketplaceAccountID: account,
	}); err != nil {
		return db.ApprovalCard{}, err
	}
	return s.EditPrice(ctx, cardID, newPrice, now)
}

// ListActionsForOrg returns the actions queue for the caller's own account only
// (issue #102). The requested account MUST equal the caller's resolved account; a
// foreign account id yields pgx.ErrNoRows (uniform not-found), never another
// account's queue.
func (s *Service) ListActionsForOrg(ctx context.Context, organizationID, requestedAccount uuid.UUID, stateFilter string, limit int32) ([]db.ApprovalCard, error) {
	account, err := s.accountForOrg(ctx, organizationID)
	if err != nil {
		return nil, err
	}
	if requestedAccount != account {
		return nil, errNotOwnedAccount
	}
	return s.ListActions(ctx, account, stateFilter, limit)
}

// PreviewBulkSelectionForOrg mints a server-side bulk selection-set preview scoped
// to the caller's own account (issue #102). The requested account MUST equal the
// caller's resolved account, and member resolution is bound to that account (a
// member naming a foreign recommendation/variant already fails closed with
// ErrUnknownMember). A foreign account id yields errNotOwnedAccount → not-found.
func (s *Service) PreviewBulkSelectionForOrg(ctx context.Context, organizationID, requestedAccount, lineage uuid.UUID, name string, criteria map[string]string, members []PreviewMemberInput) (PreviewResult, error) {
	account, err := s.accountForOrg(ctx, organizationID)
	if err != nil {
		return PreviewResult{}, err
	}
	if requestedAccount != account {
		return PreviewResult{}, errNotOwnedAccount
	}
	return s.PreviewBulkSelection(ctx, account, lineage, name, criteria, members)
}

// ConfirmBulkSelectionForOrg authoritatively confirms a bulk approval bound to one
// exact selection-set version (issue #90) ONLY when the selection-set lineage belongs
// to the caller's account (issue #102).
//
// The authoritative per-item flow in ConfirmBulkSelection (#90) derives the tenant
// from the selection set itself and enforces per-MEMBER tenant integrity (a member
// card from a different account than the set is rejected as account_mismatch). It
// does NOT, however, gate the SET against the CALLER: it resolves the lineage through
// the unscoped GetCurrentSelectionSet, so a caller who merely possesses a foreign
// lineage id could otherwise confirm another tenant's bulk selection. This variant
// closes that gap the same way the other #102 *ForOrg operations do: it resolves the
// caller's account and predicates the lineage lookup on it (via the account-scoped
// GetCurrentSelectionSetForAccount) BEFORE any authorization. A lineage owned by
// another account matches no row and yields pgx.ErrNoRows — indistinguishable from a
// missing lineage (no existence oracle), with no read of foreign members and no
// side effect. Because a selection set's marketplace_account_id is immutable within a
// lineage (selection_sets is append-only), this pre-check is airtight; #90's
// ConfirmBulkSelection then runs unchanged, so its version binding, per-item
// authorization, idempotency, and per-member account_mismatch rejection are preserved
// exactly (not weakened).
func (s *Service) ConfirmBulkSelectionForOrg(ctx context.Context, organizationID, lineage uuid.UUID, boundVersion int32, now time.Time) (BulkConfirmOutcome, error) {
	account, err := s.accountForOrg(ctx, organizationID)
	if err != nil {
		return BulkConfirmOutcome{}, err
	}
	// Ownership gate: authorize BEFORE any read of the set's members or any write.
	// A foreign/missing lineage → uniform pgx.ErrNoRows (404 at transport).
	if _, err := db.New(s.pool).GetCurrentSelectionSetForAccount(ctx, db.GetCurrentSelectionSetForAccountParams{
		LineageID:            lineage,
		MarketplaceAccountID: account,
	}); err != nil {
		return BulkConfirmOutcome{}, err
	}
	return s.ConfirmBulkSelection(ctx, lineage, boundVersion, now)
}
