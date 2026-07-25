package recommendation

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/mhosseinab/market-ops/services/core/internal/db"
)

// ErrLineageNotOwned is returned when a caller presents a selection-set LINEAGE
// owned by a DIFFERENT marketplace account (issue #90, PRD §4.6 tenant isolation).
// It fails CLOSED before any version is minted and before any member of the foreign
// lineage is read.
//
// What the transport mapping DOES guarantee (asserted by
// TestPreviewSelectionSet_NotFoundCausesAreByteIdentical): the ownership rejection's
// STATUS and RESPONSE BODY are byte-for-byte identical to the other not-found causes
// on this seam — an unknown/mismatched member and a foreign marketplace account — so
// the response never discloses WHICH of them occurred, and an ownership rejection is
// never distinguishable from an ordinary bad request by its text.
//
// What it does NOT guarantee (issue #90 fix cycle 1, M2): a foreign lineage is still
// distinguishable from an UNCLAIMED one, because claiming an unclaimed lineage is a
// legal create — the caller's own preview succeeds and mints version 1, while a
// lineage live under another tenant is rejected. A caller who already holds a
// candidate lineage id can therefore learn that it is claimed by someone. The reach
// is bounded (lineage ids are server-minted v4 UUIDs and are never exposed on another
// tenant's surface) and closing it would change the create semantics, so it is
// recorded here rather than silently asserted away.
var ErrLineageNotOwned = errors.New("recommendation: selection-set lineage is owned by another account")

// BULK-PROTOCOL DESIGN RECORD (b) — DB-ENFORCED LINEAGE→ACCOUNT OWNERSHIP.
//
//	Rule for #87/#84 to code against: a selection-set lineage belongs to EXACTLY ONE
//	marketplace account, immutably, for its whole life. The binding lives in
//	selection_set_lineages (lineage_id PRIMARY KEY) and is enforced by the composite
//	foreign key selection_sets (lineage_id, marketplace_account_id) →
//	selection_set_lineages (lineage_id, marketplace_account_id) added by migration
//	0045. Application code MUST still call claimLineageOwnership before minting (so
//	the rejection is a typed, observable, uniform not-found rather than a raw FK
//	error), but correctness does NOT depend on it: a bypassed or future code path
//	that inserts a cross-account version is rejected by PostgreSQL itself.
//
// claimLineageOwnership claims lineage for account, or verifies an existing claim,
// on the caller's transaction q. The caller MUST already hold the per-lineage
// advisory lock (LockApprovalLineage) on the SAME transaction: the claim is an
// INSERT ... ON CONFLICT DO NOTHING followed by an account-scoped read of the
// authoritative owner row, and the lock is what makes that pair race-free — two
// accounts racing on one lineage serialize, the loser reads the winner's row and is
// rejected. Ownership is never UPDATEd (append-only, §4.6): re-pointing a lineage
// would retro-actively transfer every sealed version in it to another tenant.
//
// A mismatch is reported to the ownership-rejection telemetry seam (a counter + a
// structured log carrying the failing seam) before ErrLineageNotOwned is returned:
// a tenant-isolation rejection is an audited, observable event, never a swallowed
// error (CLAUDE.md §SRE).
func (s *Service) claimLineageOwnership(ctx context.Context, q *db.Queries, seam string, lineage, account uuid.UUID) error {
	if err := q.ClaimSelectionSetLineage(ctx, db.ClaimSelectionSetLineageParams{
		LineageID:            lineage,
		MarketplaceAccountID: account,
	}); err != nil {
		return err
	}
	owner, err := q.GetSelectionSetLineage(ctx, lineage)
	if err != nil {
		// The claim above either inserted the row or found it already present, so a
		// missing row here is a genuine fault — propagate it rather than inferring
		// ownership (quarantine over inference).
		return err
	}
	if owner.MarketplaceAccountID != account {
		s.tel().lineageOwnershipRejected(ctx, seam, lineage, account, owner.MarketplaceAccountID)
		return ErrLineageNotOwned
	}
	return nil
}
