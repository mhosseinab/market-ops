package httpapi

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	openapi_types "github.com/oapi-codegen/runtime/types"

	gateway "github.com/mhosseinab/market-ops/gen/go"
	"github.com/mhosseinab/market-ops/services/core/internal/audit"
	"github.com/mhosseinab/market-ops/services/core/internal/briefing"
	"github.com/mhosseinab/market-ops/services/core/internal/recommendation"
)

// DraftService is the Draft-only write plane backing the /chat/cards/* routes
// (CHAT-041/050/061). *recommendation.Service satisfies it. Every method is
// TERMINAL AT DRAFT: it mints a Draft (card / selection set / proposal) and never
// advances the §8.4 machine or mints an approval control — the machine plane
// cannot approve or execute through this seam.
type DraftService interface {
	PrepareRecommendationDraft(ctx context.Context, account, entity, recID uuid.UUID) (recommendation.DraftTicket, error)
	PrepareSelectionSetDraft(ctx context.Context, account uuid.UUID, query string) (recommendation.DraftTicket, error)
	PrepareLevel2Proposal(ctx context.Context, account uuid.UUID, actor audit.Actor, settingKey, beforeKey, afterKey string) (recommendation.ProposalTicket, error)
}

// BriefingService backs GET /briefing (CHAT-010). *briefing.Service satisfies it.
type BriefingService interface {
	Get(ctx context.Context, account uuid.UUID, day time.Time) (briefing.Briefing, error)
}

// CreateRecommendationDraft mints the individual-approval Draft for a persisted,
// approvable recommendation (CHAT-041). It authorizes as the machine principal
// (kindGatewayDraft → perm.GatewayCan(draft.recommendation)); a human/session
// principal never reaches it. It fails closed on an unknown/foreign/non-executable
// recommendation (404) — never a fabricated Draft. The write is terminal at Draft.
func (s *gatewayServer) CreateRecommendationDraft(
	ctx context.Context, req gateway.CreateRecommendationDraftRequestObject,
) (gateway.CreateRecommendationDraftResponseObject, error) {
	if s.draft == nil {
		return gateway.CreateRecommendationDraftdefaultJSONResponse{StatusCode: 503, Body: draftUnavailableErr()}, nil
	}
	if req.Body == nil {
		return gateway.CreateRecommendationDraftdefaultJSONResponse{StatusCode: 400, Body: invalidArgErr("request body is required")}, nil
	}
	ticket, err := s.draft.PrepareRecommendationDraft(ctx, req.Body.MarketplaceAccountId, req.Body.EntityId, req.Body.RecommendationId)
	if err != nil {
		s.logDraft(ctx, "recommendation-draft", req.Body.MarketplaceAccountId, err)
		return gateway.CreateRecommendationDraftdefaultJSONResponse{StatusCode: draftErrStatus(err), Body: draftErr(err)}, nil
	}
	s.logDraft(ctx, "recommendation-draft", req.Body.MarketplaceAccountId, nil)
	return gateway.CreateRecommendationDraft200JSONResponse{
		DraftId:               ticket.DraftID,
		ActionId:              ticket.ActionID,
		ContextVersion:        versionString(ticket.ContextVersion),
		RecommendationVersion: versionString(ticket.RecommendationVersion),
		ParameterVersion:      versionString(ticket.ParameterVersion),
		ExpiresAt:             ticket.ExpiresAt,
	}, nil
}

// CreateSelectionSetDraft compiles a bulk query into a named, versioned selection
// set (CHAT-050/051). Machine principal only; terminal at Draft; NO chat bulk
// approval.
func (s *gatewayServer) CreateSelectionSetDraft(
	ctx context.Context, req gateway.CreateSelectionSetDraftRequestObject,
) (gateway.CreateSelectionSetDraftResponseObject, error) {
	if s.draft == nil {
		return gateway.CreateSelectionSetDraftdefaultJSONResponse{StatusCode: 503, Body: draftUnavailableErr()}, nil
	}
	if req.Body == nil || req.Body.Query == "" {
		return gateway.CreateSelectionSetDraftdefaultJSONResponse{StatusCode: 400, Body: invalidArgErr("a non-empty query is required")}, nil
	}
	ticket, err := s.draft.PrepareSelectionSetDraft(ctx, req.Body.MarketplaceAccountId, req.Body.Query)
	if err != nil {
		s.logDraft(ctx, "selection-set-draft", req.Body.MarketplaceAccountId, err)
		return gateway.CreateSelectionSetDraftdefaultJSONResponse{StatusCode: draftErrStatus(err), Body: draftErr(err)}, nil
	}
	s.logDraft(ctx, "selection-set-draft", req.Body.MarketplaceAccountId, nil)
	return gateway.CreateSelectionSetDraft200JSONResponse{
		DraftId:          ticket.DraftID,
		ActionId:         ticket.ActionID,
		ContextVersion:   versionString(ticket.ContextVersion),
		ParameterVersion: versionString(ticket.ParameterVersion),
		ExpiresAt:        ticket.ExpiresAt,
	}, nil
}

