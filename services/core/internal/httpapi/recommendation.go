// Recommendation-detail read: one recommendation's full PRC-001 record plus its
// §9.2 contribution breakdown (PD-3 items 1/3).
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	gateway "github.com/mhosseinab/market-ops/gen/go"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/margin"
	"github.com/mhosseinab/market-ops/services/core/internal/money"
)

// GetRecommendationDetail returns one recommendation's full PRC-001 record plus
// its §9.2 contribution breakdown, decoded verbatim from the persisted `inputs`
// column (never recomputed/fabricated at read time). It is a read (PD-3 items
// 1/3).
func (s *gatewayServer) GetRecommendationDetail(
	ctx context.Context, req gateway.GetRecommendationDetailRequestObject,
) (gateway.GetRecommendationDetailResponseObject, error) {
	if s.approval == nil {
		return gateway.GetRecommendationDetaildefaultJSONResponse{StatusCode: 503, Body: approvalUnavailableErr()}, nil
	}
	row, err := s.approval.GetRecommendationForOrg(ctx, orgFromCtx(ctx), req.Params.RecommendationId)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return gateway.GetRecommendationDetaildefaultJSONResponse{StatusCode: 404, Body: approvalErr(err)}, nil
		}
		return gateway.GetRecommendationDetaildefaultJSONResponse{StatusCode: 500, Body: approvalErr(err)}, nil
	}
	view, err := toRecommendationDetail(row)
	if err != nil {
		return gateway.GetRecommendationDetaildefaultJSONResponse{StatusCode: 500, Body: approvalErr(err)}, nil
	}
	return gateway.GetRecommendationDetail200JSONResponse(view), nil
}

// toRecommendationDetail maps a persisted recommendation row + its decoded
// §9.2 deductions onto the wire RecommendationDetail (PD-3 items 1/3). Every
// optional field stays present-or-unavailable-with-reason — never fabricated.
func toRecommendationDetail(row db.Recommendation) (gateway.RecommendationDetail, error) {
	current, err := money.New(row.CurrentPriceMantissa, row.CurrentPriceCurrency, int8(row.CurrentPriceExponent))
	if err != nil {
		return gateway.RecommendationDetail{}, err
	}
	out := gateway.RecommendationDetail{
		Id:                     row.ID,
		MarketplaceAccountId:   row.MarketplaceAccountID,
		VariantId:              row.VariantID,
		LineageId:              row.LineageID,
		Version:                int64(row.Version),
		Objective:              gateway.PolicyObjective(row.Objective),
		CurrentPrice:           toMoneyAmount(current),
		Readiness:              gateway.MarginReadinessState(row.Readiness),
		EvidenceQuality:        gateway.QualityState(row.EvidenceQuality),
		Approvable:             row.Approvable,
		Simulation:             row.Simulation,
		Assumptions:            []string{},
		Blockers:               []gateway.RecommendationBlocker{},
		ContributionDeductions: []gateway.ContributionDeduction{},
	}
	if row.EventID.Valid {
		id := row.EventID.Bytes
		out.EventId = (*uuid.UUID)(&id)
	}
	if row.ProposedPriceAvailable {
		p, err := money.New(row.ProposedPriceMantissa.Int64, row.ProposedPriceCurrency, int8(row.ProposedPriceExponent))
		if err != nil {
			return gateway.RecommendationDetail{}, err
		}
		v := toMoneyAmount(p)
		out.ProposedPrice = &v
	}
	if row.CurrentContributionAvailable {
		c, err := money.New(row.CurrentContributionMantissa.Int64, row.CurrentContributionCurrency, int8(row.CurrentContributionExponent))
		if err != nil {
			return gateway.RecommendationDetail{}, err
		}
		v := toMoneyAmount(c)
		out.CurrentContribution = &v
	}
	if row.ProposedContributionAvailable {
		c, err := money.New(row.ProposedContributionMantissa.Int64, row.ProposedContributionCurrency, int8(row.ProposedContributionExponent))
		if err != nil {
			return gateway.RecommendationDetail{}, err
		}
		v := toMoneyAmount(c)
		out.ProposedContribution = &v
	}
	if row.AllowedRangeAvailable {
		min, err := money.New(row.AllowedRangeMinMantissa.Int64, row.AllowedRangeCurrency, int8(row.AllowedRangeExponent))
		if err != nil {
			return gateway.RecommendationDetail{}, err
		}
		max, err := money.New(row.AllowedRangeMaxMantissa.Int64, row.AllowedRangeCurrency, int8(row.AllowedRangeExponent))
		if err != nil {
			return gateway.RecommendationDetail{}, err
		}
		minA, maxA := toMoneyAmount(min), toMoneyAmount(max)
		out.AllowedRange = &gateway.PolicyBoundary{Known: true, Min: &minA, Max: &maxA}
	}
	if row.EvidenceObservationID.Valid {
		id := row.EvidenceObservationID.Bytes
		out.EvidenceObservationId = (*uuid.UUID)(&id)
	}
	if row.EvidenceAsOf.Valid {
		t := row.EvidenceAsOf.Time
		out.EvidenceAsOf = &t
	}
	if row.ExpiresAt.Valid {
		t := row.ExpiresAt.Time
		out.ExpiresAt = &t
	}
	if len(row.Assumptions) > 0 {
		var a []string
		if err := json.Unmarshal(row.Assumptions, &a); err != nil {
			// Errors are actionable: corrupt persisted JSON must not silently
			// degrade to an empty (but "present") list — that would report an
			// incomplete PRC-001 record as complete. Propagate to the caller's
			// existing 500 path, exactly as recommendation.DecodeEvidenceVersions
			// (unmarshalEvidenceVersions) already does for evidence versions.
			return gateway.RecommendationDetail{}, fmt.Errorf("recommendation %s: decode assumptions: %w", row.ID, err)
		}
		out.Assumptions = a
	}
	if len(row.Blockers) > 0 {
		var raw []struct {
			Code    string `json:"Code"`
			Message string `json:"Message"`
		}
		if err := json.Unmarshal(row.Blockers, &raw); err != nil {
			return gateway.RecommendationDetail{}, fmt.Errorf("recommendation %s: decode blockers: %w", row.ID, err)
		}
		for _, b := range raw {
			out.Blockers = append(out.Blockers, gateway.RecommendationBlocker{Code: b.Code, Message: b.Message})
		}
	}
	if len(row.Inputs) > 0 {
		var deductions []margin.Deduction
		if err := json.Unmarshal(row.Inputs, &deductions); err != nil {
			return gateway.RecommendationDetail{}, fmt.Errorf("recommendation %s: decode contribution deductions: %w", row.ID, err)
		}
		for _, d := range deductions {
			out.ContributionDeductions = append(out.ContributionDeductions, gateway.ContributionDeduction{
				Component: gateway.CostComponent(d.Component),
				Amount:    toMoneyAmount(d.Amount),
				Kind:      kindToGateway(d.Kind),
				Version:   d.Version,
			})
		}
	}
	return out, nil
}
