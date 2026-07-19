package httpapi

import (
	"context"
	"errors"

	"github.com/google/uuid"

	gateway "github.com/mhosseinab/market-ops/gen/go"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/identity"
)

// IdentityService is the identity-mapping orchestration the gateway depends on
// (CAT-002, journey 4). *identity.Service satisfies it. Keeping it an interface
// lets the transport be tested with a fake and keeps httpapi free of DB wiring.
type IdentityService interface {
	NeedsReviewQueue(ctx context.Context, account uuid.UUID) ([]identity.QueueItem, error)
	Confirm(ctx context.Context, identityID uuid.UUID, actor identity.Actor) (db.MarketProductIdentity, error)
	Reject(ctx context.Context, identityID uuid.UUID, actor identity.Actor, note string) (db.MarketProductIdentity, error)
	Defer(ctx context.Context, identityID uuid.UUID, actor identity.Actor, note string) (db.MarketProductIdentity, error)
}

// ListNeedsReview returns the account's pending identity-mapping candidates.
func (s *gatewayServer) ListNeedsReview(
	ctx context.Context, req gateway.ListNeedsReviewRequestObject,
) (gateway.ListNeedsReviewResponseObject, error) {
	if s.identity == nil {
		return gateway.ListNeedsReviewdefaultJSONResponse{StatusCode: 503, Body: identityUnavailableErr()}, nil
	}
	items, err := s.identity.NeedsReviewQueue(ctx, req.Params.MarketplaceAccountId)
	if err != nil {
		return gateway.ListNeedsReviewdefaultJSONResponse{StatusCode: identityErrStatus(err), Body: identityErr(err)}, nil
	}
	out := make([]gateway.NeedsReviewItem, 0, len(items))
	for _, it := range items {
		out = append(out, gateway.NeedsReviewItem{
			IdentityId:      it.IdentityID,
			VariantId:       it.VariantID,
			NativeVariantId: it.NativeVariantID,
			NativeProductId: it.NativeProductID,
			SupplierCode:    it.SupplierCode,
			VariantTitle:    it.VariantTitle,
			ProductTitle:    it.ProductTitle,
			CandidateSource: it.CandidateSource,
			Version:         int(it.Version),
		})
	}
	return gateway.ListNeedsReview200JSONResponse(gateway.NeedsReviewQueue{Items: out}), nil
}

// ConfirmIdentity confirms a Needs Review candidate as the variant's mapping.
func (s *gatewayServer) ConfirmIdentity(
	ctx context.Context, req gateway.ConfirmIdentityRequestObject,
) (gateway.ConfirmIdentityResponseObject, error) {
	if s.identity == nil {
		return gateway.ConfirmIdentitydefaultJSONResponse{StatusCode: 503, Body: identityUnavailableErr()}, nil
	}
	if req.Body == nil {
		return gateway.ConfirmIdentitydefaultJSONResponse{StatusCode: 400, Body: invalidArgErr("request body is required")}, nil
	}
	m, err := s.identity.Confirm(ctx, req.Body.IdentityId, actorFrom(ctx))
	if err != nil {
		return gateway.ConfirmIdentitydefaultJSONResponse{StatusCode: identityErrStatus(err), Body: identityErr(err)}, nil
	}
	return gateway.ConfirmIdentity200JSONResponse(toGatewayIdentity(m)), nil
}

// RejectIdentity rejects a Needs Review candidate.
func (s *gatewayServer) RejectIdentity(
	ctx context.Context, req gateway.RejectIdentityRequestObject,
) (gateway.RejectIdentityResponseObject, error) {
	if s.identity == nil {
		return gateway.RejectIdentitydefaultJSONResponse{StatusCode: 503, Body: identityUnavailableErr()}, nil
	}
	if req.Body == nil {
		return gateway.RejectIdentitydefaultJSONResponse{StatusCode: 400, Body: invalidArgErr("request body is required")}, nil
	}
	m, err := s.identity.Reject(ctx, req.Body.IdentityId, actorFrom(ctx), noteOf(req.Body.Note))
	if err != nil {
		return gateway.RejectIdentitydefaultJSONResponse{StatusCode: identityErrStatus(err), Body: identityErr(err)}, nil
	}
	return gateway.RejectIdentity200JSONResponse(toGatewayIdentity(m)), nil
}

