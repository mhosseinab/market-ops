package recommendation

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/money"
)

// Persist writes a PRC-001 recommendation as a NEW VERSION in the given lineage
// (append-only; the store computes the next version). Every optional money field
// is stored present-or-unavailable-with-reason, so an absent value is explicit
// and reproducible (never a silent zero). It returns the persisted row.
func (s *Service) Persist(ctx context.Context, lineage uuid.UUID, rec Recommendation) (db.Recommendation, error) {
	params, err := buildInsertRecommendationParams(lineage, rec)
	if err != nil {
		return db.Recommendation{}, err
	}
	return db.New(s.pool).InsertRecommendation(ctx, params)
}

// buildInsertRecommendationParams marshals a domain Recommendation into the
// append-only InsertRecommendation params. It is shared by Persist (own pool) and the
// transactional producer path (ProduceVersion), so the present-or-unavailable-with-
// reason money mapping lives in ONE place (DRY) and both paths store an absent value
// explicitly, never a silent zero.
func buildInsertRecommendationParams(lineage uuid.UUID, rec Recommendation) (db.InsertRecommendationParams, error) {
	refs, err := json.Marshal(rec.Evidence.Refs)
	if err != nil {
		return db.InsertRecommendationParams{}, err
	}
	assumptions, err := json.Marshal(rec.Assumptions)
	if err != nil {
		return db.InsertRecommendationParams{}, err
	}
	blockers, err := json.Marshal(rec.Blockers)
	if err != nil {
		return db.InsertRecommendationParams{}, err
	}
	inputs, err := json.Marshal(rec.Inputs)
	if err != nil {
		return db.InsertRecommendationParams{}, err
	}
	// Persist the REAL per-observation evidence-version map the recommendation was
	// assembled from (APR-001 evidence-invalidation, never-cut §4.6, issue #133), so
	// a later read (the S23 chat Draft path) can rebuild the binding with the SAME
	// versions the S17 card was bound to — never an empty map that leaves the
	// evidence dimension with nothing to compare against. A nil/empty map encodes as
	// an empty object (no backing evidence recorded); no synthetic version is ever
	// fabricated.
	evidenceVersions, err := marshalEvidenceVersions(rec.binding.EvidenceVersions)
	if err != nil {
		return db.InsertRecommendationParams{}, err
	}

	params := db.InsertRecommendationParams{
		MarketplaceAccountID:  rec.AccountID,
		VariantID:             rec.VariantID,
		LineageID:             lineage,
		EventID:               optionalUUID(eventID(rec)),
		Objective:             string(rec.Objective),
		CurrentPriceMantissa:  rec.CurrentPrice.Mantissa(),
		CurrentPriceCurrency:  rec.CurrentPrice.Currency(),
		CurrentPriceExponent:  int16(rec.CurrentPrice.Exponent()),
		Readiness:             string(rec.Readiness),
		EvidenceQuality:       rec.Quality,
		EvidenceObservationID: optionalUUID(rec.Evidence.ObservationID),
		EvidenceRefs:          refs,
		CostProfileVersion:    rec.binding.CostProfileVersion,
		PolicyVersion:         rec.binding.PolicyVersion,
		ContextVersion:        rec.binding.ContextVersion,
		ParameterVersion:      rec.binding.ParameterVersion,
		Inputs:                inputs,
		Assumptions:           assumptions,
		Blockers:              blockers,
		Approvable:            rec.Approvable(),
		Simulation:            rec.Simulation,
		EvidenceVersions:      evidenceVersions,
	}

	applyOptionalMoney(rec.ProposedPrice, rec.ProposedPrice.Reason(),
		&params.ProposedPriceAvailable, &params.ProposedPriceMantissa,
		&params.ProposedPriceCurrency, &params.ProposedPriceExponent, &params.ProposedPriceReason)
	applyOptionalMoney(rec.CurrentContribution, rec.CurrentContribution.Reason(),
		&params.CurrentContributionAvailable, &params.CurrentContributionMantissa,
		&params.CurrentContributionCurrency, &params.CurrentContributionExponent, &params.CurrentContributionReason)
	applyOptionalMoney(rec.ProposedContribution, rec.ProposedContribution.Reason(),
		&params.ProposedContributionAvailable, &params.ProposedContributionMantissa,
		&params.ProposedContributionCurrency, &params.ProposedContributionExponent, &params.ProposedContributionReason)
	applyOptionalRange(rec.AllowedRange,
		&params.AllowedRangeAvailable, &params.AllowedRangeMinMantissa, &params.AllowedRangeMaxMantissa,
		&params.AllowedRangeCurrency, &params.AllowedRangeExponent, &params.AllowedRangeReason)

	if asOf := rec.Evidence.AsOf; !asOf.IsZero() {
		params.EvidenceAsOf = pgtype.Timestamptz{Time: asOf, Valid: true}
	}
	if exp, ok := rec.Expiry.Get(); ok {
		params.ExpiresAt = pgtype.Timestamptz{Time: exp, Valid: true}
	}

	return params, nil
}

// eventID returns the event id when the recommendation is event-driven, else Nil.
func eventID(rec Recommendation) uuid.UUID {
	if id, ok := rec.EventID.Get(); ok {
		return id
	}
	return uuid.Nil
}

// applyOptionalMoney fills the (available, mantissa, currency, exponent, reason)
// columns for an optional Money field. A present value writes the full triple; an
// absent value writes only the reason (no number — the CHECK enforces this).
func applyOptionalMoney(
	opt Optional[money.Money], reason string,
	available *bool, mantissa *pgtype.Int8, currency *string, exponent *int16, reasonOut *string,
) {
	if m, ok := opt.Get(); ok {
		*available = true
		*mantissa = pgtype.Int8{Int64: m.Mantissa(), Valid: true}
		*currency = m.Currency()
		*exponent = int16(m.Exponent())
		return
	}
	*reasonOut = reason
}

// applyOptionalRange fills the allowed-range columns for an optional Range.
func applyOptionalRange(
	opt Optional[Range],
	available *bool, minMantissa, maxMantissa *pgtype.Int8, currency *string, exponent *int16, reasonOut *string,
) {
	if r, ok := opt.Get(); ok {
		*available = true
		*minMantissa = pgtype.Int8{Int64: r.Min.Mantissa(), Valid: true}
		*maxMantissa = pgtype.Int8{Int64: r.Max.Mantissa(), Valid: true}
		*currency = r.Min.Currency()
		*exponent = int16(r.Min.Exponent())
		return
	}
	*reasonOut = opt.Reason()
}

// optionalUUID maps a possibly-Nil uuid to a nullable pgtype.UUID.
func optionalUUID(id uuid.UUID) pgtype.UUID {
	if id == uuid.Nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: id, Valid: true}
}
