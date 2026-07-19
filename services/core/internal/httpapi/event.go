package httpapi

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	gateway "github.com/mhosseinab/market-ops/gen/go"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/event"
)

// EventService is the event-engine orchestration the gateway depends on (PRD
// §7.4). *event.Service satisfies it. It is an interface so the transport can be
// tested with a fake and httpapi stays free of DB wiring.
//
// Every method takes the authenticated organization id as a MANDATORY first
// argument (issue #67, S8-AUTHZ-001). The handlers derive it from the session
// principal — never from request input — so possession of a marketplace-account or
// event UUID cannot grant cross-organization access. list/Today verify the account
// belongs to the org; detail/relevance are org-scoped in SQL so a foreign event id
// resolves to nothing.
type EventService interface {
	ListOpenForOrg(ctx context.Context, organizationID, account uuid.UUID) ([]db.MarketEvent, error)
	TodayForOrg(ctx context.Context, organizationID, account uuid.UUID) ([]event.Ranked, error)
	GetForOrg(ctx context.Context, organizationID, id uuid.UUID) (db.MarketEvent, error)
	RecordRelevanceForOrg(ctx context.Context, organizationID, eventID, user uuid.UUID, relevance, note string) (db.EventRelevanceFeedback, error)
}

// ListEvents returns the account's open market events (unranked list).
func (s *gatewayServer) ListEvents(
	ctx context.Context, req gateway.ListEventsRequestObject,
) (gateway.ListEventsResponseObject, error) {
	if s.event == nil {
		return gateway.ListEventsdefaultJSONResponse{StatusCode: 503, Body: eventUnavailableErr()}, nil
	}
	p, ok := principalFrom(ctx)
	if !ok {
		return gateway.ListEventsdefaultJSONResponse{StatusCode: 401, Body: noSessionErr()}, nil
	}
	// Organization scope comes ONLY from the authenticated principal; the account id
	// from the query is verified to belong to that org before any row is served.
	rows, err := s.event.ListOpenForOrg(ctx, p.OrganizationID, req.Params.MarketplaceAccountId)
	if err != nil {
		return gateway.ListEventsdefaultJSONResponse{StatusCode: eventErrStatus(err), Body: eventErr(err)}, nil
	}
	out := make([]gateway.MarketEvent, 0, len(rows))
	for _, e := range rows {
		out = append(out, toGatewayEvent(e))
	}
	return gateway.ListEvents200JSONResponse(gateway.MarketEventList{Items: out}), nil
}

// GetEvent returns a single market event by id.
func (s *gatewayServer) GetEvent(
	ctx context.Context, req gateway.GetEventRequestObject,
) (gateway.GetEventResponseObject, error) {
	if s.event == nil {
		return gateway.GetEventdefaultJSONResponse{StatusCode: 503, Body: eventUnavailableErr()}, nil
	}
	p, ok := principalFrom(ctx)
	if !ok {
		return gateway.GetEventdefaultJSONResponse{StatusCode: 401, Body: noSessionErr()}, nil
	}
	// Org-scoped detail read: a foreign event id resolves to no row and returns a
	// 404 with a FIXED not-found body — indistinguishable from an unknown id (no
	// cross-tenant existence oracle). Any other failure is a 500.
	row, err := s.event.GetForOrg(ctx, p.OrganizationID, req.Params.EventId)
	if err != nil {
		if errors.Is(err, event.ErrEventNotFound) || errors.Is(err, pgx.ErrNoRows) {
			return gateway.GetEventdefaultJSONResponse{StatusCode: 404, Body: eventNotFoundErr()}, nil
		}
		return gateway.GetEventdefaultJSONResponse{StatusCode: 500, Body: eventErr(err)}, nil
	}
	return gateway.GetEvent200JSONResponse(toGatewayEvent(row)), nil
}

// GetTodayFeed returns the deterministically ranked Today feed (EVT-004).
func (s *gatewayServer) GetTodayFeed(
	ctx context.Context, req gateway.GetTodayFeedRequestObject,
) (gateway.GetTodayFeedResponseObject, error) {
	if s.event == nil {
		return gateway.GetTodayFeeddefaultJSONResponse{StatusCode: 503, Body: eventUnavailableErr()}, nil
	}
	p, ok := principalFrom(ctx)
	if !ok {
		return gateway.GetTodayFeeddefaultJSONResponse{StatusCode: 401, Body: noSessionErr()}, nil
	}
	ranked, err := s.event.TodayForOrg(ctx, p.OrganizationID, req.Params.MarketplaceAccountId)
	if err != nil {
		return gateway.GetTodayFeeddefaultJSONResponse{StatusCode: eventErrStatus(err), Body: eventErr(err)}, nil
	}
	out := make([]gateway.RankedEvent, 0, len(ranked))
	for _, r := range ranked {
		ev := toGatewayEvent(r.Event)
		out = append(out, gateway.RankedEvent{
			Event:   ev,
			Rank:    r.Rank,
			Factors: ev.Factors,
		})
	}
	return gateway.GetTodayFeed200JSONResponse(gateway.TodayFeed{Items: out}), nil
}

