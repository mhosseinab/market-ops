package httpapi

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"

	gateway "github.com/mhosseinab/market-ops/gen/go"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/money"
	"github.com/mhosseinab/market-ops/services/core/internal/observation"
)

// ObservationService is the observation-store orchestration the gateway depends
// on (PRD §7.3). *observation.Service satisfies it. It is an interface so the
// transport can be tested with a fake and httpapi stays free of DB wiring.
type ObservationService interface {
	ListTargets(ctx context.Context, account uuid.UUID) ([]db.ObservationTarget, error)
	ListObservedOffers(ctx context.Context, account uuid.UUID) ([]db.ObservedOffer, error)
	ListObservations(ctx context.Context, target uuid.UUID, limit int32) ([]db.Observation, error)
	Ingest(ctx context.Context, c observation.Capture) (observation.IngestResult, error)
	// ListConflictedObservedOffers backs GET /market/conflicts (PD-3 item 8, S37).
	ListConflictedObservedOffers(ctx context.Context, account uuid.UUID) ([]db.ObservedOffer, error)
}

// ListObservationTargets returns the account's active observation targets.
func (s *gatewayServer) ListObservationTargets(
	ctx context.Context, req gateway.ListObservationTargetsRequestObject,
) (gateway.ListObservationTargetsResponseObject, error) {
	if s.observation == nil {
		return gateway.ListObservationTargetsdefaultJSONResponse{StatusCode: 503, Body: observationUnavailableErr()}, nil
	}
	rows, err := s.observation.ListTargets(ctx, req.Params.MarketplaceAccountId)
	if err != nil {
		return gateway.ListObservationTargetsdefaultJSONResponse{StatusCode: 500, Body: observationErr(err)}, nil
	}
	out := make([]gateway.ObservationTarget, 0, len(rows))
	for _, t := range rows {
		out = append(out, toGatewayTarget(t))
	}
	return gateway.ListObservationTargets200JSONResponse(gateway.ObservationTargetList{Items: out}), nil
}

// ListObservedOffers returns the account's derived current Observed Offers.
func (s *gatewayServer) ListObservedOffers(
	ctx context.Context, req gateway.ListObservedOffersRequestObject,
) (gateway.ListObservedOffersResponseObject, error) {
	if s.observation == nil {
		return gateway.ListObservedOffersdefaultJSONResponse{StatusCode: 503, Body: observationUnavailableErr()}, nil
	}
	rows, err := s.observation.ListObservedOffers(ctx, req.Params.MarketplaceAccountId)
	if err != nil {
		return gateway.ListObservedOffersdefaultJSONResponse{StatusCode: 500, Body: observationErr(err)}, nil
	}
	out := make([]gateway.ObservedOffer, 0, len(rows))
	for _, o := range rows {
		out = append(out, toGatewayObservedOffer(o))
	}
	return gateway.ListObservedOffers200JSONResponse(gateway.ObservedOfferList{Items: out}), nil
}

// ListObservations returns append-only observation evidence for a target.
func (s *gatewayServer) ListObservations(
	ctx context.Context, req gateway.ListObservationsRequestObject,
) (gateway.ListObservationsResponseObject, error) {
	if s.observation == nil {
		return gateway.ListObservationsdefaultJSONResponse{StatusCode: 503, Body: observationUnavailableErr()}, nil
	}
	var limit int32
	if req.Params.Limit != nil {
		limit = int32(*req.Params.Limit)
	}
	rows, err := s.observation.ListObservations(ctx, req.Params.TargetId, limit)
	if err != nil {
		return gateway.ListObservationsdefaultJSONResponse{StatusCode: 500, Body: observationErr(err)}, nil
	}
	out := make([]gateway.Observation, 0, len(rows))
	for _, o := range rows {
		out = append(out, toGatewayObservation(o))
	}
	return gateway.ListObservations200JSONResponse(gateway.ObservationList{Items: out}), nil
}

