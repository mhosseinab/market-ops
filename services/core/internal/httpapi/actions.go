// Actions queue read (PD-3 item 5): a bounded keyset page of approval-card
// versions, enriched with the execution overlay (issue #106). A read that never
// advances state.
package httpapi

import (
	"context"
	"errors"
	"log/slog"

	"github.com/google/uuid"

	gateway "github.com/mhosseinab/market-ops/gen/go"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/execution"
	"github.com/mhosseinab/market-ops/services/core/internal/recommendation"
)

// ListActions returns the account's actions queue (PD-3 item 5): a read, never
// advances state.
func (s *gatewayServer) ListActions(
	ctx context.Context, req gateway.ListActionsRequestObject,
) (gateway.ListActionsResponseObject, error) {
	if s.approval == nil {
		return gateway.ListActionsdefaultJSONResponse{StatusCode: 503, Body: approvalUnavailableErr()}, nil
	}
	// FAIL CLOSED on an unwired execution plane (§4.6: no silent fallback). Since
	// the projection includes execution-bearing card versions (PD-4 rule 1), a row's
	// mode and canonical state come ENTIRELY from the execution overlay — without it
	// a TERMINAL executed action would render exactly like a pre-execution card, i.e.
	// a queue that silently claims nothing has been executed. A half-truthful queue
	// is worse than none, so this returns the SAME structured 503 every other
	// execution-dependent route returns rather than degrading in place.
	if s.execution == nil {
		return gateway.ListActionsdefaultJSONResponse{StatusCode: 503, Body: executionUnavailableErr()}, nil
	}
	var stateFilter string
	if req.Params.State != nil {
		stateFilter = string(*req.Params.State)
	}
	// Bounded keyset page (issue #90 blocker 3, §17): the optional limit and opaque
	// cursor are passed through UNRESOLVED and validated in the service, so the
	// fail-closed rules live in one place. An over-large limit and a bad/foreign
	// cursor are both canonical 400s — never a silently clamped or silently
	// first-page result.
	page, err := s.approval.ListActionsForOrg(ctx, orgFromCtx(ctx), req.Params.MarketplaceAccountId, stateFilter,
		recommendation.ActionsPageRequest{Limit: req.Params.Limit, Cursor: req.Params.Cursor})
	if err != nil {
		switch {
		case errors.Is(err, recommendation.ErrAccountNotFound):
			// A foreign account id is a uniform not-found — never another account's queue.
			return gateway.ListActionsdefaultJSONResponse{StatusCode: 404, Body: approvalErr(err)}, nil
		case errors.Is(err, recommendation.ErrLimitAboveMax):
			// A FIXED client-facing message, symmetric with the cursor arm below: the
			// internal sentinel's phrasing is a server implementation detail and never
			// echoed to a caller.
			return gateway.ListActionsdefaultJSONResponse{StatusCode: 400, Body: invalidArgErr("page limit is above the maximum")}, nil
		case errors.Is(err, recommendation.ErrInvalidCursor):
			return gateway.ListActionsdefaultJSONResponse{StatusCode: 400, Body: invalidArgErr("invalid pagination cursor")}, nil
		default:
			return gateway.ListActionsdefaultJSONResponse{StatusCode: 500, Body: approvalErr(err)}, nil
		}
	}
	rows := page.Items
	// Overlay the execution mode + canonical state per action (issue #106) so the
	// list groups write AND recommend-only modes by canonical state without deep-
	// link-only discovery.
	//
	// The overlay is keyed by the EXACT (actionId, cardId) pair, never by action id
	// alone. An action lineage may hold SEVERAL card versions — the domain mints a
	// newer Draft on the same action id after an execution (PD-4 rule 1), and the
	// projection returns the executed version AND that newer head. Keying by action
	// id alone would stamp the executed version's terminal overlay onto the fresh
	// pre-execution Draft: a false "already executed" claim on a card that has
	// written nothing. A pre-execution card version therefore carries NO overlay
	// fields at all.
	//
	// Coverage is STRUCTURAL, not coincidental (finding F1; the same page-scoping
	// property issue #90 blocker 3 requires of a CURSOR-PAGINATED read): the overlay
	// is fetched for the EXACT card ids of the page just returned, so a page deeper
	// than any account-wide newest-N is covered by construction. A separately-limited
	// by-account overlay reads a DIFFERENT table with a DIFFERENT sort key (execution
	// created_at / recommend-only approved_at vs the page's card created_at), so an
	// execution-bearing card version could land inside the page yet outside the
	// overlay's own top-N and be emitted with NO overlay fields — which the queue
	// renders as a pre-execution card, i.e. the same false "nothing has been executed"
	// claim the fail-closed 503 above exists to prevent, reached through a different
	// door. Asking by returned ids makes a miss impossible at any limit or page depth.
	//
	// Scope the overlay to the caller's own account (issue #102): the account id was
	// already validated by ListActionsForOrg above, so a foreign id can only surface
	// here as ErrAccountNotFound — mapped to the same uniform not-found, never
	// another tenant's projection or a 500. The card id set is caller-derived but
	// never an unscoped read: both overlay queries stay predicated on the account.
	cardIDs := make([]uuid.UUID, 0, len(rows))
	for _, r := range rows {
		cardIDs = append(cardIDs, r.ID)
	}
	unified, err := s.execution.ListUnifiedByCardIDsForOrg(ctx, orgFromCtx(ctx), req.Params.MarketplaceAccountId, cardIDs)
	if err != nil {
		if errors.Is(err, execution.ErrAccountNotFound) {
			return gateway.ListActionsdefaultJSONResponse{StatusCode: 404, Body: executionErr(err)}, nil
		}
		return gateway.ListActionsdefaultJSONResponse{StatusCode: 500, Body: executionErr(err)}, nil
	}
	overlay := make(map[uuid.UUID]execution.UnifiedAction, len(unified))
	for _, u := range unified {
		overlay[u.CardID] = u
	}
	items := make([]gateway.ActionSummary, 0, len(rows))
	for _, r := range rows {
		summary := toActionSummary(r)
		// Both keys must match: the card id addresses the exact version, and the
		// action id confirms the execution belongs to this card's action.
		//
		// A mismatch is a DATA-INTEGRITY anomaly, not a routine case: nothing in the
		// schema ties action_executions.action_id / recommend_only_actions.action_id
		// to approval_cards(id = card_id).action_id, so the binding is code-enforced
		// only. Dropping the untrustworthy overlay is the right display choice, but
		// the dropped row then renders as a PRE-EXECUTION card — the same false "not
		// executed yet" claim the fail-closed 503 above exists to prevent, reached
		// through a data-integrity door. So the drop is reported (counter + structured
		// log) rather than swallowed: quarantine over silence (§4.6, EXE-005).
		if u, ok := overlay[r.ID]; ok {
			if u.ActionID == r.ActionID {
				applyExecutionOverlay(&summary, u)
			} else {
				s.reportOverlayActionMismatch(ctx, req.Params.MarketplaceAccountId, r, u)
			}
		}
		items = append(items, summary)
	}
	// Completeness travels WITH the page (issue #90 blocker 3): hasMore/nextCursor
	// are always present, so a caller can distinguish "this is the whole queue" from
	// "there is more" — the distinction the previous silent clamp destroyed.
	hasMore := page.HasMore
	return gateway.ListActions200JSONResponse(gateway.ActionList{
		Items:      items,
		HasMore:    &hasMore,
		NextCursor: page.NextCursor,
	}), nil
}

