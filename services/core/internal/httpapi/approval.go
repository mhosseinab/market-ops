package httpapi

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	gateway "github.com/mhosseinab/market-ops/gen/go"
	"github.com/mhosseinab/market-ops/services/core/internal/approval"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/money"
	"github.com/mhosseinab/market-ops/services/core/internal/recommendation"
)

// ApprovalService is the recommendation/approval orchestration the gateway
// depends on (PRD §7.5 APR-001, §8.4). *recommendation.Service satisfies it. It
// is an interface so the transport can be tested with a fake and httpapi stays
// free of DB wiring.
// Every method that reaches a tenant-owned resource takes the authenticated
// organization id (issue #102) so the service predicates the read/mutation on the
// caller's marketplace account. A card/recommendation/selection owned by another
// account returns a uniform not-found with no disclosure and no side effect. The
// organization is derived from the session principal — never from request input —
// so possession of a card/recommendation UUID cannot grant cross-tenant access.
type ApprovalService interface {
	GetCardForOrg(ctx context.Context, organizationID, id uuid.UUID) (db.ApprovalCard, error)
	History(ctx context.Context, cardID uuid.UUID) ([]db.ApprovalCardState, error)
	ConfirmIndividualForOrg(ctx context.Context, organizationID, cardID uuid.UUID, presented approval.Binding, now time.Time) (recommendation.ConfirmOutcome, error)
	// ConfirmBulkSelectionForOrg authoritatively confirms a bulk approval bound to
	// one exact selection-set version (issue #90, CHAT-052), scoped to the caller's
	// account (issue #102). It resolves the caller's account and predicates the
	// selection-set/lineage lookup on it BEFORE delegating to #90's per-item
	// authorization, so a lineage owned by another account fails closed (uniform
	// not-found) and never authorizes or probes a foreign selection set; #90's
	// per-item authoritative flow (durable per-item results, tenant account_mismatch
	// rejection) is preserved unchanged.
	ConfirmBulkSelectionForOrg(ctx context.Context, organizationID, lineage uuid.UUID, boundVersion int32, now time.Time) (recommendation.BulkConfirmOutcome, error)
	// EditPriceForOrg mints a new card version with the edited price (CHAT-044,
	// PD-3 item 2, S37), scoped to the caller's account.
	EditPriceForOrg(ctx context.Context, organizationID, cardID uuid.UUID, newPrice money.Money, now time.Time) (db.ApprovalCard, error)
	// ListActionsForOrg returns the caller's own account's actions queue (PD-3
	// item 5, S37); a foreign account id returns not-found.
	ListActionsForOrg(ctx context.Context, organizationID, account uuid.UUID, stateFilter string, limit int32) ([]db.ApprovalCard, error)
	// GetRecommendationForOrg returns a single recommendation's full PRC-001
	// record (PD-3 items 1/3, S37), scoped to the caller's account.
	GetRecommendationForOrg(ctx context.Context, organizationID, id uuid.UUID) (db.Recommendation, error)
	// PreviewBulkSelectionForOrg mints a SERVER-side selection-set preview version
	// (PD-3 item 4, S37 hard safety precondition), scoped to the caller's account.
	PreviewBulkSelectionForOrg(ctx context.Context, organizationID, account, lineage uuid.UUID, name string, criteria map[string]string, members []recommendation.PreviewMemberInput) (recommendation.PreviewResult, error)
}

// GetApprovalCard returns a card, its current §8.4 state, and its append-only
// history (APR-001 / AUD-001). It is a read; it never advances state.
func (s *gatewayServer) GetApprovalCard(
	ctx context.Context, req gateway.GetApprovalCardRequestObject,
) (gateway.GetApprovalCardResponseObject, error) {
	if s.approval == nil {
		return gateway.GetApprovalCarddefaultJSONResponse{StatusCode: 503, Body: approvalUnavailableErr()}, nil
	}
	card, err := s.approval.GetCardForOrg(ctx, orgFromCtx(ctx), req.Params.CardId)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return gateway.GetApprovalCarddefaultJSONResponse{StatusCode: 404, Body: approvalErr(err)}, nil
		}
		return gateway.GetApprovalCarddefaultJSONResponse{StatusCode: 500, Body: approvalErr(err)}, nil
	}
	history, err := s.approval.History(ctx, card.ID)
	if err != nil {
		return gateway.GetApprovalCarddefaultJSONResponse{StatusCode: 500, Body: approvalErr(err)}, nil
	}
	view, err := toApprovalCardView(card, history)
	if err != nil {
		return gateway.GetApprovalCarddefaultJSONResponse{StatusCode: 500, Body: approvalErr(err)}, nil
	}
	return gateway.GetApprovalCard200JSONResponse(view), nil
}