// UploadCapture ingests an extension (Route B) capture through the allow-listed
// ingestion contract. The route is FORCED to Route B and the server-side quality
// signals (schema/identity validity, conflict) are derived here — never taken from
// the client — so the extension cannot self-certify or forge Route C freshness.
func (s *gatewayServer) UploadCapture(
	ctx context.Context, req gateway.UploadCaptureRequestObject,
) (gateway.UploadCaptureResponseObject, error) {
	if s.observation == nil {
		return gateway.UploadCapturedefaultJSONResponse{StatusCode: 503, Body: observationUnavailableErr()}, nil
	}
	if req.Body == nil {
		return gateway.UploadCapturedefaultJSONResponse{StatusCode: 400, Body: invalidArgErr("request body is required")}, nil
	}
	// A capture credential is scoped to ONE marketplace account (EXT-001). If the
	// request authenticated with a capture credential, its account MUST match the
	// upload body — an extension can never post captures for a different seller's
	// account (cross-account containment). A human-session upload has no injected
	// capture account and skips this check (perm already authorized it).
	if acct, ok := captureAccountFrom(ctx); ok && acct != req.Body.MarketplaceAccountId {
		return gateway.UploadCapturedefaultJSONResponse{StatusCode: 403, Body: forbiddenErr()}, nil
	}
	capture := captureFromUpload(*req.Body)
	res, err := s.observation.Ingest(ctx, capture)
	if err != nil {
		if errors.Is(err, observation.ErrIncompleteEvidence) {
			return gateway.UploadCapturedefaultJSONResponse{StatusCode: 400, Body: observationErr(err)}, nil
		}
		if errors.Is(err, observation.ErrIdentityMismatch) {
			// Identity quarantine: the capture points a valid target at the wrong
			// variant's native id. Fail closed as a client error, never a silent accept.
			return gateway.UploadCapturedefaultJSONResponse{StatusCode: 409, Body: observationErr(err)}, nil
		}
		if errors.Is(err, observation.ErrTargetNotFound) {
			// No target for this identity — an unconfirmed identity has no target
			// (OBS-001). Fail closed as a client conflict, never a silent accept.
			return gateway.UploadCapturedefaultJSONResponse{StatusCode: 409, Body: observationErr(err)}, nil
		}
		return gateway.UploadCapturedefaultJSONResponse{StatusCode: 500, Body: observationErr(err)}, nil
	}
	body := gateway.CaptureAccepted{
		Deduped: res.Deduped,
		Quality: gateway.QualityState(res.Quality),
	}
	if res.ObservationID != uuid.Nil {
		id := res.ObservationID
		body.ObservationId = &id
	}
	return gateway.UploadCapture202JSONResponse(body), nil
}

// captureFromUpload maps the allow-listed upload onto an internal Capture. The
// route is FORCED to Route B (the extension cannot forge Route A/C). The server
// owns every quality signal: SchemaValid is true only because the payload passed
// the allow-listed contract schema; identity validity and conflict are decided by
// the service against the target and in-window evidence — NEVER from the client.
// The client's confidence is carried through only as a CAP: a low confidence
// degrades the derived state, but no confidence value can promote it.
func captureFromUpload(u gateway.CaptureUpload) observation.Capture {
	c := observation.Capture{
		TargetID:        u.TargetId,
		Account:         u.MarketplaceAccountId,
		NativeVariantID: u.NativeVariantId,
		Route:           observation.RouteB,
		SubRoute:        string(u.SubRoute),
		SourceType:      observation.SourceType(u.SourceType),
		ParserVersion:   u.ParserVersion,
		EvidenceRef:     u.EvidenceRef,
		Availability:    observation.Availability(u.AvailabilityStatus),
		StockSignal:     u.StockSignal,
		CapturedAt:      u.CapturedAt,
		Confidence:      observation.Confidence(u.Confidence),
		// Structural (contract) validity only; identity validity and conflict are
		// server-side determinations made in Ingest, not client assertions.
		SchemaValid: true,
		Price:       rawAmountFrom(u.Price),
		ListPrice:   rawAmountFrom(u.ListPrice),
	}
	if u.NativeSellerId != nil {
		c.NativeSellerID = *u.NativeSellerId
	}
	if u.SourceUrl != nil {
		c.SourceURL = *u.SourceUrl
	}
	if u.ConnectorVersion != nil {
		c.ConnectorVersion = *u.ConnectorVersion
	}
	if u.RawFixtureRef != nil {
		c.RawFixtureRef = *u.RawFixtureRef
	}
	return c
}

func rawAmountFrom(r *gateway.RawAmount) money.RawAmount {
	if r == nil {
		return money.RawAmount{}
	}
	return money.NewRawAmount(r.Text, r.Value, r.Unit)
}

func toGatewayTarget(t db.ObservationTarget) gateway.ObservationTarget {
	return gateway.ObservationTarget{
		Id:                       t.ID,
		MarketplaceAccountId:     t.MarketplaceAccountID,
		IdentityId:               t.IdentityID,
		VariantId:                t.VariantID,
		NativeVariantId:          t.NativeVariantID,
		NativeProductId:          t.NativeProductID,
		Tier:                     gateway.ObservationTargetTier(t.Tier),
		CadenceSeconds:           int(t.CadenceSeconds),
		FreshnessDeadlineSeconds: int(t.FreshnessDeadlineSeconds),
		Active:                   t.Active,
	}
}