// reportOverlayActionMismatch emits the observable signal for an execution overlay
// dropped because its action id disagreed with the action id of the card version it
// was fetched for (issue #106). It changes NOTHING about the response — the row is
// still rendered without execution fields — it only makes the drop visible.
//
// The metric carries no labels; the diagnosing identifiers are stable-key structured
// log fields (technical UUIDs only — no marketplace free text, no locale copy, no
// approval-control material), so the anomaly is reproducible from telemetry without
// giving the counter unbounded cardinality.
func (s *gatewayServer) reportOverlayActionMismatch(
	ctx context.Context, account uuid.UUID, card db.ListApprovalCardsPageRow, u execution.UnifiedAction,
) {
	recordActionOverlayMismatch(ctx)
	logger := s.logger
	if logger == nil {
		logger = slog.Default()
	}
	logger.WarnContext(ctx, "action overlay dropped: overlay action id does not match the card version's action id",
		slog.String("event", "action_overlay_action_id_mismatch"),
		slog.String("marketplace_account_id", account.String()),
		slog.String("card_id", card.ID.String()),
		slog.String("card_action_id", card.ActionID.String()),
		slog.String("overlay_action_id", u.ActionID.String()),
		slog.String("overlay_mode", string(u.Mode)),
	)
}

// applyExecutionOverlay enriches an action summary with its execution overlay
// (issue #106): mode + canonical state, and exactly one mode-specific raw state
// (write externalState / recommend-only recommendOnlyState). A recommend-only
// action never gets a write externalState — the never-cut separation holds on the
// list surface exactly as it does on the single read.
func applyExecutionOverlay(summary *gateway.ActionSummary, u execution.UnifiedAction) {
	mode := gateway.ExecutionMode(u.Mode)
	canonical := gateway.ActionCanonicalState(u.Canonical)
	summary.ExecutionMode = &mode
	summary.CanonicalState = &canonical
	switch u.Mode {
	case execution.ModeWrite:
		es := gateway.ExecutionExternalState(u.ExternalState)
		summary.ExternalState = &es
	case execution.ModeRecommendOnly:
		ro := gateway.RecommendOnlyState(u.RecommendOnlyState)
		summary.RecommendOnlyState = &ro
	}
}

// toActionSummary maps one row of the paginated actions page onto the wire summary.
// variantId is carried through from the row's joined recommendation (server-derived,
// never a client assertion) so a bulk-approval surface can build a selection-set
// member — which needs the PAIR (variantId, recommendationId) — from this one
// bounded read.
func toActionSummary(c db.ListApprovalCardsPageRow) gateway.ActionSummary {
	variantID := c.VariantID
	return gateway.ActionSummary{
		Id:               c.ID,
		RecommendationId: c.RecommendationID,
		VariantId:        &variantID,
		Version:          int64(c.Version),
		State:            gateway.ApprovalState(c.State),
		Price:            gateway.MoneyAmount{Mantissa: wireMantissa(c.PriceMantissa), Currency: c.PriceCurrency, Exponent: int(c.PriceExponent)},
		IdempotencyKey:   &c.IdempotencyKey,
		ExpiresAt:        c.ExpiresAt,
		CreatedAt:        &c.CreatedAt,
	}
}
