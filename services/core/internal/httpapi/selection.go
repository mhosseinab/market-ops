// Bulk selection-set preview: the version is minted ENTIRELY server-side
// (PD-3 item 4, the hard safety precondition for bulk approval).
package httpapi

import (
	"context"
	"errors"

	"github.com/google/uuid"

	gateway "github.com/mhosseinab/market-ops/gen/go"
	"github.com/mhosseinab/market-ops/services/core/internal/recommendation"
)

// PreviewSelectionSet builds the screens-native bulk selection-set preview,
// minting the version ENTIRELY SERVER-SIDE (PD-3 item 4, the hard safety
// precondition for a live reversible price write). The request carries NO
// version field by construction — there is nothing for a client to supply or
// influence.
func (s *gatewayServer) PreviewSelectionSet(
	ctx context.Context, req gateway.PreviewSelectionSetRequestObject,
) (gateway.PreviewSelectionSetResponseObject, error) {
	if s.approval == nil {
		return gateway.PreviewSelectionSetdefaultJSONResponse{StatusCode: 503, Body: approvalUnavailableErr()}, nil
	}
	if req.Body == nil || len(req.Body.Members) == 0 {
		return gateway.PreviewSelectionSetdefaultJSONResponse{StatusCode: 400, Body: invalidArgErr("at least one member is required")}, nil
	}
	var lineage uuid.UUID
	if req.Body.LineageId != nil {
		lineage = *req.Body.LineageId
	}
	criteria := map[string]string{}
	if req.Body.Criteria != nil {
		criteria = *req.Body.Criteria
	}
	members := make([]recommendation.PreviewMemberInput, 0, len(req.Body.Members))
	for _, m := range req.Body.Members {
		members = append(members, recommendation.PreviewMemberInput{VariantID: m.VariantId, RecommendationID: m.RecommendationId})
	}
	result, err := s.approval.PreviewBulkSelectionForOrg(ctx, orgFromCtx(ctx), req.Body.MarketplaceAccountId, lineage, req.Body.Name, criteria, members)
	if err != nil {
		if errors.Is(err, recommendation.ErrUnknownMember) ||
			errors.Is(err, recommendation.ErrAccountNotFound) ||
			errors.Is(err, recommendation.ErrLineageNotOwned) {
			// A foreign account, an unknown/mismatched member, and a selection-set
			// LINEAGE owned by another tenant (issue #90 blocker 1) collapse to ONE
			// uniform not-found: the STATUS and the BODY are byte-for-byte identical for
			// all three (asserted by TestPreviewSelectionSet_NotFoundCausesAreByteIdentical),
			// so the response text cannot be used to tell "this lineage belongs to
			// someone else" from "no such member". The distinction is observable to
			// OPERATORS through the tenant-isolation counter + structured log, never to
			// the caller.
			//
			// This does NOT make a foreign lineage indistinguishable from an UNCLAIMED
			// one: an unclaimed lineage id is claimed by this very call and the preview
			// mints version 1 (a legal create, 200), so a caller already holding a
			// candidate lineage id can tell "claimed by someone" from "free". That is a
			// bounded, recorded property of the create semantics (see
			// recommendation.ErrLineageNotOwned), not something this mapping claims to
			// close.
			return gateway.PreviewSelectionSetdefaultJSONResponse{StatusCode: 404, Body: selectionNotFoundErr()}, nil
		}
		return gateway.PreviewSelectionSetdefaultJSONResponse{StatusCode: 500, Body: approvalErr(err)}, nil
	}
	return gateway.PreviewSelectionSet200JSONResponse(toSelectionSetPreviewResult(result)), nil
}

func toSelectionSetPreviewResult(r recommendation.PreviewResult) gateway.SelectionSetPreviewResult {
	members := make([]gateway.SelectionSetMemberView, 0, len(r.Members))
	for _, m := range r.Members {
		members = append(members, gateway.SelectionSetMemberView{
			VariantId:        m.VariantID,
			RecommendationId: m.RecommendationID,
			Disposition:      gateway.SelectionSetDisposition(m.Disposition),
		})
	}
	out := gateway.SelectionSetPreviewResult{
		Id:          r.Set.ID,
		LineageId:   r.Set.LineageID,
		Version:     int64(r.Set.Version),
		Name:        r.Set.Name,
		MemberCount: int32(len(members)),
		Members:     members,
	}
	if r.AggregateImpact != nil {
		out.AggregateImpact = &gateway.EventExposure{Known: true, Amount: ptrMoneyAmount(*r.AggregateImpact)}
	} else {
		out.AggregateImpact = &gateway.EventExposure{Known: false}
	}
	return out
}

// selectionNotFoundErr is the UNIFORM not-found envelope for the selection-set
// preview: an unknown/mismatched member, a foreign marketplace account, and a
// selection-set lineage owned by another tenant (issue #90) produce the SAME body,
// byte for byte, at the same 404. One fixed message — never err.Error() — is what
// makes that true; the distinction is recorded for operators in the tenant-isolation
// telemetry instead. (It does not, and does not claim to, hide a CLAIMED lineage
// from an unclaimed one: claiming a free lineage is a legal create that returns 200.)
func selectionNotFoundErr() gateway.ErrorEnvelope {
	return gateway.ErrorEnvelope{Code: "APPROVAL_ERROR", Message: "selection set not found"}
}