// DeferIdentity leaves a Needs Review candidate in the queue.
func (s *gatewayServer) DeferIdentity(
	ctx context.Context, req gateway.DeferIdentityRequestObject,
) (gateway.DeferIdentityResponseObject, error) {
	if s.identity == nil {
		return gateway.DeferIdentitydefaultJSONResponse{StatusCode: 503, Body: identityUnavailableErr()}, nil
	}
	if req.Body == nil {
		return gateway.DeferIdentitydefaultJSONResponse{StatusCode: 400, Body: invalidArgErr("request body is required")}, nil
	}
	m, err := s.identity.Defer(ctx, req.Body.IdentityId, actorFrom(ctx), noteOf(req.Body.Note))
	if err != nil {
		return gateway.DeferIdentitydefaultJSONResponse{StatusCode: identityErrStatus(err), Body: identityErr(err)}, nil
	}
	return gateway.DeferIdentity200JSONResponse(toGatewayIdentity(m)), nil
}

// toGatewayIdentity maps a db.MarketProductIdentity onto the generated shape.
func toGatewayIdentity(m db.MarketProductIdentity) gateway.MarketProductIdentity {
	return gateway.MarketProductIdentity{
		Id:                   m.ID,
		MarketplaceAccountId: m.MarketplaceAccountID,
		VariantId:            m.VariantID,
		NativeVariantId:      m.NativeVariantID,
		NativeProductId:      m.NativeProductID,
		State:                gateway.MarketProductIdentityState(m.State),
		Active:               m.Active,
		CandidateSource:      m.CandidateSource,
		Version:              int(m.Version),
	}
}

// actorFrom derives the decision actor from the authenticated principal. The
// permission middleware guarantees a principal on these protected routes; a
// missing one degrades to the system actor rather than mis-attributing.
func actorFrom(ctx context.Context) identity.Actor {
	if p, ok := principalFrom(ctx); ok {
		return identity.Actor(p.UserID)
	}
	return identity.Actor(uuid.Nil)
}

func noteOf(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// identityErrStatus maps identity domain errors to HTTP status. A not-pending /
// not-found decision is a client conflict, not a server fault.
func identityErrStatus(err error) int {
	switch {
	case errors.Is(err, identity.ErrNotFound):
		return 404
	case errors.Is(err, identity.ErrNotPending), errors.Is(err, identity.ErrNotReopenable),
		errors.Is(err, identity.ErrIdentityConflict):
		return 409
	case errors.Is(err, identity.ErrInvalidReason):
		return 400
	default:
		return 500
	}
}

func identityErr(err error) gateway.ErrorEnvelope {
	code := "IDENTITY_ERROR"
	switch {
	case errors.Is(err, identity.ErrNotFound):
		code = "NOT_FOUND"
	case errors.Is(err, identity.ErrNotPending):
		code = "NOT_PENDING"
	case errors.Is(err, identity.ErrIdentityConflict):
		code = "IDENTITY_CONFLICT"
	case errors.Is(err, identity.ErrNotReopenable):
		code = "NOT_REOPENABLE"
	case errors.Is(err, identity.ErrInvalidReason):
		code = "INVALID_ARGUMENT"
	}
	return gateway.ErrorEnvelope{Code: code, Message: err.Error()}
}

func identityUnavailableErr() gateway.ErrorEnvelope {
	// Fail closed: an unwired identity service serves no queue and confirms nothing.
	return gateway.ErrorEnvelope{Code: "IDENTITY_UNAVAILABLE", Message: "identity service is not configured"}
}