// CreateLevel2Proposal writes a Level-2 before/after/scope/consequence proposal
// AND its append-only audit row in one transaction (CHAT-061/062, AUD-001).
// Machine principal only; terminal at Draft; NO Level-3 write path.
func (s *gatewayServer) CreateLevel2Proposal(
	ctx context.Context, req gateway.CreateLevel2ProposalRequestObject,
) (gateway.CreateLevel2ProposalResponseObject, error) {
	if s.draft == nil {
		return gateway.CreateLevel2ProposaldefaultJSONResponse{StatusCode: 503, Body: draftUnavailableErr()}, nil
	}
	if req.Body == nil || req.Body.SettingKey == "" || req.Body.BeforeKey == "" || req.Body.AfterKey == "" {
		return gateway.CreateLevel2ProposaldefaultJSONResponse{StatusCode: 400, Body: invalidArgErr("setting_key, before_key, and after_key are required")}, nil
	}
	actor := machineActor(ctx)
	ticket, err := s.draft.PrepareLevel2Proposal(ctx, req.Body.MarketplaceAccountId, actor, req.Body.SettingKey, req.Body.BeforeKey, req.Body.AfterKey)
	if err != nil {
		s.logDraft(ctx, "level2-proposal", req.Body.MarketplaceAccountId, err)
		return gateway.CreateLevel2ProposaldefaultJSONResponse{StatusCode: draftErrStatus(err), Body: draftErr(err)}, nil
	}
	s.logDraft(ctx, "level2-proposal", req.Body.MarketplaceAccountId, nil)
	return gateway.CreateLevel2Proposal200JSONResponse{
		DraftId:          ticket.DraftID,
		ActionId:         ticket.ActionID,
		ContextVersion:   versionString(ticket.ContextVersion),
		ParameterVersion: versionString(ticket.ParameterVersion),
		ExpiresAt:        ticket.ExpiresAt,
		ScopeKey:         ticket.ScopeKey,
		ConsequenceKey:   ticket.ConsequenceKey,
	}, nil
}

// GetBriefing serves the stored daily briefing for an account/business-day
// (CHAT-010). Its events carry the SAME ids/order as the Today feed (generated
// from the one ranking). It is a read; it never generates a briefing. A human
// session reads it; the machine principal reads it via the GatewayCan(read.events)
// fallback.
func (s *gatewayServer) GetBriefing(
	ctx context.Context, req gateway.GetBriefingRequestObject,
) (gateway.GetBriefingResponseObject, error) {
	if s.briefing == nil {
		return gateway.GetBriefingdefaultJSONResponse{StatusCode: 503, Body: briefingUnavailableErr()}, nil
	}
	day := req.Params.BusinessDay.Time
	b, err := s.briefing.Get(ctx, req.Params.MarketplaceAccountId, day)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return gateway.GetBriefingdefaultJSONResponse{StatusCode: 404, Body: briefingErr(err)}, nil
		}
		return gateway.GetBriefingdefaultJSONResponse{StatusCode: 500, Body: briefingErr(err)}, nil
	}
	return gateway.GetBriefing200JSONResponse(toBriefingView(b)), nil
}

// toBriefingView maps a stored briefing onto the wire shape, preserving rank order.
func toBriefingView(b briefing.Briefing) gateway.DailyBriefing {
	events := make([]gateway.BriefingEvent, 0, len(b.Events))
	for _, e := range b.Events {
		events = append(events, gateway.BriefingEvent{
			Rank:      e.Rank,
			EventId:   e.EventID,
			EventType: e.EventType,
			Severity:  e.Severity,
		})
	}
	return gateway.DailyBriefing{
		MarketplaceAccountId: b.AccountID,
		BusinessDay:          openapiDate(b.BusinessDay),
		GeneratedAt:          b.GeneratedAt,
		Events:               events,
	}
}

// machineActor builds the AUD-001 actor for a machine-authenticated request. It
// is identity, never free-text authority: the actor id + role + surface come from
// the injected machine principal, not from any message body.
func machineActor(ctx context.Context) audit.Actor {
	actor := audit.Actor{ID: machineActorID, Role: "machine", Surface: machineSurface}
	if p, ok := principalFrom(ctx); ok && p.Email != "" {
		actor.ID = p.Email
	}
	return actor
}

// openapiDate wraps a UTC business day as the wire Date type.
func openapiDate(t time.Time) openapi_types.Date { return openapi_types.Date{Time: t.UTC()} }

// versionString renders an APR-001 version as the opaque string the transport
// contract carries (the machine plane treats versions as identifiers, never
// numbers — no arithmetic on this path).
func versionString(v int64) string { return strconv.FormatInt(v, 10) }

// logDraft emits the structured boundary log for a Draft-only handler (never
// silent). It carries the account and outcome, never any request free text.
func (s *gatewayServer) logDraft(ctx context.Context, route string, account uuid.UUID, err error) {
	if s.logger == nil {
		return
	}
	if err != nil {
		s.logger.WarnContext(ctx, "draft handler rejected", "route", route, "account", account.String(), "error", err.Error())
		return
	}
	s.logger.InfoContext(ctx, "draft minted", "route", route, "account", account.String())
}

// draftErrStatus maps a Draft-only service error to a fail-closed status: an
// unknown/foreign recommendation is 404, a non-executable one is 409, else 500.
func draftErrStatus(err error) int {
	switch {
	case errors.Is(err, pgx.ErrNoRows), errors.Is(err, recommendation.ErrEntityMismatch):
		return 404
	case errors.Is(err, recommendation.ErrNotExecutable):
		return 409
	default:
		return 500
	}
}

func draftErr(err error) gateway.ErrorEnvelope {
	return gateway.ErrorEnvelope{Code: "DRAFT_ERROR", Message: err.Error()}
}

func draftUnavailableErr() gateway.ErrorEnvelope {
	return gateway.ErrorEnvelope{Code: "DRAFT_UNAVAILABLE", Message: "draft service is not configured"}
}

func briefingErr(err error) gateway.ErrorEnvelope {
	return gateway.ErrorEnvelope{Code: "BRIEFING_ERROR", Message: err.Error()}
}

func briefingUnavailableErr() gateway.ErrorEnvelope {
	return gateway.ErrorEnvelope{Code: "BRIEFING_UNAVAILABLE", Message: "briefing service is not configured"}
}