// RecordEventRelevance appends relevance feedback (EVT-005, append-only). The
// acting user comes from the authenticated principal — never from the body.
func (s *gatewayServer) RecordEventRelevance(
	ctx context.Context, req gateway.RecordEventRelevanceRequestObject,
) (gateway.RecordEventRelevanceResponseObject, error) {
	if s.event == nil {
		return gateway.RecordEventRelevancedefaultJSONResponse{StatusCode: 503, Body: eventUnavailableErr()}, nil
	}
	if req.Body == nil {
		return gateway.RecordEventRelevancedefaultJSONResponse{StatusCode: 400, Body: invalidArgErr("request body is required")}, nil
	}
	// Both the acting user AND the organization scope come from the authenticated
	// principal — never the request body (identity/free-text containment). No session
	// means no relevance write.
	p, ok := principalFrom(ctx)
	if !ok {
		return gateway.RecordEventRelevancedefaultJSONResponse{StatusCode: 401, Body: noSessionErr()}, nil
	}
	var note string
	if req.Body.Note != nil {
		note = *req.Body.Note
	}
	// The write is org-scoped in SQL: a foreign/unknown event id inserts zero rows
	// and returns a 404 not-found — no cross-tenant append, no existence oracle.
	rec, err := s.event.RecordRelevanceForOrg(ctx, p.OrganizationID, req.Body.EventId, p.UserID, string(req.Body.Relevance), note)
	if err != nil {
		if errors.Is(err, event.ErrEventNotFound) || errors.Is(err, pgx.ErrNoRows) {
			return gateway.RecordEventRelevancedefaultJSONResponse{StatusCode: 404, Body: eventNotFoundErr()}, nil
		}
		return gateway.RecordEventRelevancedefaultJSONResponse{StatusCode: 500, Body: eventErr(err)}, nil
	}
	return gateway.RecordEventRelevance202JSONResponse(gateway.EventRelevanceRecorded{
		Id:        rec.ID,
		EventId:   rec.EventID,
		Relevance: gateway.EventRelevanceKind(rec.Relevance),
		CreatedAt: rec.CreatedAt,
	}), nil
}

// toGatewayEvent maps a persisted market event onto the wire type. Exposure is
// mapped faithfully to EVT-005: an unknown exposure carries NO amount at all;
// only a known exposure emits the Money triple.
func toGatewayEvent(e db.MarketEvent) gateway.MarketEvent {
	factors := gateway.EventRankFactors{
		ConfidenceBp: int(e.ConfidenceBp),
		UrgencyBp:    int(e.UrgencyBp),
		Exposure:     gateway.EventExposure{Known: e.ExposureKnown},
	}
	if e.ExposureKnown && e.ExposureMantissa.Valid {
		factors.Exposure.Amount = &gateway.MoneyAmount{
			Mantissa: wireMantissa(e.ExposureMantissa.Int64),
			Currency: e.ExposureCurrency,
			Exponent: int(e.ExposureExponent),
		}
	}
	out := gateway.MarketEvent{
		Id:                   e.ID,
		MarketplaceAccountId: e.MarketplaceAccountID,
		VariantId:            e.VariantID,
		Type:                 gateway.EventType(e.EventType),
		Severity:             gateway.EventSeverity(e.Severity),
		State:                gateway.EventLifecycleState(e.State),
		Factors:              factors,
		EvidenceQuality:      gateway.QualityState(e.EvidenceQuality),
		FirstDetectedAt:      e.FirstDetectedAt,
		LastEvidenceAt:       e.LastEvidenceAt,
		ExpiresAt:            e.ExpiresAt,
		EvidenceUpdateCount:  int(e.EvidenceUpdateCount),
	}
	if e.TargetID.Valid {
		id := uuid.UUID(e.TargetID.Bytes)
		out.TargetId = &id
	}
	if e.ThresholdVersion.Valid {
		v := int(e.ThresholdVersion.Int32)
		out.ThresholdVersion = &v
	}
	if e.EvidenceObservationID.Valid {
		id := uuid.UUID(e.EvidenceObservationID.Bytes)
		out.EvidenceObservationId = &id
	}
	if e.EvidenceRef != "" {
		ref := e.EvidenceRef
		out.EvidenceRef = &ref
	}
	if e.ResolvedAt.Valid {
		t := e.ResolvedAt.Time
		out.ResolvedAt = &t
	}
	return out
}

// eventErrStatus maps a service error to an HTTP status for the list/Today paths. A
// foreign/unknown account is reported as 404 (identical to a genuinely-absent one),
// so the response never reveals whether a cross-organization account exists
// (S8-AUTHZ-001). Every other failure is a 500.
func eventErrStatus(err error) int {
	switch {
	case errors.Is(err, event.ErrAccountNotFound), errors.Is(err, event.ErrEventNotFound):
		return 404
	default:
		return 500
	}
}

func eventErr(err error) gateway.ErrorEnvelope {
	// A not-found account/event carries a FIXED code + message so a foreign and an
	// unknown id are indistinguishable to the caller (no existence oracle).
	if errors.Is(err, event.ErrAccountNotFound) || errors.Is(err, event.ErrEventNotFound) {
		return eventNotFoundErr()
	}
	return gateway.ErrorEnvelope{Code: "EVENT_ERROR", Message: err.Error()}
}

// eventNotFoundErr is the single fixed not-found envelope for a foreign or unknown
// account/event id — the no-oracle response shape (issue #67, S8-AUTHZ-001).
func eventNotFoundErr() gateway.ErrorEnvelope {
	return gateway.ErrorEnvelope{Code: "NOT_FOUND", Message: "not found"}
}

func eventUnavailableErr() gateway.ErrorEnvelope {
	return gateway.ErrorEnvelope{Code: "EVENT_UNAVAILABLE", Message: "event service is not configured"}
}
