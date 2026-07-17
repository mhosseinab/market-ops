package recommendation

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/money"
)

// Disposition is a selection-set member's bulk disposition (CHAT-050): executable,
// warning, or blocked. Only executable members may carry an approval control.
type Disposition string

const (
	// DispositionExecutable — the member's recommendation is approvable.
	DispositionExecutable Disposition = "executable"
	// DispositionWarning — approvable but flagged (e.g. low headroom).
	DispositionWarning Disposition = "warning"
	// DispositionBlocked — a PRC-002 blocker excludes it from execution.
	DispositionBlocked Disposition = "blocked"
)

// SelectionSetInput is the input to CreateSelectionSet. Criteria are the
// deterministic query parameters that define the set (CHAT-033/051), so the set
// is reproducible and a re-query cannot silently drift a bound version.
type SelectionSetInput struct {
	Account  uuid.UUID
	Lineage  uuid.UUID
	Name     string
	Criteria map[string]string
	// AggregateImpact is the summed exposure (present) or unknown (nil) — an
	// unknown aggregate never becomes a fabricated number (EVT-005 semantics).
	AggregateImpact *money.Money
	MemberCount     int
}

// CreateSelectionSet appends a NEW VERSION of a named selection set in its lineage
// (append-only; the store computes the next version). A set change is a new
// version — a bulk control bound to an older version is thereby invalidated.
func (s *Service) CreateSelectionSet(ctx context.Context, in SelectionSetInput) (db.SelectionSet, error) {
	criteria, err := json.Marshal(in.Criteria)
	if err != nil {
		return db.SelectionSet{}, err
	}
	params := db.InsertSelectionSetParams{
		MarketplaceAccountID: in.Account,
		LineageID:            in.Lineage,
		Name:                 in.Name,
		Criteria:             criteria,
		MemberCount:          int32(in.MemberCount),
	}
	if in.AggregateImpact != nil {
		params.AggregateImpactKnown = true
		params.AggregateImpactMantissa = pgtype.Int8{Int64: in.AggregateImpact.Mantissa(), Valid: true}
		params.AggregateImpactCurrency = in.AggregateImpact.Currency()
		params.AggregateImpactExponent = int16(in.AggregateImpact.Exponent())
	}
	return db.New(s.pool).InsertSelectionSet(ctx, params)
}

// AddMember appends one member with its disposition to a selection-set version.
func (s *Service) AddMember(ctx context.Context, setID, variant uuid.UUID, recID uuid.UUID, disp Disposition) (db.SelectionSetMember, error) {
	return db.New(s.pool).InsertSelectionSetMember(ctx, db.InsertSelectionSetMemberParams{
		SelectionSetID:   setID,
		VariantID:        variant,
		RecommendationID: optionalUUID(recID),
		Disposition:      string(disp),
	})
}

// Members returns a selection-set version's members.
func (s *Service) Members(ctx context.Context, setID uuid.UUID) ([]db.SelectionSetMember, error) {
	return db.New(s.pool).ListSelectionSetMembers(ctx, setID)
}

// BulkPreviewValid reports whether a bulk preview bound to boundVersion is still
// valid: it is valid ONLY when boundVersion is the CURRENT (greatest) version of
// the lineage. A selection-set change mints a new version, so the bound preview no
// longer matches and this returns false (CHAT-051 "no re-query drift" / CHAT-052
// "any set change invalidates confirmation").
func (s *Service) BulkPreviewValid(ctx context.Context, lineage uuid.UUID, boundVersion int32) (bool, error) {
	current, err := db.New(s.pool).GetCurrentSelectionSet(ctx, lineage)
	if err != nil {
		return false, err
	}
	return current.Version == boundVersion, nil
}

// CurrentSelectionSetVersion returns the current (greatest) version of a
// selection-set lineage — the version a stale bound preview differs from.
func (s *Service) CurrentSelectionSetVersion(ctx context.Context, lineage uuid.UUID) (int32, error) {
	current, err := db.New(s.pool).GetCurrentSelectionSet(ctx, lineage)
	if err != nil {
		return 0, err
	}
	return current.Version, nil
}
