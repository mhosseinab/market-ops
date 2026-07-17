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
type EventService interface {
	ListOpen(ctx context.Context, account uuid.UUID) ([]db.MarketEvent, error)
	Today(ctx context.Context, account uuid.UUID) ([]event.Ranked, error)
	Get(ctx context.Context, id uuid.UUID) (db.MarketEvent, error)
	RecordRelevance(ctx context.Context, eventID, user uuid.UUID, relevance, note string) (db.EventRelevanceFeedback, error)
}

// ListEvents returns the account's open market events (unranked list).
func (s *gatewayServer) ListEvents(
	ctx context.Context, req gateway.ListEventsRequestObject,
) (gateway.ListEventsResponseObject, error) {
	if s.event == nil {
		return gateway.ListEventsdefaultJSONResponse{StatusCode: 503, Body: eventUnavailableErr()}, nil
	}
	rows, err := s.event.ListOpen(ctx, req.Params.MarketplaceAccountId)
	if err != nil {
		return gateway.ListEventsdefaultJSONResponse{StatusCode: 500, Body: eventErr(err)}, nil
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
	row, err := s.event.Get(ctx, req.Params.EventId)
	if err != nil {
		// A missing event is a 404; any other failure is a 500 — never conflate a
		// not-found with an infrastructure error.
		if errors.Is(err, pgx.ErrNoRows) {
			return gateway.GetEventdefaultJSONResponse{StatusCode: 404, Body: eventErr(err)}, nil
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
	ranked, err := s.event.Today(ctx, req.Params.MarketplaceAccountId)
	if err != nil {
		return gateway.GetTodayFeeddefaultJSONResponse{StatusCode: 500, Body: eventErr(err)}, nil
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
	var user uuid.UUID
	if p, ok := principalFrom(ctx); ok {
		user = p.UserID
	}
	var note string
	if req.Body.Note != nil {
		note = *req.Body.Note
	}
	rec, err := s.event.RecordRelevance(ctx, req.Body.EventId, user, string(req.Body.Relevance), note)
	if err != nil {
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
			Mantissa: e.ExposureMantissa.Int64,
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

func eventErr(err error) gateway.ErrorEnvelope {
	return gateway.ErrorEnvelope{Code: "EVENT_ERROR", Message: err.Error()}
}

func eventUnavailableErr() gateway.ErrorEnvelope {
	return gateway.ErrorEnvelope{Code: "EVENT_UNAVAILABLE", Message: "event service is not configured"}
}
