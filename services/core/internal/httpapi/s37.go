// S37 consolidated PD-3 gateway endpoints (dk-p0-product-decisions.md):
// recommendation-detail + contribution, edit-price, the server-minted bulk
// selection-set preview, list-actions/list-outcomes, guardrails read+write,
// users roster, ops-queues + Market conflict view, and the EXT-007 watchlist.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	gateway "github.com/mhosseinab/market-ops/gen/go"
	"github.com/mhosseinab/market-ops/services/core/internal/audit"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/guardrail"
	"github.com/mhosseinab/market-ops/services/core/internal/margin"
	"github.com/mhosseinab/market-ops/services/core/internal/money"
	"github.com/mhosseinab/market-ops/services/core/internal/policy"
	"github.com/mhosseinab/market-ops/services/core/internal/recommendation"
	"github.com/mhosseinab/market-ops/services/core/internal/watchlist"
)

// GuardrailService backs the /guardrails routes (PD-3 item 6, S37).
// *guardrail.Service satisfies it.
type GuardrailService interface {
	Get(ctx context.Context, account uuid.UUID) (guardrail.ConfigView, error)
	Set(ctx context.Context, account uuid.UUID, actor audit.Actor, settings guardrail.Settings) (guardrail.ConfigView, error)
}

// WatchlistService backs the /watchlist routes (EXT-007, S37).
// *watchlist.Service satisfies it.
type WatchlistService interface {
	List(ctx context.Context, account uuid.UUID) ([]db.WatchlistEntry, error)
	Add(ctx context.Context, account, variant uuid.UUID, actor audit.Actor) (db.WatchlistEntry, error)
}

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
	row, err := s.approval.GetRecommendation(ctx, req.Params.RecommendationId)
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

// EditApprovalCardPrice mints a new card version with the edited price
// (CHAT-044, PD-3 item 2). It is L2 price.edit — Owner/Operator only; the
// read/Draft-only machine gateway credential can never reach this route
// (enforced by routePolicies + perm.GatewayCan, §12.3).
func (s *gatewayServer) EditApprovalCardPrice(
	ctx context.Context, req gateway.EditApprovalCardPriceRequestObject,
) (gateway.EditApprovalCardPriceResponseObject, error) {
	if s.approval == nil {
		return gateway.EditApprovalCardPricedefaultJSONResponse{StatusCode: 503, Body: approvalUnavailableErr()}, nil
	}
	if req.Body == nil {
		return gateway.EditApprovalCardPricedefaultJSONResponse{StatusCode: 400, Body: invalidArgErr("request body is required")}, nil
	}
	newPrice, err := moneyFromGateway(req.Body.NewPrice)
	if err != nil {
		return gateway.EditApprovalCardPricedefaultJSONResponse{StatusCode: 400, Body: invalidArgErr(err.Error())}, nil
	}
	card, err := s.approval.EditPrice(ctx, req.Body.CardId, newPrice, time.Now().UTC())
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return gateway.EditApprovalCardPricedefaultJSONResponse{StatusCode: 404, Body: approvalErr(err)}, nil
		}
		return gateway.EditApprovalCardPricedefaultJSONResponse{StatusCode: 500, Body: approvalErr(err)}, nil
	}
	view, err := toApprovalCardView(card, nil)
	if err != nil {
		return gateway.EditApprovalCardPricedefaultJSONResponse{StatusCode: 500, Body: approvalErr(err)}, nil
	}
	return gateway.EditApprovalCardPrice200JSONResponse(view), nil
}

// PreviewSelectionSet builds the screens-native bulk selection-set preview,
// minting the version ENTIRELY SERVER-SIDE (PD-3 item 4, the hard S35/S37
// safety precondition). The request carries NO version field by construction —
// there is nothing for a client to supply or influence.
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
	result, err := s.approval.PreviewBulkSelection(ctx, req.Body.MarketplaceAccountId, lineage, req.Body.Name, criteria, members)
	if err != nil {
		if errors.Is(err, recommendation.ErrUnknownMember) {
			return gateway.PreviewSelectionSetdefaultJSONResponse{StatusCode: 404, Body: approvalErr(err)}, nil
		}
		return gateway.PreviewSelectionSetdefaultJSONResponse{StatusCode: 500, Body: approvalErr(err)}, nil
	}
	return gateway.PreviewSelectionSet200JSONResponse(toSelectionSetPreviewResult(result)), nil
}