func toGatewayObservedOffer(o db.ObservedOffer) gateway.ObservedOffer {
	offer := gateway.ObservedOffer{
		Id:                   o.ID,
		TargetId:             o.TargetID,
		MarketplaceAccountId: o.MarketplaceAccountID,
		OfferIdentity:        o.OfferIdentity,
		NativeVariantId:      o.NativeVariantID,
		Price:                gateway.RawAmount{Text: o.PriceRawText, Value: o.PriceRawValue, Unit: o.PriceRawUnit},
		ListPrice:            gateway.RawAmount{Text: o.ListPriceRawText, Value: o.ListPriceRawValue, Unit: o.ListPriceRawUnit},
		AvailabilityStatus:   gateway.AvailabilityStatus(o.AvailabilityStatus),
		Quality:              gateway.QualityState(o.Quality),
		CapturedAt:           o.CapturedAt,
		FreshnessDeadline:    o.FreshnessDeadline,
		Routes:               decodeGatewayRoutes(o.Routes),
	}
	if o.NativeSellerID != "" {
		s := o.NativeSellerID
		offer.NativeSellerId = &s
	}
	if o.StockSignal.Valid {
		v := o.StockSignal.Int64
		offer.StockSignal = &v
	}
	if o.EndedAt.Valid {
		t := o.EndedAt.Time
		offer.EndedAt = &t
	}
	return offer
}

func toGatewayObservation(o db.Observation) gateway.Observation {
	obs := gateway.Observation{
		Id:                   o.ID,
		TargetId:             o.TargetID,
		MarketplaceAccountId: o.MarketplaceAccountID,
		OfferIdentity:        o.OfferIdentity,
		Route:                gateway.ObservationRoute(o.Route),
		ParserVersion:        o.ParserVersion,
		SourceType:           o.SourceType,
		EvidenceRef:          o.EvidenceRef,
		Price:                gateway.RawAmount{Text: o.PriceRawText, Value: o.PriceRawValue, Unit: o.PriceRawUnit},
		ListPrice:            &gateway.RawAmount{Text: o.ListPriceRawText, Value: o.ListPriceRawValue, Unit: o.ListPriceRawUnit},
		AvailabilityStatus:   gateway.AvailabilityStatus(o.AvailabilityStatus),
		Quality:              gateway.QualityState(o.Quality),
		CapturedAt:           o.CapturedAt,
		FreshnessDeadline:    o.FreshnessDeadline,
	}
	nv := o.NativeVariantID
	obs.NativeVariantId = &nv
	if o.NativeSellerID != "" {
		s := o.NativeSellerID
		obs.NativeSellerId = &s
	}
	if o.SubRoute != "" {
		sr := o.SubRoute
		obs.SubRoute = &sr
	}
	if o.ConnectorVersion != "" {
		cv := o.ConnectorVersion
		obs.ConnectorVersion = &cv
	}
	if o.SourceUrl != "" {
		su := o.SourceUrl
		obs.SourceUrl = &su
	}
	if o.StockSignal.Valid {
		v := o.StockSignal.Int64
		obs.StockSignal = &v
	}
	return obs
}

// decodeGatewayRoutes turns the provenance jsonb into typed gateway routes.
func decodeGatewayRoutes(raw []byte) []gateway.ObservationRoute {
	out := []gateway.ObservationRoute{}
	if len(raw) == 0 {
		return out
	}
	var routes []string
	if err := json.Unmarshal(raw, &routes); err != nil {
		return out
	}
	for _, r := range routes {
		out = append(out, gateway.ObservationRoute(r))
	}
	return out
}

func observationErr(err error) gateway.ErrorEnvelope {
	code := "OBSERVATION_ERROR"
	switch {
	case errors.Is(err, observation.ErrIncompleteEvidence):
		code = "INCOMPLETE_EVIDENCE"
	case errors.Is(err, observation.ErrTargetNotFound):
		code = "TARGET_NOT_FOUND"
	}
	return gateway.ErrorEnvelope{Code: code, Message: err.Error()}
}

func observationUnavailableErr() gateway.ErrorEnvelope {
	return gateway.ErrorEnvelope{Code: "OBSERVATION_UNAVAILABLE", Message: "observation service is not configured"}
}
