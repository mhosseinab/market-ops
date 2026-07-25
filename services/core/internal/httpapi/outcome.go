// Outcome-window read (PD-3 item 5): the account's outcome windows and, when
// closed, their §15.3 result/confidence.
package httpapi

import (
	"context"
	"errors"

	"github.com/google/uuid"

	gateway "github.com/mhosseinab/market-ops/gen/go"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/outcome"
)

// ListOutcomes returns the account's outcome windows and, when closed, their
// §15.3 result/confidence (PD-3 item 5). A read.
func (s *gatewayServer) ListOutcomes(
	ctx context.Context, req gateway.ListOutcomesRequestObject,
) (gateway.ListOutcomesResponseObject, error) {
	if s.outcome == nil {
		return gateway.ListOutcomesdefaultJSONResponse{StatusCode: 503, Body: outcomeUnavailableErr()}, nil
	}
	var limit int32
	if req.Params.Limit != nil {
		limit = *req.Params.Limit
	}
	rows, err := s.outcome.ListByAccountForOrg(ctx, orgFromCtx(ctx), req.Params.MarketplaceAccountId, limit)
	if err != nil {
		if errors.Is(err, outcome.ErrAccountNotFound) {
			return gateway.ListOutcomesdefaultJSONResponse{StatusCode: 404, Body: outcomeErr(err)}, nil
		}
		return gateway.ListOutcomesdefaultJSONResponse{StatusCode: 500, Body: outcomeErr(err)}, nil
	}
	items := make([]gateway.OutcomeSummary, 0, len(rows))
	for _, r := range rows {
		items = append(items, toOutcomeSummary(r))
	}
	return gateway.ListOutcomes200JSONResponse(gateway.OutcomeList{Items: items}), nil
}

func toOutcomeSummary(r db.ListOutcomeWindowsByAccountRow) gateway.OutcomeSummary {
	out := gateway.OutcomeSummary{
		ActionId: r.ActionID,
		OpenedAt: r.OpenedAt,
		ClosesAt: r.ClosesAt,
	}
	if r.CardID.Valid {
		id := r.CardID.Bytes
		out.CardId = (*uuid.UUID)(&id)
	}
	if r.Result.Valid {
		v := gateway.OutcomeSummaryResult(r.Result.String)
		out.Result = &v
	}
	if r.Confidence.Valid {
		v := gateway.OutcomeSummaryConfidence(r.Confidence.String)
		out.Confidence = &v
	}
	return out
}

func outcomeErr(err error) gateway.ErrorEnvelope {
	return gateway.ErrorEnvelope{Code: "OUTCOME_ERROR", Message: err.Error()}
}

func outcomeUnavailableErr() gateway.ErrorEnvelope {
	return gateway.ErrorEnvelope{Code: "OUTCOME_UNAVAILABLE", Message: "outcome service is not configured"}
}
