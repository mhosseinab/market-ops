// Operations screen reads (PD-3 item 8): the aggregated ops queues and the
// Market cross-route conflict view.
package httpapi

import (
	"context"
	"errors"

	gateway "github.com/mhosseinab/market-ops/gen/go"
	"github.com/mhosseinab/market-ops/services/core/internal/execution"
	"github.com/mhosseinab/market-ops/services/core/internal/observation"
)

// GetOperationsQueues returns the Operations screen's aggregated queues (PD-3
// item 8): pending-reconciliation actions (real, backed by action_executions)
// and the NOT-YET-BACKED parser/schema-drift queue, honestly reported
// unavailable rather than a fabricated empty success.
func (s *gatewayServer) GetOperationsQueues(
	ctx context.Context, req gateway.GetOperationsQueuesRequestObject,
) (gateway.GetOperationsQueuesResponseObject, error) {
	if s.execution == nil {
		return gateway.GetOperationsQueuesdefaultJSONResponse{StatusCode: 503, Body: executionUnavailableErr()}, nil
	}
	rows, err := s.execution.ListPendingReconciliationForOrg(ctx, orgFromCtx(ctx), req.Params.MarketplaceAccountId, 0)
	if err != nil {
		if errors.Is(err, execution.ErrAccountNotFound) {
			return gateway.GetOperationsQueuesdefaultJSONResponse{StatusCode: 404, Body: executionErr(err)}, nil
		}
		return gateway.GetOperationsQueuesdefaultJSONResponse{StatusCode: 500, Body: executionErr(err)}, nil
	}
	pending := make([]gateway.PendingReconciliationAction, 0, len(rows))
	for _, r := range rows {
		pending = append(pending, gateway.PendingReconciliationAction{
			ActionId:       r.ActionID,
			CardId:         r.CardID,
			IdempotencyKey: r.IdempotencyKey,
			CreatedAt:      r.CreatedAt,
		})
	}
	out := gateway.OperationsQueues{
		MarketplaceAccountId:  req.Params.MarketplaceAccountId,
		PendingReconciliation: pending,
		ParserDrift: gateway.ParserDriftQueue{
			Available: false,
			Reason:    strPtr("Route C parser/schema-drift persistence is not yet wired (§10.4); owned by go_connector_observer."),
			Items:     []interface{}{},
		},
	}
	return gateway.GetOperationsQueues200JSONResponse(out), nil
}

// ListMarketConflicts returns the account's currently cross-route-conflicted
// Observed Offers (PD-3 item 8, Market conflict banner). A read.
func (s *gatewayServer) ListMarketConflicts(
	ctx context.Context, req gateway.ListMarketConflictsRequestObject,
) (gateway.ListMarketConflictsResponseObject, error) {
	if s.observation == nil {
		return gateway.ListMarketConflictsdefaultJSONResponse{StatusCode: 503, Body: observationUnavailableErr()}, nil
	}
	// Tenant scoping (issue #237): the account is resolved from the authenticated
	// org; a foreign or org-less caller is a uniform 404, never another tenant's
	// Market conflict view.
	views, err := s.observation.ListMarketConflictsForOrg(ctx, orgFromCtx(ctx), req.Params.MarketplaceAccountId)
	if err != nil {
		if errors.Is(err, observation.ErrAccountNotFound) {
			return gateway.ListMarketConflictsdefaultJSONResponse{StatusCode: 404, Body: observationErr(err)}, nil
		}
		return gateway.ListMarketConflictsdefaultJSONResponse{StatusCode: 500, Body: observationErr(err)}, nil
	}
	out := make([]gateway.ObservedOffer, 0, len(views))
	for _, v := range views {
		offer := toGatewayObservedOffer(v.Offer)
		// Per-route disagreeing evidence (issue #94): surfaced VERBATIM from the
		// existing in-window query. A missing/incomplete comparison is the EXPLICIT
		// fail-closed `unavailable` state, never a fabricated complete panel; the
		// offer's `conflicted` quality (already on the offer) keeps the action blocked.
		ev := toGatewayConflictEvidence(v.Evidence)
		offer.ConflictEvidence = &ev
		out = append(out, offer)
	}
	return gateway.ListMarketConflicts200JSONResponse(gateway.ObservedOfferList{Items: out}), nil
}

// toGatewayConflictEvidence maps the per-route disagreeing evidence onto the wire
// read model (issue #94). When the evidence is not inspectable it becomes the
// EXPLICIT `unavailable` state with an empty route list — the client renders the
// error, never infers the missing routes.
func toGatewayConflictEvidence(e observation.ConflictEvidence) gateway.ConflictEvidence {
	if !e.Available {
		return gateway.ConflictEvidence{
			State:  gateway.ConflictEvidenceStateUnavailable,
			Routes: []gateway.ConflictRouteEvidence{},
		}
	}
	routes := make([]gateway.ConflictRouteEvidence, 0, len(e.Routes))
	for _, r := range e.Routes {
		routes = append(routes, gateway.ConflictRouteEvidence{
			Route:              gateway.ObservationRoute(r.Route),
			Value:              r.Value,
			Unit:               r.Unit,
			AvailabilityStatus: gateway.AvailabilityStatus(r.AvailabilityStatus),
			CapturedAt:         r.CapturedAt,
			FreshnessDeadline:  r.FreshnessDeadline,
		})
	}
	return gateway.ConflictEvidence{State: gateway.ConflictEvidenceStateAvailable, Routes: routes}
}

func strPtr(s string) *string { return &s }