// ListActions returns the account's actions queue (PD-3 item 5): a read, never
// advances state.
func (s *gatewayServer) ListActions(
	ctx context.Context, req gateway.ListActionsRequestObject,
) (gateway.ListActionsResponseObject, error) {
	if s.approval == nil {
		return gateway.ListActionsdefaultJSONResponse{StatusCode: 503, Body: approvalUnavailableErr()}, nil
	}
	var stateFilter string
	if req.Params.State != nil {
		stateFilter = string(*req.Params.State)
	}
	var limit int32
	if req.Params.Limit != nil {
		limit = *req.Params.Limit
	}
	rows, err := s.approval.ListActions(ctx, req.Params.MarketplaceAccountId, stateFilter, limit)
	if err != nil {
		return gateway.ListActionsdefaultJSONResponse{StatusCode: 500, Body: approvalErr(err)}, nil
	}
	items := make([]gateway.ActionSummary, 0, len(rows))
	for _, r := range rows {
		items = append(items, toActionSummary(r))
	}
	return gateway.ListActions200JSONResponse(gateway.ActionList{Items: items}), nil
}

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
	rows, err := s.outcome.ListByAccount(ctx, req.Params.MarketplaceAccountId, limit)
	if err != nil {
		return gateway.ListOutcomesdefaultJSONResponse{StatusCode: 500, Body: outcomeErr(err)}, nil
	}
	items := make([]gateway.OutcomeSummary, 0, len(rows))
	for _, r := range rows {
		items = append(items, toOutcomeSummary(r))
	}
	return gateway.ListOutcomes200JSONResponse(gateway.OutcomeList{Items: items}), nil
}

// GetGuardrails reads an account's L3 commercial guardrails (PD-3 item 6). L1
// read, every role; absent (never configured) is a structured 404.
func (s *gatewayServer) GetGuardrails(
	ctx context.Context, req gateway.GetGuardrailsRequestObject,
) (gateway.GetGuardrailsResponseObject, error) {
	if s.guardrail == nil {
		return gateway.GetGuardrailsdefaultJSONResponse{StatusCode: 503, Body: guardrailUnavailableErr()}, nil
	}
	view, err := s.guardrail.Get(ctx, req.Params.MarketplaceAccountId)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return gateway.GetGuardrailsdefaultJSONResponse{StatusCode: 404, Body: guardrailErr(err)}, nil
		}
		return gateway.GetGuardrailsdefaultJSONResponse{StatusCode: 500, Body: guardrailErr(err)}, nil
	}
	return gateway.GetGuardrails200JSONResponse(toGuardrailConfigView(view)), nil
}

// SetGuardrails writes an account's L3 commercial guardrails, Owner ONLY
// (perm.ActionWriteGuardrails; never reachable by the machine gateway
// credential, §12.3). Every write appends an AUD-001 audit record ATOMICALLY
// with the mutation (internal/guardrail.Service.Set, same transaction).
func (s *gatewayServer) SetGuardrails(
	ctx context.Context, req gateway.SetGuardrailsRequestObject,
) (gateway.SetGuardrailsResponseObject, error) {
	if s.guardrail == nil {
		return gateway.SetGuardrailsdefaultJSONResponse{StatusCode: 503, Body: guardrailUnavailableErr()}, nil
	}
	if req.Body == nil {
		return gateway.SetGuardrailsdefaultJSONResponse{StatusCode: 400, Body: invalidArgErr("request body is required")}, nil
	}
	floor, err := moneyFromGateway(req.Body.Settings.ContributionFloor)
	if err != nil {
		return gateway.SetGuardrailsdefaultJSONResponse{StatusCode: 400, Body: invalidArgErr(err.Error())}, nil
	}
	settings := guardrail.Settings{
		ContributionFloor: floor,
		MovementCapBp:     req.Body.Settings.MovementCapBasisPoints,
		CooldownSeconds:   req.Body.Settings.CooldownSeconds,
		Strategy:          policy.Strategy(req.Body.Settings.Strategy),
		StrategyEnabled:   req.Body.Settings.StrategyEnabled,
	}
	view, err := s.guardrail.Set(ctx, req.Body.MarketplaceAccountId, actorFromPrincipal(ctx, "screens"), settings)
	if err != nil {
		if errors.Is(err, guardrail.ErrInvalidStrategy) {
			return gateway.SetGuardrailsdefaultJSONResponse{StatusCode: 400, Body: guardrailErr(err)}, nil
		}
		return gateway.SetGuardrailsdefaultJSONResponse{StatusCode: 500, Body: guardrailErr(err)}, nil
	}
	return gateway.SetGuardrails200JSONResponse(toGuardrailConfigView(view)), nil
}

