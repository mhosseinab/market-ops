// Tenant-scoping seam (issue #131, mirroring issue #67/#113/#237/#102): the
// transport-facing cost reads — point-in-time cost profiles, margin readiness, and
// the CSV import preview — resolve the authenticated principal's marketplace account
// from its organization and predicate every read on that account. A variant or batch
// belonging to another organization is indistinguishable from a genuinely missing
// one (uniform not-found) and is never disclosed. Ownership is derived from the
// principal's organization, NEVER from a caller-supplied variantId/batchId — those
// are selectors, not authorization.
package cost

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mhosseinab/market-ops/services/core/internal/db"
)

// ErrAccountNotFound is returned when a caller resolves to no marketplace account at
// all (an org-less principal, OrganizationID == uuid.Nil). It maps to the same 404 a
// genuinely missing resource returns, so a caller with no resolvable account fails
// closed with no disclosure (issue #131). A foreign variant/batch under a valid own
// account is NOT surfaced as this error: it is reported with the SAME sentinel a
// genuinely-unknown variant/batch returns (ErrVariantNotFound / ErrBatchNotFound), so
// foreign and unknown are indistinguishable to the caller (no existence oracle).
var ErrAccountNotFound = errors.New("cost: account not found")

// accountForOrg resolves the single marketplace account owned by organizationID
// (org ↔ account is 1:1). A nil/unknown organization yields ErrAccountNotFound, so a
// caller with no resolvable account fails closed before any cost read.
func (s *Service) accountForOrg(ctx context.Context, organizationID uuid.UUID) (uuid.UUID, error) {
	acct, err := db.New(s.pool).GetMarketplaceAccountByOrganization(ctx, organizationID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, ErrAccountNotFound
		}
		return uuid.Nil, fmt.Errorf("cost: resolve account for org: %w", err)
	}
	return acct.ID, nil
}

// CostProfileAtForOrg returns the EXACT in-force version of each component for a
// variant at instant `at` (CST-002), SCOPED to the caller's OWN account (issue #131).
// The account is resolved from the authenticated organization and bounds the lookup
// in SQL, so a variant owned by another organization matches nothing and returns an
// empty slice — indistinguishable from a variant with no cost profile (uniform
// not-found, no existence oracle). An org-less caller fails closed with
// ErrAccountNotFound before any read.
func (s *Service) CostProfileAtForOrg(ctx context.Context, organizationID, variant uuid.UUID, at time.Time) ([]db.CostProfile, error) {
	account, err := s.accountForOrg(ctx, organizationID)
	if err != nil {
		return nil, err
	}
	rows, err := db.New(s.pool).CostProfileAtForAccount(ctx, db.CostProfileAtForAccountParams{
		VariantID:     variant,
		AccountID:     account,
		EffectiveFrom: at,
	})
	if err != nil {
		return nil, fmt.Errorf("cost: point-in-time lookup: %w", err)
	}
	return rows, nil
}

// GetReadinessForOrg returns a variant's derived margin readiness (CST-003), SCOPED
// to the caller's OWN account (issue #131). Ownership is asserted BEFORE the read: a
// variant the caller's organization does not own — or an unknown variant — yields
// ErrVariantNotFound, the SAME sentinel both cases share, so a foreign variant is
// indistinguishable from an unknown one (no existence oracle). Only after ownership
// is confirmed does the freshness-aware GetReadiness (which may recompute) run under
// the resolved account. An org-less caller fails closed with ErrAccountNotFound.
func (s *Service) GetReadinessForOrg(ctx context.Context, organizationID, variant uuid.UUID) (db.MarginReadiness, error) {
	account, err := s.accountForOrg(ctx, organizationID)
	if err != nil {
		return db.MarginReadiness{}, err
	}
	owner, err := db.New(s.pool).GetVariantAccountID(ctx, variant)
	if errors.Is(err, pgx.ErrNoRows) {
		return db.MarginReadiness{}, ErrVariantNotFound
	}
	if err != nil {
		return db.MarginReadiness{}, fmt.Errorf("cost: resolve variant owner: %w", err)
	}
	if owner != account {
		// Foreign variant: report the SAME not-found an unknown variant returns, so
		// the two are indistinguishable (no existence oracle).
		return db.MarginReadiness{}, ErrVariantNotFound
	}
	return s.GetReadiness(ctx, variant)
}

// GetPreviewForOrg re-fetches a stored CSV import preview batch (CST-001), SCOPED to
// the caller's OWN account (issue #131). Ownership is asserted on the loaded batch: a
// batch the caller's organization does not own — or an unknown batch — yields
// ErrBatchNotFound, the SAME sentinel both cases share, so a foreign batch is
// indistinguishable from an unknown one (no existence oracle). An org-less caller
// fails closed with ErrAccountNotFound before any disclosure.
func (s *Service) GetPreviewForOrg(ctx context.Context, organizationID, batchID uuid.UUID) (Preview, error) {
	account, err := s.accountForOrg(ctx, organizationID)
	if err != nil {
		return Preview{}, err
	}
	q := db.New(s.pool)
	batch, err := q.GetCostImportBatch(ctx, batchID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Preview{}, ErrBatchNotFound
	}
	if err != nil {
		return Preview{}, fmt.Errorf("cost: get batch: %w", err)
	}
	if batch.MarketplaceAccountID != account {
		// Foreign batch: report the SAME not-found an unknown batch returns.
		return Preview{}, ErrBatchNotFound
	}
	rows, err := q.ListCostImportRows(ctx, batchID)
	if err != nil {
		return Preview{}, fmt.Errorf("cost: list preview rows: %w", err)
	}
	return Preview{Batch: batch, Rows: rows}, nil
}