// ConfirmApproval activates the structured control on an individual card (§8,
// APR-001). The bound versions in the body are re-verified against the live card;
// any change routes to Invalidated, a lapse to Expired, and only a full match to
// Approved. Execution is S18 (ExecutionPending on an Approved card).
func (s *gatewayServer) ConfirmApproval(
	ctx context.Context, req gateway.ConfirmApprovalRequestObject,
) (gateway.ConfirmApprovalResponseObject, error) {
	if s.approval == nil {
		return gateway.ConfirmApprovaldefaultJSONResponse{StatusCode: 503, Body: approvalUnavailableErr()}, nil
	}
	if req.Body == nil {
		return gateway.ConfirmApprovaldefaultJSONResponse{StatusCode: 400, Body: invalidArgErr("request body is required")}, nil
	}
	presented := fromGatewayBinding(req.Body.Binding)
	outcome, err := s.approval.ConfirmIndividualForOrg(ctx, orgFromCtx(ctx), req.Body.CardId, presented, time.Now().UTC())
	if err != nil {
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return gateway.ConfirmApprovaldefaultJSONResponse{StatusCode: 404, Body: approvalErr(err)}, nil
		case errors.Is(err, approval.ErrNoControl):
			// The card is not control-bearing (not AwaitingConfirmation / a
			// simulation): free text / a stale surface cannot approve (PRC-002, §8).
			return gateway.ConfirmApprovaldefaultJSONResponse{StatusCode: 409, Body: approvalErr(err)}, nil
		default:
			return gateway.ConfirmApprovaldefaultJSONResponse{StatusCode: 500, Body: approvalErr(err)}, nil
		}
	}
	return gateway.ConfirmApproval200JSONResponse(gateway.ApprovalConfirmResult{
		CardId:           req.Body.CardId,
		State:            gateway.ApprovalState(outcome.State),
		Reason:           gateway.ApprovalInvalidationReason(outcome.Reason),
		ExecutionPending: outcome.ExecutionPending,
	}), nil
}

// ConfirmBulkApproval authoritatively confirms a bulk approval bound to one exact
// selection-set version (CHAT-052, issue #90), scoped to the caller's account
// (issue #102). The organization is derived from the session principal (never from
// request input), so a caller cannot confirm a bulk selection belonging to another
// tenant: a foreign lineage fails closed as a uniform not-found. A stale bound
// version (any set/evidence change minted a new version) is rejected as invalid and
// authorizes NOTHING. When valid, each executable member is durably authorized
// through the same §8.4 individual-confirm path and returned as an explicit per-item
// result; blocked/warning members are excluded and never execute.
func (s *gatewayServer) ConfirmBulkApproval(
	ctx context.Context, req gateway.ConfirmBulkApprovalRequestObject,
) (gateway.ConfirmBulkApprovalResponseObject, error) {
	if s.approval == nil {
		return gateway.ConfirmBulkApprovaldefaultJSONResponse{StatusCode: 503, Body: approvalUnavailableErr()}, nil
	}
	if req.Body == nil {
		return gateway.ConfirmBulkApprovaldefaultJSONResponse{StatusCode: 400, Body: invalidArgErr("request body is required")}, nil
	}
	outcome, err := s.approval.ConfirmBulkSelectionForOrg(ctx, orgFromCtx(ctx), req.Body.SelectionSetLineage, int32(req.Body.BoundVersion), time.Now().UTC())
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return gateway.ConfirmBulkApprovaldefaultJSONResponse{StatusCode: 404, Body: approvalErr(err)}, nil
		}
		return gateway.ConfirmBulkApprovaldefaultJSONResponse{StatusCode: 500, Body: approvalErr(err)}, nil
	}
	items := make([]gateway.BulkApprovalItemResult, 0, len(outcome.Items))
	for _, it := range outcome.Items {
		items = append(items, gateway.BulkApprovalItemResult{
			VariantId:        it.VariantID,
			RecommendationId: it.RecommendationID,
			Disposition:      gateway.SelectionSetDisposition(it.Disposition),
			State:            gateway.BulkApprovalItemState(it.State),
			Reason:           it.Reason,
		})
	}
	result := gateway.BulkApprovalConfirmResult{
		SelectionSetLineage: req.Body.SelectionSetLineage,
		BoundVersion:        req.Body.BoundVersion,
		Valid:               outcome.Valid,
		ExecutionPending:    outcome.ExecutionPending,
		Items:               items,
	}
	if !outcome.Valid {
		v := int64(outcome.CurrentVersion)
		result.CurrentVersion = &v
	}
	return gateway.ConfirmBulkApproval200JSONResponse(result), nil
}