// ListUsers returns the caller's organization's user roster (PD-3 item 7). L1
// read, every role.
func (s *gatewayServer) ListUsers(
	ctx context.Context, _ gateway.ListUsersRequestObject,
) (gateway.ListUsersResponseObject, error) {
	if s.auth == nil {
		return gateway.ListUsersdefaultJSONResponse{StatusCode: 503, Body: unavailableAuthErr()}, nil
	}
	p, ok := principalFrom(ctx)
	if !ok {
		return gateway.ListUsersdefaultJSONResponse{StatusCode: 401, Body: noSessionErr()}, nil
	}
	rows, err := s.auth.ListUsers(ctx, p.OrganizationID)
	if err != nil {
		return gateway.ListUsersdefaultJSONResponse{StatusCode: 500, Body: internalErr()}, nil
	}
	items := make([]gateway.UserSummary, 0, len(rows))
	for _, u := range rows {
		items = append(items, gateway.UserSummary{
			Id:        u.ID,
			Email:     u.Email,
			Role:      gateway.UserRole(u.Role),
			CreatedAt: u.CreatedAt,
		})
	}
	return gateway.ListUsers200JSONResponse(gateway.UserList{Items: items}), nil
}

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
	rows, err := s.execution.ListPendingReconciliation(ctx, req.Params.MarketplaceAccountId, 0)
	if err != nil {
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
	rows, err := s.observation.ListConflictedObservedOffers(ctx, req.Params.MarketplaceAccountId)
	if err != nil {
		return gateway.ListMarketConflictsdefaultJSONResponse{StatusCode: 500, Body: observationErr(err)}, nil
	}
	out := make([]gateway.ObservedOffer, 0, len(rows))
	for _, o := range rows {
		out = append(out, toGatewayObservedOffer(o))
	}
	return gateway.ListMarketConflicts200JSONResponse(gateway.ObservedOfferList{Items: out}), nil
}

// ListWatchlist returns the account's EXT-007 priority watchlist. A read.
func (s *gatewayServer) ListWatchlist(
	ctx context.Context, req gateway.ListWatchlistRequestObject,
) (gateway.ListWatchlistResponseObject, error) {
	if s.watchlistSvc == nil {
		return gateway.ListWatchlistdefaultJSONResponse{StatusCode: 503, Body: watchlistUnavailableErr()}, nil
	}
	rows, err := s.watchlistSvc.List(ctx, req.Params.MarketplaceAccountId)
	if err != nil {
		return gateway.ListWatchlistdefaultJSONResponse{StatusCode: 500, Body: watchlistErr(err)}, nil
	}
	items := make([]gateway.WatchlistEntry, 0, len(rows))
	for _, r := range rows {
		items = append(items, gateway.WatchlistEntry{
			Id:                   r.ID,
			MarketplaceAccountId: r.MarketplaceAccountID,
			VariantId:            r.VariantID,
			CreatedAt:            r.CreatedAt,
		})
	}
	return gateway.ListWatchlist200JSONResponse(gateway.WatchlistView{
		MarketplaceAccountId: req.Params.MarketplaceAccountId,
		Cap:                  int32(watchlist.MaxEntries),
		Items:                items,
	}), nil
}

// AddWatchlistEntry adds a Confirmed owned product to the account's priority
// watchlist (EXT-007). The SERVER enforces the cap and appends an AUD-001 audit
// record ATOMICALLY with the insert (internal/watchlist.Service.Add).
func (s *gatewayServer) AddWatchlistEntry(
	ctx context.Context, req gateway.AddWatchlistEntryRequestObject,
) (gateway.AddWatchlistEntryResponseObject, error) {
	if s.watchlistSvc == nil {
		return gateway.AddWatchlistEntrydefaultJSONResponse{StatusCode: 503, Body: watchlistUnavailableErr()}, nil
	}
	if req.Body == nil {
		return gateway.AddWatchlistEntrydefaultJSONResponse{StatusCode: 400, Body: invalidArgErr("request body is required")}, nil
	}
	entry, err := s.watchlistSvc.Add(ctx, req.Body.MarketplaceAccountId, req.Body.VariantId, actorFromPrincipal(ctx, "screens"))
	if err != nil {
		switch {
		case errors.Is(err, watchlist.ErrNotConfirmed):
			return gateway.AddWatchlistEntrydefaultJSONResponse{StatusCode: 409, Body: watchlistErr(err)}, nil
		case errors.Is(err, watchlist.ErrCapExceeded):
			return gateway.AddWatchlistEntrydefaultJSONResponse{StatusCode: 409, Body: watchlistErr(err)}, nil
		default:
			return gateway.AddWatchlistEntrydefaultJSONResponse{StatusCode: 500, Body: watchlistErr(err)}, nil
		}
	}
	return gateway.AddWatchlistEntry200JSONResponse(gateway.WatchlistEntry{
		Id:                   entry.ID,
		MarketplaceAccountId: entry.MarketplaceAccountID,
		VariantId:            entry.VariantID,
		CreatedAt:            entry.CreatedAt,
	}), nil
}

