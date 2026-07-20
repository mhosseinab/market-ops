package diagnostics

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mhosseinab/market-ops/services/core/internal/db"
)

// ErrAccountNotFound is returned when the requested marketplace account is not
// owned by the authenticated organization (cross-account fail-closed). It is
// returned identically for an absent account and one owned by a DIFFERENT
// organization, so the response never reveals whether a foreign account exists.
var ErrAccountNotFound = errors.New("diagnostics: account not found")

// ErrVariantNotFound is returned when the variant does not exist within the
// account (including a foreign variant). Fail closed → the transport maps it to
// 404, never another account's data.
var ErrVariantNotFound = errors.New("diagnostics: variant not found")

// diagnosticsQuerier is the exact query surface the read model needs. *db.Queries
// satisfies it; tests substitute a fake so the org-scoping / fail-closed logic is
// unit-tested WITHOUT a database.
type diagnosticsQuerier interface {
	GetOrgMarketplaceAccountID(ctx context.Context, arg db.GetOrgMarketplaceAccountIDParams) (uuid.UUID, error)
	GetVariantListingForDiagnostics(ctx context.Context, arg db.GetVariantListingForDiagnosticsParams) (db.GetVariantListingForDiagnosticsRow, error)
}

// ReadService is the account-scoped, READ-ONLY listing/image diagnostics read
// model (S26, LST-001). It owns NO write path: it reads captured catalog data and
// derives pass/warn results, and there is no method that generates or publishes
// content.
type ReadService struct {
	q   diagnosticsQuerier
	now func() time.Time
}

// NewReadService builds a ReadService bound to the pool.
func NewReadService(pool *pgxpool.Pool) *ReadService {
	return &ReadService{q: db.New(pool), now: time.Now}
}

// newReadServiceWith injects a querier and clock (tests only).
func newReadServiceWith(q diagnosticsQuerier, now func() time.Time) *ReadService {
	return &ReadService{q: q, now: now}
}

// GetVariantDiagnostics returns the read-only diagnostics report for one variant.
// It fails CLOSED cross-account (ErrAccountNotFound) BEFORE any catalog read, so
// possession of an account UUID cannot leak a foreign variant, and returns
// ErrVariantNotFound for an unknown/foreign variant. The organization id is a
// mandatory argument derived from the session principal — never request input.
func (s *ReadService) GetVariantDiagnostics(ctx context.Context, organizationID, accountID, variantID uuid.UUID) (Report, error) {
	if err := s.assertOwned(ctx, organizationID, accountID); err != nil {
		return Report{}, err
	}
	row, err := s.q.GetVariantListingForDiagnostics(ctx, db.GetVariantListingForDiagnosticsParams{
		MarketplaceAccountID: accountID,
		ID:                   variantID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Report{}, ErrVariantNotFound
	}
	if err != nil {
		return Report{}, fmt.Errorf("diagnostics: get variant listing: %w", err)
	}
	items := Derive(Input{
		NativeVariantID: row.NativeVariantID,
		VariantTitle:    row.VariantTitle,
		ProductTitle:    row.ProductTitle,
		ListingPresent:  row.ListingPresent,
		NativeListingID: row.NativeListingID,
		CapturedAt:      row.VariantUpdatedAt,
	})
	return Report{
		VariantID:            variantID.String(),
		MarketplaceAccountID: accountID.String(),
		EvaluatedAt:          s.now().UTC(),
		Items:                items,
	}, nil
}

// assertOwned resolves the account ONLY when it belongs to organizationID; a
// foreign or unknown account returns ErrAccountNotFound with no side effect and
// no catalog read (cross-account fail-closed).
func (s *ReadService) assertOwned(ctx context.Context, organizationID, accountID uuid.UUID) error {
	_, err := s.q.GetOrgMarketplaceAccountID(ctx, db.GetOrgMarketplaceAccountIDParams{
		ID:             accountID,
		OrganizationID: organizationID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrAccountNotFound
	}
	if err != nil {
		return fmt.Errorf("diagnostics: resolve account owner: %w", err)
	}
	return nil
}

// variantEvidenceRef is a STABLE reference to the canonical variant the diagnostic
// read — a reference, not content.
func variantEvidenceRef(nativeVariantID int64) string {
	return fmt.Sprintf("catalog/variant/%d", nativeVariantID)
}

// listingEvidenceRef references the canonical listing when one is present, else
// the variant. A reference only — never listing content.
func listingEvidenceRef(in Input) string {
	if in.ListingPresent {
		return fmt.Sprintf("catalog/listing/%d", in.NativeListingID)
	}
	return variantEvidenceRef(in.NativeVariantID)
}