// toApprovalCardView maps a persisted card + history onto the wire view. hasControl
// is true ONLY in AwaitingConfirmation (a live structured control).
func toApprovalCardView(card db.ApprovalCard, history []db.ApprovalCardState) (gateway.ApprovalCardView, error) {
	binding, err := toGatewayBindingFromCard(card)
	if err != nil {
		return gateway.ApprovalCardView{}, err
	}
	entries := make([]gateway.ApprovalStateHistoryEntry, 0, len(history))
	for _, h := range history {
		e := gateway.ApprovalStateHistoryEntry{
			ToState:    gateway.ApprovalState(h.ToState),
			Reason:     h.Reason,
			OccurredAt: h.OccurredAt,
		}
		if h.FromState.Valid {
			from := gateway.ApprovalState(h.FromState.String)
			e.FromState = &from
		}
		entries = append(entries, e)
	}
	key := card.IdempotencyKey
	return gateway.ApprovalCardView{
		Id:               card.ID,
		RecommendationId: card.RecommendationID,
		Version:          int64(card.Version),
		State:            gateway.ApprovalState(card.State),
		Binding:          binding,
		Price: gateway.MoneyAmount{
			Mantissa: wireMantissa(card.PriceMantissa),
			Currency: card.PriceCurrency,
			Exponent: int(card.PriceExponent),
		},
		IdempotencyKey: &key,
		HasControl:     card.State == string(approval.StateAwaitingConfirmation),
		History:        entries,
	}, nil
}

// toGatewayBindingFromCard maps a persisted card's bound versions to the wire
// binding, decoding the evidence-version map.
func toGatewayBindingFromCard(card db.ApprovalCard) (gateway.ApprovalBinding, error) {
	ev, err := decodeEvidenceVersions(card.EvidenceVersions)
	if err != nil {
		return gateway.ApprovalBinding{}, err
	}
	return gateway.ApprovalBinding{
		ActionId:           card.ActionID,
		ParameterVersion:   card.ParameterVersion,
		ContextVersion:     card.ContextVersion,
		PolicyVersion:      card.PolicyVersion,
		CostProfileVersion: card.CostProfileVersion,
		EvidenceVersions:   ev,
		ExpiresAt:          card.ExpiresAt,
	}, nil
}

// fromGatewayBinding maps a wire binding onto the domain binding (APR-001). The
// evidence-version array becomes a map keyed by observation id.
func fromGatewayBinding(b gateway.ApprovalBinding) approval.Binding {
	ev := make(map[uuid.UUID]int64, len(b.EvidenceVersions))
	for _, e := range b.EvidenceVersions {
		ev[e.ObservationId] = e.Version
	}
	return approval.Binding{
		ActionID:           b.ActionId,
		ParameterVersion:   b.ParameterVersion,
		ContextVersion:     b.ContextVersion,
		PolicyVersion:      b.PolicyVersion,
		CostProfileVersion: b.CostProfileVersion,
		EvidenceVersions:   ev,
		Expiry:             b.ExpiresAt,
	}
}

// decodeEvidenceVersions decodes the stored JSON evidence-version map into the
// wire array, in a deterministic (sorted) order.
func decodeEvidenceVersions(raw []byte) ([]gateway.EvidenceVersion, error) {
	m, err := recommendation.DecodeEvidenceVersions(raw)
	if err != nil {
		return nil, err
	}
	ids := make([]uuid.UUID, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sortUUIDs(ids)
	out := make([]gateway.EvidenceVersion, 0, len(m))
	for _, id := range ids {
		out = append(out, gateway.EvidenceVersion{ObservationId: id, Version: m[id]})
	}
	return out, nil
}

// sortUUIDs sorts ids by their string form for deterministic output.
func sortUUIDs(ids []uuid.UUID) {
	for i := 1; i < len(ids); i++ {
		for j := i; j > 0 && ids[j-1].String() > ids[j].String(); j-- {
			ids[j-1], ids[j] = ids[j], ids[j-1]
		}
	}
}

func approvalErr(err error) gateway.ErrorEnvelope {
	return gateway.ErrorEnvelope{Code: "APPROVAL_ERROR", Message: err.Error()}
}

func approvalUnavailableErr() gateway.ErrorEnvelope {
	return gateway.ErrorEnvelope{Code: "APPROVAL_UNAVAILABLE", Message: "approval service is not configured"}
}