// --- mapping helpers -------------------------------------------------------

// actorFromPrincipal builds an AUD-001 actor from the authenticated principal
// for a screens-originated write. Identity comes from the injected principal,
// never from any request body (free-text containment).
func actorFromPrincipal(ctx context.Context, surface string) audit.Actor {
	p, ok := principalFrom(ctx)
	if !ok {
		return audit.Actor{Surface: surface}
	}
	return audit.Actor{ID: p.Email, Role: string(p.Role), Surface: surface}
}

// toMoneyAmount is the s37.go-local alias for the shared moneyToGateway helper
// (policy.go), kept as a short name for readability in the mapping code below.
func toMoneyAmount(m money.Money) gateway.MoneyAmount { return moneyToGateway(m) }

func strPtr(s string) *string { return &s }

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

func toActionSummary(c db.ApprovalCard) gateway.ActionSummary {
	return gateway.ActionSummary{
		Id:               c.ID,
		RecommendationId: c.RecommendationID,
		Version:          int64(c.Version),
		State:            gateway.ApprovalState(c.State),
		Price:            gateway.MoneyAmount{Mantissa: c.PriceMantissa, Currency: c.PriceCurrency, Exponent: int(c.PriceExponent)},
		IdempotencyKey:   &c.IdempotencyKey,
		ExpiresAt:        c.ExpiresAt,
		CreatedAt:        &c.CreatedAt,
	}
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

func toGuardrailConfigView(v guardrail.ConfigView) gateway.GuardrailConfigView {
	out := gateway.GuardrailConfigView{
		MarketplaceAccountId: v.AccountID,
		Settings: gateway.GuardrailSettings{
			ContributionFloor:      toMoneyAmount(v.Settings.ContributionFloor),
			MovementCapBasisPoints: v.Settings.MovementCapBp,
			CooldownSeconds:        v.Settings.CooldownSeconds,
			Strategy:               gateway.PolicyStrategy(v.Settings.Strategy),
			StrategyEnabled:        v.Settings.StrategyEnabled,
		},
		UpdatedAt: v.UpdatedAt,
	}
	if v.UpdatedBy != "" {
		u := v.UpdatedBy
		out.UpdatedBy = &u
	}
	return out
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

func ptrMoneyAmount(m money.Money) *gateway.MoneyAmount {
	v := toMoneyAmount(m)
	return &v
}

func guardrailErr(err error) gateway.ErrorEnvelope {
	return gateway.ErrorEnvelope{Code: "GUARDRAIL_ERROR", Message: err.Error()}
}

func guardrailUnavailableErr() gateway.ErrorEnvelope {
	return gateway.ErrorEnvelope{Code: "GUARDRAIL_UNAVAILABLE", Message: "guardrail service is not configured"}
}

func watchlistErr(err error) gateway.ErrorEnvelope {
	return gateway.ErrorEnvelope{Code: "WATCHLIST_ERROR", Message: err.Error()}
}

func watchlistUnavailableErr() gateway.ErrorEnvelope {
	return gateway.ErrorEnvelope{Code: "WATCHLIST_UNAVAILABLE", Message: "watchlist service is not configured"}
}

func outcomeErr(err error) gateway.ErrorEnvelope {
	return gateway.ErrorEnvelope{Code: "OUTCOME_ERROR", Message: err.Error()}
}

func outcomeUnavailableErr() gateway.ErrorEnvelope {
	return gateway.ErrorEnvelope{Code: "OUTCOME_UNAVAILABLE", Message: "outcome service is not configured"}
}
